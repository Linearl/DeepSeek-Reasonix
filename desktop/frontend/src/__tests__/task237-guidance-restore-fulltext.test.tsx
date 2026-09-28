// Task 237 acceptance (stop restores the FULL guidance text, no truncation):
//  A. durable message >120 chars with newlines: stopping the turn restores the
//     composer textarea to the exact full body returned by ReadInboxItem —
//     never the 120-rune single-line preview (upstream #10640);
//  B. a local multi-line message stays ONE queue row (the old join("\n") key +
//     split("\n") fallback shredded it) and restores into the textarea intact;
//  C. wiring guards: restore path reads by id, hydrate-empty untouched.
//
// Run: npx tsx src/__tests__/task237-guidance-restore-fulltext.test.tsx

import { act } from "react";
import { installDom, installBridgeApp, renderComposer } from "./composerInboxHarness";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function flushTimers(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(label: string, check: () => boolean) {
  for (let i = 0; i < 40; i += 1) {
    if (check()) return;
    await act(async () => { await flushTimers(); });
  }
  throw new Error(`timeout: ${label}`);
}

// ── A. durable >120 chars + newlines restore verbatim ───────────────────────
{
  const dom = installDom();
  const LONG = [
    "任务 172——host 消息历史可见性判据为 IsHostProtocolMessage；resumed 提示之所以可见，是因为它走 notice 通道而非 user 消息路径。",
    "第二行：这段补充文字继续把长度推过一百二十个字符的预览上限，确保被截断的 preview 与真实全文严格不同。",
    "第三行：结尾用于验证换行、缩进与 Markdown 空行都能逐字回到输入框。",
  ].join("\n");
  const PREVIEW = LONG.slice(0, 120).replace(/\n/g, " "); // DefaultPreviewRunes + single-line fold
  ok(LONG.length > 120 && PREVIEW !== LONG, "fixture: full body >120 chars and preview differs");

  let readCalls = 0;
  installBridgeApp({
    InboxSnapshot: async () => ({
      revision: 7,
      paused: false,
      recovered: false,
      sessionPath: "session-a",
      items: [
        { id: "d-long", intent: "followup", state: "queued", preview: PREVIEW, source: "desktop", byteSize: 512, position: 1 },
      ],
      itemsCount: 1,
      bytes: 512,
      maxItems: 64,
      maxBytes: 64 * 1024 * 1024,
    }),
    ReadInboxItem: async () => {
      readCalls += 1;
      return { id: "d-long", displayText: LONG, rawText: LONG, submitText: LONG };
    },
  });
  const { root } = await renderComposer({
    running: true,
    onCancel: async () => ({ discardedItemIds: ["d-long"] }),
  });
  await waitFor("durable row rendered", () => document.querySelectorAll(".composer-guidance-item").length === 1);
  const stop = document.querySelector(".composer__btn--stop") as HTMLButtonElement;
  await act(async () => { stop.click(); await flushTimers(); });
  const textarea = document.querySelector("textarea") as HTMLTextAreaElement;
  ok(readCalls > 0, "restore path reads the durable body by id (ReadInboxItem)");
  ok(textarea.value === LONG, `stop restores the full body verbatim (len=${textarea.value.length}/${LONG.length})`);
  ok(!textarea.value.includes(PREVIEW), "the 120-rune preview never masquerades as the restored text");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── B. local multi-line message: one queue row, restores intact ─────────────
{
  const dom = installDom();
  const MULTI = "第一行：跨会话回信头\n第二行：正文含换行的引导消息\n第三行：尾巴";
  installBridgeApp({
    // Empty durable snapshot ⇒ the local preview fallback builds the queue,
    // and the array key keeps each message whole (no join("\n") re-split).
    InboxSnapshot: async () => ({
      revision: 1,
      paused: false,
      recovered: false,
      sessionPath: "session-a",
      items: [],
      itemsCount: 0,
      bytes: 0,
      maxItems: 64,
      maxBytes: 64 * 1024 * 1024,
    }),
  });
  const { root } = await renderComposer({
    running: true,
    guidanceQueuePreviewItems: [MULTI, "第二条独立消息"],
    onCancel: async () => ({ discardedItemIds: [] }),
  });
  await waitFor("local queue rendered", () => document.querySelectorAll(".composer-guidance-item").length === 2);
  const rows = Array.from(document.querySelectorAll(".composer-guidance-item"));
  ok(rows[0]?.textContent?.includes("第二行：正文含换行的引导消息") === true, "multi-line local message stays ONE row (no \n shredding)");
  ok(rows.length === 2, `two messages → two rows (got ${rows.length}; old split would give 4)`);
  const stop = document.querySelector(".composer__btn--stop") as HTMLButtonElement;
  await act(async () => { stop.click(); await flushTimers(); });
  const textarea = document.querySelector("textarea") as HTMLTextAreaElement;
  ok(textarea.value === MULTI || textarea.value.includes(MULTI), "stop restores the local message with its newlines intact");
  await act(async () => { root.unmount(); });
  dom.window.close();
}

// ── C. wiring guards ─────────────────────────────────────────────────────────
{
  const read = async (rel: string) => (await import("node:fs")).readFileSync(new URL(rel, import.meta.url), "utf8");
  const composer = await read("../components/Composer.tsx");
  ok(composer.includes("app.ReadInboxItem(tabId || \"\", item.id)"), "handleCancel restore reads by id");
  ok(composer.includes("env.displayText || env.submitText || env.rawText"), "restore prefers the full display body");

  const queue = await read("../lib/composerInboxQueue.ts");
  ok(queue.includes("export function localGuidanceFallback(previewItems: readonly string[])"), "fallback takes the array key (no join/split round-trip)");
  ok(queue.includes("export async function hydrateEmptyGuidancePreviews"), "hydrate-empty path still present (preview-empty fill untouched)");
}

if (failed > 0) {
  console.error(`\n${failed} check(s) failed`);
  process.exit(1);
}
console.log(`\nall checks passed (${passed} assertions)`);
