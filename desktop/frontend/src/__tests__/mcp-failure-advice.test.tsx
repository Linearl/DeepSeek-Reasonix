// Run: tsx --tsconfig ../../tsconfig.tsx-runner.json src/__tests__/mcp-failure-advice.test.tsx
// tl1481 — MCP failure-advice popover: the failed status chip drills down
// three levels (chip → advice popover → raw error details), the advice copy
// maps off the EXISTING failure taxonomy (CapabilitiesPanel.failureKind),
// and the pre-existing status display stays byte-for-byte the same.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { StatusBar } from "../components/StatusBar";
import { LocaleProvider } from "../lib/i18n";
import { failureKind } from "../components/CapabilitiesPanel";
import { DEFAULT_STATUS_BAR_ITEMS, normalizeStatusBarItems } from "../lib/statusBarItems";
import { useMcpStatusStore } from "../store/mcpStatus";
import type { ServerView } from "../lib/types";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function server(partial: Partial<ServerView>): ServerView {
  return {
    name: partial.name ?? "srv",
    transport: "stdio",
    status: "connected",
    autoStart: true,
    ...partial,
  } as ServerView;
}

function settle(ms = 30): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

console.log("\nmcp failure advice: mapping reuses the existing failure taxonomy");

{
  // Same fixtures CapabilitiesPanel's failureKind contract already speaks:
  // auth first, then config/command/network faces, then the unclassified rest.
  ok(failureKind(server({ status: "failed", authStatus: "required" })) === "auth", "authStatus required classifies as auth");
  ok(failureKind(server({ status: "failed", error: "command is required" })) === "missing-command", "missing command classifies as missing-command");
  ok(failureKind(server({ status: "failed", error: "launch failure: command not found" })) === "command-unavailable", "command not found classifies as command-unavailable");
  ok(failureKind(server({ status: "failed", error: "dial tcp: connect ECONNREFUSED 127.0.0.1:8080" })) === "network", "ECONNREFUSED classifies as network");
  ok(failureKind(server({ status: "failed", error: "boom" })) === "other", "unclassified error stays other");
  ok(DEFAULT_STATUS_BAR_ITEMS.includes("mcp") && normalizeStatusBarItems(["mcp"]).join(",") === "mcp", "mcp stays a default status item (registry untouched)");
}

// Everything below renders the real component tree. jsdom + createRoot is
// required on purpose (zustand snapshots are client-side state); jsdom is
// imported BEFORE react-dom/client per the harness order convention.
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({ matches: true, addEventListener() {}, removeEventListener() {} }),
});

let refreshCalls = 0;
useMcpStatusStore.setState({
  refresh: () => {
    refreshCalls += 1;
    return Promise.resolve();
  },
});

let rootRef: Root | null = null;
async function renderChip(onOpenMcp?: () => void): Promise<string> {
  if (!rootRef) {
    rootRef = createRoot(document.getElementById("root")!);
  }
  const root = rootRef;
  await act(async () => {
    root.render(
      <LocaleProvider>
        <StatusBar
          context={{ used: 0, window: 0, sessionTokens: 0 }}
          running={false}
          items={["workspace", "mcp"]}
          onOpenMcp={onOpenMcp}
        />
      </LocaleProvider>,
    );
  });
  return document.getElementById("root")!.innerHTML;
}

function advicePopover(): Element | null | undefined {
  return document.querySelector(".mcp-advice-popover");
}

console.log("\nmcp failure advice: level 1 — the chip display is unchanged");

{
  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a", status: "failed", error: "boom" })] }); });
  const html = await renderChip();
  ok(html.includes('data-statusbar-item="mcp"'), "failed chip keeps its status-bar slot");
  ok(html.includes("stat statusbar__mcp statusbar__mcp--failed"), "failed chip keeps the exact display classes");
  ok(html.includes("MCP failed 1"), "failed chip keeps the failure count copy");
  ok(html.includes("<button"), "failed chip stays a button");
  ok(advicePopover() == null, "no advice popover before the click");
}

console.log("\nmcp failure advice: level 2 — chip click opens the advice popover");

{
  let opened = false;
  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a", status: "failed", error: "boom" })] }); });
  await renderChip(() => { opened = true; });
  await act(async () => { failedButton()?.click(); });
  const popover = advicePopover();
  ok(popover != null, "chip click opens the advice popover");
  ok(popover?.getAttribute("role") === null && popover?.querySelector("[role='dialog']") != null, "popover hosts a dialog section");
  ok(popover?.querySelector("[aria-label='MCP failure advice (1)']") != null, "dialog is labelled with the advice title and count");
  ok(!opened, "chip click does not open the management page directly");
  ok(popover?.textContent?.includes("a") === true, "popover lists the failed server by name");
  ok(popover?.textContent?.includes("Other failure") === true, "unclassified failure shows the other-failure kind label");
  ok(popover?.textContent?.includes("Cause not classified") === true, "popover shows the mapped suggestion for the failure kind");
  const managerButton = popover?.querySelector<HTMLButtonElement>(".mcp-advice-popover__footer button");
  ok(managerButton?.textContent === "Open management panel", "popover footer keeps the management page reachable");
  await act(async () => { managerButton?.click(); });
  ok(opened, "popover footer still opens the MCP management page");
  await settle();
  ok(advicePopover() == null, "opening the manager closes the advice popover");

  await act(async () => { failedButton()?.click(); });
  ok(advicePopover() != null, "chip click reopens the advice popover");
  await act(async () => { failedButton()?.click(); });
  await settle();
  ok(advicePopover() == null, "clicking the chip again toggles the advice popover closed");
}

