// Run: tsx src/__tests__/task465-two-axis-matrix.test.ts
//
// 任务 465 两维矩阵：第一维【询问/自动/Yolo/autopilot】× 第二维【常规/计划/目标】。
//  - autopilot 隐含 yolo（325 门约束），档位切换自动满足前置并留痕（assumed_yolo）；
//  - goal × autopilot 合法同开：合成标签 plan>goal>autopilot>normal 只是展示层，
//    底层两维状态必须同时在（wire 裸旗 autopilot + profile.autopilot）；
//  - 第二维不再并入第一维枚举：执行方式菜单只承载计划/目标，autopilot 入口
//    移入模式条第四档（「+」运行中不可点的缺口由此补上）；
//  - X4 断点 C：desktopTabEntry 补 autopilot 列，重启忠实保留裸 tab 旗。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { composerProfileFromMeta, composerProfileFromTab, defaultComposerProfile } from "../lib/composerProfile";
import { executeComposerMode, type ComposerModeInput, type ComposerModePorts } from "../app-runtime/composerModeOwner";
import { normalizeCollaborationMode } from "../lib/types";
import { en } from "../locales/en";
import { zh } from "../locales/zh";
import { zhTW } from "../locales/zh-TW";

const testDir = dirname(fileURLToPath(import.meta.url));
const wtRoot = resolve(testDir, "../../../..");

// ── 1. 展示层合成标签：plan>goal>autopilot>normal 顺序 pin（X4 断点 B 延续）。
assert.equal(normalizeCollaborationMode("autopilot"), "autopilot");
assert.equal(normalizeCollaborationMode("plan"), "plan");
assert.equal(normalizeCollaborationMode("goal"), "goal");
assert.equal(normalizeCollaborationMode(undefined, "objective", "plan-yolo"), "plan", "plan 轴优先");
assert.equal(normalizeCollaborationMode(undefined, "objective"), "goal");

// ── 2. 两维组合 normalize（验收④）：goal × autopilot 同开——标签 "goal"，
//      裸旗 true，两维底层状态同时在，互不清除。
const goalAutopilotMeta = {
  collaborationMode: "goal",
  autopilot: true,
  toolApprovalMode: "yolo",
  goal: "unattended objective",
  goalStatus: "running",
  qualityFloor: "standard",
} as const;
const goalAutopilotProfile = composerProfileFromMeta(goalAutopilotMeta);
assert.equal(goalAutopilotProfile.collaborationMode, "goal", "合成标签按 goal>autopilot 优先");
assert.equal(goalAutopilotProfile.autopilot, true, "第一维裸旗必须在（465：底层状态都在）");
assert.equal(goalAutopilotProfile.goal, "unattended objective", "第二维 goal 保留");

// 旧主机无裸旗：合成标签 "autopilot"（裸档）回退点亮。
const legacyAutopilotProfile = composerProfileFromMeta({ collaborationMode: "autopilot", toolApprovalMode: "yolo" });
assert.equal(legacyAutopilotProfile.autopilot, true, "旧主机标签回退（X4 断点 B 兼容）");

// plan/goal 标签不隐含旗；normal 缺省关。
assert.equal(composerProfileFromMeta({ collaborationMode: "plan", toolApprovalMode: "ask" }).autopilot, false);
assert.equal(composerProfileFromMeta({ collaborationMode: "normal", toolApprovalMode: "ask" }).autopilot, false);
assert.equal(defaultComposerProfile.autopilot, false);

// TabMeta 路径同样两维保真（composerProfileFromTab）。
const tabProfile = composerProfileFromTab({
  id: "t", label: "t", ready: true, running: false, mode: "plan-yolo", active: true, cwd: "/w",
  collaborationMode: "goal", autopilot: true, toolApprovalMode: "yolo", goal: "obj", goalStatus: "running",
});
assert.equal(tabProfile.autopilot, true);
assert.equal(tabProfile.collaborationMode, "goal");

