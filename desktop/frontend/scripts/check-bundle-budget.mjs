import { readFileSync, readdirSync, statSync } from "node:fs";
import { basename, resolve } from "node:path";
import { gzipSync } from "node:zlib";

const distDir = resolve("dist");
const indexPath = resolve(distDir, "index.html");
const html = readFileSync(indexPath, "utf8");

function gzipBytes(path) {
  return gzipSync(readFileSync(path), { level: 9 }).byteLength;
}

function initialAssetPaths(extension) {
  const pattern = extension === ".js"
    ? /<(?:script|link)\b[^>]+(?:src|href)=["']([^"']+\.js)["'][^>]*>/g
    : /<link\b[^>]+href=["']([^"']+\.css)["'][^>]*>/g;
  return [...new Set([...html.matchAll(pattern)].map((match) => resolve(distDir, match[1])))];
}

function formatKiB(bytes) {
  return `${(bytes / 1024).toFixed(1)} KiB`;
}

function assertBudget(label, actual, budget) {
  if (actual > budget) {
    throw new Error(`${label} is ${formatKiB(actual)}; budget is ${formatKiB(budget)}`);
  }
  process.stdout.write(`  PASS  ${label}: ${formatKiB(actual)} / ${formatKiB(budget)}\n`);
}

const initialJS = initialAssetPaths(".js");
const initialCSS = initialAssetPaths(".css");
if (!initialJS.length) throw new Error("no initial JavaScript assets found in dist/index.html");
// initial CSS 允许为空：styles.css 走 ?url 延迟加载，feature 样式走 lazy chunk。

// main.tsx intentionally loads styles.css before mounting React so the inline
// boot shell can paint without waiting for the full application stylesheet.
// Vite emits that entry as styles-<hash>.css; keep it in the startup budget
// while also proving it never drifts back into the render-blocking HTML path.
const appShellCSS = readdirSync(resolve(distDir, "assets"))
  .filter((name) => /^styles-.+\.css$/.test(name))
  .map((name) => resolve(distDir, "assets", name));
if (appShellCSS.length !== 1) {
  throw new Error(`expected exactly one deferred app-shell stylesheet, found ${appShellCSS.length}`);
}
if (initialCSS.some((path) => appShellCSS.includes(path))) {
  throw new Error("app-shell stylesheet must not block the inline boot shell's first paint");
}

const initialJSGzip = initialJS.reduce((total, path) => total + gzipBytes(path), 0);
const initialCSSGzip = initialCSS.reduce((total, path) => total + gzipBytes(path), 0);
const appShellCSSGzip = appShellCSS.reduce((total, path) => total + gzipBytes(path), 0);
const largestInitialJS = Math.max(...initialJS.map(gzipBytes));
const largestInitialJSRaw = Math.max(...initialJS.map((path) => statSync(path).size));
const localeChunks = readdirSync(resolve(distDir, "assets"))
  .filter((name) => /^(?:zh|zh-TW)-.+\.js$/.test(name))
  .map((name) => resolve(distDir, "assets", name));

