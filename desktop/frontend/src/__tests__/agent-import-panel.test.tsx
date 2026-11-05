import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { SkillsSettingsPage } from "../components/CapabilitiesPanel";
import { LocaleProvider } from "../lib/i18n";
import type { AppBindings } from "../lib/bridge";
import type { AgentImportRequestView, AgentImportScanView } from "../lib/types";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);

const scanFixture: AgentImportScanView = {
  sources: [
    { tool: "claude", detected: true, items: 1, paths: { command: "~/.claude/commands" } },
    { tool: "codex", detected: true, items: 1, paths: { mcp: "~/.codex/config.toml" } },
    { tool: "opencode", detected: false, note: "not detected", items: 0 },
  ],
  items: [
    {
      id: "claude:command:review",
      tool: "claude",
      kind: "command",
      name: "review",
      description: "Review code",
      sourcePath: "~/.claude/commands/review.md",
      preview: "---\ndescription: Review code\n---\n\nReview $ARGUMENTS.",
      conflict: false,
      nativelyActive: true,
      recommended: true,
    },
    {
      id: "codex:mcp:codegraph",
      tool: "codex",
      kind: "mcp",
      name: "codegraph",
      description: "codegraph serve --mcp",
      preview: '{\n  "name": "codegraph"\n}',
      conflict: true,
      nativelyActive: false,
      recommended: true,
    },
    {
      id: "opencode:mcp:broken",
      tool: "opencode",
      kind: "mcp",
      name: "broken",
      description: "will fail on import",
      preview: "{}",
      conflict: false,
      nativelyActive: false,
      recommended: true,
    },
  ],
};

const importRequests: AgentImportRequestView[][] = [];
let importReceipt: Record<string, unknown> | null = null;

const appBindings = {
  Meta: async () => ({ eventChannel: "agent:event", cwd: "/workspace" }),
  ListTabs: async () => [{ id: "tab-a", scope: "project", workspaceRoot: "/workspace", active: true }],
  SkillsSettings: async () => ({ skills: [], skillRoots: [], allowImplicitInvocation: true }),
  AgentImportScan: async () => JSON.parse(JSON.stringify(scanFixture)),
  AgentImportImport: async (requests: AgentImportRequestView[]) => {
    importRequests.push(requests);
    return (
      importReceipt ?? {
        imported: 1,
        skipped: 0,
        failed: 1,
        items: [
          { id: "codex:mcp:codegraph", tool: "codex", kind: "mcp", name: "codegraph", status: "imported", detail: "codegraph-from-codex" },
          { id: "opencode:mcp:broken", tool: "opencode", kind: "mcp", name: "broken", status: "failed", detail: "sink rejected the entry" },
        ],
        skillsRefreshed: true,
        commandsReloaded: true,
      }
    );
  },
} as unknown as AppBindings;

window.go = { main: { App: appBindings } };

const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");
const root = createRoot(rootEl);

