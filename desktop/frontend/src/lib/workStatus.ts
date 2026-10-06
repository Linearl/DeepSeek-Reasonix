// Work-status helpers shared by the transcript process-fold header and the
// live turn region's status row.

import { useEffect, useState } from "react";
import type { useT } from "./i18n";

export function useTick(on: boolean): number {
  const [, setN] = useState(0);
  useEffect(() => {
    if (!on) return;
    const id = window.setInterval(() => setN((n) => n + 1), 1000);
    // Hidden webviews throttle setInterval (down to minutes per fire). When
    // visibility returns, repaint immediately from the wall clock so running
    // durations catch up to reality instead of waiting out the throttled
    // timer (task 341).
    const repaint = () => {
      if (document.visibilityState === "visible") setN((n) => n + 1);
    };
    document.addEventListener("visibilitychange", repaint);
    return () => {
      window.clearInterval(id);
      document.removeEventListener("visibilitychange", repaint);
    };
  }, [on]);
  return Date.now();
}

// Task 341: a running segment whose start timestamp is unknown (turnStartAt
// and reasoningStartedAt both missing) must not fall back to the static
// segment.durationMs snapshot — that value only advances with new events, so
// the fold header freezes during long quiet stretches. Anchor the live count
// at the first render that saw the segment running, keyed by the stable
// segment key: the anchor survives virtualized unmount/remount and parent
// re-renders, and is released once the segment stops running.
const runningAnchorBySegment = new Map<string, number>();

export type RunningDurationInput = {
  now: number;
  running: boolean;
  segmentKey: string;
  /** Static snapshot (completed work so far) — the growth base when unanchored. */
  staticDurationMs: number;
  turnStartAt?: number;
  reasoningStartedAt?: number;
};

export function resolveRunningDurationMs({
  now,
  running,
  segmentKey,
  staticDurationMs,
  turnStartAt,
  reasoningStartedAt,
}: RunningDurationInput): number {
  if (!running) {
    runningAnchorBySegment.delete(segmentKey);
    return staticDurationMs;
  }
  // Known starts keep the historical max() semantics: the larger of the
  // static snapshot and the live wall-clock count wins (a retried turn can
  // carry a static duration older than its fresh start clock).
  if (turnStartAt && turnStartAt > 0) return Math.max(staticDurationMs, Math.max(0, now - turnStartAt));
  if (reasoningStartedAt && reasoningStartedAt > 0) return Math.max(staticDurationMs, Math.max(0, now - reasoningStartedAt));
  let anchoredAt = runningAnchorBySegment.get(segmentKey);
  if (anchoredAt === undefined || anchoredAt > now) {
    anchoredAt = now;
    runningAnchorBySegment.set(segmentKey, anchoredAt);
  }
  // Extrapolate forward from the static snapshot: it is the best estimate of
  // the work already done when the run was first seen, so growing from it
  // keeps the label monotonic and ticking instead of frozen.
  return staticDurationMs + Math.max(0, now - anchoredAt);
}

export function formatWorkDuration(durationMs: number, t: ReturnType<typeof useT>): string {
  if (!Number.isFinite(durationMs) || durationMs <= 0) return "";
  const totalSeconds = Math.max(1, Math.round(durationMs / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (minutes <= 0) return t("transcript.durationSeconds", { s: totalSeconds });
  if (seconds <= 0) return t("transcript.durationMinutes", { m: minutes });
  return t("transcript.durationMinutesSeconds", { m: minutes, s: seconds });
}

export function workStatusLabel(durationMs: number, running: boolean, t: ReturnType<typeof useT>): string {
  const duration = formatWorkDuration(durationMs, t);
  if (running) {
    return duration ? t("transcript.workingDuration", { duration }) : t("transcript.working");
  }
  return duration ? t("transcript.workedDuration", { duration }) : t("transcript.worked");
}
