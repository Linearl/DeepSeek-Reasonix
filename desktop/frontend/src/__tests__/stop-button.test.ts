// Run: tsx src/__tests__/stop-button.test.ts
// 任务461-P7 三级终止: the stop button's view-state machine (pure helper).
// Levels mirror the backend RuntimeStatus; the L1 click arms locally; the
// grace renders the countdown from the backend's authoritative deadline.
// tl1420 档位显形: the button itself carries the tier — CSS tier class
// derives purely from the phase; the L3/settled 「已终止」 hold is timed.
// tl1847 去文字 (tl1420-r2, user 2026-11-27): the button renders NO text —
// tiers separate by color/animation alone; the full tier label feeds the
// aria-label, the hint copy feeds the hover title. The label/compact spec
// helpers stay (unused-on-button) to pin the i18n keys — unused keys are NOT
// deleted (防回滚).

import assert from "node:assert/strict";
import {
  stopButtonView,
  stopButtonTier,
  stopButtonLabelSpec,
  stopButtonCompactLabelSpec,
  stopButtonTitleSpec,
  STOP_COMPACT_LABEL_MAX_CHARS,
  STOP_SETTLE_LINGER_MS,
} from "../lib/stopButton";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
    process.exitCode = 1;
  }
}

const NOW = 1_000_000;

// Idle: no stop in flight.
ok(stopButtonView({ stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW }).phase === "idle", "no stop → idle");
ok(stopButtonView({ stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null, running: false, now: NOW }).phase === "idle", "a level without a running turn → idle");

// L1 normal: the surface has not clicked yet (backend level from another surface).
ok(stopButtonView({ stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW }).phase === "normal", "backend L1 without a local click reads normal");

// L1 armed: the click is older than the graceful window → 强制停止.
ok(stopButtonView({ stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: NOW - 1500, running: true, now: NOW, armAfterMs: 1000 }).phase === "armed", "1.5s after the click → armed (强制停止)");
ok(stopButtonView({ stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: NOW - 400, running: true, now: NOW, armAfterMs: 1000 }).phase === "normal", "0.4s after the click → still normal (graceful window)");

// L2 grace: the countdown reads from the backend deadline (ceil).
const grace = stopButtonView({ stopLevel: 2, stopDeadlineUnix: (NOW + 14_300) / 1000, stopInitiatedAt: null, running: true, now: NOW });
ok(grace.phase === "grace", "backend L2 → grace");
ok(grace.countdownSeconds === 15, `grace countdown ceils to 15, got ${grace.countdownSeconds}`);
// tl1514 兜底分支: an expired deadline reads the TERMINAL tier, not a stuck
// 「强制停止 · 0s」 — the backend normally pushes L3 at countdown zero, but a
// wedged publish lane can swallow that push; the view must not sit in grace
// past zero. The next click routes through onEscalateStop and the backend
// (still at Grace) fires the force immediately.
const graceExpired = stopButtonView({ stopLevel: 2, stopDeadlineUnix: NOW / 1000, stopInitiatedAt: null, running: true, now: NOW });
ok(graceExpired.phase === "force" && graceExpired.countdownSeconds === null, "an expired grace deadline reads 已终止 (force), never 强制停止 · 0s");
const graceLast = stopButtonView({ stopLevel: 2, stopDeadlineUnix: (NOW + 900) / 1000, stopInitiatedAt: null, running: true, now: NOW });
ok(graceLast.phase === "grace" && graceLast.countdownSeconds === 1, "a deadline within the current second keeps the grace countdown at 1");
const graceNoDeadline = stopButtonView({ stopLevel: 2, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW });
ok(graceNoDeadline.phase === "grace" && graceNoDeadline.countdownSeconds === null, "L2 without a deadline keeps the countdown hidden");

// L3 force.
ok(stopButtonView({ stopLevel: 3, stopDeadlineUnix: 0, stopInitiatedAt: null, running: true, now: NOW }).phase === "force", "backend L3 → force");

// The backend level wins over the local arm: a surface re-attached mid-grace.
ok(stopButtonView({ stopLevel: 2, stopDeadlineUnix: (NOW + 5_000) / 1000, stopInitiatedAt: NOW - 900, running: true, now: NOW }).phase === "grace", "an attached surface shows grace, not the local arm");

// tl1420 档位显形: tier → CSS modifier class pairing (三色阶).
ok(stopButtonTier("normal") === "stopping" && stopButtonTier("armed") === "stopping", "L1 normal/armed → stopping tier (琥珀)");
ok(stopButtonTier("grace") === "grace", "L2 grace → grace tier (深红+倒计时)");
ok(stopButtonTier("force") === "force" && stopButtonTier("settled") === "settled", "L3 force/settled → force/settled tier");
ok(stopButtonTier("idle") === null, "idle keeps the resting look (no tier class)");

// tl1420: label spec — tl1847: no longer on the button; it feeds the
// aria-label so screen readers keep the full tier text (三文案 preserved).
ok(stopButtonLabelSpec("normal", null).key === "composer.stopStopping", "L1 aria reads 停止中…");
ok(stopButtonLabelSpec("armed", null).key === "composer.stopStopping", "armed aria still reads 停止中… (escalation hint stays in the tooltip)");
const graceSpec = stopButtonLabelSpec("grace", 14);
ok(graceSpec.key === "composer.stopForceCountdown" && graceSpec.n === 14, "L2 aria reads 强制停止 · Ns with the countdown var");
ok(stopButtonLabelSpec("force", null).key === "composer.stopTerminated", "L3 force aria reads 已终止");
ok(stopButtonLabelSpec("settled", null).key === "composer.stopTerminated", "settled aria keeps reading 已终止 during the hold");

