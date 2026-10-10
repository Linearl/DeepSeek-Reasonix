// Run: tsx src/__tests__/mcp-status-item.test.tsx
// Task 559 — MCP connection chip: aggregation rules, the diff-gated store
// publish, the item registry wiring, and the chip's render states.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { StatusBar } from "../components/StatusBar";
import { LocaleProvider } from "../lib/i18n";
import { mcpServersSignature, summarizeMcpServers } from "../lib/mcpStatus";
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

console.log("\nmcp status chip: aggregation");

{
  ok(summarizeMcpServers([]).state === "hidden", "no servers hides the chip");
  ok(summarizeMcpServers([server({}), server({ name: "b" })]).state === "hidden", "all connected hides the chip");
  ok(summarizeMcpServers([server({}), server({ name: "deferred", status: "deferred" }), server({ name: "off", status: "disabled" })]).state === "hidden", "deferred and disabled servers stay out of scope");
  const connecting = summarizeMcpServers([server({}), server({ name: "b", status: "initializing" })]);
  ok(connecting.state === "connecting" && connecting.connecting === 1 && connecting.connected === 1 && connecting.total === 2, "initializing server yields connecting 1 of 2");
  const runtime = summarizeMcpServers([server({ name: "b", status: "unknown" as ServerView["status"], runtimeState: "connecting" })]);
  ok(runtime.state === "connecting" && runtime.connecting === 1 && runtime.total === 1, "runtimeState connecting counts without a settled status");
  const failed = summarizeMcpServers([server({ name: "a", status: "failed" }), server({ name: "b", status: "initializing" })]);
  ok(failed.state === "failed" && failed.failed === 1, "failed takes precedence over connecting");
}

console.log("\nmcp status chip: snapshot signature");

{
  const base = [server({ name: "a" })];
  ok(mcpServersSignature(base) === mcpServersSignature([server({ name: "a" })]), "equal snapshots share a signature");
  ok(mcpServersSignature(base) !== mcpServersSignature([server({ name: "a", status: "initializing" })]), "status change moves the signature");
}

console.log("\nmcp status chip: store publish diff gate");

{
  const before = useMcpStatusStore.getState().servers;
  useMcpStatusStore.getState().publish([server({ name: "a" }), server({ name: "b", status: "failed" })]);
  const after = useMcpStatusStore.getState().servers;
  ok(after !== before && after.length === 2, "publish stores a changed snapshot");
  useMcpStatusStore.getState().publish([server({ name: "a" }), server({ name: "b", status: "failed" })]);
  ok(useMcpStatusStore.getState().servers === after, "publishing an unchanged snapshot keeps the state reference (no re-render, no flicker)");
}

console.log("\nmcp status chip: item registry");

{
  ok(DEFAULT_STATUS_BAR_ITEMS.includes("mcp"), "mcp is a default configurable status item");
  ok(normalizeStatusBarItems(["workspace", "mcp"]).join(",") === "workspace,mcp", "mcp survives normalization");
  ok(!normalizeStatusBarItems(["workspace", "cache"]).includes("mcp"), "a saved list without mcp keeps the user's explicit choice");
}

// Everything below renders the real component tree. jsdom + createRoot is
// required on purpose: renderToStaticMarkup would read zustand's server
// snapshot (the initial state) and never see the snapshots under test.
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
Object.defineProperty(window, "matchMedia", {
  configurable: true,
  value: () => ({ matches: true, addEventListener() {}, removeEventListener() {} }),
});

// Stub the store's bridge read so the test snapshot is authoritative and no
// polling/refresh hits the mock bridge during assertions.
let refreshCalls = 0;
useMcpStatusStore.setState({
  refresh: () => {
    refreshCalls += 1;
    return Promise.resolve();
  },
});

let rootRef: Root | null = null;
async function renderChip(items: string[], onOpenMcp?: () => void): Promise<string> {
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
          items={items}
          onOpenMcp={onOpenMcp}
        />
      </LocaleProvider>,
    );
  });
  return document.getElementById("root")!.innerHTML;
}

console.log("\nmcp status chip: render states");

{
  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a" }), server({ name: "b", status: "initializing" })] }); });
  const html = await renderChip(["workspace", "mcp"]);
  ok(html.includes("statusbar__mcp-spinner"), "connecting chip renders the spinner");
  ok(html.includes("connecting 1/2"), "connecting chip reads n/N in the default locale");
  ok(html.includes('data-statusbar-item="mcp"'), "connecting chip occupies its configured slot");
  ok(!html.includes("statusbar__mcp--failed"), "connecting chip does not render the failed treatment");

  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a" }), server({ name: "b", status: "initializing" })] }); });
  const stable = await renderChip(["workspace", "mcp"]);
  ok(stable.includes("connecting 1/2"), "chip state is stable across renders within one refresh cycle");

  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a" }), server({ name: "b" })] }); });
  const allConnected = await renderChip(["workspace", "mcp"]);
  ok(!allConnected.includes('data-statusbar-item="mcp"'), "fully connected fleet leaves the status bar untouched");

  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a" }), server({ name: "b", status: "failed" })] }); });
  const failedHtml = await renderChip(["workspace", "mcp"]);
  ok(failedHtml.includes("statusbar__mcp--failed"), "failed chip uses the warning treatment");
  ok(failedHtml.includes("MCP failed 1"), "failed chip counts failed servers");
  ok(failedHtml.includes("<button"), "failed chip is clickable");

  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a", status: "failed" })] }); });
  const offHtml = await renderChip(["workspace"]);
  ok(!offHtml.includes('data-statusbar-item="mcp"'), "turning the item off in settings removes the chip");
}

console.log("\nmcp status chip: interactive click-through");

{
  let opened = false;
  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a", status: "failed" })] }); });
  const html = await renderChip(["mcp"], () => { opened = true; });
  ok(refreshCalls >= 1, "chip refreshes the snapshot when it becomes visible");
  const failedButton = document.querySelector<HTMLButtonElement>("button.statusbar__mcp--failed");
  ok(failedButton != null && html.includes("<button"), "failed chip renders as a button");
  await act(async () => { failedButton?.click(); });
  ok(opened, "clicking the failed chip opens the MCP management page");

  await act(async () => { useMcpStatusStore.setState({ servers: [server({ name: "a" })] }); });
  const settled = await renderChip(["mcp"], () => { opened = true; });
  ok(!settled.includes("statusbar__mcp--failed"), "chip disappears once the fleet settles, without a settings change");
  ok(!settled.includes('data-statusbar-item="mcp"'), "settled chip frees its slot entirely");
}

if (rootRef) {
  await act(async () => { rootRef!.unmount(); });
}
useMcpStatusStore.setState({ servers: [] });
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
