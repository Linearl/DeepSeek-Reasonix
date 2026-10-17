// Run: npx tsx src/__tests__/task971-tool-opt-hub.test.ts
// 任务 971 acceptance harness (实验室-提效-工具优化聚合页):
//  ① 聚合页渲染：顶部 rev2 警告定稿原文逐字断言 + 三生效点机制说明 + 四配套
//     + 开关全集在页（readBack / 证据门 / multi_edit 直注 / restart_update /
//     session_hub 预留键位）；
//  ② 迁移零回归：restartUpdate 自界面组更新反馈卡迁入聚合页——键/setter/档位
//     不变，仅 UI 落点迁移（updateFeedback 不再含该行）；multiEditInjection
//     独立卡退役为成员行，键/setter/回落链不变（943 两开关独立语义保持）；
//  ③ session_hub 键位就绪：experimental_capability_groups 同键预留（954 未
//     合并，仅占位不生效）；
//  ④ 三语：toolOptHub 全键在 zh / zh-TW / en 齐备；布局 yaml 与内置默认同构。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const frontendSrc = fileURLToPath(new URL("../", import.meta.url));
const read = (p: string) => readFileSync(`${frontendSrc}${p}`, "utf8");

console.log("\ntask 971 tool-optimization hub: aggregated page + rev2 warning + migration");

// ① 聚合页渲染 + rev2 警告原文。
{
  const zh = read("locales/zh.ts");
  // rev2 定稿全文（任务书 note 逐字）——双向语义（关了但没关掉=安全类误导）
  // 必须保留。
  const rev2 = "⚠️ 本页开关影响模型可用的工具列表：①当前对话将于下次压缩或重启后生效（新会话立即生效）；②生效时工具列表会实际增减，正在进行的工作若依赖被关闭的工具可能中断，请避免在任务中途切换。修改后如需立即在当前对话生效，建议执行一次压缩（/compact）或重启会话；否则当前对话保持原工具列表——新开的工具不可用、已关的工具仍可被调用。";
  ok(zh.includes(rev2), "zh locale carries the rev2 warning verbatim");
  const panel = read("components/SettingsPanel.tsx");
  ok(panel.includes('data-testid="tool-opt-hub-warning"'), "pane renders the persistent top warning banner");
  ok(panel.includes('t("settings.toolOptHub.warning")'), "banner pulls the rev2 text from the locale key");
  // 三生效点机制说明 + 四配套。
  ok(panel.includes('t("settings.toolOptHub.effect")'), "pane explains the three effective points");
  ok(panel.includes('t("settings.toolOptHub.pending")'), "pane states the pending-state hint (配套①)");
  ok(panel.includes('t("settings.toolOptHub.notice")'), "pane documents the swap notice (配套③)");
  ok(panel.includes('t("settings.toolOptHub.scope")'), "pane scopes to pure-schema switches (配套④)");
  // 开关全集在页。
  ok(panel.includes('app.SetExperimentalToolOptimizations(on)'), "hub hosts the readBack switch");
  ok(panel.includes('app.SetExperimentalEditEvidenceRelaxed(on)'), "hub hosts the edit-evidence-relaxed switch");
  ok(panel.includes('app.SetExperimentalMultiEditInjection(on)'), "hub hosts the multi_edit injection switch");
  ok(panel.includes('app.SetExperimentalRestartUpdate(on)'), "hub hosts the restart_update switch");
  ok(panel.includes('data-testid="session-hub-slot"'), "hub hosts the session_hub reserved slot");
}