console.log("\nmcp failure advice: level 3 — error details one hop below");

{
  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "net", status: "failed", error: "dial tcp: connect ECONNREFUSED 127.0.0.1:8080" })] }); });
  await renderChip();
  await act(async () => { failedButton()?.click(); });
  const popover = advicePopover();
  ok(popover?.textContent?.includes("Connection failure") === true, "network failure shows the connection-failure kind label");
  ok(popover?.textContent?.includes("Check the network and proxy") === true, "network failure shows the mapped suggestion");
  ok(popover?.querySelector("pre") == null, "raw error stays hidden on level 2");
  const detailsButton = popover?.querySelector<HTMLButtonElement>(".mcp-advice-popover__detail-actions button");
  ok(detailsButton?.textContent === "Error details" && detailsButton?.getAttribute("aria-expanded") === "false", "details toggle starts collapsed");
  await act(async () => { detailsButton?.click(); });
  const pre = advicePopover()?.querySelector("pre");
  ok(pre != null && pre.textContent === "dial tcp: connect ECONNREFUSED 127.0.0.1:8080", "level 3 reveals the raw error verbatim");
  ok(advicePopover()?.querySelector<HTMLButtonElement>(".mcp-advice-popover__detail-actions button")?.getAttribute("aria-expanded") === "true", "details toggle reports expanded");
  ok(advicePopover()?.textContent?.includes("Raw log") === true, "level 3 keeps the raw-log label");
  await act(async () => { advicePopover()?.querySelector<HTMLButtonElement>(".mcp-advice-popover__detail-actions button")?.click(); });
  ok(advicePopover()?.querySelector("pre") == null, "collapsing hides the raw error again");
  await act(async () => {
    window.dispatchEvent(new window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  });
  await settle();
  ok(advicePopover() == null, "Escape closes the advice popover");
}

console.log("\nmcp failure advice: mapping — all five kinds render their own advice");

{
  await act(async () => {
    useMcpStatusStore.setState({
      servers: [
        server({ name: "a-auth", status: "failed", authStatus: "required" }),
        server({ name: "b-cfg", status: "failed", error: "command is required" }),
        server({ name: "c-tool", status: "failed", error: "command not found" }),
        server({ name: "d-net", status: "failed", error: "connect ECONNREFUSED" }),
        server({ name: "e-odd", status: "failed", error: "boom" }),
      ],
    });
  });
  const html = await renderChip();
  ok(html.includes("MCP failed 5"), "chip counts all five failures");
  await act(async () => { failedButton()?.click(); });
  const popover = advicePopover();
  ok(popover?.querySelector("[aria-label='MCP failure advice (5)']") != null, "advice title carries the full count");
  const text = popover?.textContent ?? "";
  ok(text.includes("Authentication") && text.includes("authenticate this server again"), "auth kind maps to its own label and suggestion");
  ok(text.includes("Config error") && text.includes("set command or URL"), "missing-command kind maps to its own label and suggestion");
  ok(text.includes("Executable missing") && text.includes("install it, or point command at a full path"), "command-unavailable kind maps to its own label and suggestion");
  ok(text.includes("Connection failure") && text.includes("Check the network and proxy"), "network kind maps to its own label and suggestion");
  ok(text.includes("Other failure") && text.includes("Cause not classified"), "other kind maps to its own label and suggestion");
  const listHTML = popover?.querySelector(".mcp-advice-popover__list")?.innerHTML ?? "";
  ok(listHTML.indexOf("a-auth") >= 0 && listHTML.indexOf("a-auth") < listHTML.indexOf("e-odd"), "failed servers list in stable name order");
  ok((popover?.querySelectorAll(".mcp-advice-popover__item")?.length ?? 0) === 5, "one advice item per failed server");
}

console.log("\nmcp failure advice: zero-change pins on the pre-existing display");

{
  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a" }), server({ name: "b", status: "initializing" })] }); });
  const connectingHTML = await renderChip();
  ok(connectingHTML.includes("statusbar__mcp-spinner"), "connecting chip keeps the spinner treatment");
  ok(connectingHTML.includes("connecting 1/2"), "connecting chip keeps the n/N copy");
  ok(!connectingHTML.includes("statusbar__mcp--failed"), "connecting chip keeps no failed treatment");
  ok(advicePopover() == null, "connecting state never renders the advice popover");

  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a", status: "failed" })] }); });
  await renderChip();
  await act(async () => { failedButton()?.click(); });
  ok(advicePopover() != null, "popover open before the fleet settles");
  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a" })] }); });
  await renderChip();
  await settle();
  ok(advicePopover() == null, "a settled fleet closes the advice popover");
  const settledHTML = document.getElementById("root")!.innerHTML;
  ok(!settledHTML.includes('data-statusbar-item="mcp"'), "settled fleet frees the slot exactly as before");
}

if (rootRef) {
  await act(async () => { rootRef!.unmount(); });
}
useMcpStatusStore.setState({ servers: [] });
dom.window.close();

function failedButton(): HTMLButtonElement | null {
  return document.querySelector<HTMLButtonElement>("button.statusbar__mcp--failed");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
