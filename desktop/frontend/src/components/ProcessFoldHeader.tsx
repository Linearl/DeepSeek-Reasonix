// ProcessFoldHeader: the fold header row of one process segment.
// The fold body is NOT rendered here: an open fold contributes its body rows
// to the virtual row model (they mount only when scrolled into view), a closed
// fold builds no React subtree at all.
// tl 1141: while a CONCISE fold is closed and its segment still runs, a
// single-line live status (current action + reasoning preview) renders under
// the head button — all state stays local to this component.

import { useContext, useMemo } from "react";
import { ChevronRight } from "lucide-react";
import { useT } from "../lib/i18n";
import type { CompactionItem, SegmentModel } from "../lib/transcriptRows";
import { countTools } from "../lib/turnToolTally";
import { useTick, resolveRunningDurationMs, workStatusLabel } from "../lib/workStatus";
import { compactionLiveReadout } from "../lib/compactionLive";
import { clampLine, hasLiveAssistant, latestReasoningText, latestRunningTool, LIVE_TEXT_THROTTLE_MS, useThrottledValue } from "../lib/workProcessLiveStatus";
import { useWorkProcessPresentation } from "../lib/sessionExperience";
import { subjectOf } from "../lib/tools";
import { probeLiveStatus, head40ForProbe } from "../lib/liveStatusDebug";
import { LiveStreamContext } from "./LiveStreamContext";
import { ReasoningSummary } from "./ReasoningSummary";

