// Run: tsx src/__tests__/project-tree-group-activity.test.tsx
//
// Task 312: a collapsed session-group row shows the member activity dot beside
// the count badge. The aggregation reuses the member rows' own pipeline —
// topicStatus (folds node.running into "streaming") filtered by the exact
// dot-status set the rows render — so the dot appears and clears with the rows
// at the same rate and an all-idle group never shows one.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { StrictMode, useState } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ProjectTreeGroupRows, useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
import { projectTreeGroupDotStatus, projectTreeStatusShowsDot } from "../lib/projectTreeTopic";
import type { Translator } from "../lib/i18n";
import type { ProjectNode, ProjectTreeOrganizationBindings, SessionGroup } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else failed += 1;
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── lib layer ────────────────────────────────────────────────────────────────

console.log("\nproject tree group activity (task 312)");

// The row condition in ProjectTree.tsx (showStatusInSide) is the source of
// truth for what a member row draws. This source guard keeps the two in step:
// if someone edits one set without the other, this fails loudly instead of the
// group header silently drifting from the rows it aggregates.
{
  const thisFile = fileURLToPath(import.meta.url);
  const treeSource = readFileSync(resolve(dirname(thisFile), "../components/ProjectTree.tsx"), "utf8");
  const rowCondition = 'const showStatusInSide = status === "thinking" || status === "streaming" || status === "waiting_confirmation" || status === "background_job";';
  ok(treeSource.includes(rowCondition), "member-row dot set unchanged and in sync with projectTreeStatusShowsDot");
}

for (const status of ["thinking", "streaming", "waiting_confirmation", "background_job"] as const) {
  ok(projectTreeStatusShowsDot(status), `${status} qualifies for the dot`);
}
for (const status of ["", "paused", "error", "finishing", "awaiting_delivery"] as const) {
  ok(!projectTreeStatusShowsDot(status), `${status || "empty"} does not qualify for the dot`);
}

{
  const running: ProjectNode = { key: "r", kind: "topic", label: "R", topicId: "r", running: true, children: [] };
  const streaming: ProjectNode = { key: "s", kind: "topic", label: "S", topicId: "s", status: "streaming", children: [] };
  const idle: ProjectNode = { key: "i", kind: "topic", label: "I", topicId: "i", status: "paused", children: [] };
  const empty: ProjectNode = { key: "e", kind: "topic", label: "E", topicId: "e", children: [] };

  eq(projectTreeGroupDotStatus([idle, empty]), "", "an all-idle group aggregates to no dot");
  eq(projectTreeGroupDotStatus([running]), "streaming", "node.running folds into streaming, same as the rows");
  eq(projectTreeGroupDotStatus([idle, streaming, running]), "streaming", "the first qualifying member decides the visual");
  // Multi-group independence: each group aggregates only its own members.
  eq(projectTreeGroupDotStatus([idle]), "", "group B (idle) stays dotless while…");
  eq(projectTreeGroupDotStatus([streaming]), "streaming", "…group A (active) shows its own dot — groups are independent");
}

// ── render layer ─────────────────────────────────────────────────────────────

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.Element = dom.window.Element;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.PointerEvent = dom.window.MouseEvent as unknown as typeof PointerEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: false,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

const t = ((key: string) => key) as unknown as Translator;

const folder: ProjectNode = {
  key: "project-/repo",
  kind: "project",
  label: "Repo",
  root: "/repo",
  children: [],
};

function bindingsFor(groups: () => SessionGroup[]): ProjectTreeOrganizationBindings {
  return {
    ReorderTopics: async () => {},
    ListProjectGroups: async () => groups(),
    SaveSessionGroups: async () => {},
  };
}

function GroupHarness({ bindings, children }: { bindings: ProjectTreeOrganizationBindings; children: ProjectNode[] }) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => {},
    organizationRevision: 0,
    bindings,
  });
  return <ProjectTreeGroupRows
    folder={folder}
    children={children}
    depth={0}
    section="pinned"
    visible
    organization={organization}
    renderNode={() => null}
    t={t}
  />;
}

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

