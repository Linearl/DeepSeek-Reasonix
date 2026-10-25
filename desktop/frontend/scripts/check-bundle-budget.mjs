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
const initialJSBudgetKiB = 613.6; // 第24波（包27件7 1157 侧栏图标+三语4键）合后实测 612.5 对 612.6 线余 0.1<0.3 触扩权（01:33 裁决预演即抬）— gzip 棘轮 +1.0 一次到位 → 613.6（合并线独立 commit，报备协调线） // 第23波（包27候件1137 回合尾部信息带：数据链+渲染+三语4键入 eager 面）净增申报 +0.7 推过 611.6 线 — JS gzip 棘轮照 09-30 步长 +1.0 一次到位 → 612.6 含余量（协调线抬线令批准，合并线独立 commit，报备协调线） // 补记 第8波（包22 七件 JS 增量）合后构建实测 610.6 恰等顶线（恰等=必炸区）— JS gzip 棘轮 +1.0 一次到位 → 611.6 含余量（协调线抬线令批准，合并线独立 commit，报备协调线） // 第十八包 1087+1088 合后实测 609.4 超 605.6 并集线 3.8 — 超幅大于单步，照 624 先例五步 +5.0 一次到位 → 610.6 含余量 1.2（合并线独立 commit，报备新10）// 1088 二期（椭圆/画笔/序号 三工具+复制剪贴板+跨屏开关入 eager 面）实测 604.5 对 604.4 线余 0.1=贴线抖动必炸区 — JS gzip 棘轮照 09-30 步长 +1.0 预抬 → 605.6 含余量（分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 1087 截图标注一期（ScreenshotOverlay 组件+标注模型+三语 24 键+bridge 契约入 eager 面）实测 603.4 超 599.6 线 3.8 — JS gzip 棘轮照 09-30 步长 +1.0 三步并一步 +4.8 一次到位 → 604.4 含余量（1052 同款；分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 1052 草稿长度四档弹窗（三语 15 键+弹窗组件+bridge 四参签名入 eager 面）合后实测 598.6 超 598.0 线 0.6 — JS gzip 棘轮照 09-30 步长 +1.0 一次到位 → 599.6 含余量（合并线独立 commit，按预授权报备新9）（恰等=构建抖动必炸区，758/575.8 先例）— JS gzip 棘轮照 09-30 步长 +1.0 一次到位 → 598.0 含余量（998 前端时间戳面增量；合并线独立 commit，报备新9） // 1045（未入档恢复卡片三缺陷：折叠+头部按钮+批量入口，前端面 UnfiledInputsBanner+styles.css）实测 595.5 对 596.0 线余 0.5<1.0 属抖动区 — JS gzip 棘轮照 09-30 步长 +1.0 一次到位 → 597.0 含抖动余量（分支 wt-tl1045 自报，报备派活方，独立 commit） // 四件（1025/1031/1032/1033）合后实测 594.3 对 595.0 线余 0.7<1.0 属抖动区 — JS gzip 棘轮照 09-30 步长 +1.0 一次到位 → 596.0（合并线独立 commit，报备新8） // 四件待合批（1029 压缩救援+1026 导航遮罩超时逃逸+1019 heads 限流+1023 未入档横幅，1026/1023 前端面：导航 lib+UnfiledInputsBanner+三语 4 键入 eager 面）合后实测 593.3 超 591.0 线 2.3 — JS 棘轮照 09-30 步长 +1.0 四次并一步 +4.0 一次到位 → 595.0 含余量 1.7（实测余 0.7<1.0 属抖动区，故再抬一档；合并线独立 commit，报备新8） // 五支攒批（1010 落后/断层通知 2 键+1011 设置页分区 20 键+1015 composer 4 键+1008 音频 7 键，三语×3 叠加入 eager 面）合后实测 590.2 超 589.0 线 1.2 — JS 棘轮照 2026-09-30 步长 +1.0 一次到位 → 591.0 含余量（合并线独立 commit，报备新8） // 1008 模型音频输入前端面（providerAudioInput 判定+对话框音频 checkbox+Composer 音频附件链+三语 7 键入 eager 面）实测 588.3 超 588.0 线 0.3 — JS 棘轮照 2026-09-30 步长 +1.0 一次到位 → 589.0 含余量（分支 wt-tl1008 自报，报备派活方，独立 commit） // 合并线同值收敛（批1005（稳定档四件套：注册表稳定档+config.Default 四项默认开+禁用态分支+三语 8 键）+982（write gate 开关化）合后构建实测 588.0x 恰等 588.0 线（恰等=构建抖动必炸区先例 403/591/721/758）— JS 棘轮照 2026-09-30 步长 +1.0 一次到位 → 589.0 含余量（合并线独立 commit，报备新8）） // 六支攒批（981/1006/973/990/991/996 合入+7d1839aed class-contract 修复）合后实测 587.9 超 587.0 线 0.9 — JS 棘轮照 2026-09-30 步长 +1.0 一次到位 → 588.0 含余量（合并线独立 commit，报备派活方） // 批41（976/979/980 合入：979 开关 UI 三语+表A 随动、980 协作中心弹窗样式入 eager 面）出包构建实测 586.1 超 586.0 线 0.1 — JS 棘轮 +1.0 一次到位 → 587.0 含余量（主对话出包前独立 commit，按 11:02 合并令既有预授权报备派活方） // 971 工具优化聚合页（SettingsPanel 聚合 pane+rev2 警告+三开关行+session_hub 预留入 eager 面）实测 584.8 对 585.0 线余 0.2<1.0 属抖动区 — JS 棘轮 +1.0 预抬 → 586.0 含余量（分支 wt-971 自报，报备派活方，独立 commit） // 批38c（944 renderArtifact/ArtifactCard + 938 SessionPurposeEditor 叠加入 eager 面）实测 584.1 超 582.0 线 2.1 — JS 棘轮 +3.0 一次到位 → 585.0 含余量（合并线独立 commit，报备派活方） // // 938 会话职责预抬 580.0→581.0（分支 wt-938 自报，独立 commit 96a02ff31，报备派活方）——本次合并 larger-of-two 取本线 582.0（940 抬线），自报值已被覆盖 // 940（composer 权限选择器两态：SettingsSelect 引入 Composer eager 面 + 三语 4 键×3 + SettingsPanel 成员行）实测 581.1 超 580.0 线 1.1 — 单步 +1.0 到 581.0 仍低于实测，JS 棘轮两步 +2.0 一次到位 → 582.0 含余量（B2 stacked/21 版九支先例；分支 wt-940 自报，报备派活方，独立 commit） // 批36 #53（npm 5 updates 叠最新树模拟合并）实测 579.1 超 579.0 线 0.1 — JS 棘轮 +1.0 一次到位 → 580.0 含余量（合并线独立 commit，报备派活方） // 686 工作流面板前端面（en 词典 41 键×3 入 initial+App 接线/labFlags/pane 增量；面板本体+fixture+store 走懒加载 chunk 未入 initial）实测 578.6 超 578.0 线 0.6 — JS 棘轮 +1.0 一次到位 → 579.0 含余量（分支 wt-686 自报，报备派活方，独立 commit） // 892B（会话/项目分组任意嵌套 UI：三语键+递归渲染/提案弹窗入 initial 面）实测 574.1 对 574.8 线余 0.7 <1.0 属抖动区，且下批前端件在队 — JS 棘轮 +1.0 预抬 → 575.8 含余量（批25A 判据；分支 wt-892b 自报，报备派活方，独立 commit） // 出包构建恰等 575.8/575.8 炸线（合并线段1 抬后实测贴死=抖动必炸区）— JS 棘轮 +1.0 一次到位 → 578.0 含余量（主对话出包清障后独立 commit，报备派活方） // 批27段1（884/885/889/879/890/887/811/877/875/861/896/893/860 十三件叠加，893 三语+811 折叠控件+860 ask 卡入 initial 面）实测 575.3 超 574.8 线 0.5 — JS 棘轮 +1.0 一次到位 → 575.8 含余量（合并线独立 commit，报备派活方） // 批25A（874 空白哨兵+重提交几何回底、882/878 叠加，874 sentinel lib+Transcript.tsx 入 initial 面）实测 573.2 对 573.8 线余 0.6 <1.0 属抖动区，且 868/870 两支在队 — JS 棘轮 +1.0 预抬 → 574.8 含余量（合并线独立 commit，报备派活方） // 低峰批（827/869/855/815/859 五支叠加，emoji catalog+815 登记窗口入 initial 面）实测 572.8 超 572.5 线 0.3 — JS 棘轮 +1.0 一次到位 → 573.5 含余量（主对话出包清障后独立 commit，报备派活方） // 24.4 合并线（856 协作中心全量九 commit 叠加，24.3 棘轮后再涨）实测 571.4 超 568.5 线 2.9 — JS 棘轮 +4.0 一次到位 → 572.5 含余量（856 无自报值，合并线实测代抬，独立 commit 报备派活方） // 24.3 出包（849/850 九件合入叠加）实测 567.5 超 567.2 线 0.3 — JS 棘轮 +1.0 一次到位 → 568.5 含余量（主对话出包前独立 commit） // 846 数据目录迁移向导前端面（SettingsPanel pane 分支+labLightKeys 谓词+bridge 接口与 mock；HomePointerLabPane 本体走懒加载 chunk 不进启动预算）实测 566.5 超 566.2 线 0.3 — JS 棘轮 +1.0 一次到位 → 567.2 含抖动余量（分支 wt-846 自报，报备派活方） // 24.2 出包（837/839/844/842/828+831②③ 七件合入叠加）实测 565.2 超 564.5 线 0.7 — JS 棘轮 +1.0 一次到位 → 566.2 含余量（主对话出包前独立 commit） // 24.1 出包（833 token 化+834+826/823/829 合入叠加）实测 564.5 超 563.5 线 1.0 — JS 棘轮 +1.0 一次到位 → 564.5（主对话补 commit——前笔 666fba3d3 因脚本二次读覆盖丢失 JS 修改，此笔补齐） // 24 版出包（wave3 七件+820 合入叠加+出包门禁三修）实测 562.8 超 562.5 线 0.3 — JS 棘轮 +1.0 一次到位 → 563.5 含抖动余量（主对话出包前独立 commit，报备派活方=用户） // 802 微信式群聊 UI（ChannelChatPanel 组件 679 行+CollabGroupPanel 频道 tab 换装+三语 31 键×3+CSS 438 行）实测 561.9 超 559.2 线 2.7 — JS 棘轮 +3.3 一次到位 → 562.5 含 0.6 抖动余量（409 同位组件先例；分支 wt-802 自报，报备派活方，独立 commit） // 批23-wave2 五支+800 叠加（790 ACP 登记+800 用量卡布局）实测 558.4 超 558.2 线 0.2 — JS 棘轮 +1.0 一次到位 → 559.2 含余量（合并线独立 commit，报备派活方） // 758 渲染统一（SteerCard 卡片化+collabMessage 抽取）合后实测恰等 557.0/557.0（恰等=抖动必炸区，403/591/721 先例）— JS 棘轮 +0.2 一次到位 → 558.2（758 自报无抬线、合并线实测恰等代抬，独立 commit 报备派活方） // 21 版九支合并叠加（720/731/539/733/738/744/739/740/742 前端面：731 双 notice 键、739 预取+看门狗、740/742 lab 管理与分组）实测 556.3 超 555.0 线 1.3 — JS 棘轮 +2.0 一次到位 → 557.0 含余量（B2 stacked 先例；合并线独立 commit，报备派活方） // 726 合后实测 553.9 对 554.0 线余 0.1=构建间抖动必炸区，且 724+722+727 连做件（724 懒加载改造动 initial 面）在队 — JS 棘轮 +1.0 预抬 → 555.0（合并线独立 commit，报备派活方） // 增件先行波（716 信件中心重排+723 steer 去重）合后实测 552.9 对 553.0 线余 0.1=构建间抖动必炸区，且 721/722/726/727/728 前端件在队 — JS 棘轮 +1.0 预抬 → 554.0（合并线独立 commit，报备派活方） // 19 版出包 build 抖动炸线：恰等 551.0/551.0 形态实测 551.1 超线（合并线回板预警兑现）— JS 棘轮 +1.0 一次到位 → 552.0（出包前独立 commit，报备派活方） // 第 19 批第三轮五件叠加（690 折叠条+674 弹窗折叠/一键直达+687/688 全场景/694+695 dismiss 扫描）实测 550.2 超 550.0 线 0.2 — JS 棘轮 +1.0 一次到位 → 551.0 含抖动余量（合并线独立 commit，报备派活方） // 663 一键分析产品化前端面（hang 监测入口+pending-crash 启动横幅+分析进度/重启面+三语键×12+bridge 类型/mock）实测 547.3 超 546.8 线 0.5 — JS 棘轮 +1.0 一次到位 → 547.8 含 0.5 抖动余量（分支 wt-663 自报，报备派活方，独立 commit） // 409 群聊式协作视图前端面（CollabGroupPanel 组件+MessagesSquare 图标+图标行挂载点+bridge 类型/mock）实测 544.4 超 542.9 线 1.5 — JS 棘轮 +1.7 一次到位 → 544.6 含 0.2 抖动余量（分支 wt-409 自报，报备派活方） // 287 Plan 用量前端面（store+概览卡+statusBar 项+三语键）实测 541.5 超 540.8 线 0.7 — JS 棘轮 +1.0 一次到位 → 541.8 含余量（分支 wt-287 自报，报备派活方）（本次合并与合并线已抬 541.8 同值，两侧同值并集） // 第 15 版批 10 件叠加炸线抬：实测 541.4 超 541.0 线 0.4 — JS 棘轮 +0.8 含余量（合并线独立 commit，报备派活方） 658 并发写通知内联跳转按钮（action helper+GitBranch 图标+四处挂载点 prop 线）实测 540.9 超 540.8 线 0.1 — JS 棘轮 +0.2 → 541.0 留抖动余量（616+619 同款；分支 wt-658 自报，报备派活方） //  // 9 件批量合入后两轮构建 536.9→539.5 抖动超 538.8 线 0.7 — JS 棘轮 +1.0 一次到位 → 540.8 含余量（合并线独立 commit，报备派活方） 624 诊断页文案本地化（issue 句子/建议动作 + runtime 区块标签，64 键×3，en 词典入 initial chunk）实测 538.5 超 537.8 线 0.7 — gzip 棘轮 +1.0 一步到位 → 538.8（分支 wt-624 自报，报备派活方） // 617+618 合入叠加炸线抬：分支自报 535.7 基于旧基线（未含 616+619 已合增量），合并后实测 536.8 超 536.2 线 0.6 — JS 棘轮 +1.0 一次到位 → 537.8 含余量（合并线独立 commit，报备派活方） // 618+617 合件前端面合入后实测 535.7 超 535.2 门限 0.5 618+617 合件前端面合入后实测 535.7 超 535.2 门限 0.5（crash overlay 一键分析按钮+确认流、issue 骨架模块、诊断页 pending 区）— gzip 步长 +1.0 一次到位 → 536.2（报备派活方，独立 commit） // 616+619 叠加炸线抬：616 侧自报 535.1 贴线、619 侧自报 534.3 均未动线，双件同批合并叠加实测 535.3 超 0.1 — JS 棘轮 +0.2 → 535.4 留抖动余量（合并线独立 commit，报备派活方） // 第 11 版 14 件大批合入后实测 534.2 恰等 534.2 炸线（build-to-build 字节抖动必炸区）— gzip 步长 +1.0 一次到位 → 535.2（报备新4，独立 commit） // 591 按钮分级（a 型 28 处 primary + b 型 158 处 secondary class 字符串）实测 534.1 超 534.0 门限 0.1 — 照 403 恰等抬先例门限停在实测+0.1（避开恰等抖动炸线区），+0.2 → 534.2（报备） // composer 四连合入后叠加实测 534.0 超 1.0（587 环+38-B1 hooks+composer 四连三层增量同落 initial chunk）— gzip 步长 +1.0 一次到位 → 534.0（报备新4） // 587 // composer 四连批(588/593/594/595 前端面)合入后实测 532.9 超 0.4（593 !! 触发面板+594 菜单门控拆分+595 tier wire 增量）— gzip 步长 +0.5 一次到位 → 533.0 (independent commit, 报备派活方)（主线 587 已抬同值 533.0，两侧同值并集） // 587 合入炸线抬：实测 532.8 超 0.3（587 面板失败态/重试环 + frontendLog ring 兜底）— gzip 步长 +0.5 一次到位 → 533.0（报备派活方） // 第三批八支(548/555/341/508/533/552/560/512 多面板组件)合入后实测 531.7 — 大批次 one-shot 到实测+0.8 余量 → 532.5（九支批先例 20261002；报备派活方） // 558 合入炸线抬：实测 519.3 超 0.3（558 SubagentsPanel/CapsulePanel 大组件）— gzip 步长 +1.0 一次到位 → 520.0 (independent commit, 报备派活方) // 465 合入后贴顶预抬（派活方批准）：实测 518.0/518.0 余 0.0，下批前端件必炸 — gzip 步长 +1.0 一次到位 → 519.0 (independent commit, 报备派活方) // zcode 十支批(536/524/525/546/547/551 前端面)炸线抬：实测 517.4 超 0.4 — gzip 步长 +1.0 一次到位 → 518.0 (independent commit, 报备派活方) // 9a/9b(506增量+514) 合后贴顶预抬：实测 515.9/516.0 余量仅 0.1（构建间抖动必炸区，下批 38/480/499 后续仍可能碰前端）— gzip 步长 +1.0 一次到位 → 517.0 (independent commit, 按「贴卡提前抬」规则报备派活方) // 9 支合并批(410/463/464/466/467/477/507/510)炸线抬：实测 515.5/515.0 超 0.5（前端面 463/466/467/507/510 叠加）— gzip 步长 +1.0 一次到位 → 516.0 (independent commit, 按棘轮规则报备派活方) // 506 分支预抬（贴卡余量提前抬判据）：506 实测 513.3/514.0 余量仅 0.7 且下批 499 前端面在队 — gzip 步长 +1.0 一次到位 → 515.0 (independent commit, 报备派活方) // 495 merge 后预抬（派活方裁决「现在就抬，不等下批炸」）：512.5/513.0 余量仅 0.5 且 501 明确改三语 hint 入下批必炸区 — gzip 步长 +1.0 一次到位 → 514.0 (independent commit) // nine-batch wave-2 (D-group+450+capsule+prompt three-section stacked): 505.2 measured, one-shot +6.8 per user 20261002 // nine-batch round-2 (12 pending pieces stacked incl 443/447): 501.0 measured, one-shot +4 headroom per user 20261002 "连续卡就一次多提" // nine-batch (443 scope note + 447 wrap variants): 493.2 measured, one-shot +0.5 // eight-batch package (446 hover+448 gate+442 popup+325 cluster front stacked): 491.0 measured, one-shot to measured+0.5 per the 2026-09-20 ratchet rule (no drip, independent commit) // wave-2 (172+152+128) merge-state: 485.4 over the 484.1 line (nudge+todo-tree+worktree-project JS stacked) — under the 2026-09-30 +1.0 step a single step lands 485.1 short of measured, so two steps +2.0 in one shot (B2 stacked precedent), no drip, independent commit // task 379 rework merge-state: 483.6 over the 480.6 line (wall+yaml+tests stacked on the batch-8 gate) — one-shot to measured+0.5 per the 2026-09-20/09-30 ratchet rule (no drip, independent commit) // +0.5: batch-8 release build measures 480.1x at the exact 480.1 boundary (build-to-build byte jitter) — one-shot +0.5 per the 2026-09-20 ratchet rule // fork: chain 468.8-era → 471.1 → 471.6 → 472.1 → 472.6 (tasks 242/192) → 473.1 (merge batch) → 473.6 (tasks 244 batch 1 + 163, same gate) → 474.1 (244 batch 2 + batch 4 same gate) → 474.6 (335 收口 jitter, pre-registered rule) → 475.1 (B3+opencodefix+effortfix2 merge batch, 475.0 measured; one-shot +0.5 per the 2026-09-20 ratchet rule) → 475.6 (task 337 locale guidance, 475.1 measured; one-shot +0.5) → 476.1 (task 333 rotation gate, 475.7 measured; one-shot +0.5) → 476.6 (task 282 fork features intro, 476.2 measured; one-shot +0.5) → 476.6 (task 338 memory pages, 476.2 measured; one-shot +0.5, same gate — B2 merge batch single const) → 477.6 (B2 merge batch stacked, 477.3 measured; one-shot two steps +1.0) → 478.1 (task 297 cold-cache card, 477.8 measured; one-shot +0.5) → 478.6 (task 361 pie/order-label second round, 478.1 measured; one-shot +0.5) → 479.1 (task 379 rework yaml-driven wall + columns config, 479.0 measured; one-shot +0.5 — superseded by the larger 480.6, larger-of-two rule) → 480.6 (batch-8 release jitter, 480.1x measured; one-shot +0.5) → 484.1 (task 379 rework merge-state, 483.6 measured; one-shot to measured+0.5) → 486.1 (wave-2 merge-state, 485.4 measured; two steps +2.0 under the 09-30 +1.0 rule) → 512.0 (nine-batch wave-2, 505.2 measured) → 513.0 (task 495, 512.5 measured; one-shot +0.5) → 514.0 (495 merge 后预抬, 512.5/513.0 measured) // 580 消息边界渲染信号链（user_input 事件链+切回 fingerprint 核对）分支实测恰等 540.8/540.8 炸线自抬 541.3——本次合并与主线 541.8 同值（两侧同值并集）（本次合并取 409 自报 544.6 终值；主线 542.9→544.3 链保留在 git 历史） //18版批合入（20件前端面叠加）实测 549.3 超 547.8 线 1.5 — JS 棘轮 +2.2 一步到位 → 550.0 留抖动余量（合并线独立 commit，报备派活方）  // 668 工具分组开关分支自报 +0.1 → 550.1（基于旧线 550.0，自报含独立 commit 与报备）——本次合并 larger-of-two 取本线 551.0，自报值已被覆盖（668 合并注释，报备派活方） // 707 压缩模型指定前端面（上下文治理卡开关行+off 提示行+模型偏好压缩模型行+bridge 两方法+三语 8 键×3）实测 551.4 超 551.0 线 0.4 — JS 棘轮 +1.0 一次到位 → 552.0 留抖动余量（705 同款；分支 wt-707 自报，报备派活方） // 704 活树接线补齐（App.tsx 是挂载树：主面 transcript|trajectory 分支 + 内联 topicbar 开关挂载 + flag/store 订阅；AppRuntimeView 壳为未挂载遗留）实测 552.3 超 552.0 线 0.3 — JS 棘轮 +1.0 一次到位 → 553.0 含抖动余量（同笔合并自报；轨迹视图本体仍走懒加载 chunk） // 704 轨迹视图开关链 eager 面（AppRuntime flag 订阅+AppRuntimeView 门控/bag+ChatPaneRegion 懒加载线+SettingsPanel 入口/详情卡+labFlags/bridge 类型）实测 551.5 超 551.0 线 0.5 — JS 棘轮 +1.0 一次到位 → 552.0 含抖动余量（轨迹视图本体走懒加载 chunk 不进启动预算；分支 wt-704 自报，报备派活方） // 721 快捷指令面板前端面（管理弹窗抽共享组件 QuickCommandsManagerDialog 双面挂载+picker 入口改呼出回调线；内嵌表单代码/styles 移除抵消部分增量）实测 553.5 超 553.0 线 0.5 — JS 棘轮 +1.0 一次到位 → 554.0 含抖动余量（704/705 同款；分支 wt-721 自报，报备派活方，独立 commit） // 722+724+727 连做前端面（lab-layout yaml 机制六文件：解析器/键引用注册表/内置默认布局数据/resolver + SettingsPanel 布局数据驱动改造 + 三子开关行/行迁移 + 727 心跳轮换卡内开关 + 三语键增删）合入主线 wave-2/3 后实测 554.1 超 554.0 线 0.1 — JS 棘轮 +1.0 一次到位 → 555.0 含抖动余量（704/705/707/721 同款；分支 wt-722 自报，报备派活方，独立 commit）// Task 269 rebased onto 471.1: measured 470.7, also within 471.1 — // 并集注：与主线第十七包 604.6 撞车，larger 取分支 605.6（分支含 1087+1088 增量预抬）
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
// 721 同笔棘轮：largest chunk 实测 320.1 超 320.0 线 0.1（总线抬至 554.0 的同一次构建）— 首抬 320.1 仍炸（显示值恰等=字节抖动必炸区，403/591 先例）再 +0.1 → 320.2 留余量（分支 wt-721 自报，报备派活方）
// 第三波（719/721/728）合并叠加实测 320.4 超 320.2 线 0.2，且 722/726/727 前端件在队 — +0.4 一次到位 → 320.6 含余量（合并线独立 commit，报备派活方）
// 726 合后实测 320.5 对 320.6 线余 0.1=抖动必炸区，且 724+722+727 连做件在队 — +0.2 预抬 → 320.8（合并线独立 commit，报备派活方）
// 731 前端面（双 notice code 映射 + en 词典 2 键入 initial chunk）实测恰等 320.8/320.8（恰等=抖动必炸区，403/591/721 先例）— largest-chunk 棘轮 +0.2 一次到位 → 321.0 留抖动余量（分支 wt-731 自报，报备派活方，独立 commit）
// 739 设置面板「点击即发」预取+挂起看门狗（App.tsx 挂钩子+lib/settingsPrefetch+ManagementSurface settled 锁存，均进 initial chunk）实测 320.8 恰等 320.8 线=抖动必炸区（HEAD 基线 320.6 余 0.2，增量全为本件）— +0.2 预抬 → 321.0（分支 wt-739 自报，报备派活方，独立 commit）——两侧同值 321.0，合并取同值并集
// 741 修复后实测 320.9 超 320.8 线 0.1（projectTreePageLoadFlipsIndicator 门函数+loadProjectTopics 条件包裹进 initial chunk）— +0.2 一次到位 → 321.0 含抖动余量（分支 wt-740 自报，报备派活方，独立 commit）——三支同值 321.0，合并三段并集
// 21 版九支合并叠加实测 322.0 超 321.0 线 1.0（九支前端面进 largest chunk：739 预取/742 lab 管理/740 项目树等）— largest 棘轮 +2.0 一次到位 → 324.0 含余量（合并线独立 commit，报备派活方）
// 802 微信式群聊 UI（ChannelChatPanel 组件+CollabGroupPanel 换装进 largest chunk）实测 325.8 超 324.0 线 1.8 — largest 棘轮 +2.5 一次到位 → 326.5 含 0.7 抖动余量（721/731 同款；分支 wt-802 自报，报备派活方，独立 commit） // 24.1 出包（833 token 化批量替换+833/834/826 叠加）实测 327.6 超 326.5 线 1.1 — largest 棘轮 +1.0 一次到位 → 327.5 含抖动余量（主对话出包前独立 commit，报备派活方=用户） // 24.1 二轮构建实测 327.6 仍超 327.5 线 0.1（构建间内容重排抖动）——连续三道线卡关，照用户 20261002「连续卡就一次多提」站规 one-shot → 330.0 含足量余量（nine-batch +6.8/+4 先例）
assertBudget("largest initial JavaScript chunk gzip", largestInitialJS, 360.2 * 1024); // 第23波（包27候件1137 Message.tsx +67 行推 largest chunk）实测 359.3 超 359.2 线 0.1 — largest gzip 棘轮照 L271-273 先例 +1.0 一次到位 → 360.2 含余量（协调线 00:56 裁决批准，合并线独立 commit，与 1137 返修分开） // 第十八包 1087+1088 合后实测 357.5 超 355.2 并集线 2.3 — 四步 +4.0 一次到位 → 359.2 含余量 1.7（报备新10）// 1088 二期（三工具+复制剪贴板+跨屏开关叠进 largest chunk）实测 354.1 超 354.0 线 0.1=贴线抖动必炸区 — largest gzip 棘轮照 09-30 步长 +1.0 预抬 → 355.2 含余量（分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 批38c 实测 352.8 超 339.0 线 13.8 — largest gzip 一次到位 → 354.0 含余量（合并线独立 commit，报备派活方） // 940（Composer eager 面引入 SettingsSelect 引用进 largest chunk）实测 338.6 超 338.0 线 0.6 — largest 棘轮 +1.0 一次到位 → 339.0 含余量（分支 wt-940 自报，报备派活方，独立 commit） // 686 工作流面板前端面叠加（en 词典/App 接线入 largest 所在 chunk）实测 336.5 对 337.0 线余 0.5 <1.0 属抖动区 — largest 棘轮 +1.0 预抬 → 338.0 含余量（分支 wt-686 自报，报备派活方，独立 commit） // 批27段1（十三件叠加，893 todo 空态卡+860 ask 卡+811 折叠控件进 largest）实测 334.8 超 334.5 线 0.3 — largest 棘轮 +1.0 一次到位 → 337.0 含余量（合并线独立 commit，报备派活方） // 批26（867 压缩失败卡+872 用量卡增量进 largest）实测 333.2 对 333.5 线余 0.3 = 构建间抖动必炸区，且 811/853/861/875/877 大批在队 — largest 棘轮 +1.0 预抬 → 334.5 含余量（合并线独立 commit，报备派活方） // 811 跨会话折叠统一（SteerCollabCard 折叠控件 + reconcile 气泡全文升级进 largest chunk）实测 332.7 超 332.5 线 0.2 — largest 棘轮 +0.3 → 332.8（分支 wt-811 自报；本次合并 larger-of-two 取主线批26 已抬 334.5，自报值被覆盖留档） // 861 readSnapshot 跨帧块坐标缓存（#50 切 tab 几何读取热点）+ TranscriptWindow 测量批量化，实测 332.93 超 332.5 线 0.43 — gzip 棘轮 +1.0 → 333.5（分支 wt-861 自报；本次合并 larger-of-two 取主线 334.5，自报值被覆盖留档） // 批25A（874 transcript 空白哨兵+Transcript.tsx 增量进 largest chunk）实测 332.9 超 332.5 线 0.4 — largest 棘轮 +1.0 一次到位 → 333.5 含余量（合并线独立 commit，报备派活方） // 24.4 largest raw 同批实测 1177.7 超 1170 线 7.7 — raw 棘轮 +10 一步到位 → 1180.0 含余量（同笔独立 commit） // 24.4 合并线（856 协作中心全量九 commit：CollabHubPanel 大组件族进 largest chunk）实测 331.7 超 330.0 线 1.7 — largest 棘轮 +2.5 一次到位 → 332.5 含余量（856 无自报值，合并线实测代抬，独立 commit 报备派活方） // assertBudget("largest initial JavaScript chunk gzip", largestInitialJS, 330.0 * 1024); // 938 largest 同批增量（组件入 largest 所在 chunk 实测 337.1，线同值 339.0 两侧并集，无二次抬线） 
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
assertBudget("deferred app-shell CSS gzip", appShellCSSGzip, 145.4 * 1024); // 包21 第2+3件（1136 图文排版回归重做 + 1129 侧栏窄态截断）合后实测恰等 144.4/144.4（余 0.0 必炸区）— CSS 棘轮 +1.0 一次到位 → 145.4 含余量（合并线独立 commit，报备协调线，按 20261024 预授权上限 147.4 内取实测+1.0） // 1087 截图标注一期（截图覆盖层+composer 按钮 CSS 入 app-shell 面）实测 143.1 对 143.4 线余 0.3<1.0 属抖动区 — CSS 棘轮照 09-30 步长 +1.0 预抬 → 144.4 含余量（分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 五支攒批合后实测 141.5 对 142.4 线余 0.9<1.0 属抖动区（1011 设置页新分区样式入 app-shell 面）— CSS 棘轮照 09-30 步长 +1.0 预抬 → 143.4 含余量（合并线独立 commit，报备新8） // 六支攒批合后实测 141.5 超 141.4 线 0.1 — CSS 棘轮照 09-30 步长 +1.0 一次到位 → 142.4 含余量（合并线独立 commit，报备派活方） // 938 弹窗+hover 卡职责行 CSS 实测 140.3 对 140.4 线余 0.1 贴线=抖动必炸区 — CSS 棘轮 +1.0 一次到位 → 141.4 含余量（分支 wt-938 自报，报备派活方，独立 commit） // 686 工作流面板 CSS（wf-panel/wf-node/wf-edge 族入 styles.css app-shell 面）实测 140.1 超 139.4 线 0.7 — CSS 棘轮 +1.0 一次到位 → 140.4 含余量（分支 wt-686 自报，报备派活方，独立 commit） // 低峰批（827 图标列统一网格/869 emoji/855 面板）实测 138.4 超 138.1 线 0.3 — CSS 棘轮 +1.0 一次到位 → 139.1 留余量（合并步进规则同 JS 线；主对话独立 commit 报备派活方） // 24.4 合并线（856 协作中心全量 CSS 大增：collab-hub/chat/deputybadge/atpop 等数百行，24.3 后首涨）实测恰等 137.6/137.6（恰等=抖动必炸区，403/591 先例）— CSS 棘轮 +0.5 一次到位 → 138.1 含余量（856 无自报值，合并线实测代抬，独立 commit 报备派活方） // assertBudget("deferred app-shell CSS gzip", appShellCSSGzip, 137.6 * 1024); 

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
  // 805 存储治理（wt-805 自报，报备派活方，独立 commit）：①存储页文案修订
  // （hint/stage/risk 三语改写，净增约 0.1）+ ③④迁移 UI 三语 15 键×3
  // （settings.migration.*，两段确认/模式标签/备份恢复指引，已把键前缀从
  // settings.sessionStorage.migration. 缩到 settings.migration. 并删未用的
  // title/lockNote 键后仍超线）。实测 zh 99.45 超 99.2 线 0.25、zh-TW 101.03
  // 超 100.8 线 0.23 — 双棘轮照 2026-09-30 步长规则 +0.5 一次到位 →
  // zh 99.7 / zh-TW 101.3 各含余量（781/707 双棘轮同款）。初始 JS 棘轮不动
  // （实测 558.9 对 559.2 线余量 0.3）。
  // tl 1108/1109 撞车记录：方案 E 标签条三语 7 键×3+子代理 2 键×3，分支侧基于基线 7bcc19a4f 实测 zh-TW 114.0 超 113.4 线自抬 114.4/zh 113.2——与主线第十六包已抬 zh-TW 115.4 撞车，larger 取主线（zh 同值 113.2 并集；完整链见分支 commit c0bab7ab0）
  const budget = name.startsWith("zh-TW-") ? 117.4 * 1024 : 115.3 * 1024; // 补记 包24 首件 1053 后 zh-TW 余 0.1 必炸区（三语 +7 键繁体刚性 116.1→116.3）— 按协调线抬线令 +1.0 → 117.4（合并线独立 commit，报备协调线） // 补记 包22 后 zh 余 0.1 必炸区（1138 三语键刚性成本）— 按协调线抬线令 +1.0 → 115.3（合并线独立 commit，报备协调线） // 补记 第七波（1131 轨迹等宽+1132 StartupSplash 开关五键）合后实测 zh 恰等 114.2/114.2 炸线（恰等=抖动必炸区）— 按协调线批准最小步长 +0.1 → 114.3（1132 五键净增 81B 刚性成本；合并线独立 commit，报备协调线） // 第十七包十件（zh 恰等 113.2 必炸区+zh-TW 同批繁体必炸预抬，各 +1.0 一次到位，报备新10）；第十六包六件（1100+1101 宫格/帮助组/跳转标签页三语键量大+1110 2键+1099 6键）合后 check 口径实测 zh 112.6 超 112.2 线 0.4、zh-TW 114.4 恰等 114.4 线（恰等=抖动必炸区）— 双棘轮照 09-30 步长 +1.0 一次到位 → 113.2/115.4 各含余量（vite reporter 口径报 115.77/117.63 系已知口径差，check 自算为准）（合并线独立 commit，报备新10）// tl 1097（日志管理三语 16 键×3 入 eager locale chunk：logManagement 卡 6 键 + alertLogRetention/alertLogMaxTotal 各 5 键）实测 zh 111.2 恰等 111.2 线（恰等=构建抖动必炸区，403/591/721/758 先例）— zh 棘轮照 09-30 步长 +1.0 一次到位 → 112.2 含余量；zh-TW 实测未触线不动（分支 wt-tl1097 自报，报备派活方，独立 commit） // 1063族（1063 面板整合/1065 合成入口/队尾回执，三语+2键×3 入 eager locale chunk）合后实测 zh-TW 112.4 恰等 112.4 线（恰等=构建抖动必炸区，403/591/721/758 先例）— locale gzip 棘轮照 09-30 步长 +1.0 一次到位 → 113.4 含余量；zh 未触线不动（合并线独立 commit，按预授权报备新9） // 四件（1025/1031/1032/1033）合后实测 zh 109.3 对 110.2 线余 0.9<1.0 属抖动区，zh-TW 111.0 对 112.4 余 1.4 不动 — zh 棘轮 +1.0 照 zh-TW 先例一次到位 → 111.2（合并线独立 commit，报备新8） // 四件待合批合后实测 zh-TW 111.0 对 111.4 线余 0.4<1.0 属抖动区（zh 109.2 对 110.2 余 1.0 不动）— zh-TW 棘轮照 09-30 步长 +1.0 一次到位 → 112.4 含余量（合并线独立 commit，报备新8） // 五支攒批合后实测 zh 109.1 对 109.2 线余 0.1=贴线抖动必炸区、zh-TW 110.8 超 110.4 线 0.4 — 双棘轮照 09-30 步长 +1.0 一次到位 → zh 110.2 / zh-TW 111.4 各含抖动余量（合并线独立 commit，报备新8） // 六支攒批合后实测 zh 107.9 对 108.2 线余 0.3<1.0 抖动区、zh-TW 109.7 超 109.4 线 0.3 — 双棘轮照 09-30 步长 +1.0 一次到位 → zh 109.2 / zh-TW 110.4 各含抖动余量（971 同款 + 繁体同批必炸先例；合并线独立 commit，报备派活方） // // 971 工具优化聚合页三语 8 键×3（settings.toolOptHub.*：rev2 警告长文案+三生效点+四配套+session_hub 预留）实测 zh 106.9 对 107.2 线余 0.3<1.0 抖动区、zh-TW 108.7 超 108.4 线 0.3 — 双棘轮照 2026-09-30 步长 +1.0 一次到位 → zh 108.2 / zh-TW 109.4 各含抖动余量（zh-TW 繁体同批必炸照 802/915 先例；分支 wt-971 自报，报备派活方，独立 commit） // 686 工作流面板三语 41 键×3（settings.workflowPanel 主链 + palette.cmd + workflowPanel.* 状态/图例/详情面）实测 zh 105.4 对 106.2 线余 0.8<1.0 属抖动区 — zh 棘轮 +1.0 预抬 → 107.2；zh-TW 实测 107.1 对 108.4 余 1.3 不动（分支 wt-686 自报，报备派活方，独立 commit） // 915（multi_edit 注入面三语 hint 改写加长：toolOptimizationsHint 补第二效果+生效时机+默认开说明）实测 zh 104965B 对 105.2KiB 线余 0.24KiB<1.0 抖动区 — zh 棘轮 +1.0 一次到位 → 106.2；zh-TW 实测 106626B 余 0.77KiB<1.0 且繁体同批必炸照 802/287/722 先例预抬 +1.0 → 108.4 各含抖动余量（分支 wt-915 自报，报备派活方，独立 commit） // 892B（分组嵌套 UI 三语 9 键×3：createSubGroup/deletePlan*）实测 zh 106662B 对 106700B 线余 38B=显示恰等 104.2/104.2（恰等=抖动必炸区，403/591/649 先例）— zh 棘轮 +0.5 → 104.7；zh-TW 实测 108417B 余 0.52KiB<1.0 抖动区且繁体同批必炸（802/287/722 先例）— +0.5 → 106.9（分支 wt-892b 自报，报备派活方，独立 commit） // 批27段2（895 sentinel 设置面三语键+892a/881 叠加）实测 zh 104.3 超 104.2 线 0.1 — zh 棘轮 +1.0 一次到位 → 105.2；zh-TW 同批繁体必炸照 802/287/722/846 先例预抬 +1.0 → 107.4 各含余量（合并线独立 commit 报备派活方） // 低峰批（827/869/855 三语键入）实测 105.5 超 105.4 线 0.1（贴线=抖动必炸区）— zh-TW 棘轮 +1.0 一次到位 → 106.4 含余量（主对话独立 commit 报备派活方） // 24.4 zh-TW 同批实测 104.9 超 104.8 线 0.1（繁体同批必炸先例兑现，802/287/722 同判）— zh-TW 棘轮 +0.5 一次到位 → 105.4 含余量（同笔独立 commit） // 24.4 合并线（856 协作中心全量三语键新增）实测 zh 103.2 超 102.2 线 1.0 — zh 棘轮 +2.0 一次到位 → 104.2 含余量（zh-TW 实测见下笔；856 无自报值，合并线实测代抬，独立 commit 报备派活方） // 24.3 出包实测 zh-TW 103.8 恰等 103.8 线（恰等=抖动必炸区，403/591 先例；849/850 三语增量）— zh-TW 棘轮 +1.0 一次到位 → 104.8 含余量；zh 实测低于线不动（主对话出包前独立 commit） // 846 数据目录迁移向导三语 29 键×3（settings.homePointer.* 主链 + risk. 六条长文案 + warning/reset/lock 等，hint 与风险文案均长）实测 zh 101.9 超 101.2 线 0.7 — 双棘轮照 2026-09-30 步长规则 +1.0 一次到位 → zh 102.2 / zh-TW 103.8 各含抖动余量（zh-TW 同批繁体更长照 802「繁体同批必炸」先例预抬；704/722 双棘轮同款；分支 wt-846 自报，报备派活方） // 24 版出包（wave3+820+出包门禁三修的 locale 微涨）实测 zh 100.5 超 100.2 线 0.3 — zh 棘轮 +1.0 一次到位 → 101.2 含抖动余量（主对话出包前独立 commit，报备派活方=用户） // 24 版 zh-TW 随批实测 102.1 超 101.8 线 0.3（802 注释「繁体同批必炸」预警兑现）— zh-TW 棘轮 +1.0 一次到位 → 102.8 含抖动余量（同笔独立 commit） // 802 微信式群聊三语 31 键×3（collabGroup.chat.* 建群/详情/邀请/撤回等，文案较短）实测 zh 100.0 超 99.7 线 0.3 — 双棘轮照 2026-09-30 步长规则 +0.5 一次到位 → zh 100.2 / zh-TW 101.8 各含余量（zh-TW 繁体同批必炸照 287/722 先例预抬；677/707 双棘轮同款；分支 wt-802 自报，报备派活方，独立 commit） // 805 存储治理三语（settings.migration.* 11 键×3 + sessionStorage. 旧前缀回收，hint 较长。曾以 settings.sessionStorage.migration. 缩到 settings.migration. 并删未用的 title/lockNote 键后仍超线）。实测 zh 99.45 超 99.2 线 0.25、zh-TW 101.03 超 100.8 线 0.23 — 双棘轮照 2026-09-30 步长规则 +0.5 一次到位 → zh 99.7 / zh-TW 101.3 各含余量（781/707 双棘轮同款）。初始 JS 棘轮不动（实测 558.9 对 559.2 线余量 0.3）。 // 707 压缩模型指定三语 8 键×3（settings.compactModel 6 键 + compactModelPreference 3 键，hint 较长）实测 zh 97.0 超 96.7 线 0.3 — 双棘轮一次到位 zh +0.5 → 97.2 / zh-TW +0.5 → 98.8 各含余量（zh-TW 实测 98.6 超 98.3 线 0.3；705 双棘轮同款；分支 wt-707 自报，报备派活方） // 19 批六轮合并线复验：zh 实测恰等 96.6/96.6 炸线（恰等=抖动必炸区，403/591 先例）— locale 棘轮 zh +0.1 → 96.7 / zh-TW +0.1 → 98.3 留抖动余量（合并线独立 commit，报备派活方） // 705 折叠开关三语 6 键×3（settings.sessionCollabAutoFold + Hint + on/off + msg.collabFoldExpand/Collapse，hint 较长）实测 zh 96.5 超 96.4 线 0.1、zh-TW 98.1 超 98.0 线 0.1 — 双棘轮 +0.2 一步到位 → 96.6/98.2 留抖动余量（677/670 双棘轮同款；分支 wt-705 自报，报备派活方） // 677 群聊开关三语 4 键×3（settings.collabGroupView + Hint + on/off，hint 较长）实测 zh 96.3 超 96.3 线（恰等=抖动必炸区，403/591/642 先例）— zh 棘轮 +0.1 → 96.4（实测 96.3 对 96.4 线余量 0.1）；zh-TW 首抬 +0.1 → 97.9 后实测恰等 97.9/97.9（恰等=抖动必炸区，591/642 同判）— 再 +0.1 → 98.0 留抖动余量（分支 wt-677 自报，报备派活方）；651 自抬 97.6/96.1；18 版批叠加实测 zh-TW 97.7 超 97.6 线 0.1 — zh-TW 棘轮 +0.2 → 97.8（同笔报备）；18 版批叠加实测 zh 96.2 超 96.1 线 0.1 — zh 棘轮 +0.2 → 96.3（合并线独立 commit，报备派活方）；651 链：（649 的 95.6/97.1、495 的 95.7/97.2、652 的 97.2/95.7、663 的 97.3/95.8 均被覆盖，链保留在 git 历史） （max 终值；652 自报 97.2/95.7、663 自报 97.3/95.8 均被覆盖，链保留在 git 历史）（与 651 已抬 97.6 同值，两侧同值并集） //670 fork 首启提示三语 10 键×3（forkNotice.* 6 键 + settings.forkNotice* 4 键）实测 zh 95.814 KiB 超 95.6 线 0.214、zh-TW 97.319 KiB 超 97.1 线 0.219 — 双棘轮 +0.5 一步到位 → 96.1/97.6（分支 wt-670 自报，报备派活方） //649 收件箱改进包三语 3 键×3（senderLabel/recipientLabel/viewGroup）叠加后 zh 实测 95.473 KiB（97764B）对 95.5 线余量仅 28B、zh-TW 96.979 KiB（99306B）对 97.0 线余量仅 22B（恰等=抖动必炸区，403/591/642 先例）— zh 棘轮 +0.1 → 95.6、zh-TW +0.1 → 97.1（分支 wt-649 自报，报备派活方，与同支 CSS 棘轮同笔 chore commit） //517 zh-TW 恰等 96.2/96.2 自抬 96.4（分支自报独立 commit；本次合并取主线 97.0 终值，不重复抬） //  zh-TW 655 rename stacked over 642 measured 96.1 over 0.1 → 96.2（合并线独立 commit） // 287 Plan 用量三语 16 键×3（planUsage.* 14 键 + status.plan* 2 键）实测 zh 94.7 超 94.5 线 0.2 — zh 棘轮 +1.0 一次到位 → 95.5；zh-TW 繁体同批必炸照 624 先例预抬 +1.0 → 97.0（分支 wt-287 自报，报备派活方） // 642 繁体键同批实测 95.9 超 95.7 线 0.2 — zh-TW 棘轮 +0.3 → 96.0 含余量（分支 wt-642 自报，报备派活方） // 642 模拟崩溃演练三语键（crash.mock* 7 键 + settings.mockCrash 3 键）实测 zh 恰等 94.4/94.4（恰等=抖动必炸区，403/591 先例）— zh 棘轮 +0.1 → 94.5，zh-TW 实测低于线不动（分支 wt-642 自报，报备派活方） // 624 诊断页文案本地化（64 键×3：issue 句子/建议动作/runtime 区块标签；繁体天然比简体长）实测 zh 93.978 超 92.4 线 1.578、zh-TW 95.412 超 93.7 线 1.712 — 单步 +1.0 不足，照 wave-2/B2 叠加先例两步 +2.0 一次到位 → 94.4/95.7（分支 wt-624 自报，报备派活方） // 第 11 版 14 件大批合入后 zh-TW 实测 92.7 恰等 92.7 炸线（591 二段 158 处 b 型 class 叠加）+ zh 预抬防同批炸 — gzip 步长 +1.0 一次到位 → 93.7/92.4（报备新4，独立 commit） // 403+604 三语新增实测 zh 恰等 91.3/91.3 炸线（403 加载文案+604 settingsPath 前缀键）— zh +0.1 → 91.4（报备新4） // composer 四连合入实测 zh 91.3 超 0.3 + zh-TW 92.7 超 0.3（!! 快捷指令面板 hint+菜单二级页+594 disabled tooltip 三语增量）— 一次到位 91.3/92.7（报备新4） // 563 合入 zh-TW 恰等 92.2/92.2 构建抖动必炸区 +0.2 → 92.4（报备新4） // 247 合入恰等 90.8/90.8（构建抖动必炸区）+0.2 → 91.0 // zh-TW 实测恰等 92.1/92.1（构建抖动必炸区）+0.1 → 92.2 // 第三批八支三语键合入后实测 zh 90.7 / zh-TW 91.8 — zh +0.3 到 90.8、zh-TW 预抬 +0.2 到 92.1（大批次 one-shot；报备派活方) // 506 分支预抬（贴卡余量提前抬判据）：506 三语 hint 实测 88.6/90.0，对 89.5/90.9 余量仅 0.9/0.9 且下批 499 前端面在队 — gzip 步长 +1.0 一次到位 → 90.5/91.9 (independent commit, 报备派活方) // 501 merge 后补抬 zh-TW（新证据：501 三语 hint 推高至实测 89.7/89.9 余量仅 0.2，下批 499 前端面必炸）— 与上轮 initial/zh 预抬同判据，gzip 步长 +1.0 一次到位 → 90.9 (independent commit, 报备派活方) // 495 merge 后预抬（派活方裁决，与 initial gzip 514.0 同笔）：zh 88.1/88.5 余量仅 0.4 且 501 改 settings.perfMonitorHint 三语必炸 — gzip 步长 +1.0 一次到位 → 89.5 (independent commit) // zh-TW eight-batch: 87.4 measured (325 cluster traditional copy longer than zh) — one-shot +0.5 // eight-batch (325 cluster copy): zh 86.1 over 86.0 — one-shot +0.5 to 86.5 (equal to zh-TW, larger-of-two rule), independent commit // wave-2 (172 feedbackNudge settings copy): zh 85.1 over the 85.0 line — one-shot +1.0 under the 2026-09-30 step rule (independent commit) // task 385a: zh 84.0->84.5, zh-TW 85.5->86.0; one-shot +0.5 each // batch-8 merge stack: zh measures 84.6 over the merged 84.5 line (373-R1 + 385a stacked) — one-shot +0.5 to 85.0 per the 2026-09-20 ratchet rule (no drip) // R1.1 zh-TW 85.5->86.0 collides at 86.0 with 385a (same value, larger-of-two rule); zh keeps the larger 85.0 // batch-8 release build: zh-TW measures 86.04 at the exact 86.0 boundary — one-shot +0.5 to 86.5 per the same ratchet rule // task 261 (prompt history picker, 7 keys x3): zh measures 86.6 over 86.5 — one-shot +1.0 to 87.5 per the gzip-step rule; zh-TW then measured dead-even 87.9 at its 87.9 line (task 261 traditional copy longer) — one-shot +1.0 to 88.9 // B1 (2026-10-03 night dispatch, hooks label simplified into the risk line, net ~0): zh 87.3 / 87.5, zh-TW 88.6 / 88.9 measured — both under, no bump. // 461-P3/P4/P5 (2026-10-03: resend button + inbox dropdown + preapproval relayout, 3-dialect keys): zh measures dead-even 87.5/87.5 — one-shot +1.0 to 88.5 per the 2026-09-30 step rule (independent commit); zh-TW holds at 88.9 (measured under the line this batch). // 461-P7 (2026-10-04 00:17 package: three-tier stop escalation, 5 keys x3): zh-TW measures 89.0 over the merged 88.9 line — one-shot +1.0 to 89.9 per the 2026-09-30 step rule (independent commit); zh measures 87.7, holds under 88.5. // 580 同批 zh-TW 实测 96.1 超 96.0 线 0.1 自抬 96.2（分支自报独立 commit；与 287 已抬 97.0 取大者 → 97.0 终值） // 670 fork 首启提示三语 10 键×3 分支自抬 97.6（同值并集；本次合并取 97.6 终值）（同值并集） // 704 轨迹视图三语 21 键×3（开关 4 键 + 台账/切换/空态/加载 15 键 + 检查器/时间线 2 键，hint 较长）实测 zh 97.168 超 96.7 线 0.468、zh-TW 98.782 超 98.3 线 0.482 — 双棘轮照 2026-09-30 步长规则 +1.0 一次到位 → 97.7/99.3 含抖动余量（分支 wt-704 自报，报备派活方） // 722+724+727 连做三语键（settings.heartbeatRotation + Hint + on/off + settings.safetyCostControl.subHint 四键×3 新增，memberState 一键×3 移除，safetyCostControlHint 重写加长）实测 zh 97.9 超 97.7 线 0.2、zh-TW 99.5 超 99.3 线 0.2 — 双棘轮照 2026-09-30 步长规则 +1.0 一次到位 → 98.7/100.3 含抖动余量（704 同款；分支 wt-722 自报，报备派活方，独立 commit）  // Task 380/O4 (detached idle release input): zh-TW measures dead-even
  // 补记 1087+1088（截图标注三语 24+11 键×3）：分支侧基于基线 98cac0797 自抬 zh-TW 115.4/zh 113.2——与主线第十七包已抬 116.4/114.2 撞车，larger 取主线（完整链见分支 commit 1b4be726e）
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
const rawInitialBudgetKiB = 3_216.0; // 补记 包25候件（1150 Go 面+1141 前端增量）合后实测 3206.9 超 3206.0 线 0.9 — raw 棘轮照先例 +10 一次到位 → 3216.0 含余量（合并线独立 commit，报备协调线） // 第十八包 1087+1088 合后实测 3197.6 超 3176.0 并集线 21.6 — 超幅三步 +30 一次到位 → 3206.0 含余量 8.4（报备新10）// 1088 二期（三工具+复制剪贴板+跨屏开关入 raw 面）实测 3173.4 超 3169.0 线 4.4 — raw 棘轮照先例 +10 一次到位 → 3176.0 含余量（分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 1087 截图标注一期（组件+模型+三语键+CSS 入 raw 面）实测 3168.3 超 3155.0 线 13.3 — raw 棘轮照先例 +10 一步并一步 +14 一次到位 → 3169.0 含余量（实测贴 3167.0 线仅 1.3<1.5 属抖动区，照 09-30 贴卡预抬判据再 +2.0；分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 1063族（1063 面板整合+1064 删除+1065 合成+队尾回执，组件/三语/Go binding 前端面叠加）合后实测 3149.0 超 3145.0 线 4.0 — raw 棘轮照先例 +10 一次到位 → 3155.0 含余量（合并线独立 commit，按预授权报备新9） // 1021 合后实测 3136.3 超 3135.0 线 1.3 — raw 棘轮照先例 +10 一次到位 → 3145.0 含余量 8.7（合并线独立 commit，报备新9） // 四件（1025/1031/1032/1033）合后实测 3126.0 超 3125.0 线 1.0 — raw 棘轮照先例 +10 一次到位 → 3135.0 含余量 9.0（合并线独立 commit，报备新8） // 四件待合批合后实测 3123.1 超 3115.0 线 8.1 — raw 棘轮照先例 +10 一次到位 → 3125.0 含余量 1.9（合并线独立 commit，报备新8） // 六支攒批合后实测 3105.5 超 3105.0 线 0.5 — raw 棘轮照先例 +10 一次到位 → 3115.0 含余量（合并线独立 commit，报备派活方） // 批41（976/979/980 合入）出包构建实测 3095.5 超 3095.0 线 0.5 — raw 棘轮 +10 一次到位 → 3105.0 含余量（主对话出包前独立 commit，按 11:02 合并令既有预授权报备派活方） // // 批37 合并线（932 events 行+931 修法+909 清单 raw 面叠加）实测 3065.6 超 3065.0 线 0.6 — raw 棘轮照 686/批27段2/低峰批先例 +10 一次到位 → 3075.0 含余量（合并线独立 commit 报备派活方） // 686 工作流面板前端面（en 词典 41 键+App 接线/pane/lab 增量入 raw 面）实测 3061.7 超 3055.0 线 6.7 — raw 棘轮 +10 一次到位 → 3065.0 含余量（分支 wt-686 自报，报备派活方，独立 commit） // 批27段2（十七件叠加 raw 面）实测 3045.0 恰等 3045.0 线（恰等=抖动必炸区）— raw 棘轮 +10 一次到位 → 3055.0 含余量（合并线独立 commit 报备派活方） // 低峰批（827/869/855/815 叠加 raw 面）实测 3033.6 超 3030.0 线 3.6 — raw 棘轮照 231/318/B2 先例 +10 一次到位 → 3045.0 含余量（主对话独立 commit 报备派活方） // 24.4 合并线（856 协作中心全量九 commit：组件+CSS+三语 raw 面大增）实测 3024.4 超 3000.0 线 24.4 — raw 棘轮 +10 步长三步并一步 +30 一次到位 → 3030.0 含余量（856 无自报值，合并线实测代抬，独立 commit 报备派活方） // const rawInitialBudgetKiB = 3_000.0; // 并集注：与主线第十七包 3_175 撞车，larger 取分支 3_176
// [fork note] the smallest one-decimal ratchet. Bumped to 2_461.0 for the serve pool
assertBudget("initial raw JavaScript and CSS", rawInitialBytes, rawInitialBudgetKiB * 1024);
// [fork note] 2026-09-15: measured 1102.2 KiB raw - same pre-existing growth as the
// gzip budget above; re-set from the measured value with headroom.
  // 2520: raw ratchet step widened to ~+10 KiB (user 2026-09-20). Merge batch measured 2511.8 (tasks 185/181/collab frontend).
