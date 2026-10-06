// ProcessFoldHeader: the fold header row of one process segment.
// The fold body is NOT rendered here: an open fold contributes its body rows
// to the virtual row model (they mount only when scrolled into view), a closed
// fold builds no React subtree at all.

import { useContext } from "react";
import { ChevronRight } from "lucide-react";
import { useT } from "../lib/i18n";
import { formatTokens } from "../lib/format";
import type { CompactionItem, SegmentModel } from "../lib/transcriptRows";
import { useTick, resolveRunningDurationMs, workStatusLabel } from "../lib/workStatus";
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

  const hasRunningWork = segment.hasRunningWork;
  const now = useTick(hasRunningWork);
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
  const baseLabel = compactionItem
    ? compactionItem.pending
      ? (compactionItem.done ?? 0) > 0
        ? t("compaction.progress", { done: (compactionItem.done ?? 0), total: (compactionItem.total ?? 0) })
        : t("compaction.working")
      : t("compaction.title")
    : workStatusLabel(effectiveDurationMs, hasRunningWork, t);
  // 任务 556: live "12.3k tokens · 45 tokens/s" readout while the pass is
  // pending, fed by throttled backend CompactionProgress events. Same shape
  // as the composer run-strip's readout (formatTokens + status.tokens unit +
  // "<n> tokens/s") so a compacting session reads like a working one. tps
  // stays hidden until the backend's 500ms noise floor passes (it sends 0).
  const liveTokens = compactionItem?.pending ? (compactionItem.tokens ?? 0) : 0;
  const liveTps = compactionItem?.pending ? (compactionItem.tokensPerSec ?? 0) : 0;
  const readout =
    liveTokens > 0
      ? liveTps > 0
        ? ` · ${t("compaction.liveReadout", { tokens: formatTokens(liveTokens), tps: liveTps })}`
        : ` · ${formatTokens(liveTokens)} ${t("status.tokens")}`
      : "";
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
