// Run: pnpm tsx-runner src/__tests__/rewind-preflight-dialog.test.tsx
// (tsx --tsconfig tsconfig.tsx-runner.json)
// tl1476: rewind preflight dialog — unsafe files keep the confirm button
// disabled with the reason visible, all-safe plans confirm in one screen, and
// every pre-existing rewind flow (conversation, coverage-warning, no files,
// presenter-less bridges) stays byte-identical.

import { JSDOM } from "jsdom";
import { useState } from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { useConfirmDialog } from "../components/ConfirmDialog";
import { LocaleProvider } from "../lib/i18n";
import { RewindPreflightList } from "../components/RewindPreflightList";
import { registerRewindPreflightPresenter, presentRewindPreflight } from "../lib/rewindPreflight";
import { commitRewindWithPreview } from "../lib/rewindCommit";
import type { RewindPreflightFile } from "../lib/rewindSafety";
import type { RewindPlanView } from "../lib/rewindTypes";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 20));
}

// ── part 1: the dialog renders the classification and gates its confirm ──────

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const unsafeFiles: RewindPreflightFile[] = [
  { path: "safe.txt", safe: true },
  { path: "outside.txt", safe: false, reasonKey: "outside_workspace" },
  { path: "deleted.txt", safe: false, reasonKey: "missing_payload" },
  { path: "locked.txt", safe: false, reasonKey: "permission_denied" },
];
const allSafeFiles: RewindPreflightFile[] = [{ path: "a.txt", safe: true }, { path: "b.txt", safe: true }];

type OpenRequest = { title: string; confirmDisabled: boolean; files: readonly RewindPreflightFile[] };
function DialogHarness({ openRef }: { openRef: { current: ((request: OpenRequest) => Promise<boolean>) | null } }) {
  const { confirm, dialog } = useConfirmDialog();
  const [result, setResult] = useState("pending");
  openRef.current = (request) => confirm({
    title: request.title,
    message: <RewindPreflightList files={request.files} />,
    confirmLabel: `Rewind ${request.files.filter((file) => file.safe).length} file(s)`,
    cancelLabel: "Cancel",
    wide: true,
    confirmDisabled: request.confirmDisabled,
  }).then((confirmed) => { setResult(String(confirmed)); return confirmed; });
  return (
    <>
      <button id="open-unsafe" type="button" onClick={() => void openRef.current!({ title: "unsafe", confirmDisabled: true, files: unsafeFiles })}>open unsafe</button>
      <button id="open-safe" type="button" onClick={() => void openRef.current!({ title: "safe", confirmDisabled: false, files: allSafeFiles })}>open safe</button>
      <output id="dialog-result">{result}</output>
      {dialog}
    </>
  );
}

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);
const openRef: { current: ((request: OpenRequest) => Promise<boolean>) | null } = { current: null };

await act(async () => {
  root.render(<LocaleProvider><DialogHarness openRef={openRef} /></LocaleProvider>);
  await flush();
});

// unsafe: confirm disabled, reasons visible, only cancel resolves
const openUnsafe = document.getElementById("open-unsafe") as HTMLButtonElement;
await act(async () => {
  openUnsafe.click();
  await flush();
});
let dialogButtons = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"));
let confirmButton = dialogButtons.find((button) => button.textContent?.includes("Rewind"));
let cancelButton = dialogButtons.find((button) => button.textContent === "Cancel");
ok(confirmButton?.disabled === true, "unsafe plan disables the confirm button");
ok(confirmButton?.getAttribute("disabled") === "", "disabled confirm is a real disabled attribute");
ok(document.querySelector(".reasonix-confirm-dialog")?.textContent?.includes("outside.txt") === true, "unsafe path visible in dialog");
ok(document.querySelector(".reasonix-confirm-dialog")?.textContent?.includes("Outside the workspace") === true, "outside-workspace reason visible");
ok(document.querySelector(".reasonix-confirm-dialog")?.textContent?.includes("Checkpoint payload deleted or expired") === true, "deleted-payload reason visible");
ok(document.querySelector(".reasonix-confirm-dialog")?.textContent?.includes("Insufficient permission to write this file") === true, "permission-denied reason visible");
ok(document.querySelector(".rewind-preflight__file--safe")?.textContent === "safe.txt", "safe files listed alongside unsafe ones");
await act(async () => {
  confirmButton?.click();
  await flush();
});
ok(document.getElementById("dialog-result")?.textContent === "pending", "clicking a disabled confirm resolves nothing");
await act(async () => {
  cancelButton?.click();
  await flush();
});
ok(document.getElementById("dialog-result")?.textContent === "false", "cancel resolves false on unsafe plan");

// all-safe: one-screen confirm, enabled button resolves true
const openSafe = document.getElementById("open-safe") as HTMLButtonElement;
await act(async () => {
  openSafe.click();
  await flush();
});
dialogButtons = Array.from(document.querySelectorAll<HTMLButtonElement>(".reasonix-confirm-dialog button"));
confirmButton = dialogButtons.find((button) => button.textContent?.includes("Rewind"));
ok(confirmButton?.disabled === false, "all-safe plan keeps confirm enabled");
ok(confirmButton?.textContent === "Rewind 2 file(s)", "confirm label counts the safe files");
ok(document.querySelectorAll(".rewind-preflight__file--unsafe").length === 0, "no unsafe group rendered for all-safe plans");
await act(async () => {
  confirmButton?.click();
  await flush();
});
ok(document.getElementById("dialog-result")?.textContent === "true", "confirm resolves true on all-safe plan");

