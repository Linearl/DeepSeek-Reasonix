import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { useComposerInsertOwner, type ComposerInsertOwnerInput } from "../app-runtime/useComposerInsert";
import type { Translator } from "../lib/i18n";

const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, { window: dom.window, document: dom.window.document, IS_REACT_ACT_ENVIRONMENT: true });
const root = createRoot(document.getElementById("root")!);

const t = ((key: string) => key) as Translator;
const toasts: string[] = [];

const terminalReads: string[] = [];
const ports: ComposerInsertOwnerInput["ports"] = {
  terminalOutput: async (tabId, sessionId) => {
    terminalReads.push(`${tabId}:${sessionId}`);
    if (sessionId === "term-boom") throw new Error("bridge down");
    return sessionId === "term-empty" ? "   " : "last output";
  },
};

let states!: ReturnType<typeof useComposerInsertOwner>;
function Probe({ approval }: { approval?: { id: string; tool: string } | null }) {
  states = useComposerInsertOwner({
    activeTabId: "A",
    approval,
    t,
    showToast: (message) => { toasts.push(message); },
    ports,
  });
  return null;
}
const paint = (approval?: { id: string; tool: string } | null) =>
  act(async () => root.render(<Probe approval={approval} />));

try {
  await paint();
  await act(async () => { states.addWorkspaceTextToComposer("hello"); });
  assert.equal(states.composerInsertRequest?.text, "hello", "plain workspace text lands in the composer");
  assert.equal(states.composerInsertRequest?.mode, undefined, "plain insert keeps the default append mode");

  await act(async () => { states.prefillSubagentCommand("/run tests"); });
  assert.equal(states.composerInsertRequest?.mode, "prefix", "subagent prefill uses prefix mode");

  await act(async () => { states.insertQuickCommand("/quick"); });
  assert.equal(states.composerInsertRequest?.mode, "insert", "quick commands insert at the caret");
  await act(async () => { states.insertQuickCommand(""); });
  assert.equal(states.composerInsertRequest?.text, "/quick", "empty quick commands insert nothing");

  await act(async () => { states.replaceComposerInsert("B", "restored prompt"); });
  assert.equal(states.composerInsertRequest?.text, "/quick", "a replace insert only touches its own tab");
  await act(async () => { states.replaceComposerInsert("A", ""); });
  assert.equal(states.composerInsertRequest?.mode, "replace", "undo clears through a replace insert");
  assert.equal(states.composerInsertRequest?.text, "", "the replace insert carries the restored text");

  await act(async () => { states.addSelectedTextToComposer("  snippet  "); });
  assert.equal(states.selectedTextRequest?.text, "snippet", "selected text is trimmed before insert");
  await act(async () => { states.addSelectedTextToComposer("   "); });
  assert.equal(states.selectedTextRequest?.text, "snippet", "blank selections insert nothing");

  await act(async () => { states.addWorkspaceCodeToComposer("src/a.ts", "const a = 1;"); });
  assert.equal(states.selectedTextRequest?.path, "src/a.ts", "workspace code carries its path");
  await act(async () => { states.addWorkspaceCodeToComposer("src/skip.ts", "  "); });
  assert.equal(states.selectedTextRequest?.path, "src/a.ts", "blank code inserts nothing");

  await act(async () => { states.handleRevisionActiveChange(true); });
  await paint({ id: "ap-1", tool: "exit_plan_mode" });
  await act(async () => { states.addWorkspaceTextToComposer("revise this"); });
  assert.equal(states.activePlanRevisionInsertRequest?.text, "revise this", "plan-revision target routes plain text to the revision input");
  assert.equal(states.composerInsertRequest?.mode, "replace", "plan-revision routing does not touch the composer");
  await act(async () => { states.addWorkspaceCodeToComposer("src/b.ts", "code"); });
  assert.equal(states.activePlanRevisionInsertRequest?.text?.includes("src/b.ts"), true, "code lands in the revision input as a fenced reference");

  await paint({ id: "ap-2", tool: "exit_plan_mode" });
  assert.equal(states.activePlanRevisionInsertRequest, null, "a replacement approval id invalidates the pending revision insert");

  await act(async () => { states.handleRevisionActiveChange(false); });
  await act(async () => { states.addWorkspaceTextToComposer("back to composer"); });
  assert.equal(states.composerInsertRequest?.text, "back to composer", "leaving the revision target routes back to the composer");

  await paint(null);
  await act(async () => { await states.addTerminalOutputToComposer("term-9"); });
  assert.deepEqual(terminalReads, ["A:term-9"], "terminal output reads through the bridge port");
  assert.equal(states.composerInsertRequest?.text?.includes("last output"), true, "terminal output is formatted into the composer");

  await act(async () => { await states.addTerminalOutputToComposer("term-empty"); });
  assert.deepEqual(toasts, ["terminal.noOutput"], "blank terminal output reports once");

  await act(async () => { await states.addTerminalOutputToComposer("term-boom"); });
  assert.deepEqual(toasts, ["terminal.noOutput", "bridge down"], "a failed terminal read reports the error");

  await act(async () => { states.setInsertTarget("composer"); });
  await act(async () => { states.addWorkspaceTextToComposer("plain again"); });
  assert.equal(states.composerInsertRequest?.text, "plain again", "setInsertTarget resets the routing target");

  await act(async () => root.unmount());
  console.log("composer insert owner: routing, replace channel, plan-revision target, trimming and terminal port chains passed");
} finally { dom.window.close(); }
