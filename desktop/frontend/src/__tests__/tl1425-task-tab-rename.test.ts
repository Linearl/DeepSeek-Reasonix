// Run: tsx src/__tests__/tl1425-task-tab-rename.test.ts
//
// tl1425（用户 20261105 拍板）：右侧栏「子代理」tab 改名「任务」——为未来
// 后台任务并入做语义准备。改名只落在**入口/容器层**，面板内列表项仍是
// 子代理条目（它们本来就是子代理）。三层钉死：
// ① tab 标签三语（workspace.subagentsTab）＝ 任务/任務/Tasks；
// ② 实验室开关标题与描述三语（settings.subagentPanel / …Hint /
//    …TabDisabledHint / subagentDetailHint 的容器指称）同步；
// ③ 语义边界锁——面板内条目文案（subagentPanel.*）、实验室「子代理族」卡
//    标题（settings.subagentSuite，容器是真子代理开关 policy/tps 等）保持
//    「子代理」不动；Go 侧 send_message 回执指向面板处的指称同步为 task panel。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

const testDir = dirname(fileURLToPath(import.meta.url));

let passed = 0;
let failed = 0;
function ok(cond: unknown, msg: string): void {
  if (cond) {
    passed += 1;
    console.log(`  ok  ${msg}`);
  } else {
    failed += 1;
    console.log(`  FAIL  ${msg}`);
  }
}

// ── ① tab 标签三语 ──────────────────────────────────────────────
ok(zh["workspace.subagentsTab"] === "任务", "zh tab 标签＝任务");
ok(zhTW["workspace.subagentsTab"] === "任務", "zh-TW tab 標籤＝任務");
ok(en["workspace.subagentsTab"] === "Tasks", "en tab label = Tasks");

// 渲染接线仍走同一键（App 计数徽标 + DockRegion 图标 tab + 面板 aria）——
// 改的是键值不是键名，接线断言防漂移。
const appSource = readFileSync(resolve(testDir, "../App.tsx"), "utf-8");
const dockRegionSource = readFileSync(
  resolve(testDir, "../app-shell/WorkspaceDockRegion.tsx"),
  "utf-8",
);
const panelSource = readFileSync(resolve(testDir, "../components/SubagentsDockPanel.tsx"), "utf-8");
ok(
  appSource.includes('subagentsTabLabel(t("workspace.subagentsTab")'),
  "App: tab 标签仍经 workspace.subagentsTab 键渲染（含已结束计数徽标）",
);
ok(
  dockRegionSource.includes('label={t("workspace.subagentsTab")}'),
  "WorkspaceDockRegion: DockTab 标签仍读 workspace.subagentsTab 键",
);
ok(
  panelSource.includes('aria-label={t("workspace.subagentsTab")}'),
  "SubagentsDockPanel: aria-label 同键跟随（改名不换键）",
);

// ── ② 实验室开关标题/描述三语 ──────────────────────────────────
ok(zh["settings.subagentPanel"] === "任务面板", "zh 开关标题＝任务面板");
ok(zhTW["settings.subagentPanel"] === "任務面板", "zh-TW 開關標題＝任務面板");
ok(en["settings.subagentPanel"] === "Task panel", "en toggle title = Task panel");

// 开关描述：标签页名指称同步；面板内条目（子代理）描述保留。
ok(
  zh["settings.subagentPanelHint"].includes("「任务」标签页") &&
    !zh["settings.subagentPanelHint"].includes("「子代理」标签页"),
  "zh 开关描述：标签页指称＝「任务」标签页",
);
ok(
  zh["settings.subagentPanelHint"].includes("列出本会话的子代理"),
  "zh 开关描述：面板列出的条目仍表述为子代理（边界保留）",
);
ok(
  zhTW["settings.subagentPanelHint"].includes("「任務」標籤頁") &&
    !zhTW["settings.subagentPanelHint"].includes("「子代理」標籤頁"),
  "zh-TW 開關描述：標籤頁指稱＝「任務」標籤頁",
);
ok(
  zhTW["settings.subagentPanelHint"].includes("列出本會話的子代理"),
  "zh-TW 開關描述：條目表述保留子代理（邊界保留）",
);
ok(
  en["settings.subagentPanelHint"].includes("Adds a Tasks tab") &&
    !en["settings.subagentPanelHint"].includes("Subagents tab"),
  "en hint: tab reference = Tasks tab",
);
ok(
  en["settings.subagentPanelHint"].includes("listing this session's subagents"),
  "en hint: listed entries still described as subagents (boundary kept)",
);

// 联动提示（标签显示墙的置灰指认）与详情开关描述的容器指称：同步面板新名。
ok(
  zh["settings.subagentPanelTabDisabledHint"].includes("「任务面板」") &&
    !zh["settings.subagentPanelTabDisabledHint"].includes("「子代理面板」"),
  "zh 标签显示墙联动提示指认＝「任务面板」",
);
ok(
  zhTW["settings.subagentPanelTabDisabledHint"].includes("「任務面板」") &&
    !zhTW["settings.subagentPanelTabDisabledHint"].includes("「子代理面板」"),
  "zh-TW 標籤顯示牆聯動提示指認＝「任務面板」",
);
ok(
  zh["settings.subagentDetailHint"].includes("右栏「任务」面板") &&
    !zh["settings.subagentDetailHint"].includes("「子代理」面板"),
  "zh 详情开关描述容器指称＝右栏「任务」面板",
);
ok(
  zhTW["settings.subagentDetailHint"].includes("右欄「任務」面板") &&
    !zhTW["settings.subagentDetailHint"].includes("「子代理」面板"),
  "zh-TW 詳情開關描述容器指稱＝右欄「任務」面板",
);
ok(
  en["settings.subagentPanelTabDisabledHint"].includes("task panel") &&
    !en["settings.subagentPanelTabDisabledHint"].includes("subagent panel"),
  "en disabled hint names the task panel",
);
ok(
  en["settings.subagentDetailHint"].includes("right-dock Tasks panel") &&
    !en["settings.subagentDetailHint"].includes("Subagents panel"),
  "en detail hint: container reference = Tasks panel",
);

// ── ③ 语义边界锁：面板内条目/真子代理配置面不改 ────────────────
ok(zh["subagentPanel.empty"] === "当前会话暂无子代理", "边界锁: 面板空态仍称子代理（列表项语义）");
ok(zh["subagentPanel.detailTitle"] === "子代理详情", "边界锁: 行详情标题仍称子代理");
ok(zhTW["subagentPanel.empty"] === "目前會話暫無子代理", "边界锁: zh-TW 面板空態仍稱子代理");
ok(en["subagentPanel.empty"].includes("subagents"), "boundary: en panel empty state still says subagents");
ok(zh["settings.subagentSuite"] === "子代理", "边界锁: 实验室「子代理族」卡标题不动（容器=真子代理开关）");
ok(zh["settings.subagentDetail"] === "子代理详情视图", "边界锁: 详情开关标题不动（描述的是条目详情形态）");
ok(zh["settings.subagentModel"] === "子代理模型", "边界锁: 子代理配置面（设置页）不动");

// ── ④ Go 侧回执指称同步（用户可见工具输出，指向此面板）────────
const goSource = readFileSync(
  resolve(testDir, "../../../../internal/agent/send_message_tool.go"),
  "utf-8",
);
ok(
  goSource.includes("read it in the task panel.") &&
    goSource.includes("can also read it in the task panel."),
  "Go: send_message 回执（queued/parked）面板指称＝task panel",
);
ok(!goSource.includes("in the subagent panel"), "Go: 回执无「subagent panel」残留");

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
