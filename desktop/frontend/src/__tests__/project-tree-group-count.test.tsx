// Run: tsx src/__tests__/project-tree-group-count.test.tsx
//
// Task 313 — 分组行去缩进对齐 + 折叠态活跃数数字:
//   the collapsed group header sits on the project-folder icon column
//   (paddingLeft rewritten to the folder row's formula), and the activity dot
//   is joined by a live count of the members it stands for — same topicStatus
//   pipeline, count and dot born and dead together, expanded rows untouched.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import React, { StrictMode } from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { ProjectTreeGroupRows, useProjectTreeOrganization } from "../components/ProjectTreeOrganization";
import { projectTreeGroupActiveCount, projectTreeGroupDotStatus } from "../lib/projectTreeTopic";
import type { Translator } from "../lib/i18n";
import type { ProjectNode, ProjectTreeOrganizationBindings, SessionGroup } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

// ── lib: the count is the real number of dot-bearing members ────────────────
console.log("\nproject tree group count (task 313)");

const running = (id: string): ProjectNode => ({ key: id, kind: "topic", label: id, topicId: id, running: true, children: [] });
const streaming = (id: string): ProjectNode => ({ key: id, kind: "topic", label: id, topicId: id, status: "streaming", children: [] });
const waiting = (id: string): ProjectNode => ({ key: id, kind: "topic", label: id, topicId: id, status: "waiting_confirmation", children: [] });
const idle = (id: string): ProjectNode => ({ key: id, kind: "topic", label: id, topicId: id, status: "paused", children: [] });

eq(projectTreeGroupActiveCount([running("a")]), 1, "one running member counts 1");
eq(projectTreeGroupActiveCount([running("a"), streaming("b")]), 2, "two active members count 2");
eq(projectTreeGroupActiveCount([running("a"), streaming("b"), waiting("c")]), 3, "three active members count 3");
eq(projectTreeGroupActiveCount([idle("a"), idle("b")]), 0, "all idle counts 0");
eq(projectTreeGroupActiveCount([]), 0, "an empty group counts 0");
// The dot and the count share one predicate: count > 0 ⇔ dot exists.
for (const members of [[running("a")], [idle("a")], [running("a"), idle("b")], []]) {
  const count = projectTreeGroupActiveCount(members);
  const dot = projectTreeGroupDotStatus(members);
  eq((count > 0) === Boolean(dot), true, `dot ⇔ count consistency for ${members.length} member(s)`);
}

// ── source guards: folder/project indentation untouched, group rewritten ────
{
  const thisFile = fileURLToPath(import.meta.url);
  const treeSource = readFileSync(resolve(dirname(thisFile), "../components/ProjectTree.tsx"), "utf8");
  const orgSource = readFileSync(resolve(dirname(thisFile), "../components/ProjectTreeOrganization.tsx"), "utf8");
  ok(treeSource.includes('className="project-tree__folder-main"\n            style={{ paddingLeft: 8 + depth * 16 }}'),
    "project folder rows keep their own indentation formula (zero regression)");
  ok(/style=\{\{ paddingLeft: 14 \+ depth \* 16 \}\}/.test(treeSource),
    "member/topic rows keep 14 + depth * 16 (expanded children do not move)");
  // Task 350: the base formula stays the task-313 folder column; a nested
  // group rides one extra 16px step on top of it.
  ok(orgSource.includes("style={{ paddingLeft: 8 + (depth - 1) * 16 + (nested ? 16 : 0) }}"),
    "the group header keeps the task-313 folder column (task 350 adds a nested step on top)");
  ok(orgSource.includes("depth={depth + 1}") === false || treeSource.includes("depth={depth + 1}"),
    "the caller still passes folder depth + 1 — member rows keep their level");
}

// ── render: count badge + alignment ─────────────────────────────────────────
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

