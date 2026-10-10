// Run: npx tsx src/__tests__/task649-inbox-improvements.test.ts
// 任务 649（收件箱改进包）的接缝钉子——源码契约层（DOM 行为由
// collab-inbox-panel.test.tsx 承担）：
//  ① 排序/视图分段控件落位视图工具条（任务716 重排后的定稿第三位；649 时
//     曾在头行 actions），样式保持胶囊分段（参照设置-权限档位 .set-seg）；
//  ② 收发双方口径：列表行路由与 from/to 下拉都带「发信方/收信方」标签词；
//  ③ 行 hover：.collab-inbox-panel__row 有一条 hover 高亮规则（620 件1 只落了
//     下拉 hover，行本身实现遗漏——定位结论钉在这里，防再次「交付了但看不见」）；
//  ④ 三语：senderLabel / recipientLabel / viewGroup 三键在 zh / zh-TW / en 全落。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const read = (rel: string) => readFileSync(fileURLToPath(new URL(rel, import.meta.url)), "utf8");
const inbox = read("../components/CollabInboxPanel.tsx");
const css = read("../styles.css");

function sliceBetween(source: string, startMarker: string, endMarker: string): string {
  const start = source.indexOf(startMarker);
  const end = source.indexOf(endMarker, start + 1);
  return start >= 0 && end > start ? source.slice(start, end) : "";
}

console.log("\ntask 649 收件箱改进包（分段控件上移 / 收发双方标签 / 行 hover）");

// ① 排序/视图分段控件落位视图工具条 + 胶囊分段样式（任务716 重排后位置）。
{
  const toolbar = sliceBetween(inbox, 'className="collab-inbox-panel__toolbar"', 'className="collab-inbox-panel__buckets"');
  ok(toolbar.includes('className="collab-inbox-panel__ordertoggle"') && toolbar.includes('className="collab-inbox-panel__viewtoggle"'),
    "the sort/view segmented controls live in the view toolbar (716 定稿第三位)");
  const head = sliceBetween(inbox, 'className="collab-inbox-panel__head"', 'className="collab-inbox-panel__maint"');
  ok(head.length > 0
    && !head.includes("collab-inbox-panel__ordertoggle") && !head.includes("collab-inbox-panel__viewtoggle"),
    "the head row carries only title + close (716 定稿第一位，无副标题)");
  const filters = sliceBetween(inbox, 'className="collab-inbox-panel__filters"', 'className="collab-inbox-panel__rows"');
  ok(!filters.includes("collab-inbox-panel__ordertoggle") && !filters.includes("collab-inbox-panel__viewtoggle"),
    "the filters row no longer hosts the sort/view controls");
  ok(css.includes(".collab-inbox-panel__ordertoggle,\n.collab-inbox-panel__viewtoggle")
    && /border-radius: 999px;\s*\n\s*overflow: hidden;/.test(css),
    "the segmented controls render as joined capsule segments (999px capsule tier)");
  ok(css.includes(".collab-inbox-panel__state--on {\n  background: var(--accent-soft);"),
    "the active segment lights up with the accent-soft token (.set-seg language)");
}

// ② 收发双方口径：路由行 + 下拉都带标签词。
ok(inbox.includes('className="collab-inbox-panel__routelabel"')
  && inbox.includes('t("collabInbox.senderLabel")') && inbox.includes('t("collabInbox.recipientLabel")'),
  "the list row route renders sender/recipient tag words");
ok(inbox.includes('className="collab-inbox-panel__filterwrap"'),
  "the from/to dropdowns carry visible label words (filterwrap)");

// ③ 行 hover：邮件行有 hover 高亮规则（按钮 hover token，与下拉 hover 同构）。
ok(/\.collab-inbox-panel__row:hover \{\n  border-color: var\(--button-border-hover\);\n  background: var\(--button-bg-hover\);\n\}/.test(css),
  "the mail row has a visible hover rule (button hover tokens)");

// ④ 三语。
for (const locale of ["zh", "zh-TW", "en"]) {
  const dict = read(`../locales/${locale}.ts`);
  ok(dict.includes('"collabInbox.senderLabel"') && dict.includes('"collabInbox.recipientLabel"')
    && dict.includes('"collabInbox.viewGroup"'),
    `${locale}: sender/recipient/viewGroup keys are present`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
