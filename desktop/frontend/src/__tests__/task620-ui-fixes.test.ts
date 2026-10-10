// Run: npx tsx src/__tests__/task620-ui-fixes.test.ts
// 任务 620（UI 三小修）的接缝钉子——源码契约层（DOM 行为由
// collab-inbox-panel.test.tsx 与 Go 侧 collab_inbox_clean_now_test.go 承担）：
//  ① 收件箱「立即清理」：绑定在面板 seam 上、按钮渲染在维护行（716 重排后
//     落位；620 时曾在头行）、反馈就地可见；
//  ② 子代理标签显隐 checkbox 移入「侧栏增强」卡的侧栏标签显示墙，并挂
//     「子代理面板」前置闸（未开启置灰不可勾选）；子代理族卡不再重复渲染；
//  ③ hover 反馈：收件箱 from/to 下拉与 set-gates 勾选项各有一条 hover 规则；
//  ④ 三语：cleanNow 四键在 zh / zh-TW / en 全部落键。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const panel = read("../components/SettingsPanel.tsx");
const inbox = read("../components/CollabInboxPanel.tsx");
const css = read("../styles.css");

function cardSlice(startMarker: string, endMarker: string): string {
  const start = panel.indexOf(startMarker);
  const end = panel.indexOf(endMarker);
  return start >= 0 && end > start ? panel.slice(start, end) : "";
}

console.log("\ntask 620 UI 三小修（立即清理 / 子代理 checkbox 移墙 / hover）");

// ① 立即清理：面板 seam 带绑定，维护行带按钮（716 布局），点击反馈就地渲染。
ok(inbox.includes("CleanCollabMailNow(): Promise<CollabInboxCleanResult>;"),
  "inbox bindings expose CleanCollabMailNow");
ok(inbox.includes('className="collab-inbox-panel__maint"')
  && inbox.includes('className="btn btn--secondary btn--small collab-inbox-panel__cleannow"')
  && inbox.includes('t("collabInbox.cleanNow")'),
  "the clean-now button renders in the maintenance row (716 layout)");
ok(inbox.includes("collabInbox.cleaned") && inbox.includes("collabInbox.cleanNothing"),
  "the clean-now click renders a removed-count feedback");

// ② 子代理标签 checkbox 移墙 + 前置闸。
{
  const sidebarCard = cardSlice('{selected === "todoSidebar" && (', '{selected === "subagentSuite" && (');
  ok(sidebarCard.includes('["subagents", "workspace.subagentsTab"]'),
    "the subagents tab row lives in the sidebar-enhancement card's visibility wall");
  ok(sidebarCard.includes('tabId === "subagents" && !Boolean(s.experimentalSubagentPanel)'),
    "the subagents row carries its own subagent-panel precondition (grey-out linkage)");
  ok(sidebarCard.includes('t("settings.subagentPanelTabDisabledHint")'),
    "the wall names the switch to flip when the subagent panel is off");
  const subagentCard = cardSlice('{selected === "subagentSuite" && (', '{selected === "tabCompress" && (');
  ok(subagentCard.length > 0, "the subagent-suite card branch located");
  ok(!subagentCard.includes('isDockTabHidden("subagents")'),
    "the subagent-suite card no longer renders its own tab-visibility checkbox");
  ok(subagentCard.includes("app.SetExperimentalSubagentPanel(on)"),
    "the subagent-panel switch itself stays in the subagent-suite card");
}

// ③ hover 反馈。
ok(css.includes(".collab-inbox-panel__filter:hover"),
  "the inbox from/to selects have a hover rule");
ok(css.includes(".set-gates__item:not(.set-gates__item--locked):hover"),
  "the gate checkboxes have a hover rule (locked items stay silent)");

// ④ 三语。
for (const locale of ["zh", "zh-TW", "en"]) {
  const dict = read(`../locales/${locale}.ts`);
  ok(dict.includes('"collabInbox.cleanNow"') && dict.includes('"collabInbox.cleanNowHint"')
    && dict.includes('"collabInbox.cleaned"') && dict.includes('"collabInbox.cleanNothing"'),
    `${locale}: the clean-now keys are present`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
