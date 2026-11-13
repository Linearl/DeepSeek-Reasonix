// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1575-hover-minimum-tier.test.ts
//
// tl1575 hover 补齐最小档——tl1558 报告 B 清单（B0 实锤例 + B1 七族 + B2 逐项）
// 的 CSS 契约钉。静态读源断言（css-contract 判例），B3 排除表负钉防误伤。
//   B0 侧栏折叠钮三形态：基础 hover 背景换 --button-bg（原 --bg-elev 与
//       topicbar 容器同值=零变化）；workbench 抹平链保留可见底色（静止透明、
//       悬停现形）；--pressed 两个死类补样式。
//   B1：set-seg 全局一条（--on 不覆盖）、composer__btn 基类（:where 零特异
//       性，变体继续覆盖）、web 壳 hover 族（零 :hover → 六族）、三个死类
//       （icon-btn/remote-status/load-more）最小反馈面。
//   B2：23 行逐项 hover 落位（内层 span 类按 B3 口径补在行级父按钮上）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const src = resolve(here, "..");
const styles = readFileSync(resolve(src, "styles.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const classic = readFileSync(resolve(src, "styles/mode-classic.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const workbench = readFileSync(resolve(src, "styles/mode-workbench.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const connections = readFileSync(resolve(src, "components/ProviderConnections.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
const web = readFileSync(resolve(src, "web/web.css"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

function cssBlocks(css: string, selectorSubstring: string): string[] {
  const blocks: string[] = [];
  const rule = /([^{}]+)\{([^{}]*)\}/g;
  let match: RegExpExecArray | null;
  while ((match = rule.exec(css)) !== null) {
    if (match[1].includes(selectorSubstring)) blocks.push(match[2]);
  }
  return blocks;
}

console.log("\ntl1575 hover minimum tier (tl1558 B list)");

// ── B0 实锤例：侧栏折叠钮 ──────────────────────────────────────────────
{
  const hover = cssBlocks(styles, ".topicbar__chrome-btn:hover:not(.topicbar__chrome-btn--blocked)").join("\n");
  ok(hover.includes("background: var(--button-bg)"), "B0: base chrome-btn hover paints --button-bg (was --bg-elev = same as topicbar bg)");
  ok(!hover.includes("var(--bg-elev)"), "B0: base chrome-btn hover no longer uses the invisible --bg-elev background");
  ok(styles.includes(".topicbar__chrome-btn--pressed:not(.topicbar__chrome-btn--blocked)") &&
     cssBlocks(styles, ".topicbar__chrome-btn--pressed").join("\n").includes("var(--button-bg-hover)"),
    "B0/B1-4: topicbar --pressed dead class now has a pressed style");
  const flat = cssBlocks(workbench, ".topicbar__chrome-btn:hover:not(.topicbar__chrome-btn--blocked)").join("\n");
  ok(flat.includes("background: var(--button-bg)") && !flat.includes("background: transparent"),
    "B0: workbench flatten chain keeps a visible hover background (transparent at rest only)");
  ok(classic.includes(".app-chrome__panel-toggle--pressed:not(.app-chrome__panel-toggle--blocked)"),
    "B0/B1-4: classic panel-toggle --pressed dead class now has a pressed style");
}

// ── B1 系统性缺口 ──────────────────────────────────────────────────────
{
  ok(styles.includes(".set-seg__btn:hover:not(.set-seg__btn--on):not(:disabled)") &&
     cssBlocks(styles, ".set-seg__btn:hover").join("\n").includes("var(--button-bg)"),
    "B1-1: set-seg base hover added, --on selected state excluded");
  ok(styles.includes(".composer__btn:where(:not(:disabled):hover)") &&
     cssBlocks(styles, ".composer__btn:where(:not(:disabled):hover)").join("\n").includes("var(--button-bg)"),
    "B1-3: composer__btn base hover added with :where (variants keep overriding by source order)");
  ok(cssBlocks(styles, ".icon-btn:hover").length > 0, "B1-5: icon-btn dead class has hover feedback");
  ok(cssBlocks(styles, ".project-tree__remote-status:hover").length > 0, "B1-6: project-tree__remote-status dead class has hover feedback");
  ok(cssBlocks(styles, ".taskmonitor__load-more:hover").length > 0, "B1-7: taskmonitor__load-more dead class has hover feedback");
  ok(cssBlocks(web, ".web-icon-btn:hover").length > 0 &&
     cssBlocks(web, ".web-new-btn:hover").length > 0 &&
     cssBlocks(web, ".web-composer .web-stop:not(:disabled):hover").length > 0 &&
     cssBlocks(web, ".web-sidebar li button:not(.active):hover").length > 0,
    "B1-2: web shell hover family added (icon/new/session-row/stop; web.css had zero :hover rules)");
}

// ── B2 逐项（23 行；span 类按 B3 口径落在行级父按钮）───────────────────
{
  const b2: Array<[string, string, string]> = [
    ["styles", ".topicbar__worktree-btn:hover", "B2-1 topicbar worktree chip"],
    ["styles", ".capsule-panel__row:hover", "B2-2 capsule running row (mirrors --ended)"],
    ["styles", ".notice-line__summary-toggle:hover", "B2-3 notice summary toggle"],
    ["styles", ".msg-merged__head:hover", "B2-4 merged message head"],
    ["styles", ".recovery-wait-banner__stop:not(:disabled):hover", "B2-5 recovery stop (danger)"],
    ["styles", ".turn-file-list__entry:hover", "B2-6 turn file list entry"],
    ["styles", ".mem-link:not(.mem-link--dead):hover", "B2-7 mem link (dead excluded)"],
    ["styles", ".diag-section__header:hover", "B2-8 diagnostics section header"],
    ["styles", ".diag-issue-cluster__toggle:hover", "B2-9 issue cluster toggle"],
    ["styles", ".typography-settings__presets button:not(.is-active):hover", "B2-10 typography presets"],
    ["styles", ".typography-settings__advanced:hover", "B2-10 typography advanced"],
    ["styles", ".typography-settings__undo:not(:disabled):hover", "B2-10 typography undo"],
    ["styles", ".theme-gallery__back:hover", "B2-11 theme gallery back"],
    ["styles", ".theme-gallery__tab:not(.theme-gallery__tab--on):hover", "B2-11 theme gallery tab (covers tab-count span)"],
    ["styles", ".remote-tree__row:not(.is-selected):hover", "B2-12 remote tree row"],
    ["styles", ".terminal-session__select:hover", "B2-13 terminal session select"],
    ["styles", ".channel-chat__back:hover", "B2-14 channel back"],
    ["styles", ".channel-chat__item:hover", "B2-14 channel item row (covers itemname span)"],
    ["styles", ".channel-chat__atrow:not(.channel-chat__atrow--on):hover", "B2-14 channel @ row (covers atname span)"],
    ["styles", ".channel-chat__chipremove:not(:disabled):hover", "B2-14 channel chip remove"],
    ["styles", ".channel-workspace__backlist:hover", "B2-15 channel workspace back"],
    ["styles", ".collab-notif-panel__level:not(.collab-notif-panel__level--on):hover", "B2-16 collab notif level (covers levelicon span)"],
    ["styles", ".collab-inbox-panel__state:not(.collab-inbox-panel__state--on):hover", "B2-17 collab inbox view state"],
    ["styles", ".collab-inbox-panel__chainhead:hover", "B2-17 collab inbox chain head (covers chaincount span)"],
    ["styles", ".composer-guidance-head__collapse:hover", "B2-18 guidance collapse"],
    ["styles", ".composer-guidance-head__select-toggle:not([aria-pressed=\"true\"]):hover", "B2-18 guidance select toggle (pressed keeps accent)"],
    ["connections", ".connection-title:hover", "B2-19 connection title"],
    ["styles", ".toast:hover", "B2-20 whole-toast clickable hover"],
    ["styles", ".wf-tpl__diag:hover", "B2-21 workflow template diag"],
    ["styles", ".project-tree__batchbar-report-dismiss:hover", "B2-22 batchbar report dismiss"],
    ["styles", ".question-search__index:hover", "B2-23 question search index"],
  ];
  const sources: Record<string, string> = { styles, connections, web };
  for (const [source, selector, label] of b2) {
    ok(sources[source].includes(selector), `B2: hover rule present — ${label}`);
  }
}

// ── B3 负钉（防误伤：有意无 hover 的面不得被"修复"）────────────────────
{
  ok(styles.includes(".topicbar__chrome-btn--blocked") && styles.includes("cursor: not-allowed"),
    "B3: --blocked variants keep their intentional no-hover/not-allowed design");
  ok(classic.includes(".app-chrome__panel-toggle--blocked:hover"), "B3: classic blocked:hover still flattens (intentional)");
  ok(cssBlocks(styles, ".set-seg__btn--on").join("\n").includes("var(--accent-soft)"),
    "B3: set-seg --on selected visual untouched");
}

console.log(`\ntl1575 hover minimum tier: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
