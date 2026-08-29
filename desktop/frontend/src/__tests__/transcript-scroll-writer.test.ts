// Run: tsx src/__tests__/transcript-scroll-writer.test.ts

import { equal } from "node:assert/strict";
import { JSDOM } from "jsdom";
import type { VirtuosoHandle } from "react-virtuoso";
import { createTranscriptScrollWriter } from "../lib/transcriptScrollWriter";
import type { TranscriptScrollMode } from "../lib/transcriptScrollArbiter";
import type { TranscriptScrollWriteRecord } from "../lib/transcriptScrollProbe";

const dom = new JSDOM("<div id='scroll'></div>", { pretendToBeVisual: true });
globalThis.window = dom.window as unknown as Window & typeof globalThis;
const element = dom.window.document.getElementById("scroll") as HTMLDivElement;
Object.defineProperties(element, {
  scrollTop: { configurable: true, writable: true, value: 200 },
  scrollHeight: { configurable: true, value: 2_000 },
  clientHeight: { configurable: true, value: 500 },
});

const calls: string[] = [];
const handle = {
  scrollTo: () => calls.push("scrollTo"),
  scrollBy: () => calls.push("scrollBy"),
  scrollToIndex: () => calls.push("scrollToIndex"),
} as unknown as VirtuosoHandle;
const generationRef = { current: 4 };
const modeRef = { current: "manual" as TranscriptScrollMode };
const records: TranscriptScrollWriteRecord[] = [];
dom.window.__REASONIX_TRANSCRIPT_SCROLL_WRITE__ = (record) => records.push(record);
const writer = createTranscriptScrollWriter({
  virtuosoRef: { current: handle },
  scrollRef: { current: element },
  modeRef,
  generationRef,
});

equal(writer.write({
  owner: "reader-stability",
  operation: "scrollBy",
  top: 120,
  source: "reader-rebound",
  expectedGeneration: 4,
  geometryRevision: 9,
}), true, "the current generation may write");
equal(calls.join(","), "scrollBy", "the gateway emits the requested operation once");
equal(records[0]?.generation, 4, "the diagnostic binds the write to its generation");
equal(records[0]?.geometryRevision, 9, "the diagnostic binds the write to its geometry revision");
equal(records[0]?.sequence, 1, "the gateway assigns a monotonic sequence");

equal(writer.write({
  owner: "recovery",
  operation: "scrollTo",
  top: 600,
  source: "stale-recovery",
  expectedGeneration: 3,
  geometryRevision: 9,
}), false, "a stale generation cannot write to the replacement surface");
equal(calls.length, 1, "a rejected stale write never reaches Virtuoso");

modeRef.current = "native-thumb";
equal(writer.write({
  owner: "tail-follow",
  operation: "scrollTo",
  top: 1_500,
  source: "tail-settle",
  expectedGeneration: 4,
  geometryRevision: 10,
}), false, "native-thumb ownership suppresses every imperative writer");
equal(calls.length, 1, "the native thumb remains browser-owned");

dom.window.close();
console.log("\ntranscript scroll writer tests passed");