// tl1847 去文字: hover title spec — the on-button text moved here (tier-
// matched hint copy; the component overrides it with 状态未知 under unknown).
ok(stopButtonTitleSpec("idle").key === "composer.stopNormal", "tl1847 idle title reads the first-press semantics");
ok(stopButtonTitleSpec("normal").key === "composer.stopNormal", "tl1847 L1 title reads the first-press note");
ok(stopButtonTitleSpec("armed").key === "composer.stopNormal", "tl1847 armed title reads the first-press note (pulse carries the escalation)");
ok(stopButtonTitleSpec("grace").key === "composer.stopForceHint", "tl1847 L2 title reads the 15s-grace hint (点击两次…)");
ok(stopButtonTitleSpec("force").key === "composer.stopImmediate", "tl1847 L3 force title reads the immediate hint (点击三次…)");
ok(stopButtonTitleSpec("settled").key === "composer.stopImmediate", "tl1847 settled title keeps the immediate hint during the hold");

// tl1420 L3 定格: settled hold after an engaged stop settles the turn…
const settled = stopButtonView({
  stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW,
  stopEverEngaged: true, stopSettledAt: NOW - 600,
});
ok(settled.phase === "settled" && settled.countdownSeconds === null, "within the linger the button holds 已终止 (settled)");
// …and returns to idle once the hold expires.
const settledDone = stopButtonView({
  stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW + STOP_SETTLE_LINGER_MS,
  stopEverEngaged: true, stopSettledAt: NOW,
});
ok(settledDone.phase === "idle", "after the linger the button is back to idle (unmounts)");
// A turn that ends without any stop engagement skips the hold entirely.
const naturalEnd = stopButtonView({
  stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW, stopEverEngaged: false, stopSettledAt: NOW - 100,
});
ok(naturalEnd.phase === "idle", "a non-stop ending does not hold 已终止");
// Engagement missing → no hold even with a fresh settle stamp.
const stampWithoutEngagement = stopButtonView({
  stopLevel: 0, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: false, now: NOW, stopEverEngaged: false, stopSettledAt: NOW,
});
ok(stampWithoutEngagement.phase === "idle", "no engagement → settle stamp alone does not hold");
// A running turn never reads settled (the hold is a post-turn state only).
const runningNotSettled = stopButtonView({
  stopLevel: 1, stopDeadlineUnix: 0, stopInitiatedAt: null,
  running: true, now: NOW, stopEverEngaged: true, stopSettledAt: NOW - 100,
});
ok(runningNotSettled.phase === "normal", "a running turn never shows settled");

// tl1582 按钮紧凑化 → tl1847: the compact tags no longer render anywhere (the
// button is text-free). The spec helpers stay to pin the composer.stopCompact*
// i18n keys — tl1847 ④ keeps unused keys (防回滚), so a rollback is a one-line
// revert. idle has no tag (the button unmounts).
ok(stopButtonCompactLabelSpec("idle", null) === null, "tl1582 idle has no compact tag");
ok(stopButtonCompactLabelSpec("normal", null)?.key === "composer.stopCompactStopping", "tl1582 L1 normal → 停止中 tag");
ok(stopButtonCompactLabelSpec("armed", null)?.key === "composer.stopCompactStopping", "tl1582 armed → same 停止中 tag (pulse carries the escalation)");
const compactGrace = stopButtonCompactLabelSpec("grace", 14);
ok(compactGrace?.key === "composer.stopCompactGrace" && compactGrace.n === 14, "tl1582 L2 grace → {n}s tag with the countdown var");
ok(stopButtonCompactLabelSpec("grace", null)?.n === 0, "tl1582 grace without a countdown reads 0s, never NaN");
ok(stopButtonCompactLabelSpec("force", null)?.key === "composer.stopCompactTerminated", "tl1582 L3 force → 终止 tag");
ok(stopButtonCompactLabelSpec("settled", null)?.key === "composer.stopCompactTerminated", "tl1582 settled → 终止 tag during the hold");

// tl1582 宽度实测（验收①：各段宽度 ≤ 按钮）。jsdom 没有布局引擎，宽度按
// 任务1033 判例用确定性代理：半角当量字符预算（CJK=2）× 11px 字号 ≈ 33px，
// 按钮物理上限 72px（styles.css max-width，含图标 12 + gap 5 + padding 18 +
// border 2）。断言三份 locale 的三段 compact tag 全部落在预算内——未来任何
// 一档文案加长即红，不会静默溢出；配合 CSS ellipsis 双保险。
const cjkUnits = (s: string): number =>
  [...s].reduce((n, ch) => n + (/[\u2E80-\u9FFF\uF900-\uFAFF\uFF01-\uFF60\u3000-\u303F]/.test(ch) ? 2 : 1), 0);
const compactKeys = [
  "composer.stopCompactStopping",
  "composer.stopCompactGrace",
  "composer.stopCompactTerminated",
] as const;
for (const [localeName, dict] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  for (const key of compactKeys) {
    const rendered = dict[key].replace("{n}", "99"); // 2-digit countdown = widest grace tag
    const units = cjkUnits(rendered);
    ok(units <= STOP_COMPACT_LABEL_MAX_CHARS, `tl1582 ${localeName} ${key} fits the width budget: "${rendered}" = ${units}/${STOP_COMPACT_LABEL_MAX_CHARS} half-width units`);
  }
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
