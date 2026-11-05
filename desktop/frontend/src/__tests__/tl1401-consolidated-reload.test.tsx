// tl1401 harness: the session:consolidated event → tab reload funnel.
//
// The backend emits session:consolidated after a successful merge (desktop/
// recovery_consolidate.go). The App-level subscription passes the merged main
// path to the controller, which reloads every open tab showing that session
// (reason=synthesize-done). This harness pins the subscription contract:
//  ① a well-formed payload reaches the callback with the main path;
//  ② malformed payloads (null / non-string / blank) never trigger a reload —
//    the sidebar refresh does not depend on this channel, so a bad frame must
//    not fire a transcript reload with an empty path;
//  ③ unmount revokes the subscription (the EventsOn off function runs).

import { JSDOM } from "jsdom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { useEffect } from "react";

const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;

let handler: ((payload: unknown) => void) | undefined;
let offCalled = false;
(window as unknown as { runtime: unknown }).runtime = {
  EventsOn: (_name: string, cb: (payload: unknown) => void) => {
    handler = cb;
    return () => {
      offCalled = true;
      handler = undefined; // a real bus stops delivering after the off
    };
  },
};

const { useConsolidatedSessionReload, SESSION_CONSOLIDATED_EVENT } = await import("../app-runtime/useConsolidatedSessionReload");

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1; else failed += 1;
}

const received: string[] = [];
function Fixture() {
  const reload = (mainPath: string) => { received.push(mainPath); };
  useEffect(() => { /* no-op body; the hook carries the effect */ }, []);
  useConsolidatedSessionReload(reload);
  return null;
}

const root = createRoot(document.getElementById("root")!);
try {
  await act(async () => { root.render(<Fixture />); });
  ok(typeof handler === "function", "hook subscribes on mount");
  ok(SESSION_CONSOLIDATED_EVENT === "session:consolidated", "event name is session:consolidated");

  await act(async () => { handler?.({ mainPath: "/sessions/gc.jsonl" }); });
  ok(received.length === 1 && received[0] === "/sessions/gc.jsonl", "① well-formed payload delivers the main path");

  for (const bad of [null, undefined, { mainPath: 42 }, { mainPath: "   " }, {}, "just a string"]) {
    await act(async () => { handler?.(bad); });
  }
  ok(received.length === 1, "② malformed payloads (null/non-string/blank/missing) never trigger a reload");

  await act(async () => { root.unmount(); });
  ok(offCalled, "③ unmount revokes the EventsOn subscription");

  await act(async () => { handler?.({ mainPath: "/sessions/after-unmount.jsonl" }); });
  ok(received.length === 1, "③ no delivery after unmount");

  console.log(`tl1401 consolidated-reload funnel: ${passed} passed, ${failed} failed`);
  if (failed > 0) process.exit(1);
} finally {
  dom.window.close();
}