// ── 3. owner 分支（两条 Composer 路径共用的执行器）：
//      autopilot 档切换不清 goal、patch 携带 yolo+裸旗；审批离 yolo 镜像反向联动。
function makeOwnerInput(overrides: Partial<ComposerModeInput>): { input: ComposerModeInput; ports: Record<string, unknown> } {
  const calls: Record<string, unknown> = {};
  const ports: ComposerModePorts = {
    setMode: () => {},
    setCollaboration: (_tabId, mode) => { calls.setCollaboration = mode; },
    setApproval: (_tabId, mode) => { calls.setApproval = mode; },
    clearGoal: () => { calls.clearGoal = true; },
    setRemote: async (_tabId, collaboration, approval, goal) => {
      calls.setRemote = { collaboration, approval, goal };
      return [];
    },
    drainRemote: () => {},
    patch: (_tabId, patch, fields) => { calls.patch = { patch, fields }; },
    rememberPlan: () => {},
    rememberApproval: () => {},
  };
  const input: ComposerModeInput = {
    target: { tabId: "t1", sessionKey: "k" },
    request: { kind: "collaboration", mode: "autopilot" },
    remote: false,
    collaborationMode: "goal",
    toolApprovalMode: "ask",
    autopilot: false,
    goal: "unattended objective",
    ports,
    ...overrides,
  };
  return { input, ports: calls as Record<string, unknown> };
}

const authority = { checkpoint: () => {}, ownsUI: () => true };

// goal × autopilot：本地路径绝不清 goal（旧互斥语义退役）。
{
  const { input, ports } = makeOwnerInput({});
  await executeComposerMode(input, authority);
  assert.equal(ports.setCollaboration, "autopilot");
  assert.equal(ports.clearGoal, undefined, "autopilot 档切换不得清 goal（goal × autopilot 同开）");
  const patch = (ports.patch as { patch: Record<string, unknown>; fields: string[] });
  assert.equal(patch.patch.toolApprovalMode, "yolo", "档位切换隐含 yolo");
  assert.equal(patch.patch.autopilot, true);
  assert.equal(patch.patch.collaborationMode, "goal", "乐观标签保持 goal（两维同开）");
  assert.ok(patch.fields.includes("autopilot"));
}

// 审批离开 yolo：镜像 325 反向联动（autopilot 落旗）。
{
  const { input, ports } = makeOwnerInput({
    request: { kind: "approval", mode: "ask" },
    autopilot: true,
    toolApprovalMode: "yolo",
  });
  await executeComposerMode(input, authority);
  const patch = (ports.patch as { patch: Record<string, unknown>; fields: string[] });
  assert.equal(patch.patch.autopilot, false, "审批离开 yolo → autopilot 反向联动落旗");
  assert.equal(patch.patch.toolApprovalMode, "ask");
}

// 审批切 yolo（autopilot 已开）：旗保持。
{
  const { input, ports } = makeOwnerInput({
    request: { kind: "approval", mode: "yolo" },
    autopilot: true,
    toolApprovalMode: "ask",
  });
  await executeComposerMode(input, authority);
  const patch = (ports.patch as { patch: Record<string, unknown> });
  assert.equal(patch.patch.autopilot, undefined, "yolo 与 autopilot 相容，旗不动");
}

// 第二维切 plan（autopilot 关）：行为不变（清 goal）。
{
  const { input, ports } = makeOwnerInput({
    request: { kind: "collaboration", mode: "plan" },
    goal: "some goal",
  });
  await executeComposerMode(input, authority);
  assert.equal(ports.clearGoal, true, "plan 档仍清 goal（第二维单选）");
}

// ── 4. Composer 源码锚：模式条第四档 + 徽章只承载第二维 + 菜单 autopilot 撤出。
const composerSrc = readFileSync(resolve(testDir, "../components/Composer.tsx"), "utf8");
assert.ok(composerSrc.includes('composer-modebar__item--autopilot'), "模式条第四档按钮存在");
assert.match(composerSrc, /data-mode=\{autopilotModeOn \? "autopilot" : toolApprovalMode\}/, "滑块档位跟随裸旗");
assert.match(composerSrc, /data-autopilot=\{autopilotEnabled \? "on" : "off"\}/, "四格布局开关随偏好");
assert.ok(composerSrc.includes('onClick={() => chooseTaskMode("autopilot")}'), "第四档点击走档位切换");
assert.ok(composerSrc.includes("(planModeOn || goalModeOn)"), "徽章只承载第二维（计划/目标）");
assert.ok(!composerSrc.includes('t("composer.taskModeAutopilot")</span>\n              </span>\n              {autopilotModeOn'), "执行方式菜单不再有 autopilot 项（其关闭路径已失效）");

