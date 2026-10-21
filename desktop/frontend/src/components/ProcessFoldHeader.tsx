// ProcessFoldHeader: the fold header row of one process segment.
// The fold body is NOT rendered here: an open fold contributes its body rows
// to the virtual row model (they mount only when scrolled into view), a closed
// fold builds no React subtree at all.

import { useContext } from "react";
import { ChevronRight } from "lucide-react";
import { useT } from "../lib/i18n";
import type { CompactionItem, SegmentModel } from "../lib/transcriptRows";
import { useTick, resolveRunningDurationMs, workStatusLabel } from "../lib/workStatus";
import { compactionLiveReadout } from "../lib/compactionLive";
import { LiveStreamContext } from "./LiveStreamContext";

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
  // and users have no way to know process detail sits behind it.
  const toolCount = displayItems.reduce((n, it) => n + (it.kind === "tool" ? 1 : 0), 0);
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
    </div>
  );
}
