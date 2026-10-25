// tl 1160 诊断探针 — concise 档运行中动态状态行（ProcessFoldHeader 的
// liveStatus 两腿 + 三道门）取数链路的现场取证。
//
// 背景：多对话/中断恢复场景下动态行（思考中/工具名）消失而时长行仍在，
// 代码层同版本既有正常又有失效样本，只能靠运行时状态分辨。本探针把
// 协调线指定的取数点字段经既有 reportFrontendLog 通道（内存环 + console
// + desktop.log 三落点）落盘，一次复跑即可分辨：
//   门① experience!=="concise"（exp 字段）
//   门② open=true（展开态按设计不叠状态行）
//   门③ hasRunningWork=false（与「工作中」标签矛盾则说明状态行挂在别的 fold）
//   腿A latestRunningTool 恒空（tool 字段）
//   腿B live 指针脱钩（tail=displayItems 尾 assistant id vs liveId）/
//       快照 reasoning 为空（snap 字段）
//
// 频率纪律（frontendLog 约定：只记解释行为的迁移）：门签名（布尔门 + 指针
// + 命中腿结果）变化即时上报；纯内容变化（流式思考文本增长）每 fold 至多
// 5s 一条心跳，避免流式 delta 刷爆 4MB 滚动日志。诊断件，修复后随定因
// commit 一并摘除或降级。

import { reportFrontendLog } from "./frontendLog";

export type LiveStatusProbeFields = {
  /** ProcessFoldHeader 所在 fold 的 segment key（稳定折叠键）。 */
  seg: string;
  /** 门①：档位解析结果（concise 才可能显示状态行）。 */
  exp: string;
  /** 门②：该 fold 当前展开态。 */
  open: boolean;
  /** 门③：segment.hasRunningWork（时长行同源标志）。 */
  run: boolean;
  /** 三门合成后的 conciseLive。 */
  concise: boolean;
  /** displayItems 长度（聚合器是否给到行）。 */
  n: number;
  /** LiveStreamContext 的 live?.id（undefined → "-"）。 */
  liveId: string;
  /** 腿B 选择子：尾部 assistant 是否命中 live 指针。 */
  hasLive: boolean | "-";
  /** displayItems 尾部 assistant 的 id（与 liveId 对照看指针脱钩）。 */
  tail: string;
  /** 腿A：latestRunningTool 命中（id:name，未命中 "-"）。 */
  tool: string;
  /** 腿B 兜底：latestReasoningText 首 40 字（空则 ""）。 */
  snap: string;
  /** live.reasoning 长度（live 缺席 → -1）。 */
  liveRLen: number;
  /** live.reasoningComplete（live 缺席 → "-"）。 */
  liveRDone: boolean | "-";
  /** 实际喂给 UI 的 liveReasoning memo 结果首 40 字。 */
  eff: string;
  /** 实际渲染的当前动作文本首 40 字。 */
  act: string;
  /** 最终 showLiveStatus。 */
  shown: boolean;
};

const GATE_SIG_MAP_MAX = 64;
const CONTENT_HEARTBEAT_MS = 5000;

const lastGateSig = new Map<string, string>();
const lastContentAt = new Map<string, number>();

function head40(text: string): string {
  return text.length <= 40 ? text : text.slice(0, 40);
}

/** 调用方侧的文本采样（首 40 字），与探针行内 head40 同一口径。 */
export function head40ForProbe(text: string): string {
  return head40(text);
}

export function probeLiveStatus(f: LiveStatusProbeFields): void {
  // 门签名：布尔门 + 指针 + 腿命中（不含流式内容，内容增长不算迁移）。
  const gate = `${f.concise ? 1 : 0}${f.open ? 1 : 0}${f.run ? 1 : 0}|${f.exp}|${f.liveId}|${f.hasLive}|${f.tail}|${f.tool}|${f.n}`;
  const now = Date.now();
  const gateChanged = lastGateSig.get(f.seg) !== gate;
  const contentDue = now - (lastContentAt.get(f.seg) ?? 0) >= CONTENT_HEARTBEAT_MS;
  if (!gateChanged && !contentDue) return;
  if (lastGateSig.size > GATE_SIG_MAP_MAX) {
    lastGateSig.clear();
    lastContentAt.clear();
  }
  lastGateSig.set(f.seg, gate);
  // 任何一次上报（门迁移或心跳）都重置内容窗口锚点，保证心跳间隔下限。
  lastContentAt.set(f.seg, now);
  reportFrontendLog(
    "live-status",
    `probe ${f.seg}`,
    `exp=${f.exp} concise=${f.concise ? 1 : 0} open=${f.open ? 1 : 0} run=${f.run ? 1 : 0}`
      + ` n=${f.n} liveId=${f.liveId} hasLive=${f.hasLive} tail=${f.tail}`
      + ` tool=${f.tool} liveRLen=${f.liveRLen} liveRDone=${f.liveRDone}`
      + ` snap="${f.snap}" eff="${f.eff}" act="${f.act}" shown=${f.shown ? 1 : 0}`,
  );
}
