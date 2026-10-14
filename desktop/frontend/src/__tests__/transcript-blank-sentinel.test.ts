// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/transcript-blank-sentinel.test.ts
//
// 任务874: transcript 空白哨兵状态机单测。背景：2026-10-13 19:10 大会话 reset
// 重建后渲染层丢失（items 在、可见块 0），无日志无恢复，持续到重开窗口。
// 验收口径：
// ① 单帧空白（虚拟器预测量）不触发恢复；
// ② 隐藏面（clientHeight 0）与无内容行不计空白；
// ③ 连续 N 次空白 → shouldRecover 恰好一次，恢复计数递增且有上限；
// ④ 上限用尽后 shouldReportExhausted 恰好一次（升级日志只发一条）；
// ⑤ 恢复期间来了正常帧 → 计数清零、不重复恢复；
// ⑥ surface 变化整体重置。

import {
  assessTranscriptBlank,
  blankSentinelState,
  rollBlankSentinelSurface,
  TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY,
  TRANSCRIPT_BLANK_RECOVERY_LIMIT,
  type BlankSentinelGeometry,
} from "../lib/transcriptBlankSentinel";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (a === b) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

function geometry(overrides: Partial<BlankSentinelGeometry> = {}): BlankSentinelGeometry {
  return { scrollTop: 0, scrollHeight: 4000, clientHeight: 800, visibleBlockCount: 0, ...overrides };
}

// ① 单帧空白不触发
{
  let state = blankSentinelState("s1");
  for (let i = 0; i < TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY - 1; i += 1) {
    const verdict = assessTranscriptBlank(state, geometry(), 84);
    eq(verdict.blank, true, `单帧空白第 ${i + 1} 次命中 blank`);
    eq(verdict.shouldRecover, false, `单帧空白第 ${i + 1} 次不恢复`);
    state = verdict.state;
  }
  eq(state.blankChecks, TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY - 1, "阈值前只累计计数");
}

// ② 隐藏面 / 无内容行不计空白
{
  let state = blankSentinelState("s2");
  let verdict = assessTranscriptBlank(state, geometry({ clientHeight: 0 }), 84);
  eq(verdict.blank, false, "clientHeight 0 不算空白");
  eq(verdict.state.blankChecks, 0, "隐藏面计数保持 0");
  verdict = assessTranscriptBlank(state, geometry(), 0);
  eq(verdict.blank, false, "无内容行不算空白");
  verdict = assessTranscriptBlank(state, geometry({ visibleBlockCount: 3 }), 84);
  eq(verdict.blank, false, "可见块存在不算空白");
  eq(verdict.state.blankChecks, 0, "正常帧清零计数");
}

// ③ 连续空白到阈值 → 恰好一次恢复，计数有上限
{
  let state = blankSentinelState("s3");
  let recoveries = 0;
  let exhaustedReports = 0;
  for (let i = 0; i < 40; i += 1) {
    const verdict = assessTranscriptBlank(state, geometry(), 84);
    state = verdict.state;
    if (verdict.shouldRecover) recoveries += 1;
    if (verdict.shouldReportExhausted) exhaustedReports += 1;
  }
  eq(recoveries, TRANSCRIPT_BLANK_RECOVERY_LIMIT, `持续空白只恢复 ${TRANSCRIPT_BLANK_RECOVERY_LIMIT} 次`);
  eq(exhaustedReports, 1, "放弃升级日志恰好一条");
  eq(state.recoveries, TRANSCRIPT_BLANK_RECOVERY_LIMIT, "恢复计数封顶");
  eq(state.exhaustedReported, true, "已标记放弃上报");
}

// ④ 恢复动作后到来正常帧 → 计数清零；再空白需重新累计
{
  let state = blankSentinelState("s4");
  let recovered = false;
  for (let i = 0; i < TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY; i += 1) {
    const verdict = assessTranscriptBlank(state, geometry(), 84);
    state = verdict.state;
    if (verdict.shouldRecover) recovered = true;
  }
  eq(recovered, true, "首轮持续空白触发一次恢复");
  const healthy = assessTranscriptBlank(state, geometry({ visibleBlockCount: 5 }), 84);
  eq(healthy.state.blankChecks, 0, "恢复后正常帧清零计数");
  eq(healthy.state.recoveries, 1, "恢复计数保留（预算跨空白episode）");
  let secondRecovery = false;
  for (let i = 0; i < TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY; i += 1) {
    const verdict = assessTranscriptBlank(healthy.state, geometry(), 84);
    healthy.state = verdict.state;
    if (verdict.shouldRecover) secondRecovery = true;
  }
  eq(secondRecovery, true, "第二次空白episode仍可恢复（未超上限）");
}

// ⑤ surface 变化整体重置
{
  let state = blankSentinelState("s5");
  for (let i = 0; i < TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY - 1; i += 1) {
    state = assessTranscriptBlank(state, geometry(), 84).state;
  }
  eq(state.blankChecks, TRANSCRIPT_BLANK_CHECKS_BEFORE_RECOVERY - 1, "切面如前已累计");
  const rolled = rollBlankSentinelSurface(state, "s6");
  eq(rolled.surface, "s6", "surface 已切换");
  eq(rolled.blankChecks, 0, "切面后空白计数清零");
  eq(rolled.recoveries, 0, "切面后恢复预算重置");
  eq(rolled.exhaustedReported, false, "切面后放弃标记重置");
  const same = rollBlankSentinelSurface(rolled, "s6");
  eq(same, rolled, "同面返回原状态（不重置）");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