console.log("\nbundle budgets");
// React Virtuoso replaces the transcript's custom measurement/anchor engine.
// Its production runtime adds 16.9 KiB gzip (4.2%) over the 402 KiB baseline.
// This exceptional overrun is locally attributable and trades ~1400 lines of
// competing state machines for a maintained library. Native-tail finish helpers
// then sat on the 423.5 KiB gate (Windows CI: 423.5 / 423.5); this 0.5 KiB
// raise (0.12%) absorbs that leave-cancel / remasure-once code without
// widening the original Virtuoso exception. The project-tree archive race
// guards add 611 bytes gzip over main-v2's 423.988 KiB startup path after the
// blank-project flow landed; project-topic sort invalidation and request
// ordering add another bounded 0.2 KiB. Retain both owner boundaries with a
// narrowly rounded 1 KiB ratchet.
// Diagnostic builds intentionally keep content-free row geometry and scroll
// transition probes in the initial transcript path. Stable builds retain the
// existing production ratchet. Per-row measurement versions and a bounded
// recovery probe add less than 0.1% gzip; retain them with a 0.5 KiB (0.118%)
// production ratchet rather than weakening either recovery contract. The
// bounded allowance also covers small gzip drift from the embedded build SHA.
// Reader extent stabilization adds 1.2 KiB gzip (0.28%) in production for its
// bounded input, collapse, rebound, and ownership transaction. Retain it with
// a 1.5 KiB (0.35%) ratchet instead of weakening the Windows scroll invariant.
// Complete-history navigation adds 0.3 KiB gzip (0.070%) to that production
// path while keeping its 1.68 KiB question rail lazy-loaded. Test diagnostics
// plus the navigation owner add 0.7 KiB gzip (0.164%) over the merged test gate.
// DingTalk channel status and locale wiring move the current-base production
// build from 427.2 to 427.7 KiB and test from 428.6 to 429.1 KiB. The unified
// state-aware geometry contract, session diagnostics counters, and guarded
// native-scroll probes add 2.4 KiB gzip to the initial path. The current
// main-v2 merge adds another 0.3 KiB of deterministic shared startup code.
// Keep the increase explicit and bounded instead of hiding it in a broad
// percentage ratchet.
// The retained-transcript surface adds a small, bounded navigation owner to
// the startup path (overlay state + stale-completion guard). Keep the increase
// explicit and narrow; the measured build is 431.1 KiB gzip.
// The web-search tool card now resolves the same display projection lazily so
// its filtered count matches the assistant Sources panel. The measured build
// is 431.509 KiB gzip; keep 0.1 KiB of explicit headroom for hash/toolchain
// drift instead of relying on a rounded equality.
// Remote onboarding [0.5/3] adds project-group and credential-chain wiring on
// top of the lazy wizard. Exact-turn routing, the extracted event-gap
// projector, checkpoint resets, and the navigation surface transaction bring
// the current main-v2 path to 437.36 KiB gzip.
// The full remote-session surface adds the lazy transcript bridge and tab
// lifecycle on top of [0.5/3]. Keep the measured stack's narrow ratchet.
// Remote approval hardening adds authoritative composer-profile hydration,
// scoped rewind dispatch, and attachment/inbox fences to the always-mounted
// remote hook. The measured production path is 438.38 KiB gzip after keeping
// the integration modules below repolint's ownership ceilings; retain 0.12 KiB
// toolchain headroom with a bounded 1.4 KiB ratchet.
// Remote status isolation keeps the always-mounted status bar on the active
// remote transcript and routes job cancellation to that host. Parsers and
// retry policy remain lazy; the measured selector adds under 0.1 KiB gzip.
// Remote runtime parity adds scoped approvals, status-only reconciliation,
// session quality-floor routing, dropped-frame reconciliation, and remote
// runtime-command dispatch. The measured initial path is 439.60 KiB;
// retain 0.10 KiB of bounded toolchain headroom.
// Closing the remaining review gaps adds generation-fenced hydration plus
// remote-only tool payload, Todo, and terminal isolation. The measured path is
// 439.74 KiB; retain 0.06 KiB of headroom with a 0.1 KiB ratchet.
// The final remote-runtime parity pass adds remote run-strip telemetry,
// explicit session verbs, and specialized plan decisions. The measured path
// is 440.02 KiB. The current main-v2 turn-event, finish-protocol, and session
// repair runtime then moves the combined path to 445.097 KiB; retain 0.103 KiB
// of bounded build/toolchain headroom.
// Atomic remote profile changes, exact approval draining, and generation-safe
// history handoff bring the measured path to 445.228 KiB. Retain 0.072 KiB of
// headroom with the smallest existing decimal ratchet.
// Direct pending-prompt recovery and authoritative remote Goal state bring the
// measured path to 445.473 KiB. Retain 0.027 KiB of bounded headroom.
// Restored remote shells now activate their backend session immediately and
// keep disconnected state out of the mounted surface. The merged production
// path measures 445.614 KiB; retain 0.086 KiB of bounded build/toolchain
// headroom with the smallest existing decimal ratchet.
// Runtime-aware Todo presentation plus exact-tab continuation adds 0.3 KiB gzip
// to the always-mounted footer path. Keep the state/routing guard with a narrow
// ratchet rather than showing idle restored work as actively running. The
// combined path measures 445.9 KiB; retain 0.1 KiB of toolchain headroom.
// Transcript surface ownership and the token-fenced unloaded-question commit
// move the exact main-v2 baseline from 445.865 to 447.587 KiB gzip (+0.39%).
// The final 0.266 KiB retains jump ownership through paint-ready instead of
// allowing a native scrollend to release it. Keep only 0.213 KiB headroom;
// native validation hosts and test fixtures stay outside the production graph.
// Cross-platform shell inventory, current-session vs after-reload rows,
// manual repair guidance, and exact download-host allowlisting move the merged
// path from 448.692 to 449.758 KiB (+1.066 KiB). Retain 0.142 KiB of bounded
// build/toolchain headroom.
// The reader transaction contract (geometry revisions, generation-fenced
// writer requests, gesture travel proof, stabilized-shrink extent acceptance,
// and the blank-rebound prepaint lane) adds a measured 3.978 KiB gzip on the
// merged main-v2 baseline. MCP elicitation and the inline Apps lifecycle remain
// on that startup graph; the combined path measures 455.0 KiB. Retain 0.2 KiB
// of bounded build/toolchain headroom.
// Generic elicitation validation adds field-specific localized accessibility
// copy to the English startup dictionary. The interaction code and CSS remain
// lazy; the measured path is 455.437 KiB. Retain 0.163 KiB of headroom.
// Stream-failure visibility (#9560) adds the last-discard reason and one
// terminal-notice dedupe flag, while provider no_proxy copy now states the
// custom-proxy precedence. The merged path measures 455.9 KiB; retain 0.1 KiB
// of bounded build/toolchain headroom.
// Exhausted tail repair now releases ownership so jump-bottom remains usable
// after a stranded native WebView extent. The WebView2 reachable-tail clamp
// then absorbs a second post-quiet extent without an unbounded write loop.
// The combined path measures 456.316 KiB; retain 0.084 KiB with the smallest
// one-decimal ratchet.
// The generation-bound history-prepend lease adds stable-key reader anchoring,
// full mounted coverage, and one final arbiter-owned correction. The measured
// path is 457.406 KiB after extracting the lease owner to satisfy repolint.
// Latest-base transcript settle ownership measures 457.523 KiB with this UX.
// Isolated conversation forks and their extracted browser mock adapter bring
// the combined tree to 458.158 KiB; completion uncertainty adds a terminal
// outcome and notice without exposing evaluator audits to the frontend,
// measuring 458.287 KiB gzip.
// Transactional Ask resolution and authoritative rejected-submit recovery add
// 0.3 KiB gzip to the initial controller path. Retain the exact turn fence,
// bounded ListTabs retry, and stale-prompt guard.
// Session-catalog repair presentation stays in the lazy project-tree chunk;
// compact shared helpers keep the combined initial path within the same gate.
// Merge-Back adds identity-bound inspection, navigation, and retained-recovery
// orchestration on top. The merged stable build measures 461.338 KiB and the
// test channel measures 461.323 KiB. Deferring selection ownership until a
// real range exists (#9703/#9711) and adding the session takeover banners
// move the combined path to 462.2 KiB. Local spectator reclaim adds the
// desktop-vs-remote command branch. Sticky Context's session-scoped file chips
// bring the merged stable path to 462.587 KiB. Windows' embedded build metadata
// lands just above the rounded 462.6 KiB boundary; retain one cross-platform
// decimal step without widening any chunk or raw gate.
// Reading the applied item-list transform (instead of the remembered offset)
// keeps the reader/anchor visual guards from compounding under reduced-motion
// WebView2; the merged path measures 462.827 KiB. Retain one decimal step.
// Generation-bound native-thumb transactions and the rebased custom-scrollbar
// drag add 0.3 KiB gzip; the merged path measures 463.102 KiB.
// Absorbing content-preserving block-window prepends into the active reader
// transaction adds 0.2 KiB gzip on top; the merged path measures 463.292 KiB,
// 8 bytes under the next decimal. Retain one cross-platform decimal step.
// The subagent outcome envelope, partial-state card, and history hydration add
// 0.5 KiB gzip on the initial path. The model-capability resolver and its
// read-only provider badges add a measured 0.2 KiB including gzip/toolchain
// rounding. The integrated management shell, image capability controls, and
// upstream updater refresh measure 465.4 KiB gzip (base: 464.7 KiB).
// Keep the next decimal ceiling and leave feature editors lazy.
// Durable protocol recovery controls and search-source status add 1.2 KiB
// over the same-environment main-v2 build (465.4 -> 466.6 KiB gzip).
// Keep one decimal of cross-platform headroom for this measured shell change.
// Integrating main-v2 rich-link menus measures 466.905 KiB combined.
// The AskCard session-draft wiring adds a bounded 30-byte gzip drift on the
// initial route. The session-runtime ordering fence adds 56 bytes and
// cross-platform zlib rounding reaches the same startup path; retain the
// explicit budget rather than failing on a rounded 467.0 KiB display value.
// The latest main-v2 session-runtime fence and exact prompt protocol measure
// 468.2 KiB here; retain a 0.1 KiB ceiling for platform zlib rounding.
// 1f8c3fe50: fork UI-restoration batch (TopicbarMoreMenu return, locale
// backfills + recovered keys) measures 469.6 KiB; +0.4 KiB headroom on top.
// Task 258 + 267 merged onto batch6 measures 470.2 KiB combined (message
// presentation bubbles/fold + tail-follow kernel fixes); one-shot ratchet
// +0.5 KiB per the one-shot rule (no +0.1 nibbling).
// Task 258b (MiMo phase-1 absorption: 4 entry-refusal toasts + latch
// self-heal + 4 locale keys per dialect) measures 470.7 on top of the
// merged batch6 tip — one-shot +0.5 to 471.1.
// Task 312 (collapsed group activity dot): baseline at 8641f24d1 measures
// 471.1 (at the line, PASS) and the group-row dot inline adds bytes over it —
// the gate trips. One-shot +0.5 to 471.6 per the ratchet rule.
// Task 192 (active-tab residency): measured 472.5 over 472.1 — the settings
// entry/card, store policy and locale keys; one-shot +0.5 to 472.6.
// Merge batch (242+192+315, audit-ratchet one-shot): each landed at 472.6 for
// its own measured baseline, but the stacked initial chunk measures 472.7 —
// one-shot +0.5 to 473.1 (same merge-batch precedent as the raw 2511.8 landing).
// Task 163 (OpenCode Go usage card): measured 473.1 over the line — the detail
// card component, bridge pair and 45 locale keys; one-shot +0.5 to 473.6.
// Merge batch (244 batch 2 stacked onto 163): measured 473.9 over 473.6 —
// one-shot +0.5 to 474.1 (merge-batch precedent, same as the 472.7 landing).
// Task 244 batch 4/B9 panel entry landed at the same 474.1 gate — one bump,
// shared value (no double).
// Zero-src-change re-build tripped the 474.1 gate twice (edge jitter at the
// dead-even line — the pre-registered 280 warning): one-shot +0.5 to 474.6
// per the pre-filed rule (initial tripping -> 474.6).
// Merge batch (B3 remote + opencodefix + effortfix2 stacked onto 318):
// measured 475.0 over 474.6 — one-shot +0.5 to 475.1 (merge-batch precedent).
// Task 337 (usage-card key reuse): the guidance copy in the three noKey
// locale strings pushed initial gzip to 475.1 against the 475.1 line —
// measured 475.1, one-shot +0.5 to 475.6 (ratchet rule, no drip).
// Task 333 (rotation gate panel + 20 locale keys ×3): measured 475.7 against
// the 475.6 line — one-shot +0.5 to 476.1 (ratchet rule, no drip).
// Task 282 (fork features intro panel + 23 locale keys ×3): measured 476.2
// against the 476.1 line — one-shot +0.5 to 476.6 (ratchet rule, no drip).
// Task 338 (WS chart + heap pie section, 18 locale keys ×3): measured 476.2
// against the 476.1 line — one-shot +0.5 to 476.6 (ratchet rule, no drip).
// B2 merge batch (LA 336/237/181 + LB 282/279/277 + 338 + 304 + 333 tail + 343):
// measured 477.3 against the 476.6 line — stacked 0.7 over, so one-shot two
// steps (+1.0) to 477.6 per the 2026-09-20 ratchet rule (no drip, independent commit).
// Task 385a (lab 回答风格 selector + 11 locale keys ×3): initial gzip
// measures 479.632 over the 479.6 line — one-shot +0.5 to 480.1 (ratchet rule).
// Task 495 (subagent panel: App gate arg + directory model + en locale keys in
// the initial chunk): initial gzip measures 512.5 over the 512.0 line —
// one-shot +0.5 to 513.0 per the 2026-09-20 ratchet rule (no drip, independent commit).
const initialJSBudgetKiB = 540.8; // 9 件批量合入后两轮构建 536.9→539.5 抖动超 538.8 线 0.7 — JS 棘轮 +1.0 一次到位 → 540.8 含余量（合并线独立 commit，报备派活方） 624 诊断页文案本地化（issue 句子/建议动作 + runtime 区块标签，64 键×3，en 词典入 initial chunk）实测 538.5 超 537.8 线 0.7 — gzip 棘轮 +1.0 一步到位 → 538.8（分支 wt-624 自报，报备派活方） // 617+618 合入叠加炸线抬：分支自报 535.7 基于旧基线（未含 616+619 已合增量），合并后实测 536.8 超 536.2 线 0.6 — JS 棘轮 +1.0 一次到位 → 537.8 含余量（合并线独立 commit，报备派活方） // 618+617 合件前端面合入后实测 535.7 超 535.2 门限 0.5 618+617 合件前端面合入后实测 535.7 超 535.2 门限 0.5（crash overlay 一键分析按钮+确认流、issue 骨架模块、诊断页 pending 区）— gzip 步长 +1.0 一次到位 → 536.2（报备派活方，独立 commit） // 616+619 叠加炸线抬：616 侧自报 535.1 贴线、619 侧自报 534.3 均未动线，双件同批合并叠加实测 535.3 超 0.1 — JS 棘轮 +0.2 → 535.4 留抖动余量（合并线独立 commit，报备派活方） // 第 11 版 14 件大批合入后实测 534.2 恰等 534.2 炸线（build-to-build 字节抖动必炸区）— gzip 步长 +1.0 一次到位 → 535.2（报备新4，独立 commit） // 591 按钮分级（a 型 28 处 primary + b 型 158 处 secondary class 字符串）实测 534.1 超 534.0 门限 0.1 — 照 403 恰等抬先例门限停在实测+0.1（避开恰等抖动炸线区），+0.2 → 534.2（报备） // composer 四连合入后叠加实测 534.0 超 1.0（587 环+38-B1 hooks+composer 四连三层增量同落 initial chunk）— gzip 步长 +1.0 一次到位 → 534.0（报备新4） // 587 // composer 四连批(588/593/594/595 前端面)合入后实测 532.9 超 0.4（593 !! 触发面板+594 菜单门控拆分+595 tier wire 增量）— gzip 步长 +0.5 一次到位 → 533.0 (independent commit, 报备派活方)（主线 587 已抬同值 533.0，两侧同值并集） // 587 合入炸线抬：实测 532.8 超 0.3（587 面板失败态/重试环 + frontendLog ring 兜底）— gzip 步长 +0.5 一次到位 → 533.0（报备派活方） // 第三批八支(548/555/341/508/533/552/560/512 多面板组件)合入后实测 531.7 — 大批次 one-shot 到实测+0.8 余量 → 532.5（九支批先例 20261002；报备派活方） // 558 合入炸线抬：实测 519.3 超 0.3（558 SubagentsPanel/CapsulePanel 大组件）— gzip 步长 +1.0 一次到位 → 520.0 (independent commit, 报备派活方) // 465 合入后贴顶预抬（派活方批准）：实测 518.0/518.0 余 0.0，下批前端件必炸 — gzip 步长 +1.0 一次到位 → 519.0 (independent commit, 报备派活方) // zcode 十支批(536/524/525/546/547/551 前端面)炸线抬：实测 517.4 超 0.4 — gzip 步长 +1.0 一次到位 → 518.0 (independent commit, 报备派活方) // 9a/9b(506增量+514) 合后贴顶预抬：实测 515.9/516.0 余量仅 0.1（构建间抖动必炸区，下批 38/480/499 后续仍可能碰前端）— gzip 步长 +1.0 一次到位 → 517.0 (independent commit, 按「贴卡提前抬」规则报备派活方) // 9 支合并批(410/463/464/466/467/477/507/510)炸线抬：实测 515.5/515.0 超 0.5（前端面 463/466/467/507/510 叠加）— gzip 步长 +1.0 一次到位 → 516.0 (independent commit, 按棘轮规则报备派活方) // 506 分支预抬（贴卡余量提前抬判据）：506 实测 513.3/514.0 余量仅 0.7 且下批 499 前端面在队 — gzip 步长 +1.0 一次到位 → 515.0 (independent commit, 报备派活方) // 495 merge 后预抬（派活方裁决「现在就抬，不等下批炸」）：512.5/513.0 余量仅 0.5 且 501 明确改三语 hint 入下批必炸区 — gzip 步长 +1.0 一次到位 → 514.0 (independent commit) // nine-batch wave-2 (D-group+450+capsule+prompt three-section stacked): 505.2 measured, one-shot +6.8 per user 20261002 // nine-batch round-2 (12 pending pieces stacked incl 443/447): 501.0 measured, one-shot +4 headroom per user 20261002 "连续卡就一次多提" // nine-batch (443 scope note + 447 wrap variants): 493.2 measured, one-shot +0.5 // eight-batch package (446 hover+448 gate+442 popup+325 cluster front stacked): 491.0 measured, one-shot to measured+0.5 per the 2026-09-20 ratchet rule (no drip, independent commit) // wave-2 (172+152+128) merge-state: 485.4 over the 484.1 line (nudge+todo-tree+worktree-project JS stacked) — under the 2026-09-30 +1.0 step a single step lands 485.1 short of measured, so two steps +2.0 in one shot (B2 stacked precedent), no drip, independent commit // task 379 rework merge-state: 483.6 over the 480.6 line (wall+yaml+tests stacked on the batch-8 gate) — one-shot to measured+0.5 per the 2026-09-20/09-30 ratchet rule (no drip, independent commit) // +0.5: batch-8 release build measures 480.1x at the exact 480.1 boundary (build-to-build byte jitter) — one-shot +0.5 per the 2026-09-20 ratchet rule // fork: chain 468.8-era → 471.1 → 471.6 → 472.1 → 472.6 (tasks 242/192) → 473.1 (merge batch) → 473.6 (tasks 244 batch 1 + 163, same gate) → 474.1 (244 batch 2 + batch 4 same gate) → 474.6 (335 收口 jitter, pre-registered rule) → 475.1 (B3+opencodefix+effortfix2 merge batch, 475.0 measured; one-shot +0.5 per the 2026-09-20 ratchet rule) → 475.6 (task 337 locale guidance, 475.1 measured; one-shot +0.5) → 476.1 (task 333 rotation gate, 475.7 measured; one-shot +0.5) → 476.6 (task 282 fork features intro, 476.2 measured; one-shot +0.5) → 476.6 (task 338 memory pages, 476.2 measured; one-shot +0.5, same gate — B2 merge batch single const) → 477.6 (B2 merge batch stacked, 477.3 measured; one-shot two steps +1.0) → 478.1 (task 297 cold-cache card, 477.8 measured; one-shot +0.5) → 478.6 (task 361 pie/order-label second round, 478.1 measured; one-shot +0.5) → 479.1 (task 379 rework yaml-driven wall + columns config, 479.0 measured; one-shot +0.5 — superseded by the larger 480.6, larger-of-two rule) → 480.6 (batch-8 release jitter, 480.1x measured; one-shot +0.5) → 484.1 (task 379 rework merge-state, 483.6 measured; one-shot to measured+0.5) → 486.1 (wave-2 merge-state, 485.4 measured; two steps +2.0 under the 09-30 +1.0 rule) → 512.0 (nine-batch wave-2, 505.2 measured) → 513.0 (task 495, 512.5 measured; one-shot +0.5) → 514.0 (495 merge 后预抬, 512.5/513.0 measured)
// Task 269 rebased onto 471.1: measured 470.7, also within 471.1 —
// larger one-shot value stands (audit ruling, no second ratchet).
// Task 264 rebased onto the same budget: measured 470.3 KiB (collab-background
// switch in the sessionCollab pane), also within 470.5 — same one-shot value
// stands for both lines (no second ratchet needed).
// Task 266-A on batch6 at d7b939756 measures 470.1; one-shot +0.5 per the
// ratchet rule. NOTE: the task-267 line already widened the same gate to
// 470.5 (8854103c1, in audit) — when both merge, keep the LARGER value
// (470.6), which remains one-shot compliant for either measured baseline.
// (266-A note: 470.6 kept as the LARGER of the two one-shot ratchets —
// one-shot compliant for either measured baseline, per audit ruling.) // fork: +2.3 KiB vs upstream 468.3
// Task 257 (batch6 merge): measured 470.3 KiB (yolo lab entry + danger gate),
// within 470.6 — larger one-shot value stands (audit ruling).
// [fork note] settings panel (LocalServerPage) that ships with the serve pool gateway.
assertBudget("initial JavaScript gzip", initialJSGzip, initialJSBudgetKiB * 1024);
// [fork note] 2026-09-15: pre-existing overage, not task 122 - the clean baseline
// (b4afe0bf5) already measures 310.7 KiB and the change adds 0.3 KiB. Re-set from
// the measured value with headroom; see handoff/fork开发-出包台账-20260914.md.
assertBudget("largest initial JavaScript chunk gzip", largestInitialJS, 320 * 1024);
// Render-blocking CSS is intentionally absent: styles.css loads deferred via
// ?url, and feature styles (heartbeat) live in lazy chunks loaded on demand.
// An empty initial CSS list is the desired state, not a build error.
if (initialCSS.length > 0) {
  assertBudget("render-blocking CSS gzip", initialCSSGzip, 4 * 1024);
} else {
  process.stdout.write("  PASS  render-blocking CSS: none (all styles deferred)\n");
}
// Extension surfaces, Task Monitor, and compact decision receipts share the
// application stylesheet loaded before React mounts. Keep their combined
// allowance bounded even though the file is no longer render-blocking.
// Navigation overlay styles add a bounded 0.1 KiB to the deferred shell.
// The cleaned source panel adds 0.1 KiB gzip to the deferred shell on top of
// the retained-transcript navigation allowance; keep the ratchet explicit.
// The navigation mask's stable composer footprint and remote tab/surface
// states bring the merged shell to roughly 115.7 KiB gzip.
// The one-row model configuration list, responsive stacking, Automation's
// shared title-safe shell, and the shared harness decision surface measure
// 116.9 KiB gzip while reusing existing layout primitives. Retain a bounded
// 0.1 KiB headroom ratchet.
// Wave3 UI settings (experiment rail, write-root tiers, local-server page)
// push deferred shell CSS to 124.1 KiB; take the next one-decimal ceiling.
  // 125.2: task 181 guidance edit banner styles (+0.1 over 124.6 measured at merge, 2026-09-20).
  // 125.2 again: task 253 set-gates checkbox grid styles measured 124.9 KiB (2026-09-22); ratchet +0.5 in one step.
  // 125.2: task 251 pre-existing-red fix — composer-guidance-head token --fg-default → --fg re-gzips +0.1 (measured 124.8).
  // 125.7: task 312 collapsed-group activity dot (.project-tree__group-state rule + comment)
  // — baseline at 8641f24d1 measures 125.2 (at the line, PASS); the new rule trips the
  // gate. One-shot +0.5 per the ratchet rule.
  // 126.2: side-track merge batch (346 usage-card CSS grouping among the four pieces)
  // measures 125.7 over the 125.7 line — one-shot +0.5 per the ratchet rule (independent commit).
  // 126.7: task 379 (intro dialog + card wall styles, 126.2 measured) — one-shot +0.5.
  // 127.7: wave-2 (152 todo-tree archive styles) measures dead-even at the exact 126.7
  // boundary — exact-boundary trip, one-shot +1.0 under the 2026-09-30 step rule (independent commit).
