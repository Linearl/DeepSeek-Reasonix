// Run: tsx src/__tests__/tl1698-question-jump-bottom-anchor.test.tsx
//
// tl1698：跳转条末刻度点不到底部。
//
// 旧行为：rail 的 Y→提问映射铺满整条轨道，末刻度落「最后一条用户提问」，
// 而转录底部是提问之后的回复/工具流——结构上够不到底部（用户 20:05 实测）。
// 本钉锁住：①末刻度之下 = 底部带（点它落转录末行）②中间刻度仍按提问跳
// （tl10/384 的上滚跳转语义不回归）③三语文案齐备 ④底部锚标记随渲染出现。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import assert from "node:assert/strict";

import { isBottomRailBand, sampledQuestionTurns } from "../components/QuestionJumpBar";

const testDir = dirname(fileURLToPath(import.meta.url));
const read = (rel: string) => readFileSync(resolve(testDir, rel), "utf8");

const TOP = 100;
const HEIGHT = 400;

test("tl1698: 末刻度之下判为底部带（点它落转录末行）", () => {
  // 10 个提问 → 最后刻度在 ratio=(9+0.5)/10=0.95 处。
  assert.equal(isBottomRailBand(TOP + HEIGHT * 0.99, TOP, HEIGHT, 10, 9), true);
  assert.equal(isBottomRailBand(TOP + HEIGHT * 0.96, TOP, HEIGHT, 10, 9), true);
  // 末刻度本身（0.95）仍属提问区，不算底部。
  assert.equal(isBottomRailBand(TOP + HEIGHT * 0.95, TOP, HEIGHT, 10, 9), false);
});

test("tl1698: 中间刻度不回归（仍是提问，不是底部）", () => {
  for (const ratio of [0.05, 0.25, 0.5, 0.75, 0.9]) {
    assert.equal(
      isBottomRailBand(TOP + HEIGHT * ratio, TOP, HEIGHT, 10, 9),
      false,
      `ratio=${ratio} 被误判成底部`,
    );
  }
});

test("tl1698: 退化输入不误判（零高度/无提问）", () => {
  assert.equal(isBottomRailBand(TOP, TOP, 0, 10, 9), false);
  assert.equal(isBottomRailBand(TOP, TOP, HEIGHT, 0, -1), true);
  assert.equal(isBottomRailBand(NaN, TOP, HEIGHT, 10, 9), false);
});

test("tl1698: 采样刻度下最后刻度仍是最后一问（底部锚独立于提问刻度）", () => {
  const turns = sampledQuestionTurns(100, null);
  assert.equal(turns[turns.length - 1], 99, "采样后最后刻度应是最后一问");
  // 末刻度之下的带 = 底部锚。
  assert.equal(isBottomRailBand(TOP + HEIGHT * 0.999, TOP, HEIGHT, 100, 99), true);
});

test("tl1698: 组件与样式已接底部锚（三语键齐备）", () => {
  const comp = read("../components/QuestionJumpBar.tsx");
  assert.match(comp, /onJumpBottom/, "组件缺 onJumpBottom 接线");
  assert.match(comp, /data-bottom="true"/, "缺底部锚标记");
  assert.match(comp, /isBottomRailBand/, "缺底部带判定调用");
  // 接线形态：scrollToBottom 是 hook 级函数（不在 kernel 类上），所以由
  // Transcript.tsx 传入 → navigator 透传 → 跳转条。
  const nav = read("../components/TranscriptQuestionNavigator.tsx");
  assert.match(nav, /onJumpBottom=\{onJumpBottom\}/, "navigator 未透传 onJumpBottom");
  assert.match(nav, /onJumpBottom\?: \(\) => void/, "navigator 未声明 onJumpBottom prop");
  const transcript = read("../components/Transcript.tsx");
  assert.match(transcript, /onJumpBottom=\{scrollToBottom\}/, "Transcript 未把内核 scrollToBottom 传给跳转条");
  const css = read("../styles.css");
  assert.match(css, /\.jump-item--bottom/, "缺底部锚样式");
  for (const locale of ["zh.ts", "en.ts", "zh-TW.ts"]) {
    assert.match(read(`../locales/${locale}`), /"questionNav\.bottom"/, `${locale} 缺 questionNav.bottom 键`);
  }
});