async function mount(groups: () => SessionGroup[], children: ProjectNode[]) {
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const render = async (nextChildren: ProjectNode[]) => {
    await act(async () => {
      root.render(<StrictMode><GroupHarness bindings={bindingsFor(groups)}>{nextChildren}</GroupHarness></StrictMode>);
      await flush();
    });
  };
  await render(children);
  return { dom, root, render };
}

function groupRow(index: number): Element | null {
  return document.querySelectorAll(".project-tree__group")[index] ?? null;
}

async function clickToggle(row: Element | null) {
  const main = row?.querySelector(".project-tree__group-main");
  if (!main) throw new Error("missing group row");
  await act(async () => {
    (main as HTMLElement).click();
    await flush();
  });
}

{
  const groups: SessionGroup[] = [
    { id: "g-a", title: "Active group", topicIds: ["active"] },
    { id: "g-b", title: "Idle group", topicIds: ["idle"] },
  ];
  const activeMember: ProjectNode = { key: "t-a", kind: "topic", label: "A", root: "/repo", topicId: "active", status: "streaming", children: [] };
  const idleMember: ProjectNode = { key: "t-i", kind: "topic", label: "I", root: "/repo", topicId: "idle", status: "paused", children: [] };

  const { dom, root, render } = await mount(() => groups, [activeMember, idleMember]);

  // Acceptance 1 + 5: collapse a group with a running member → dot beside the
  // count; a second collapsed group without activity stays dotless.
  await clickToggle(groupRow(0));
  await clickToggle(groupRow(1));
  const dotsA = groupRow(0)?.querySelectorAll(".project-tree__group-state") ?? [];
  const dotsB = groupRow(1)?.querySelectorAll(".project-tree__group-state") ?? [];
  eq(dotsA.length, 1, "collapsed group with a streaming member shows exactly one dot");
  ok(dotsA[0]?.className.includes("project-tree__topic-state--streaming") ?? false, "the dot reuses the member-row topic-state visual (--streaming)");
  eq(dotsB.length, 0, "collapsed all-idle group shows no dot (no false positive)");
  ok((groupRow(0)?.querySelector(".project-tree__group-count")?.textContent ?? "") === "1", "count badge still renders beside the dot");

  // Acceptance 4 (mechanism): the same data update the rows consume clears the
  // group dot on the next render — no separate polling, no residue.
  const stillStreaming: ProjectNode = { ...activeMember, status: "streaming" };
  await render([stillStreaming, idleMember]);
  eq(groupRow(0)?.querySelectorAll(".project-tree__group-state").length ?? -1, 1, "still streaming keeps the dot after re-render");
  const finished: ProjectNode = { ...activeMember, status: undefined };
  await render([finished, idleMember]);
  eq(groupRow(0)?.querySelectorAll(".project-tree__group-state").length ?? -1, 0, "the dot disappears as soon as the member finishes");

  // Acceptance 3: expanded rows show no group-level dot (member rows own the
  // visual there; this component renders no member rows itself in this harness).
  await clickToggle(groupRow(0));
  eq(groupRow(0)?.querySelectorAll(".project-tree__group-state").length ?? -1, 0, "expanded group header shows no dot (rows unchanged)");

  await act(async () => root.unmount());
  dom.window.close();
}

// Acceptance 2 (no false positive with no members at all).
{
  const groups: SessionGroup[] = [{ id: "g-empty", title: "Empty", topicIds: [] }];
  const { dom, root } = await mount(() => groups, []);
  await clickToggle(groupRow(0));
  eq(groupRow(0)?.querySelectorAll(".project-tree__group-state").length ?? -1, 0, "empty collapsed group shows no dot");
  await act(async () => root.unmount());
  dom.window.close();
}

assert.ok(passed >= 12, `expected at least 12 checks, got ${passed}`);
process.stdout.write(`\nproject-tree-group-activity: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