assertBudget("deferred app-shell CSS gzip", appShellCSSGzip, 133.4 * 1024); // 642 模拟崩溃演练防误报标注（mock 横幅+徽章 ~17 行，--warn token + color-mix）实测恰等 133.3/133.3（恰等=抖动必炸区，403/591 先例）— CSS 棘轮 +0.1 → 133.4（分支 wt-642 自报，报备派活方） // 626 合入叠加棘轮：626 自报 133.071→133.1 + 合并线 z-index token 化修复（626 裸值触发 check:z-index，--z-app-content 同档替换含注释）实测 133.2 超 133.1 线 0.1 — CSS 棘轮 +0.2 一次到位 → 133.3 含余量（合并线独立 commit，报备派活方） // 625 诊断页同类问题聚合（severity 组内按 code 折叠行 + 展开明细 + 双入口「前往设置」按钮 + 节头类型数/信息级口径）实测 133.071 KiB（136265B）超 133.0 线 73B gzip — CSS 棘轮 +0.1 → 133.1（报备派活方，独立 commit；本次合并与合并线预防棘轮同值 133.1，两侧同值并集） 9 件批量合入后恰等 133.0/133.0（恰等=抖动必炸区，403/591 先例）— CSS 棘轮 +0.1 → 133.1 预防出包构建炸线（合并线独立 commit，报备派活方） // 617+618 合入恰等 132.9/132.9（叠加后贴顶；恰等=抖动必炸区，403/591 先例）— CSS 棘轮 +0.1 → 133.0（合并线独立 commit，报备派活方） // 618+617 合件恰等 132.6/132.6（crash overlay 一键分析/分析提示区 + 诊断页 pending 区样式；恰等=抖动必炸区）— CSS 棘轮 +0.1 → 132.7（报备派活方，独立 commit；本次合并与 616 已合 132.9 取大者 → 132.9 终值） // 616 合入炸线抬：实测 132.8 超 0.2（capsule 信箱发送面 composer+角标+运行中子代理分组 CSS 约 60 行）— CSS 棘轮 +0.3 一步到 132.9 留抖动余量（报备派活方） // 614 合入恰等 132.6/132.6 炸线（autopilot 橙修色注释扩行 + plan/goal token 三块 + 徽章拆两条规则 + tint 段语义注释，raw 约 +2.2KB）— CSS 棘轮 +0.1 → 132.7 (independent commit, 报备派活方) // 591 ghost 全局定义（约 20 行）合入后恰等 132.5/132.5（403 注释同判：恰等=抖动必炸区）— CSS 棘轮 +0.1 → 132.6（报备） // 403 合入实测恰等 132.4/132.4 炸线（.project-tree__loading 骨架样式 10 行）— CSS locale-chunk 棘轮 +0.1 → 132.5（报备新4） // 587 // composer 四连批(588/593/594/595)合入炸线抬：实测 132.3 超 0.0x（594 composer-access-menu__item disabled 态 + 595 --mode-autopilot-* 三处 token，raw 约 +1.6KB）— CSS locale-chunk 棘轮 +0.1 步长 → 132.4 (independent commit, 报备派活方)（主线 587 已抬同值 132.4，两侧同值并集） // 587 合入恰等 132.3/132.3（构建抖动必炸区）+0.1 → 132.4（587 面板失败态/细条 CSS；报备派活方） // 578 前置棘轮：579 后实测 132.1 持平（无前端面），578 按钮样式申报 +907B raw/+131B gzip — 一次性 +0.1 → 132.3 含余量（报备新4） // 563 合入微涨：实测 132.1 超 0.1 — CSS locale-chunk 棘轮 +0.1 步长 → 132.2（报备新4） // 第三批八支面板样式合入后实测 131.6 — 大批次 one-shot 到 132.0（+0.4 余量；报备派活方） // 558 合入炸线抬：实测 131.2 超 0.1 — CSS locale-chunk 棘轮 +0.1 步长 → 131.2 (independent commit, 报备派活方) // 506 分支预抬（贴卡余量提前抬判据）：506 实测 129.6/130.1 余量仅 0.5 且 506 本批即动 styles.css、下批 499 前端面在队 — +1.0 一次到位 → 131.1 (independent commit, 报备派活方) // eight-batch (446 hover portal + 442 segmented bar styles): 127.9 measured, one-shot +0.5 per the 2026-09-20 ratchet rule // fork: upstream 1.38.3 raised its own budget to 120.4 KiB; the fork's LocalServerPage delta plus the merge measured 121.3 KiB. The session-version panel adds its table and phase styles, measuring 122.0 KiB. Task 123's session-monitor board adds its own ~0.4 KiB of panel styles (measured 123.1 KiB), so keep bounded headroom. // task 447 capsule floating panel adds its trigger/panel/ended-directory/history styles: 128.59 measured, one-shot +0.5 per the 2026-09-20 ratchet rule (independent commit). // B1 (2026-10-03 night dispatch, pre-approval vertical layout, +14 CSS lines): 128.9 measured — under the 129.1 line, no bump. // 461-P5 (2026-10-03: pre-approval two-column relayout, intro-left/switch-right, checkbox row + risk row): 129.2 measured — one-shot +1.0 per the 2026-09-30 step rule (independent commit).
if (localeChunks.length !== 2) {
  throw new Error(`expected 2 on-demand Chinese locale chunks, found ${localeChunks.length}`);
}
for (const path of localeChunks) {
  const name = basename(path);
  // Task Monitor, billing, indexed history, Task Center, Extension UI, and
  // runtime controls plus execution-setting receipts add localized copy. The
  // write-access approval card adds four scoped actions and a home-risk
  // warning (~0.15 KiB gzip, +0.27% over the old 54.75 gate). Context
  // compaction settings add 40 bytes gzip of policy guidance to simplified
  // Chinese, while scheduled billing adds compact rate-band labels/tooltips.
  // The three StepFun presets add localized names/descriptions (~0.1 KiB
  // gzip); the two pay-as-you-go presets add the same again. The delivery
  // floor segmented control adds two labels plus one explanatory tooltip,
  // measured at 23 B gzip for zh and 8 B for zh-TW. Completion receipts add
  // six short status labels in each locale, requiring another 0.2 KiB per
  // language. DingTalk setup and mention guidance add at most 0.2 KiB more
  // (0.36%); retain the complete security and group-chat copy instead of
  // abbreviating user-facing instructions to fit the old locale ratchet.
  // Recovery-copy and catalog-only sidebar labels can move the simplified
  // Chinese chunk across the rounded 55.9 KiB boundary on CI's Node/zlib;
  // retain a narrow 0.1 KiB headroom rather than making gzip output a
  // platform-dependent gate. The OpenCode one-key setup adds product-level
  // connection, fallback, and legacy-state copy while removing protocol
  // choices from the primary UI; keep that complete guidance with a bounded
  // 0.4–0.5 KiB locale-only ratchet.
  // Git-Bash installation guidance adds localized copy across dialects.
  // MCP elicitation adds fourteen short labels per locale (~40 B gzip).
  // Generic schema validation adds complete field-error, privacy, and safe-
  // fallback copy. Measured chunks are 58.574 KiB zh and 59.368 KiB zh-TW;
  // retain roughly 0.13 KiB of platform headroom for each.
  // Stream-failure diagnostics add five strings per dialect. Together with the
  // reachable-tail recovery copy, the merged chunks measure 58.923 KiB zh and
  // 59.710 KiB zh-TW. The isolated-fork guidance brings the measured chunks
  // to 59.1 KiB zh and 59.9 KiB zh-TW; retain a narrow one-decimal ratchet.
  // Merge-Back lifecycle and recovery guidance measure 59.819 KiB zh and
  // 60.612 KiB zh-TW; retain only the next one-decimal ceiling for each.
  // The retained-recovery receipt and copy action move zh to 59.911 KiB;
  // session-catalog recovery guidance on the merged base moves zh-TW to
  // 60.757 KiB; retain only its exact one-decimal ceiling.
  // Session takeover adds ~20 locale keys per dialect (banners, dialog,
  // reclaim), while Sticky Context adds file-state and limit diagnostics. The
  // merged stable chunks measure 60.395 KiB zh and 61.232 KiB zh-TW; retain
  // only the next one-decimal ceiling for each dialect.
  // The outcome card adds one short localized status label per dialect. CI's
  // Windows zlib measured zh at 60.4 KiB exactly; capability-status copy adds
  // a small 0.1 KiB ratchet, so retain the next decimal ceiling rather than
  // dropping the unknown-state explanation.
  // Image input mode, provenance and unknown-state guidance measure 60.724 KiB
  // zh and 61.570 KiB zh-TW. Keep the next decimal ceiling per locale.
  // Protocol recovery and source-availability copy measure 60.927 KiB zh
  // and 61.789 KiB zh-TW (base: 60.8 / 61.6 rounded).
  // Rich-link action copy on the current base brings these to
  // 61.027/61.881 KiB; retain bounded cross-platform headroom.
  // Recovery retry copy reaches the rounded 61.1 KiB boundary on Node/zlib
  // toolchains; keep the next one-decimal ceiling for cross-platform CI.
  // fork: locale soft-limit preserved from 1.31.x (UI copy growth is not a perf regression)
  // upstream 1.38.1 measured 61.2/62.0; fork +1.0 KiB for LocalServerPage/consolidate keys
  // 1f8c3fe50 locale backfill: real Chinese copy is longer than the Title-Case
  // fallback it replaced; zh measures 63.1 KiB gzip, zh-TW has the same keys.
  // 1.38.3 merge: upstream copy plus the fork's own keys measure 65.3 KiB, so
  // the ceiling moves to 66.0/66.5 with the same bounded headroom.
  // Task 49 A5: the autopilot switch and the approval-model setting add eight
  // keys plus help copy per dialect; zh-TW measures 66.7 KiB. Take the next
  // decimal ceiling for each with the same bounded headroom as before.
  // Session-version panel adds twenty-one keys per dialect (panel opener, column
  // headers, row labels, merge preview, phase headings) plus the table copy.
  // zh measures 67.2 KiB, zh-TW 67.8 KiB; the exact ceiling is not headroom, so
  // take the next decimal beyond the measurement for each dialect.
  // zh 68.0 became an exact-boundary failure after the branch-picker preview
  // strings landed (12 new locale keys across the picker); ratchet one decimal
  // of headroom, mirroring how every prior locale-key landing was absorbed.
  // zh 68.4 became an exact-boundary failure after the preview-building status
  // string landed; ratchet one decimal again by the same convention.
  // Task 56/64 evidence-chain port: the receipt, operation-lifecycle and recovery
  // copy adds keys per dialect; zh measures 69.0 KiB and zh-TW 69.7 KiB, so take the
  // next decimal past each measurement by the same convention.
  // Task 81's Settings switch adds four keys per dialect (label, hint, on, off); the hint is
  // a full sentence because it has to say what the experiment does. zh measures just past the
  // 69.0 ceiling, so take the next decimal by the same convention as every prior landing.
  // The concise session-experience tier (task 111) and the transcript "no more history" line
  // land together: zh-TW reaches exactly 70.0 KiB, an exact-boundary failure like the 68.0 and
  // 68.4 cases above, so the same one-decimal ratchet applies to it. zh stays at 69.5.
  // Task 123's session-monitor board adds 23 keys per dialect; zh measures 69.7 KiB, so the
  // same one-decimal convention takes zh to 70.5 (zh-TW keeps its 71.0 ceiling).
  // Parallel line C (feedback inbox + boot/transcript timing labels) adds locale keys;
  // zh-TW measures exactly 71.0 KiB (exact-boundary failure). Ratchet zh-TW to 71.5 by the
  // same one-decimal convention; zh remains within 70.5.
  // 2026-09-16: the v4 storage switch copy had to say which directory stops receiving
  // writes; that pushed zh to 70.6 KiB, so the same one-decimal ratchet applies (zh-TW
  // keeps its 71.5 ceiling).
  // Task 113/114 turn-edit + artifacts/references measured zh-TW 71.7; wave3 UI settings
  // (129/136/130/131/132) pushed zh to exact 71.0 and zh-TW to 71.8. Merge then measured
  // zh 71.3 / zh-TW 72.1; ratchet both one decimal: zh 71.5, zh-TW 72.5.
  // Task 60 UI + experiment openers add locale keys; zh measures 71.6 KiB
  // (past 71.5). Same one-decimal ratchet: zh 72.0; zh-TW keeps 72.5.
  // Task 115 dream UI keys push zh-TW to 72.7 (past 72.5). Ratchet zh-TW to 73.0.
  // Task 19 session-collaboration copy (collabCard.* + sessionCollab*) adds keys
  // per dialect; zh measures 72.2 KiB (past 72.0) and zh-TW 73.1 (past 73.0).
  // Same one-decimal ratchet: zh 72.5, zh-TW 73.5.
  // Task 161 cache-tuning settings copy adds 12 keys per dialect; zh measures
  // 72.7 KiB (past 72.5). Same one-decimal ratchet: zh 73.0; zh-TW keeps 73.5.
  // Task 159/160 (fork): the guidance delivery state, the settings save-rejection
  // copy, the "load older" button and the scroll-trigger experiment add the same
  // keys to both dialects. zh still measures 72.9 KiB under its 73.0 ceiling, but
  // zh-TW measures 73.8 KiB past 73.5, so the same one-decimal ratchet applies:
  // zh-TW 74.0; zh keeps 73.0.
  // Task 155 (fork): the four-mode conversation store adds 13 keys per dialect
  // (mode labels, per-stage notes, per-stage risks, the restart-pending line) and
  // rewrites the hint. Measured gzip: zh 73.5 KiB, zh-TW 74.3 KiB, so the same
  // one-decimal ratchet applies with the usual 0.1 KiB headroom: zh 73.6;
  // zh-TW 74.4. Tasks 162/170 (2026-09-18) added close-flow copy and session
  // manage tool strings: measured zh-TW 74.4 KiB again at the ceiling, so the
  // ratchet moves to 74.5; tasks 184 monitor strings pushed both again: zh-TW 74.9, zh 74.0.
  // 75.2: ratchet step +0.5 (user 2026-09-20, one-shot rule). Batch 3 adds 8 keys ×3 locales (210/153/221/173) + terser variance; measured zh 74.8.
  // 75.9: ratchet step +0.5 same batch. zh-TW tracks zh +0.7.
  // 75.7 / 76.4: batch 4 line K adds splitView.resizeDivider ×3 locales; measured zh 75.3, so the one-shot +0.5 step lands both budgets at once.
  // 76.4 / 75.9: task 253 adds sessionCollabGates.masterOffHint ×3 locales; zh-TW measured 76.4 at the old ceiling (2026-09-22), one-shot +0.5 step on both.
  // 76.2 / 76.9: task 251 ratchet +0.5 one-shot (user 2026-09-20 rule) — zh-TW hit its exact ceiling (76.4) on Node/zlib variance with no locale copy change.
  // 76.2 / 77.4: task 259 ratchet +0.5 one-shot — the todo-sidebar hint gains a layout note per locale; zh-TW measured 77.0 at the old 76.9 ceiling.
  // 78.0 / 79.0: task 265 lab intake — 37 new keys per locale (9 features × effect copy); zh measured 77.8 at the old 76.2 ceiling, one-shot +0.5.
  // 78.5 / 79.5: task 264 collab-background switch — 4 new keys per locale;
  // zh measured 78.1 at the old 78.0 ceiling, one-shot +0.5 step on both.
  // (257 merge note: yolo adds 7 keys/locale; zh measured 78.2 — within 78.5,
  // same one-shot ceilings kept.)
  // 78.5 / 80.0: task 258b MiMo phase-1 absorption — 4 new keys per locale
  // (entry-refusal toasts); zh-TW measured 79.6 at the old 79.5 ceiling,
  // one-shot +0.5 (zh measured exactly 78.5 and passes — untouched).
  // 79.0 / 80.0: task 278 quick-command wide dialog (no new locale keys — the
  // wide prop adds bytes to the SettingsPanel chunk) pushed zh past the exact
  // 78.5 ceiling; one-shot +0.5 per the ratchet rule.
  // 79.0 → 79.5 and zh-TW 80.0 → 80.5: task 242 fallback locale (7 keys ×
  // 3 dialects) measured exactly at each ceiling — one-shot +0.5 each.
  // Task 192: four zh-TW locale keys measured 80.1 over 80.0 — one-shot +0.5
  // (zh-TW folded into the shared 80.5 line; zh keeps the LARGER 79.5 of the
  // two one-shot values, per the 266-A/267 audit ruling).
  // Task 244 batch 1: 12 new keys × 3 locales (B1/B2/B3 switches) measured zh
  // 79.6 over 79.5 — one-shot +0.5 each per the 2026-09-20 ratchet rule.
  // Task 163: fifteen locale keys per dialect measured zh 79.6 over 79.5 and
  // zh-TW 80.8 over 80.5 — one-shot +0.5 each (266-A/267 larger-value rule;
  // both tasks landed at the same gate — one shared line, no double bump).
  // Merge batch (244 batch 1 + 163 stacked): zh trips the shared 80.0 gate in
  // the merged tree — one-shot +0.5 to 80.5; zh-TW measures 81.2 over 81.0 —
  // one-shot +0.5 to 81.5 (same merge-batch precedent as the initial gzip 472.7 landing).
  // Task 244 batch 4: B9 added 4 keys x 3 locales while zh-TW already sat at
  // the exact 81.0 ceiling — one-shot +0.5 each (its own bump landed at the
  // same 80.5/81.5 gate as the merge-batch lines above — one shared value, no double).
  // Merge batch (B9 stacked onto the merged tree): zh-TW measures 81.6 over
  // 81.5 — one-shot +0.5 to 82.0 (merge-batch precedent; zh 80.5 holds at the
  // line with zero headroom, watch the next landing).
  // Task 318 (that next landing): zh measures 80.8 over the dead-even 80.5 —
  // one-shot +0.5 to 81.0 per the ratchet rule (11 new locale keys x 3 dialects);
  // zh-TW then measured dead-even at 82.0 (same Error semantics as the 474.1
  // initial line) — one-shot +0.5 to 82.5.
  // Task 333 (rotation gate panel): zh measures 81.4 over 81.0 (20 new keys
  // x 3 dialects) — one-shot +0.5 to 81.5; zh-TW measures 82.6 over 82.5 —
  // one-shot +0.5 to 83.0.
  // Task 282 (fork features intro panel): 23 new keys x 3 dialects — one-shot
  // +0.5 to 82.0 per the 2026-09-20 ratchet rule (declared in the delivery
  // letter); zh-TW measures 83.1 over 83.0 — one-shot +0.5 to 83.5.
  // Task 338 (memory pages): zh measures 81.7 over 81.5 (18 new keys x 3) —
  // one-shot +0.5 to 82.0, which also meets 282's pre-raise (81.9→82.0):
  // keep the larger/identical value on merge (266-A/267 rule). zh-TW pending
  // — 338's 83.0 loses to 282's measured 83.1→83.5 (larger value wins).
  // B2 merge batch (LB 282+279+277 stacked): zh measures 82.4 over the 82.0
  // line — one-shot +0.5 to 82.5 (ratchet rule, independent commit).
  // zh-TW measures 83.6 over the 83.5 line (B2 stack) — one-shot +0.5 to 84.0.
  // task 297 locale keys push zh-TW to 84.1 over 84.0 — one-shot +0.5 to 84.5.
  // Side-track batch (196fix2+347+345+346): zh measures 82.6 over the 82.5
  // line — one-shot +0.5 to 83.0 (ratchet rule, independent commit).
  // Task 309 (mailbox defaults panel): zh measures 83.1 over the 83.0 line
  // (7 new locale keys x 3 dialects) — one-shot +0.5 to 83.5 per the
  // 2026-09-20 ratchet rule.
  // Task 363A (runtime reuse lab page): zh-TW measures dead-even 84.5/84.5
  // (4 new locale keys x 3 dialects) — one-shot +0.5 to 85.0; zh holds at
  // 83.5 (measured under the line this batch).
  // Task 380/O4 (detached idle release input): zh-TW measures dead-even
  // 84.5/84.5 (2 new locale keys x 3 dialects) — one-shot +0.5 to 85.0; zh
  // Task 373-R1 (image dedup panel): zh measures 84.1 over the 84.0 line
  // (4 new locale keys x 3 dialects) — one-shot +0.5 to 84.5; zh-TW holds
  // at 85.5 (this branch's merged budget, measured under the line).
  // Task 385a (回答风格 selector, 11 locale keys ×3): zh measures 84.250 over
  // the 84.0 line and zh-TW 85.547 over 85.5 — one-shot +0.5 each (ratchet rule).
  // Task 373-R1.1 (three-position switch hint rewrite lengthens zh-TW):
  // zh-TW measures 85.6 over 85.5 — one-shot +0.5 to 86.0; zh holds at
  // 84.5 (measured under the line this batch).
  // task 379 rework batch (branch-side): zh merge-state measured 83.8 -> 84.0
  // one-shot; zh-TW measured 85.1 over 85.5 — larger 86.5/85.0 stack stands
  // (larger-of-two rule).
  const budget = name.startsWith("zh-TW-") ? 96.0 * 1024 : 94.5 * 1024; // 642 繁体键同批实测 95.9 超 95.7 线 0.2 — zh-TW 棘轮 +0.3 → 96.0 含余量（分支 wt-642 自报，报备派活方） // 642 模拟崩溃演练三语键（crash.mock* 7 键 + settings.mockCrash 3 键）实测 zh 恰等 94.4/94.4（恰等=抖动必炸区，403/591 先例）— zh 棘轮 +0.1 → 94.5，zh-TW 实测低于线不动（分支 wt-642 自报，报备派活方） // 624 诊断页文案本地化（64 键×3：issue 句子/建议动作/runtime 区块标签；繁体天然比简体长）实测 zh 93.978 超 92.4 线 1.578、zh-TW 95.412 超 93.7 线 1.712 — 单步 +1.0 不足，照 wave-2/B2 叠加先例两步 +2.0 一次到位 → 94.4/95.7（分支 wt-624 自报，报备派活方） // 第 11 版 14 件大批合入后 zh-TW 实测 92.7 恰等 92.7 炸线（591 二段 158 处 b 型 class 叠加）+ zh 预抬防同批炸 — gzip 步长 +1.0 一次到位 → 93.7/92.4（报备新4，独立 commit） // 403+604 三语新增实测 zh 恰等 91.3/91.3 炸线（403 加载文案+604 settingsPath 前缀键）— zh +0.1 → 91.4（报备新4） // composer 四连合入实测 zh 91.3 超 0.3 + zh-TW 92.7 超 0.3（!! 快捷指令面板 hint+菜单二级页+594 disabled tooltip 三语增量）— 一次到位 91.3/92.7（报备新4） // 563 合入 zh-TW 恰等 92.2/92.2 构建抖动必炸区 +0.2 → 92.4（报备新4） // 247 合入恰等 90.8/90.8（构建抖动必炸区）+0.2 → 91.0 // zh-TW 实测恰等 92.1/92.1（构建抖动必炸区）+0.1 → 92.2 // 第三批八支三语键合入后实测 zh 90.7 / zh-TW 91.8 — zh +0.3 到 90.8、zh-TW 预抬 +0.2 到 92.1（大批次 one-shot；报备派活方) // 506 分支预抬（贴卡余量提前抬判据）：506 三语 hint 实测 88.6/90.0，对 89.5/90.9 余量仅 0.9/0.9 且下批 499 前端面在队 — gzip 步长 +1.0 一次到位 → 90.5/91.9 (independent commit, 报备派活方) // 501 merge 后补抬 zh-TW（新证据：501 三语 hint 推高至实测 89.7/89.9 余量仅 0.2，下批 499 前端面必炸）— 与上轮 initial/zh 预抬同判据，gzip 步长 +1.0 一次到位 → 90.9 (independent commit, 报备派活方) // 495 merge 后预抬（派活方裁决，与 initial gzip 514.0 同笔）：zh 88.1/88.5 余量仅 0.4 且 501 改 settings.perfMonitorHint 三语必炸 — gzip 步长 +1.0 一次到位 → 89.5 (independent commit) // zh-TW eight-batch: 87.4 measured (325 cluster traditional copy longer than zh) — one-shot +0.5 // eight-batch (325 cluster copy): zh 86.1 over 86.0 — one-shot +0.5 to 86.5 (equal to zh-TW, larger-of-two rule), independent commit // wave-2 (172 feedbackNudge settings copy): zh 85.1 over the 85.0 line — one-shot +1.0 under the 2026-09-30 step rule (independent commit) // task 385a: zh 84.0->84.5, zh-TW 85.5->86.0; one-shot +0.5 each // batch-8 merge stack: zh measures 84.6 over the merged 84.5 line (373-R1 + 385a stacked) — one-shot +0.5 to 85.0 per the 2026-09-20 ratchet rule (no drip) // R1.1 zh-TW 85.5->86.0 collides at 86.0 with 385a (same value, larger-of-two rule); zh keeps the larger 85.0 // batch-8 release build: zh-TW measures 86.04 at the exact 86.0 boundary — one-shot +0.5 to 86.5 per the same ratchet rule // task 261 (prompt history picker, 7 keys x3): zh measures 86.6 over 86.5 — one-shot +1.0 to 87.5 per the gzip-step rule; zh-TW then measured dead-even 87.9 at its 87.9 line (task 261 traditional copy longer) — one-shot +1.0 to 88.9 // B1 (2026-10-03 night dispatch, hooks label simplified into the risk line, net ~0): zh 87.3 / 87.5, zh-TW 88.6 / 88.9 measured — both under, no bump. // 461-P3/P4/P5 (2026-10-03: resend button + inbox dropdown + preapproval relayout, 3-dialect keys): zh measures dead-even 87.5/87.5 — one-shot +1.0 to 88.5 per the 2026-09-30 step rule (independent commit); zh-TW holds at 88.9 (measured under the line this batch). // 461-P7 (2026-10-04 00:17 package: three-tier stop escalation, 5 keys x3): zh-TW measures 89.0 over the merged 88.9 line — one-shot +1.0 to 89.9 per the 2026-09-30 step rule (independent commit); zh measures 87.7, holds under 88.5.
  // Task 380/O4 (detached idle release input): zh-TW measures dead-even
  // 84.5/84.5 (2 new locale keys x 3 dialects) — one-shot +0.5 to 85.0; zh
  // holds at 83.5 (measured under the line this batch).
  // task 383 automation->heartbeat copy unification lengthens both locale
  // chunks (zh 83.8 over 83.5; zh-TW 85.1 over 85.0) — one-shot +0.5 each.
  assertBudget(`${name} gzip`, gzipBytes(path), budget);
// [fork note] Fork v1.31.4: locale copy is product text that grows with every feature,
// [fork note] feature adds copy; we instead keep a soft (warn-only) threshold at 60.0
}