// ② 迁移零回归。
{
  const panel = read("components/SettingsPanel.tsx");
  // restartUpdate 迁出 updateFeedback pane：该 pane 不再含 restartUpdate 行。
  const ufStart = panel.indexOf('{selected === "updateFeedback" && (');
  const ufEnd = panel.indexOf('{selected === "monitoring" && (');
  ok(ufStart >= 0 && ufEnd > ufStart, "updateFeedback pane branch located");
  const ufSlice = panel.slice(ufStart, ufEnd);
  ok(!ufSlice.includes("app.SetExperimentalRestartUpdate(on)"),
    "updateFeedback pane no longer renders the restartUpdate switch");
  // restartUpdate 键/setter 语义不变（仍走原 setter，仅落点在聚合页）。
  const hubStart = panel.indexOf('{selected === "toolOptimizations" && (');
  ok(hubStart >= 0, "toolOptimizations hub pane branch located");
  const hubSlice = panel.slice(hubStart);
  ok(hubSlice.includes("app.SetExperimentalRestartUpdate(on)"),
    "hub renders the migrated restartUpdate switch via its unchanged setter");
  ok(hubSlice.includes('Boolean(s.experimentalRestartUpdate)'),
    "restartUpdate switch still reads its own key");
  // multiEditInjection 语义不变：独立 pane 退役、回落链/档位保持。
  ok(!panel.includes('{selected === "multiEditInjection" && ('),
    "multiEditInjection standalone pane retired (943 split preserved as independent switch)");
  ok(panel.includes("(s.experimentalMultiEditInjection ?? (s.experimentalToolOptimizations ?? true)) === on"),
    "multi_edit injection keeps the SAME fallback chain (independent semantics)");
  // updateFeedback 卡不再含 restartUpdate 成员（onKeys 去 experimentalRestartUpdate）。
  // 剥行注释再断言——避免迁移注释里的关键字造成假红。
  const layoutDefault = read("lab/labLayoutDefault.ts").split("\n").map((l) => l.replace(/\/\/.*$/, "")).join("\n");
  const ufIdx = layoutDefault.indexOf('id: "updateFeedback"');
  const ufEntry = layoutDefault.slice(ufIdx, ufIdx + 220);
  ok(!ufEntry.includes("experimentalRestartUpdate") && !ufEntry.includes('id: "restartUpdate"'),
    "updateFeedback layout entry drops the restartUpdate member and light key");
  const toolOptEntry = layoutDefault.slice(layoutDefault.indexOf('id: "toolOptimizations"'));
  ok(toolOptEntry.includes('id: "restartUpdate"') && toolOptEntry.includes('id: "multiEditInjection"'),
    "toolOptimizations hub hosts restartUpdate + multiEditInjection as members");
}

// ③ session_hub 键位就绪（954 未合并，同键名预留）。
{
  const panel = read("components/SettingsPanel.tsx");
  ok(panel.includes("experimental_capability_groups"),
    "pane reserves the experimental_capability_groups key slot (954 pending)");
  const zh = read("locales/zh.ts");
  ok(zh.includes('"settings.toolOptHub.sessionHubSlot"') && zh.includes("experimental_capability_groups"),
    "zh locale labels the reserved session_hub slot with the same key name");
}

// ④ 三语 + 布局同构。
{
  const keys = [
    "settings.toolOptHub",
    "settings.toolOptHub.warning",
    "settings.toolOptHub.effect",
    "settings.toolOptHub.pending",
    "settings.toolOptHub.notice",
    "settings.toolOptHub.scope",
    "settings.toolOptHub.sessionHubSlot",
    "settings.toolOptHub.sessionHubSlotHint",
  ];
  for (const locale of ["zh", "zh-TW", "en"]) {
    const src = read(`locales/${locale}.ts`);
    const missing = keys.filter((k) => !src.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} carries all toolOptHub keys${missing.length ? ` (missing ${missing.join(", ")})` : ""}`);
  }
  // 布局 yaml 与内置默认同构（724 硬约束）。
  const layoutDefault = read("lab/labLayoutDefault.ts");
  const yaml = read("lab/lab-layout.yaml");
  ok(layoutDefault.includes('labelKey: "settings.toolOptHub"') && yaml.includes("labelKey: settings.toolOptHub"),
    "both layout sources label the hub with toolOptHub");
  ok(yaml.includes("onKeys: [experimentalToolOptimizations, experimentalMultiEditInjection, experimentalRestartUpdate]"),
    "yaml hub entry lights on all three switch keys");
  // 聚合页不回并：readBack 与 multi_edit 仍是两个独立 setter。
  ok(layoutDefault.includes('id: "multiEditInjection"') && layoutDefault.includes('id: "restartUpdate"'),
    "default layout keeps the two switches as distinct member rows (not re-merged)");
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