function GroupHarness({ bindings, children, depth }: { bindings: ProjectTreeOrganizationBindings; children: ProjectNode[]; depth: number }) {
  const organization = useProjectTreeOrganization({
    tree: [folder],
    refresh: async () => {},
    organizationRevision: 0,
    bindings,
  });
  return <ProjectTreeGroupRows
    folder={folder}
    children={children}
    depth={depth}
    section="pinned"
    visible
    organization={organization}
    renderNode={() => null}
    t={t}
  />;
}

async function mount(groups: () => SessionGroup[], children: ProjectNode[], depth = 1) {
  const dom = installDom();
  const root = createRoot(document.getElementById("root")!);
  const render = async (next: { groups?: () => SessionGroup[]; children?: ProjectNode[]; depth?: number } = {}) => {
    await act(async () => {
      root.render(
        <StrictMode>
          <GroupHarness bindings={bindingsFor(next.groups ?? groups)} depth={next.depth ?? depth}>
            {next.children ?? children}
          </GroupHarness>
        </StrictMode>,
      );
      await new Promise((r) => setTimeout(r, 0));
    });
  };
  await render();
  return { dom, root, render };
}

async function clickToggle(row: Element | null) {
  const main = row?.querySelector(".project-tree__group-main");
  if (!main) throw new Error("missing group row");
  await act(async () => { (main as HTMLElement).click(); await new Promise((r) => setTimeout(r, 0)); });
}

const groupRow = () => document.querySelector(".project-tree__group");
const badge = () => groupRow()?.querySelector(".project-tree__group-active-count");
const dot = () => groupRow()?.querySelector(".project-tree__group-state");

{
  const groups: SessionGroup[] = [{ id: "g", title: "G", topicIds: ["a", "b", "c", "d"] }];
  const threeActive = [running("a"), streaming("b"), waiting("c"), idle("d")];
  const { dom, root, render } = await mount(() => groups, threeActive, 1);
  await clickToggle(groupRow());

  // Acceptance 2 + 3: a collapsed group with 3 active members shows dot + "3".
  ok(dot() !== null, "collapsed active group shows the dot");
  eq(badge()?.textContent ?? null, "3", "the badge shows the real count (3 active of 4)");
  // Alignment: depth (folder depth + 1) = 1 → 8 + (1-1)*16 = 8px, the folder row's column.
  eq((groupRow()?.querySelector(".project-tree__group-main") as HTMLElement).style.paddingLeft, "8px",
    "the group header sits on the project-folder icon column (8px at folder depth 0)");

  // Real counts: drop one active member → 2.
  await render({ children: [running("a"), streaming("b"), idle("c"), idle("d")] });
  eq(badge()?.textContent ?? null, "2", "two active members show 2");
  await render({ children: [running("a"), idle("b"), idle("c"), idle("d")] });
  eq(badge()?.textContent ?? null, "1", "one active member shows 1");

  // Acceptance 2: all idle → dot and badge die together.
  await render({ children: [idle("a"), idle("b"), idle("c"), idle("d")] });
  eq(dot(), null, "all idle removes the dot");
  eq(badge(), null, "all idle removes the badge with it");

  // Alignment at a deeper folder.
  await render({ children: threeActive, depth: 2 });
  eq((groupRow()?.querySelector(".project-tree__group-main") as HTMLElement).style.paddingLeft, "24px",
    "depth 2 lands on 8 + (2-1)*16 = 24px — the formula tracks the folder row");

  // Acceptance 4: expanded shows neither (rows own their indicators).
  await act(async () => { (groupRow()?.querySelector(".project-tree__group-main") as HTMLElement).click(); await new Promise((r) => setTimeout(r, 0)); });
  eq(dot(), null, "expanded group header shows no dot (312 zero regression)");
  eq(badge(), null, "expanded group header shows no badge");

  await act(async () => root.unmount());
  dom.window.close();
}

assertNoFailure();
function assertNoFailure() {
  ok(passed >= 16, `expected at least 16 checks, got ${passed}`);
  process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
  if (failed > 0) process.exit(1);
}