export function ProcessFoldHeader({
  segment,
  open,
  onToggle,
  turnStartAt,
}: {
  segment: SegmentModel;
  open: boolean;
  onToggle: () => void;
  turnStartAt?: number;
}) {
  const t = useT();
  const live = useContext(LiveStreamContext);
  const displayItems = segment.displayItems;

  // A process fold that coalesces context-compaction events must not show a
  // bare "worked Xm" label — the chunked /compact fallback (over-length
  // sessions) runs minutes and produces no tool/assistant rows, so the fold
  // reads as a mysteriously empty "worked" segment. Label it by the actual
  // compaction state instead: "compacting (N/M)" while the fold is still
  // pending, "context compacted" only once it has finished. Hide the
  // duration/counts that would otherwise imply timing for work that is really
  // async summarization.
  const compactionItem = displayItems.find((it): it is CompactionItem => it.kind === "compaction");
  const hasCompaction = compactionItem !== undefined;
  const hasRunningWork = segment.hasRunningWork;
  // 任务 973: a pending compaction also keeps the tick alive — segment
  // hasRunningWork only counts tool/assistant rows, and a pure summarization
  // fold has neither, which would freeze the「已 XXs」leg at its first value.
  const now = useTick(hasRunningWork || (compactionItem?.pending ?? false));
  // Task 341: with no start timestamp the resolver anchors the count at the
  // first render that saw this segment running and grows from the static
  // snapshot, so the duration keeps ticking through long quiet stretches
  // instead of freezing on a stale segment.durationMs.
  const effectiveDurationMs = resolveRunningDurationMs({
    now,
    running: hasRunningWork,
    segmentKey: segment.key,
    staticDurationMs: segment.durationMs,
    turnStartAt,
    reasoningStartedAt: live?.reasoningStartedAt,
  });

  // 任务 1021: pending labels carry the multi-pass round (「第 N 轮」) so a
  // mid-session round never reads as the final「已压缩」state; the resolved
  // terminal card names its total rounds.
  const compactionBaseLabel = compactionItem
    ? compactionItem.pending
      ? ((compactionItem.done ?? 0) > 0
          ? t("compaction.progress", { done: (compactionItem.done ?? 0), total: (compactionItem.total ?? 0) })
          : t("compaction.working")) + ((compactionItem.pass ?? 0) > 1 ? ` ${t("compaction.round", { n: compactionItem.pass ?? 0 })}` : "")
      : compactionItem.failed
        ? (compactionItem.attempt ?? 0) > 0
          ? t("compaction.failedAttempt", { n: compactionItem.attempt ?? 0 })
          : t("compaction.failed")
        : t("compaction.title") + ((compactionItem.pass ?? 0) > 1 ? ` · ${t("compaction.rounds", { n: compactionItem.pass ?? 0 })}` : "")
    : undefined;
  const baseLabel = compactionBaseLabel ?? workStatusLabel(effectiveDurationMs, hasRunningWork, t);
  // 任务 556/973: live "40s · 12.3k tokens · 45 tokens/s" readout while the
  // pass is pending, fed by throttled backend CompactionProgress events plus
  // the reducer's compaction_started stamp. The shared compactionLiveReadout
  // is the single formatting seam (fold header + compaction card); tps stays
  // hidden until the backend's 500ms noise floor passes (it sends 0).
  const readout = compactionLiveReadout(compactionItem ?? {}, now, t);
  // Surface what the closed fold hides — a bare duration reads as pure timing
  // and users have no way to know process detail sits behind it. 任务 1082:
  // toolCount moves to the shared turnToolTally single source so the fold
  // header and the run-strip tally can never drift apart.
  const toolCount = countTools(displayItems);
  const thoughtCount = displayItems.reduce((n, it) => n + (it.kind === "assistant" ? 1 : 0), 0);
  const countParts: string[] = [];
  if (!hasCompaction) {
    if (toolCount > 0) countParts.push(t("transcript.toolCount", { n: toolCount }));
    if (thoughtCount > 0) countParts.push(t("transcript.thoughtCount", { n: thoughtCount }));
  }
  const label = hasCompaction
    ? baseLabel + readout
    : segment.labelStyle === "counts"
      ? (countParts.length > 0 ? countParts.join(" · ") : t("transcript.processed"))
      : countParts.length > 0
        ? `${baseLabel} · ${countParts.join(" · ")}`
        : baseLabel;
  // ── tl 1141: concise 档运行中的动态过程状态行（当前动作 + 思考摘要）──
  // standard/deep 渲染路径零改动：conciseLive 为 false 时以下全部短路，
  // 渲染输出与本节引入前逐字节一致（不叠状态行）。
  const presentation = useWorkProcessPresentation();
  const conciseLive = presentation.experience === "concise" && !open && hasRunningWork;
  // 帧预算约束①：扫描只在此渲染期发生，useMemo 以 displayItems 引用为依赖
  // ——仅工具/思考事件到达（行模型重建）时重算；秒 tick 重渲染引用不变，
  // 缓存命中零扫描。纯函数从尾部倒序首中即返，与千步会话规模无关。
  const liveReasoning = useMemo(() => {
    if (!conciseLive) return "";
    const streaming = hasLiveAssistant(displayItems, live?.id);
    // tl 1160: live 指针命中只说明"尾部 assistant 就是当前流式项"，不保证
    // 缓冲非空——中断恢复/重连把指针重新挂上（或回滚清空缓冲）时 id 命中而
    // reasoning=""，此时直接取空值会把 item 上已落盘的快照顶成空白（动态行
    // 消失、时长行照走的现场构象）。缓冲有内容才优先 live（比快照及时），
    // 空缓冲一律回退 latestReasoningText。
    const liveText = streaming ? (live?.reasoning ?? "") : "";
    return liveText || latestReasoningText(displayItems);
  }, [conciseLive, displayItems, live?.id, live?.reasoning]);
  const throttledReasoning = useThrottledValue(liveReasoning, LIVE_TEXT_THROTTLE_MS);
  const liveActionText = useMemo(() => {
    if (!conciseLive) return "";
    const tool = latestRunningTool(displayItems);
    if (!tool) return "";
    const base = t("transcript.liveAction", { name: tool.resolvedName || tool.name });
    const subject = tool.subject || subjectOf(tool.name, tool.args);
    return clampLine(subject ? `${base} · ${subject}` : base);
  }, [conciseLive, displayItems, t]);
  const showLiveStatus = conciseLive && Boolean(liveActionText || throttledReasoning);
  // ── tl 1160 诊断探针（定因后摘除）：运行中 fold 的取数链路现场取证 ──
  // 只在 hasRunningWork 的 fold 上做独立腿扫描（诊断件，成本与生产 memo 同
  // 级、事件驱动）；非运行 fold 零扫描零上报。日志经 reportFrontendLog 落
  // desktop.log，字段清单见 lib/liveStatusDebug.ts 模块注释。
  const probeLegs = useMemo(() => {
    if (!hasRunningWork) return null;
    let tail: string = "-";
    for (let index = displayItems.length - 1; index >= 0; index -= 1) {
      const item = displayItems[index];
      if (item.kind === "assistant") {
        tail = item.id;
        break;
      }
    }
    const tool = latestRunningTool(displayItems);
    return {
      hasLive: hasLiveAssistant(displayItems, live?.id),
      tail,
      tool: tool ? `${tool.id}:${tool.resolvedName || tool.name}` : "-",
      snap: latestReasoningText(displayItems),
    };
  }, [hasRunningWork, displayItems, live?.id]);
  if (probeLegs) {
    probeLiveStatus({
      seg: segment.key,
      exp: presentation.experience,
      open,
      run: hasRunningWork,
      concise: conciseLive,
      n: displayItems.length,
      liveId: live?.id ?? "-",
      hasLive: probeLegs.hasLive,
      tail: probeLegs.tail,
      tool: probeLegs.tool,
      snap: head40ForProbe(probeLegs.snap),
      liveRLen: live ? (live.reasoning?.length ?? 0) : -1,
      liveRDone: live ? live.reasoningComplete : "-",
      eff: head40ForProbe(liveReasoning),
      act: head40ForProbe(liveActionText),
      shown: showLiveStatus,
    });
  }
  return (
    <div className={`turn-collapse${open ? " turn-collapse--open" : ""}`} data-kind="reasoning" data-entrance={displayItems[0]?.id || undefined}>
      <button
        type="button"
        className="reasoning__head"
        onClick={onToggle}
        aria-expanded={open}
      >
        <span className="turn-collapse__label" data-creation-label={label}>{label}</span>
        {!hasRunningWork && <ChevronRight className={`reasoning__chevron${open ? " reasoning__chevron--open" : ""}`} size={12} />}
      </button>
      {showLiveStatus && (
        <div className="turn-collapse__live-status" data-live-status="">
          {liveActionText && <span className="turn-collapse__live-action">{liveActionText}</span>}
          {throttledReasoning && (
            <>
              <span className="turn-collapse__live-thinking">{t("transcript.liveThinking")}</span>
              <ReasoningSummary
                text={throttledReasoning}
                streaming
                maxChars={160}
                onOpen={onToggle}
                className="turn-collapse__live-summary"
              />
            </>
          )}
        </div>
      )}
    </div>
  );
}
