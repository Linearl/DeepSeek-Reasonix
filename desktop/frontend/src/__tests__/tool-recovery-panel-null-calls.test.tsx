// Run: tsx src/__tests__/tool-recovery-panel-null-calls.test.tsx
//
// Regression for the crash reported on the 19:43 build:
//
//   TypeError: Cannot read properties of null (reading 'length')
//       at ToolRecoveryPanel
//
// Cause: Go's nil slice marshals to JSON null, so a snapshot with no interrupted tools
// arrived as { calls: null }. The panel read .length straight off it in three places. The
// optional chain on `snapshot` does not cover `calls` - `snapshot?.calls.length` throws when
// snapshot exists and calls is null.
//
// The payload is remote data, so the client has to tolerate it; the backend was also fixed to
// send an empty array. This test pins the client half.

import { JSDOM } from "jsdom";
import { registerHooks } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";

// ToolRecoveryPanel imports its own CSS and tsx has no asset loader, so redirect those
// specifiers to the shared stub, the way Vite handles them (see extension-surface.test.tsx).
registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.endsWith(".css") || specifier.endsWith(".svg")) {
      return nextResolve("./asset-stub-for-tests.ts", { ...context, parentURL: import.meta.url });
    }
    return nextResolve(specifier, context);
  },
});

import type { ToolRecoveryBindings, ToolRecoverySnapshot } from "../lib/toolRecovery";

// Loaded dynamically on purpose: these modules carry top-level CSS imports, and static
// imports are resolved before registerHooks above can take effect. extension-surface.test.tsx
// gets away with static imports only because its CSS arrives through a lazy chunk.
const { LocaleProvider } = await import("../lib/i18n");
const { ToolRecoveryPanel } = await import("../components/ToolRecoveryPanel");

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
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.Event = dom.window.Event;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  return dom;
}

const dom = installDom();
const rootEl = document.getElementById("root");
if (!rootEl) throw new Error("missing root");

// The shape the backend used to send: a Go zero value, whose nil Calls slice becomes JSON null.
const nullCalls = { silent: false, statistics: {}, sessionPath: "/s/a.jsonl", runtimeEpoch: "", revision: "", calls: null, retryEnabled: false } as unknown as ToolRecoverySnapshot;

function bindingsFor(snapshot: ToolRecoverySnapshot): ToolRecoveryBindings {
  return {
    GetToolRecoveryForTab: async () => snapshot,
    ResolveToolRecoveryForTab: async () => snapshot,
  } as unknown as ToolRecoveryBindings;
}

async function mount(label: string, snapshot: ToolRecoverySnapshot): Promise<{ html: string; threw: unknown }> {
  const host = document.createElement("div");
  rootEl!.appendChild(host);
  const root = createRoot(host);
  let threw: unknown = null;
  try {
    await act(async () => {
      root.render(
        <LocaleProvider>
          <ToolRecoveryPanel tabId="tab-1" sessionKey="k1" running={false} refreshKey={0} bindings={bindingsFor(snapshot)} />
        </LocaleProvider>,
      );
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
  } catch (err) {
    threw = err;
  }
  const html = host.innerHTML;
  await act(async () => { root.unmount(); });
  host.remove();
  ok(threw === null, `${label}: rendering does not throw`);
  return { html, threw };
}

// 1. The reported crash: calls arrived as JSON null.
{
  const { threw } = await mount("calls: null", nullCalls);
  if (threw) process.stdout.write(`        threw: ${String(threw)}\n`);
}

// 2. A snapshot with real work still renders the panel - the silent-degradation fix must not
//    have swallowed the case that matters.
{
  const withCall = {
    silent: false, statistics: {}, sessionPath: "/s/a.jsonl", runtimeEpoch: "", revision: "", retryEnabled: false,
    calls: [{
      identity: { attempt_id: "a1" }, inspection_id: "i1", tool: "write", reason: "interrupted",
      arguments_preview: "", created_at: "",
    }],
  } as unknown as ToolRecoverySnapshot;
  const { html } = await mount("calls: one entry", withCall);
  ok(html.includes("jump") || html.length > 0, "a snapshot with entries renders something");
}

// 3. An empty array (what the backend now sends) renders nothing, as before.
{
  const empty = { ...nullCalls, calls: [] } as unknown as ToolRecoverySnapshot;
  const { html } = await mount("calls: []", empty);
  ok(html === "", "an empty snapshot renders nothing");
}

dom.window.close();
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