const rawInitialBytes = [...initialJS, ...initialCSS, ...appShellCSS]
  .reduce((total, path) => total + statSync(path).size, 0);
// The maintained Virtuoso engine adds 49.1 KiB raw (2.2%) over the previous
// 2268.7 KiB gate. Navigation remains inside the 2341 KiB production ceiling;
// its combined diagnostic wiring adds 2.2 KiB (0.094%) to the test channel.
// DingTalk startup wiring moves current-base production from 2341.0 to 2343.6
// KiB and test from 2346.2 to 2348.8 KiB; the pinned heading adds 0.5 KiB raw
// (0.021%). The workspace panel rework (change-row hover/revert, status badges,
// More menu, completion summary) makes the latest-base merge 2353.1 KiB in
// production and test channels both measure 2357.92 KiB after project-group
// wiring. Exact-turn routing, checkpoint resets, and failure-atomic navigation
// bring the current main-v2 path to 2379.22 KiB. The remote approval
// fences, extracted ownership modules, and remote status-bar isolation bring
// the measured initial payload to 2380.9 KiB; retain 0.1 KiB of bounded
// raw/toolchain headroom. Scoped remote approvals, status reconciliation, and
// runtime command dispatch bring the measured payload to 2382.9 KiB. The
// remaining review fences measure 2383.2 KiB; retain 0.1 KiB of headroom.
// Final remote-runtime parity measures 2384.4 KiB raw. The current main-v2
// runtime additions bring the combined path to 2404.364 KiB. The final merged
// restored-shell activation and disconnected-state revival path measures
// 2404.898 KiB; retain 0.102 KiB of bounded headroom alongside the gzip
// ratchet above.
// Runtime-aware Todo status and exact-tab continuation then add to the same
// initial path. The combined payload measures 2406.2 KiB; retain 0.1 KiB of
// raw/toolchain headroom for both owners.
// The same transcript transaction measures 2413.012 KiB raw (+0.28%) against
// the 2406.204 KiB baseline. Retain 0.188 KiB of bounded headroom.
// The notification-volume control adds one persisted master gain, per-source
// loudness trims, and its accessible Settings surface. Current main-v2 moves
// from 2413.183 to 2414.879 KiB raw (+1.696 KiB); retain 0.121 KiB of bounded
// headroom.
// Owner-lifecycle reasoning disclosure, pre-paint tail pinning, and the live
// footer growth floor then add 2.390 KiB after extracting ownership modules
// below repolint's source ceilings. Lifecycle fencing adds 0.258 KiB; the
// combined path measures 2417.526 KiB. Retain 0.074 KiB while preventing
// phase-boundary reverse flashes and cross-surface floor leaks.
// The same shell-support surface moves the merged path from 2417.526 to
// 2422.371 KiB raw (+4.845 KiB). Retain 0.129 KiB of bounded headroom without
// widening unrelated chunk ceilings.
// The WebView2 extent rebound prepaint handoff adds 0.204 KiB raw so a native
// scroll delivery can restore mounted coverage before the next visible frame.
// Retain 0.096 KiB of headroom without widening gzip or chunk ceilings.
// The reader transaction contract then adds a measured 15.317 KiB raw on the
// merged main-v2 baseline (including its own prepaint port). MCP elicitation
// and Apps add their bounded payload on the shared graph; the combined path
// measures 2442.6 KiB. Retain 0.4 KiB of bounded build/toolchain headroom.
// The browser MCP interaction preview adds 0.6 KiB of route wiring while its
// 0.75 KiB form fixture and lifecycle remain lazy. The combined path measures
// 2443.2 KiB; retain 0.1 KiB of bounded build/toolchain headroom.
// Generic field copy adds 1.134 KiB raw to the startup dictionary; all schema
// parsing, rendering, and CSS remain lazy. The measured path is 2444.334 KiB;
// retain 0.166 KiB of bounded build/toolchain headroom.
// The off-flow composer measurement mirror adds 0.472 KiB raw while removing
// live-textarea layout mutation. The merged path measures 2444.806 KiB; retain
// 0.194 KiB of bounded toolchain headroom without widening gzip/chunk gates.
// Stream-failure visibility and corrected proxy guidance bring the merged path
// to 2446.6 KiB; retain the smallest existing decimal ratchet.
// The stranded-tail recovery transition plus the WebView2 reachable-tail clamp
// bring the measured initial payload to 2447.953 KiB. Retain 0.047 KiB with
// the smallest one-decimal ratchet.
// The extracted history-prepend owner adds 3.953 KiB of bounded transaction
// state and stable-key coverage checks. Together with the compact
// session-version host, they measure 2452.7 KiB; the recovery coordinator and
// dialog remain lazy. Completion uncertainty adds a distinct terminal notice
// and localized startup copy without collapsing into recovery-paused UX.
// 2454.719 KiB on the release toolchain. Completion uncertainty brings the
// final merged payload to 2455.154 KiB.
// Ask turn fencing, rejection reconciliation, and the localized submit-failure
// notice measure 2456.044 KiB raw; retain 0.056 KiB of one-decimal headroom.
// Merge-Back's startup ownership and failure-atomic navigation fence add the
// remaining bounded payload. The retained recovery receipt makes the stable
// path 2465.105 KiB raw; the merged test channel measures 2464.979 KiB.
// Session takeover banners and #9703/#9711's provisional-selection handoff
// combine with Sticky Context's pinned-file state at 2469.125 KiB raw on the
// merged stable path. Retain only the next one-decimal ceiling.
// The passive reader-anchor lease for delayed WebView2 range commits measures
// 2469.347 KiB raw (+0.222 KiB, +0.009%). Retain only the next one-decimal
// ceiling; gzip and largest-chunk budgets remain unchanged.
// Reading the applied item-list transform for the reader/anchor visual guards
// adds 0.5 KiB raw on top; the merged path measures 2469.815 KiB.
// The scrollbar generation fence and drag rebase add 1.1 KiB raw; the merged
// path measures 2470.932 KiB.
// The reader-transaction offset absorption adds 0.8 KiB raw on top; the merged
// path measures 2471.741 KiB. Controller-owned management dispositions and
// optimistic management settlement add 0.6 KiB raw; retain the smallest
// one-decimal ceiling with bounded headroom.
// The outcome card and history hydration add 2.3 KiB raw on the initial path
// (2474.0 KiB measured in CI). Keep this narrowly attributable ratchet rather
// than removing persisted-result visibility or changing chunk ownership.
// On the current main-v2 base, the combined measured path is 2474.6 KiB;
// the model-capability helper and localized status copy add 0.9 KiB; retain
// the smallest bounded cross-platform ceiling.
// Retain the upstream updater ceiling and independent chunk gates.
// Recovery controls add 3.6 KiB raw over the measured 2480.9 KiB base;
// current payload is 2484.509 KiB. Retain only bounded toolchain headroom.
// With the current-base rich-link menus: 2485.715 KiB raw.
// The shared harness decision surface adds a bounded startup stylesheet
// payload. The current base plus exact prompt identity and stale-card recovery
// measure 2496.4 KiB locally; retain the smallest bounded ceiling.
// The context truncation-rescue notice and its three locale strings measure
// 2496.6 KiB; retain the smallest bounded ceiling.
// Task 265: the 37-key lab intake landed raw at exactly the old 2540.0 ceiling; one-shot +10 KiB (user 2026-09-20 rule).
// Task 316: one-shot +10 KiB again (2550.6 measured, ratchet rule — no drip).
// Task 231 (managed-path pre-approval): the settings card + 27 locale keys measured 2550.3, tripping the 2550.0 line; one-shot +10 KiB per the raw-step rule.
// Task 318 (lab internals five): three switches + merge/regroup pages + 11 locale keys x 3 measured 2560.6 over the 2560.0 line — one-shot +10 KiB per the raw-step rule.
// B3 remote fix (App tree remote-project navigation branch + RemoteNavigationContext Provider + Composer inbox props): measured 2560.5, tripping the 2560.0 line; one-shot +10 KiB per the ratchet rule (same gate as 318 — one shared 2570 value, no double).
// B2 merge batch (LA+LB+338+304+333 tail+343 stacked): measured 2571.9 over the 2570.0 line — one-shot +10 KiB per the raw-step rule (independent commit).
const rawInitialBudgetKiB = 2_856.0; // 642 模拟崩溃演练前端面（crashMock 模块 + devDebug 卡按钮 + bridge 可选成员 + en 键入 initial chunk）实测 2855.6 超 2855.0 线 0.6 — 照 614/619 微涨先例 raw 实测对齐 +0.4 含余量 → 2856.0（分支 wt-642 自报，报备派活方） // 627 图墙零渲染诊断（App 32 处 dismiss reason 串+墙生命周期日志+SessionWallBoundary 静态入 initial）实测 2831.3 超 0.3 → 2831.5（分支自报，独立 commit；本次合并与 624 已合 2855.0 取大者 → 2855.0 终值，不重复抬） // 624 诊断页文案本地化（64 键×3，en 词典入 initial chunk）实测 raw 2848.4 超 2845.0 线 3.4 — raw 步长 +10 保有量充足一次覆盖 → 2855.0（分支 wt-624 自报，报备派活方） // 617+618 合入叠加炸线抬：分支自报 2835.6 基于旧基线，合并后（含 616+619 叠加）实测 2842.8 超 2840.0 线 2.8 — raw +5 一次到位 → 2845.0 含余量（合并线独立 commit，报备派活方） 618+617 合件前端面合入后实测 2835.6 超 2830.0 门限 5.6（一键分析按钮+确认流、issue 骨架模块、诊断页 pending 区 + 三语新键）— raw 步长 +10 一次到位 → 2840.0（报备派活方，独立 commit；与 616 同值 2840，两侧同值并集） // 616 合入炸线抬：实测 2836.0 超 6.0（capsule 信箱发送面组件+11 locale 键×3+CSS，与 JS/locale/CSS 同笔叠加）— raw 步长 +10 一次覆盖到 2840.0（报备派活方） // 619 启动 tab 恢复优化（TabBar 未加载徽标 + useController 启动轮询 + bridge tabs:restored 监听）实测 2830.5 超 2830.0 线 0.5 — 照 403/591 先例门限停实测+0.1（避开恰等抖动炸线区）→ 2830.6（报备派活方，独立 commit；本次合并与 616 同笔叠加取 616 的 2840.0 为终值，不重复抬） // 614 合入实测 2830.3 超 0.3（plan/goal token 三块 + 徽章两规则 + tint/锚注释扩行）— raw 一次到位 +0.5 含余量 → 2830.5 (independent commit, 报备派活方) // 591 按钮分级（186 处 class 字符串 + ghost CSS）实测 2829.5 超 0.5 — raw 实测对齐 +0.5 → 2830.0（报备） // composer 四连合入后叠加实测 2828.3 超 3.3（587 环+38-B1 hooks+composer 四连前端增量叠加，与 JS/locale 同笔叠加）— 一次到位 → 2829.0（报备新4） // composer // 38-B1 合入微涨：实测 2823.6 超 0.6（composerInsert owner 迁移新 hooks 152 行）— 一次到位 → 2824.0（报备新4） // 587 合入微涨：实测 2822.9 超 1.9（587 面板失败态/重试环 + frontendLog ring + 失败态 CSS）— raw 保有量充足 +2 实测对齐 → 2823.0（报备派活方） // 578 合入微涨：实测 2820.4 超 0.4（+907B raw 申报吻合）— 一次到位 → 2821.0（报备新4） // 563 合入微涨：实测 2819.5 超 1.5 — raw 保有量充足 +2 实测对齐（报备新4） // 247 合入微涨：实测 2817.4 超 1.4 — raw 保有量充足 +2 实测对齐（报备派活方） // 562 合入微涨：实测 2816.0 超 1.0 — raw 步长 +10 保有量充足直接覆盖（+1 实测对齐；报备派活方） // 第三批八支(多面板组件)合入后实测 2814.2 超 39.2 — raw 步长 +10 ×4 一次到位 → 2815.0（报备派活方） // 9 支合并批(410/463/464/466/467/477/507/510)炸线抬：实测 2755.1/2755.0 超 0.1（前端面叠加）—— 按 +10 KiB raw 步长一次到位 → 2765.0 (independent commit, 按棘轮规则报备派活方) // 505（会话图墙）实测 2746.9 超 2745.0 线 —— palette 门 lib（insertSessionWallEntry/分组排序）+App 接线+en 新键入 initial chunk（组件本体 lazy 不进，gzip 513.9/514.0 已过）；按 +10 KiB raw 步长一次到位（用户 2026-09-20 one-shot 站规，独立 commit） // 2026-10-05 批（469/485/487/491 前端改动）实测 2735.0 等值贴顶，按 +10 KiB 步长一次到位（派活方决策二授权） // nine-batch wave-2: 2710.7 measured, one-shot +14.3 per user 20261002 // nine-batch round-2: 2688.4 measured, one-shot to round number per user 20261002 "连续卡就一次多提" // eight-batch (446+448+442+325 cluster front): 2636.5 measured, one-shot +10 beyond measured per the raw-step rule (step exceeds, single landing) // 169+149 merge-state: 2600.2 over the 2600.0 line (group-drag + jump-preview JS) — one-shot +10 per the raw-step rule (independent commit) // task 379 rework merge-state: 2596.8 over the 2590.0 line — one-shot +10 per the raw-step rule (independent commit) // fork: ratchet step +10 KiB (user 2026-09-20, one-shot rule); task 254 measured 2530.1 (autonomous-update pane + resume dial + locales 210/153/221/173) -> 2590.0 (batch 7.6+CDP-B merge batch stacked, 2581.2 measured; one-shot +10); task 379 rework also measured 2581.2 over the 2580.0 line on its branch — same one-shot +10 value, no double (larger-of-two rule) -> 2600.0 (379 merge-state, 2596.8 measured) -> 2610.0 (169+149, 2600.2 measured) // 461 terminal batch (2026-10-04 00:17 package: P7 stop escalation + P8 fold + 427 lab cards stacked): measured 2725.3 over the 2725.0 line — one-shot +10 per the raw-step rule (independent commit). // composer 四连批(588/593/594/595 前端面)合入炸线抬：实测 2824.2 超 3.2 — raw 步长 +10 保有量充足一次覆盖（实测对齐 +4；报备派活方） // 578 合入微涨：实测 2820.4 超 0.4（+907B raw 申报吻合）— 一次到位 → 2821.0（报备新4） // 563 合入微涨：实测 2819.5 超 1.5 — raw 保有量充足 +2 实测对齐（报备新4） // 247 合入微涨：实测 2817.4 超 1.4 — raw 保有量充足 +2 实测对齐（报备派活方） // 562 合入微涨：实测 2816.0 超 1.0 — raw 步长 +10 保有量充足直接覆盖（+1 实测对齐；报备派活方） // 第三批八支(多面板组件)合入后实测 2814.2 超 39.2 — raw 步长 +10 ×4 一次到位 → 2815.0（报备派活方） // 9 支合并批(410/463/464/466/467/477/507/510)炸线抬：实测 2755.1/2755.0 超 0.1（前端面叠加）—— 按 +10 KiB raw 步长一次到位 → 2765.0 (independent commit, 按棘轮规则报备派活方) // 505（会话图墙）实测 2746.9 超 2745.0 线 —— palette 门 lib（insertSessionWallEntry/分组排序）+App 接线+en 新键入 initial chunk（组件本体 lazy 不进，gzip 513.9/514.0 已过）；按 +10 KiB raw 步长一次到位（用户 2026-09-20 one-shot 站规，独立 commit） // 2026-10-05 批（469/485/487/491 前端改动）实测 2735.0 等值贴顶，按 +10 KiB 步长一次到位（派活方决策二授权） // nine-batch wave-2: 2710.7 measured, one-shot +14.3 per user 20261002 // nine-batch round-2: 2688.4 measured, one-shot to round number per user 20261002 "连续卡就一次多提" // eight-batch (446+448+442+325 cluster front): 2636.5 measured, one-shot +10 beyond measured per the raw-step rule (step exceeds, single landing) // 169+149 merge-state: 2600.2 over the 2600.0 line (group-drag + jump-preview JS) — one-shot +10 per the raw-step rule (independent commit) // task 379 rework merge-state: 2596.8 over the 2590.0 line — one-shot +10 per the raw-step rule (independent commit) // fork: ratchet step +10 KiB (user 2026-09-20, one-shot rule); task 254 measured 2530.1 (autonomous-update pane + resume dial + locales 210/153/221/173) -> 2590.0 (batch 7.6+CDP-B merge batch stacked, 2581.2 measured; one-shot +10); task 379 rework also measured 2581.2 over the 2580.0 line on its branch — same one-shot +10 value, no double (larger-of-two rule) -> 2600.0 (379 merge-state, 2596.8 measured) -> 2610.0 (169+149, 2600.2 measured) // 461 terminal batch (2026-10-04 00:17 package: P7 stop escalation + P8 fold + 427 lab cards stacked): measured 2725.3 over the 2725.0 line — one-shot +10 per the raw-step rule (independent commit).
// [fork note] the smallest one-decimal ratchet. Bumped to 2_461.0 for the serve pool
assertBudget("initial raw JavaScript and CSS", rawInitialBytes, rawInitialBudgetKiB * 1024);
// [fork note] 2026-09-15: measured 1102.2 KiB raw - same pre-existing growth as the
// gzip budget above; re-set from the measured value with headroom.
  // 2520: raw ratchet step widened to ~+10 KiB (user 2026-09-20). Merge batch measured 2511.8 (tasks 185/181/collab frontend).
assertBudget("largest initial JavaScript chunk raw", largestInitialJSRaw, 1_160 * 1024);