await act(async () => { root.unmount(); });

// ── part 2: commitRewindWithPreview gating (zero-change guarantees) ──────────

const previewCalls: string[] = [];
const commitCalls: string[] = [];
let nextPlan: RewindPlanView = {};
function fakeApp() {
  return {
    PreviewRewindForTab: async (_tabID: string, turn: number, scope: string) => {
      previewCalls.push(`preview:${turn}:${scope}`);
      return { ...nextPlan };
    },
    CommitRewindForTab: async (tabID: string, planID: string, turn: number, scope: string) => {
      commitCalls.push(`commit:${tabID}:${planID}:${turn}:${scope}`);
      return { ok: true, transactionId: "tx-1" };
    },
  };
}
(dom.window as unknown as { go: unknown }).go = { main: { App: fakeApp() } };

// A: unsafe clean-path plan — presenter declines, commit never runs
nextPlan = {
  ok: true, planId: "plan-a", canFiles: true, files: ["safe.txt", "locked.txt"],
  fileSafety: [{ path: "safe.txt", safe: true }, { path: "locked.txt", safe: false, reason: "permission_denied" }],
};
let presenterCalls = 0;
registerRewindPreflightPresenter(async (request) => {
  presenterCalls += 1;
  return !request.files.some((file) => !file.safe);
});
let outcome = await commitRewindWithPreview("tabA", 3, "code");
ok(presenterCalls === 1, "unsafe plan opens the preflight dialog");
ok(commitCalls.length === 0, "declined preflight never commits");
ok(outcome.ok === false && outcome.error === "rewind cancelled", "declined preflight cancels the rewind");

// B: all-safe plan — confirm proceeds through the unchanged commit path
presenterCalls = 0;
nextPlan = {
  ok: true, planId: "plan-b", canFiles: true, files: ["a.txt"],
  fileSafety: [{ path: "a.txt", safe: true }],
};
outcome = await commitRewindWithPreview("tabA", 4, "code");
ok(presenterCalls === 1 && commitCalls.length === 1, "all-safe plan confirms in one screen then commits");
ok(commitCalls[0] === "commit:tabA:plan-b:4:code", "commit keeps the existing commitRewindForTab contract");
ok(outcome.ok === true, "confirmed all-safe rewind succeeds");

// C: no file scenario — dialog never opens (existing behavior untouched)
presenterCalls = 0;
nextPlan = { ok: true, planId: "plan-c", canFiles: true, canConversation: true };
outcome = await commitRewindWithPreview("tabA", 5, "code");
ok(presenterCalls === 0 && commitCalls.length === 2, "no-file rewind runs immediately without a dialog");
outcome = await commitRewindWithPreview("tabA", 6, "conversation");
ok(presenterCalls === 0 && commitCalls.length === 3, "conversation rewind never opens the preflight dialog");

// D: coverage-warning plan (legacy) — keeps the existing window.confirm flow
presenterCalls = 0;
const confirmLog: string[] = [];
(dom.window as unknown as { confirm: (message?: string) => boolean }).confirm = (message?: string) => {
  confirmLog.push(message ?? "");
  return true;
};
nextPlan = {
  ok: true, planId: "plan-d", canFiles: true, legacy: true, files: ["a.txt"],
  fileSafety: [{ path: "a.txt", safe: true }],
};
outcome = await commitRewindWithPreview("tabA", 7, "code");
ok(presenterCalls === 0, "legacy coverage plan skips the preflight dialog");
ok(confirmLog.length === 1 && commitCalls.length === 4, "legacy coverage plan keeps the existing window.confirm flow");
ok(outcome.ok === true, "legacy coverage rewind proceeds as before");

// E: per-file conflicts (canFiles=false) — dialog shows the blocked set, then
// the refusal result stays identical to the pre-preflight shape
presenterCalls = 0;
let presenterFiles: RewindPreflightFile[] = [];
registerRewindPreflightPresenter(async (request) => {
  presenterCalls += 1;
  presenterFiles = request.files;
  return false;
});
nextPlan = {
  ok: true, planId: "plan-e", canFiles: false, disabledReason: "file conflicts detected",
  files: ["x.txt"], conflicts: ["x.txt: path_unsafe"],
};
outcome = await commitRewindWithPreview("tabA", 8, "both");
ok(presenterCalls === 1 && presenterFiles.length === 1 && presenterFiles[0].reasonKey === "outside_workspace", "conflict-blocked plan classifies its unsafe file");
ok(commitCalls.length === 4, "conflict-blocked plan never commits");
ok(outcome.ok === false && outcome.error === "file conflicts detected", "refusal result unchanged after the dialog");

// F: presenter-less environment — rewind proceeds exactly as before
registerRewindPreflightPresenter(null);
nextPlan = {
  ok: true, planId: "plan-f", canFiles: true, files: ["locked.txt"],
  fileSafety: [{ path: "locked.txt", safe: false, reason: "permission_denied" }],
};
outcome = await commitRewindWithPreview("tabA", 9, "code");
ok(commitCalls.length === 5 && outcome.ok === true, "without a registered presenter the rewind proceeds unchanged");
const passthrough = await presentRewindPreflight({ turn: 1, scope: "code", files: unsafeFiles });
ok(passthrough === true, "presentRewindPreflight defaults to proceed without a presenter");

dom.window.close();
process.stdout.write(`rewind preflight dialog: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