function flushPromises(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    await act(async () => {
      await flushPromises();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}`);
}

function buttons(): HTMLButtonElement[] {
  return Array.from(rootEl.querySelectorAll("button"));
}

function buttonWithText(text: string): HTMLButtonElement {
  const hit = buttons().find((b) => (b.textContent ?? "").includes(text));
  if (!hit) throw new Error(`button ${JSON.stringify(text)} not found`);
  return hit;
}

// buttonWithExactText avoids substring traps (the section header's hint also
// contains the word "Scan").
function buttonWithExactText(text: string): HTMLButtonElement {
  const hit = buttons().find((b) => (b.textContent ?? "").trim() === text);
  if (!hit) throw new Error(`exact button ${JSON.stringify(text)} not found`);
  return hit;
}

async function click(button: HTMLButtonElement) {
  await act(async () => {
    button.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
    await flushPromises();
  });
}

function selects(): HTMLSelectElement[] {
  return Array.from(rootEl.querySelectorAll("select"));
}

await act(async () => {
  root.render(
    <LocaleProvider>
      <SkillsSettingsPage activeWorkspaceKey="tab-a\u0000/workspace" />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("skills page render", () => (rootEl.textContent ?? "").length > 0);

// 1. Open the collapsed import section.
await click(buttonWithText("Import from external tools"));
await waitFor("import body open", () => (rootEl.textContent ?? "").includes("Scan"));

// 2. Scan: sources include Claude Code detected and OpenCode not detected.
await click(buttonWithExactText("Scan"));
await waitFor("scan results", () => (rootEl.textContent ?? "").includes("Claude Code") && (rootEl.textContent ?? "").includes("Not detected"));
if (!(rootEl.textContent ?? "").includes("codegraph")) throw new Error("scanned item codegraph missing");
if (!(rootEl.textContent ?? "").includes("Name in use")) throw new Error("conflict badge missing");
if (!(rootEl.textContent ?? "").includes("Already discovered natively")) throw new Error("natively-active badge missing");

// 3. Default selection: the natively-active claude item and the conflicting
// codex item start unchecked; only the plain opencode item is selected.
const checkboxes = () => Array.from(rootEl.querySelectorAll("input[type=\"checkbox\"]")) as HTMLInputElement[];
if (checkboxes().length < 3) throw new Error(`expected 3 item checkboxes, got ${checkboxes().length}`);
const checkedCount = checkboxes().filter((c) => c.checked && c.type === "checkbox").length;
// The implicit-invocation switch on the skills page is also a checkbox; count
// only the three import ones (all inside .agent-import).
const importChecks = Array.from(rootEl.querySelectorAll(".agent-import input[type=\"checkbox\"]")) as HTMLInputElement[];
const defaultChecked = importChecks.filter((c) => c.checked).length;
if (defaultChecked !== 1) throw new Error(`expected exactly 1 default-checked import item, got ${defaultChecked}`);
const importSelectedCount = importChecks.filter((c) => c.checked === false).length;
if (importChecks.length !== 3 || importSelectedCount !== 2) throw new Error("import checkbox setup wrong");

// 4. Check the conflicting item: a policy selector must appear.
const codegraphRow = Array.from(rootEl.querySelectorAll(".agent-import__item")).find((row) => (row.textContent ?? "").includes("codegraph"));
if (!codegraphRow) throw new Error("codegraph row missing");
const codegraphCheck = codegraphRow.querySelector("input[type=\"checkbox\"]") as HTMLInputElement;
await act(async () => {
  codegraphCheck.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true }));
  await flushPromises();
});
await waitFor("policy select", () => selects().length === 1);
const policy = selects()[0];
await act(async () => {
  policy.value = "rename";
  policy.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await flushPromises();
});

// 5. Expand a preview and assert the converted content is shown.
const previewToggle = Array.from(rootEl.querySelectorAll(".agent-import__preview-toggle")).find((b) => (b.textContent ?? "").includes("Preview"));
if (!previewToggle) throw new Error("preview toggle missing");
await click(previewToggle as HTMLButtonElement);
await waitFor("preview content", () => (rootEl.textContent ?? "").includes("Review $ARGUMENTS."));

// 6. Import: the request must carry the chosen conflict policy and the default.
const importButton = buttonWithText("Import selected");
await click(importButton);
await waitFor("import receipt", () => (rootEl.textContent ?? "").includes("failed 1"));
if (importRequests.length !== 1) throw new Error(`expected one import call, got ${importRequests.length}`);
const sent = importRequests[0];
const byID = new Map(sent.map((r) => [r.id, r.policy]));
if (byID.get("codex:mcp:codegraph") !== "rename") throw new Error(`codegraph policy = ${byID.get("codex:mcp:codegraph")}`);
if (byID.get("opencode:mcp:broken") !== "skip") throw new Error(`broken policy = ${byID.get("opencode:mcp:broken")}`);
if (byID.has("claude:command:review")) throw new Error("unchecked natively-active item must not be sent");

// 7. Failures are expandable with per-item reasons.
const failuresButton = buttonWithText("Failed items (1)");
await click(failuresButton);
await waitFor("failure detail", () => (rootEl.textContent ?? "").includes("sink rejected the entry"));

// 8. Rescan after import keeps the panel usable.
await click(buttonWithExactText("Scan"));
await waitFor("rescan done", () => selects().length === 0);

await act(async () => {
  root.unmount();
  await flushPromises();
});
console.log("agent import panel: passed");