assertBudget("largest initial JavaScript chunk raw", largestInitialJSRaw, 1_275 * 1024); // 第十八包 1087+1088 合后实测 1266.7 超 1265 并集线 1.7 < 单步 10 — +10 一次到位 → 1275.0 含余量 8.3（报备新10）// 1088 二期（三工具+复制剪贴板+跨屏开关叠进 largest chunk）实测 1255.8 超 1255 线 0.8 — largest raw 棘轮照批38c 先例 +10 一次到位 → 1265 含余量（分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 1087 截图标注一期（ScreenshotOverlay+标注模型+三语键叠进 largest chunk）实测 1252.2 超 1245 线 7.2 — largest raw 棘轮照批38c 先例 +10 一次到位 → 1255 含余量（分支 wt-tl1087-1088 自报，报备派活方，独立 commit） // 批38c 实测 1238.4 超 1210 线 28.4 — largest raw 一次到位 → 1245 含余量（合并线独立 commit，报备派活方） // 940（Composer eager 面引入 SettingsSelect 进 largest chunk）实测 1201.4 超 1200 线 1.4 — raw 棘轮 +10 一次到位 → 1210 含余量（913 同款；分支 wt-940 自报，报备派活方，独立 commit） // 任务 913 焦点归还件（composerFocus 共享器+导航遮罩恢复 hook+自愈放弃留痕）实测 1190.1 超 1190.0 线 0.1 — raw 棘轮 +10 一次到位 → 1200 含余量（913 独立 commit 报备派活方） // 低峰批实测 1180.2 超 1180.0 线 0.2（恰等/贴线=抖动，分支 emoji catalog 入 chunk）— raw 棘轮 +10 一次到位 → 1190 含余量（主对话独立 commit 报备派活方） // (原) 24.2 出包实测 1160.1 贴 1160.0 线 0.1（恰等/贴线=抖动必炸区）— raw 步长 +10 一次到位 → 1170.0（主对话出包前独立 commit） // 并集注：与主线第十七包 1_255 撞车，larger 取分支 1_265（4a6621443 先例）