// ── 5. CSS 锚：四格网格 + 滑块四等分 + autopilot 专属橙（595 改色：465 时期
//    沿用 yolo 红与 Yolo 档同色难区分，任务 595 换 --mode-autopilot-* 橙系）。
const styles = readFileSync(resolve(testDir, "../styles.css"), "utf8");
assert.match(styles, /\.composer-modebar--approval\[data-autopilot="on"\]\s*\{\s*grid-template-columns:\s*repeat\(4, minmax\(0, 1fr\)\);/, "四格网格");
assert.match(styles, /\.composer-modebar--approval\[data-autopilot="on"\] \.composer-modebar__thumb\s*\{\s*width:\s*calc\(\(100% - 4px\) \/ 4\);/, "滑块四等分");
assert.match(styles, /\.composer-modebar--approval\[data-mode="autopilot"\]\s*\{\s*--composer-modebar-active-bg:\s*var\(--mode-autopilot-bg\);/, "autopilot 点亮用专属橙色系（595：与 yolo 红区分）");
assert.doesNotMatch(styles, /\.composer-modebar--approval\[data-mode="autopilot"\]\s*\{[^}]*--mode-yolo-bg/, "autopilot 档不再借用 yolo 红");
assert.match(styles, /\.composer-modebar\[data-mode="autopilot"\]\s*\{\s*--composer-modebar-index:\s*3;/, "滑块落到第 4 格");

// ── 6. 留痕三语 + notice 映射（验收⑤/铁律：决策必须可见可查）。
for (const [name, dict] of [["en", en], ["zh", zh], ["zh-TW", zhTW]] as const) {
  const text = (dict as Record<string, string>)["notice.autopilotAssumedYolo"];
  assert.ok(text, `${name} 缺 notice.autopilotAssumedYolo`);
  assert.ok(text.includes("Yolo") || text.includes("YOLO") || text.includes("yolo"), `${name} 文案须点名 yolo`);
}
const notices = readFileSync(resolve(testDir, "../lib/controllerNotices.ts"), "utf8");
assert.ok(notices.includes('autopilot_assumed_yolo: "notice.autopilotAssumedYolo"'), "notice 码必须按 code 本地化");

// ── 7. Go 侧锚：desktopTabEntry 列 + 恢复 OR + 档位自动满足 + 留痕日志字段。
const entrySrc = readFileSync(resolve(wtRoot, "desktop/tabs_persistence_types.go"), "utf8");
assert.match(entrySrc, /Autopilot bool\s+`json:"autopilot,omitempty"`/, "X4 断点 C：desktopTabEntry 补 autopilot 列");
assert.match(entrySrc, /Autopilot:\s+tab\.autopilot/, "持久化写入裸旗");
const appSrc = readFileSync(resolve(wtRoot, "desktop/app.go"), "utf8");
assert.match(appSrc, /if entry\.Autopilot \|\| tabSessionAutopilot\(tab\.SessionPath\)/, "恢复路径双源（entry 列 + goal sidecar）");
assert.match(appSrc, /"assumed_yolo", assumedYolo/, "X4 判据锚带 assumed_yolo 决策字段");
assert.match(appSrc, /NoticeCodeAutopilotAssumedYolo, autopilotAssumedYoloText/, "档位自动满足 yolo 的用户可见留痕");
assert.match(appSrc, /effectiveApproval = control\.ToolApprovalYolo/, "自动满足前置的落点");
const gateSrc = readFileSync(resolve(wtRoot, "desktop/autopilot_gate.go"), "utf8");
assert.ok(gateSrc.includes('"autopilot_assumed_yolo"'), "门文件定义新 notice 码");
// 325 反向联动不变：离开 yolo 仍 fail-closed 关 autopilot。
assert.ok(appSrc.includes("closeAutopilotForOffYolo(tab, toolApprovalMode)"), "SetComposerProfileForTab 反向联动保留");
assert.ok(appSrc.includes("closeAutopilotForOffYolo(tab, mode)"), "SetToolApprovalModeForTab 反向联动保留");

console.log("  PASS  task 465 两维矩阵：profile 裸旗/goal×autopilot 同开/owner 分支/第四档锚/断点 C/留痕三语");
