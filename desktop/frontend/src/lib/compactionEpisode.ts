// tl1588: 压缩回合状态收敛（episode sink）。压缩现场（用户实测 2026-11-14）
// 十余种内部状态逐条刷 transcript：正在压缩(第N轮) → 摘要未形成短视图 →
// 预填充提示 → 本轮已中断 → 无人值守自动续轮 → 上下文已裁剪 → 回复被截断…
// 每条都占一整行 notice 卡，用户被迫手动 compact 才恢复。
//
// 收敛形态（验收四条）：压缩进行中转录区只显示
//   ① 一行进度（pending 压缩卡头行，973 既有）+ 一行失败原因（867 失败卡
//     reason 行，既有）；
//   ② 内部状态（预填充/中断/续轮/裁剪/恢复提示/压缩等待/流中断）不再逐条
//     落行，改沉淀为压缩卡上的 episodeNotes（有界 FIFO，内存态，水合卡不
//     携带——与 973 startedAt 同一约定），卡上以 <details> 可展开；
//   ③ 兜底动作（裁剪/续轮/中断）在卡上以一条人话 rescue 行说明（最近的
//     兜底动作胜出，全部动作仍留在详情区）；
//   ④ rescue 行与详情标题三语。
//
// 兼容窗（分流条件，reducer 侧判断）：compactionSessionOpen === true 且存在
// 压缩卡。窗口外（回合间主动维护、无压缩卡的一切场景）行为逐字不变——
// 1021/867/973/1147/1559 各代钉的判例全部构造在窗口外，零回退。

import type { Translator } from "./i18n";

// 兜底动作三类——③ 的人话行按此取 locale 键。trim=裁剪（含压缩连续失败后
// 丢弃最早历史的自救）；resume=自动续轮（autopilot 续跑/恢复门自动继续）；
// interrupt=本轮中断。
export type EpisodeRescueKind = "trim" | "resume" | "interrupt";

export interface CompactionEpisodeNote {
  /** 事件到达时刻（epoch ms），详情区逐条时间戳。 */
  at: number;
  /** 人话一行——复用该 notice 本来的本地化文案（localizedNoticeText 产物），不另造词。 */
  label: string;
  /** 技术细节（后端 reason/detail 原文），双轨不隐藏。 */
  detail?: string;
  /** 该条属于哪类兜底动作；纯内部状态（预填充/等待/维护降级）不带。 */
  rescue?: EpisodeRescueKind;
}

// ③ 的三个 locale 键。集中在此，rescue 行解析与三语钉共用同一映射。
const rescueLocaleKeys: Record<EpisodeRescueKind, string> = {
  trim: "compaction.rescueTrim",
  resume: "compaction.rescueResume",
  interrupt: "compaction.rescueInterrupt",
};

// 分流码闭式集合：验收②点名的内部状态族。码集合之外的 notice（重试耗尽、
// 供应商拒绝等需要用户行动的告警）一律照旧落行——收敛只吃状态刷屏，不吃告警。
const episodeNoticeCodes: ReadonlySet<string> = new Set([
  // 预填充（任务929）：超大上下文首 token 等待，两条一族。
  "prefill_slow",
  "header_wait_slow",
  // 中断（turn_done interrupted 的事件路径；turn_done 直填路径在 reducer 单独分流）。
  "cancelled_turn_display",
  // 续轮（任务731/868/tl1357）：三类自动继续留痕。
  "autopilot_resume_fired",
  "unattended_gate_auto_continue",
  "restart_gate_auto_continue",
  // 恢复提示（任务1011）：压缩连续失败、投影超窗的自救提示——历史已被
  // 丢弃即裁剪兜底，rescue=trim。
  "session_compact_fail_streak",
  // 压缩等待（任务1147）：压缩中静默的防误停提示。
  "turn_stalled_compacting",
  // 维护降级：短视图维护退回本地维护的一行说明。
  "context_editing_fallback",
]);

// 流中断族（回复被截断/连接失败）按前缀命中——五个 reason 码同族。
export function isStreamInterruptCode(code?: string): boolean {
  return Boolean(code && code.startsWith("stream_interrupted_"));
}

export function isEpisodeNoticeCode(code?: string): boolean {
  if (!code) return false;
  if (episodeNoticeCodes.has(code)) return true;
  return isStreamInterruptCode(code);
}

// 兜底动作 → rescue 类别。流中断不占 rescue 行（它不是兜底动作，是失败细节，
// 留在详情区；失败原因行由 867 失败卡的 reason 承担）。
export function rescueKindForCode(code?: string): EpisodeRescueKind | undefined {
  switch (code) {
    case "session_compact_fail_streak": return "trim";
    case "autopilot_resume_fired":
    case "unattended_gate_auto_continue":
    case "restart_gate_auto_continue": return "resume";
    case "cancelled_turn_display": return "interrupt";
    default: return undefined;
  }
}

// context_maintenance 的裁剪兜底判定：applied + truncate（有损上限自救）。
export function isTrimRescueMaintenance(status?: string, action?: string): boolean {
  return status === "applied" && action === "truncate";
}

const MAX_EPISODE_NOTES = 12;

// 有界 FIFO：压缩回合一轮最多沉淀 12 条内部状态，旧的先出——详情区不因
// 多轮长回合成无底洞。notes 为空/undefined 时从零起。
export function appendEpisodeNote(
  notes: readonly CompactionEpisodeNote[] | undefined,
  note: CompactionEpisodeNote,
): CompactionEpisodeNote[] {
  return [...(notes ?? []), note].slice(-MAX_EPISODE_NOTES);
}

// ③：rescue 行解析——最近的兜底动作胜出（用户最关心最后发生了什么），全部
// 动作仍在详情区。无兜底动作 → 空串（不渲染该行，与无 notes 时逐字一致）。
export function episodeRescueLine(notes: readonly CompactionEpisodeNote[] | undefined, t: Translator): string {
  for (let i = (notes?.length ?? 0) - 1; i >= 0; i -= 1) {
    const rescue = notes![i].rescue;
    if (rescue) return t(rescueLocaleKeys[rescue] as never);
  }
  return "";
}
