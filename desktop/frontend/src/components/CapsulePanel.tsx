// Task 447 capsule: composer-side floating panel (悬浮窗) with three
// sections following the task-440 panel form — running agents, running
// terminal commands, and the ended sub-agents directory. Running data is the
// controller jobs snapshot (desktop JobsForTab) the status-bar chip already
// uses; the ended directory is the new read-only ListSubagentsByParent
// surface over persisted sub-agent transcripts. Clicking an ended entry
// loads its transcript through ReadSubagentSession — the same preview
// pipeline the history drawer uses — and renders it read-only with the
// shared Transcript component.
//
// The two bridge calls are injected as props (onListSubagents /
// onReadSubagent) instead of importing lib/bridge here — the same pattern as
// onCancelJob — so the panel stays a pure component and the tsx test harness
// can stub them without loading the bridge module graph.
//
// This file is intentionally independent of task 440's unmerged
// RunningTasksPanel: same design language, separate component/CSS names, so
// the eventual merge/reconciliation stays a main-conversation decision.

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Activity, ArrowLeft, Bot, Sparkles, Square, TerminalSquare } from "lucide-react";
import { AnchoredPopover } from "./AnchoredPopover";
import { useT, type DictKey } from "../lib/i18n";
import { historyMessagesToItems } from "../lib/useController";
import type { HistoryMessage, JobView, SubagentArtifactView } from "../lib/types";

// Transcript loads lazily: the capsule only mounts it once a transcript is
// actually opened, and the static import would drag the full transcript
// module graph (SVG assets included) into every consumer of this file. In
// the desktop app the chunk is already in the bundle (main transcript).
const LazyTranscript = lazy(async () => ({ default: (await import("./Transcript")).Transcript }));

// formatCapsuleElapsed renders a live elapsed label from the job's StartedAt
// (unix ms). Negative deltas (clock skew) clamp to zero so the label never
// ticks backwards. Same shape as the task-440 panel clock.
export function formatCapsuleElapsed(startedAtMs: number, nowMs: number): string {
  const total = Math.max(0, Math.floor((nowMs - startedAtMs) / 1000));
  if (total < 60) return `${total}s`;
  const m = Math.floor(total / 60);
  const s = total % 60;
  if (m < 60) return `${m}m${String(s).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h${String(m % 60).padStart(2, "0")}m`;
}

// groupCapsuleJobs splits jobs into the two running sections. "bash" jobs
// are terminal commands; every other kind ("task", "fleet", unknown) is an
// agent-style job. Snapshot order is preserved within each group.
export function groupCapsuleJobs(jobs: readonly JobView[]): { agents: JobView[]; commands: JobView[] } {
  const agents: JobView[] = [];
  const commands: JobView[] = [];
  for (const job of jobs) {
    if (job.kind === "bash") commands.push(job);
    else agents.push(job);
  }
  return { agents, commands };
}

// endedCapsuleStatusKey maps a persisted status to its locale key. Entries
// still marked "running" never reach the ended directory (filtered by the
// caller), but unknown statuses fall back to the raw text instead of a
// missing translation.
function endedCapsuleStatusKey(status: string): DictKey | null {
  switch (status) {
    case "completed":
      return "composer.capsuleStatusCompleted";
    case "failed":
      return "composer.capsuleStatusFailed";
    case "interrupted":
      return "composer.capsuleStatusInterrupted";
    default:
      return null;
  }
}

function capsuleTimeLabel(unixMs: number): string {
  if (!unixMs) return "";
  const date = new Date(unixMs);
  const now = new Date();
  const sameDay = date.toDateString() === now.toDateString();
  const clock = `${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
  return sameDay ? clock : `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")} ${clock}`;
}

interface CapsuleDetailState {
  loading: boolean;
  failed: boolean;
  messages: HistoryMessage[];
}

const EMPTY_DETAIL: CapsuleDetailState = { loading: false, failed: false, messages: [] };

export function CapsuleIndicator({
  jobs = [],
  onCancelJob,
  sessionPath,
  onListSubagents,
  onReadSubagent,
}: {
  jobs?: readonly JobView[];
  // Existing stop chain (App.CancelJobForTab -> Controller.CancelJob ->
  // jobs.KillForSession); shared with the status-bar jobs chip.
  onCancelJob?: (jobID: string) => Promise<boolean>;
  // Active tab's session path; owner filter for the ended sub-agents
  // directory. Empty (no bound session) simply yields an empty directory.
  sessionPath?: string;
  // Read-only Wails surface (desktop/subagents_app.go), injected by App.
  onListSubagents?: (sessionPath: string) => Promise<SubagentArtifactView[]>;
  onReadSubagent?: (sessionPath: string, ref: string) => Promise<HistoryMessage[]>;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [stopping, setStopping] = useState<Set<string>>(() => new Set());
  const [now, setNow] = useState(() => Date.now());
  const [ended, setEnded] = useState<SubagentArtifactView[]>([]);
  const [endedLoaded, setEndedLoaded] = useState(false);
  const [selected, setSelected] = useState<SubagentArtifactView | null>(null);
  const [detail, setDetail] = useState<CapsuleDetailState>(EMPTY_DETAIL);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const detailSeq = useRef(0);
  const groups = useMemo(() => groupCapsuleJobs(jobs), [jobs]);
  const runningCount = jobs.length;
  const hasRunning = runningCount > 0;

  // Load the ended directory every time the panel opens and whenever the
  // running set drains (a just-finished batch writes its sidecars at
  // completion). Read-only and cheap; a failure renders an empty directory.
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    onListSubagents?.(sessionPath ?? "").then((views) => {
        if (cancelled) return;
        // Still-running invocations belong to the live sections above, not
        // the ended directory; their transcripts are unfinished anyway.
        setEnded(views.filter((view) => view.status !== "running"));
        setEndedLoaded(true);
      })
      .catch(() => {
        if (cancelled) return;
        setEnded([]);
        setEndedLoaded(true);
      });
    return () => {
      cancelled = true;
    };
    // onListSubagents is a stable useCallback in App; re-listing keys on the
    // open state and the running-set drain, not on callback identity.
  }, [open, hasRunning, sessionPath, onListSubagents]);

  // Live elapsed clock only while the panel is open and something is still
  // running (same rule as the task-440 panel).
  useEffect(() => {
    if (!open || !hasRunning) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [open, hasRunning]);

  // An empty panel nobody asked for is noise: auto-close once both the
  // running sections and the ended directory are empty. Gated on
  // endedLoaded so the async directory load can't race the closer.
  useEffect(() => {
    if (!open || !endedLoaded) return;
    if (!hasRunning && ended.length === 0) {
      setOpen(false);
      setSelected(null);
      setDetail(EMPTY_DETAIL);
    }
  }, [open, endedLoaded, hasRunning, ended.length]);

  const close = () => {
    setOpen(false);
    setSelected(null);
    setDetail(EMPTY_DETAIL);
  };

  const stop = async (jobID: string) => {
    if (!onCancelJob || stopping.has(jobID)) return;
    setStopping((current) => new Set(current).add(jobID));
    try {
      await onCancelJob(jobID);
    } finally {
      setStopping((current) => {
        const next = new Set(current);
        next.delete(jobID);
        return next;
      });
    }
  };

  const openEnded = (view: SubagentArtifactView) => {
    if (!view.hasTranscript) return;
    const seq = ++detailSeq.current;
    setSelected(view);
    setDetail({ loading: true, failed: false, messages: [] });
    onReadSubagent?.(sessionPath ?? "", view.ref).then((messages) => {
        if (seq !== detailSeq.current) return;
        setDetail({ loading: false, failed: false, messages });
      })
      .catch(() => {
        if (seq !== detailSeq.current) return;
        setDetail({ loading: false, failed: true, messages: [] });
      });
  };

  const renderRunningGroup = (title: string, rows: readonly JobView[], icon: "agent" | "terminal") => {
    if (rows.length === 0) return null;
    return (
      <div className="capsule-panel__group" data-capsule-group={icon}>
        <div className="capsule-panel__group-title">{title}</div>
        {rows.map((job) => {
          const pending = stopping.has(job.id);
          return (
            <div className="capsule-panel__row" key={job.id} data-capsule-job-id={job.id} data-capsule-job-kind={job.kind}>
              <span className="capsule-panel__row-icon" aria-hidden="true">
                {icon === "agent" ? <Bot size={14} /> : <TerminalSquare size={14} />}
              </span>
              <span className="capsule-panel__copy">
                <strong title={job.label || job.kind}>{job.label || job.kind}</strong>
                <small className="capsule-panel__elapsed" data-elapsed={formatCapsuleElapsed(job.startedAt, now)}>
                  {formatCapsuleElapsed(job.startedAt, now)}
                </small>
              </span>
              {onCancelJob && (
                <button
                  type="button"
                  className="capsule-panel__stop"
                  disabled={pending}
                  aria-label={`${t("status.jobStop")} ${job.label || job.kind}`}
                  onClick={() => void stop(job.id)}
                >
                  <Square size={10} fill="currentColor" aria-hidden="true" />
                  {pending ? t("status.jobStopping") : t("status.jobStop")}
                </button>
              )}
            </div>
          );
        })}
      </div>
    );
  };

  const endedRows = ended.map((view) => {
    const statusKey = endedCapsuleStatusKey(view.status);
    const statusLabel = statusKey ? t(statusKey) : view.status;
    const metaBits = [statusLabel, view.model, capsuleTimeLabel(view.createdAt)].filter(Boolean);
    return (
      <button
        type="button"
        className="capsule-panel__row capsule-panel__row--ended"
        key={view.ref}
        data-capsule-ended-id={view.ref}
        data-capsule-ended-status={view.status}
        data-capsule-ended-openable={view.hasTranscript}
        disabled={!view.hasTranscript}
        title={view.hasTranscript ? view.name || view.ref : t("composer.capsuleNotOpenable")}
        onClick={() => openEnded(view)}
      >
        <span className="capsule-panel__row-icon" aria-hidden="true">
          {view.kind === "skill" ? <Sparkles size={14} /> : <Bot size={14} />}
        </span>
        <span className="capsule-panel__copy">
          <strong>{view.name || view.kind || view.ref}</strong>
          <small>{metaBits.join(" · ")}</small>
        </span>
        {view.outcome && <span className="capsule-panel__outcome">{view.outcome}</span>}
      </button>
    );
  });

  const detailItems = useMemo(() => historyMessagesToItems(detail.messages, "capsule").items, [detail.messages]);

  return (
    <span className="capsule" data-capsule-count={runningCount} data-capsule-ended-count={ended.length}>
      <button
        ref={triggerRef}
        type="button"
        className={`capsule__trigger${hasRunning ? " capsule__trigger--active" : " capsule__trigger--idle"}`}
        aria-label={t("composer.capsuleTitle")}
        aria-expanded={open}
        aria-haspopup="dialog"
        title={t("composer.capsuleTitle")}
        onClick={() => (open ? close() : setOpen(true))}
      >
        <Activity size={15} aria-hidden="true" />
        {(hasRunning || ended.length > 0) && (
          <span className="capsule__badge" data-capsule-badge={hasRunning ? "running" : "ended"}>
            {hasRunning ? runningCount : ended.length}
          </span>
        )}
      </button>
      <AnchoredPopover open={open} anchorRef={triggerRef} onClose={close} className={`capsule-panel${selected ? " capsule-panel--detail" : ""}`} align="start">
        <section role="dialog" aria-label={t("composer.capsuleTitle")}>
          {selected ? (
            <>
              <header className="capsule-panel__header capsule-panel__header--detail">
                <button type="button" className="capsule-panel__back" aria-label={t("composer.capsuleBack")} onClick={() => { setSelected(null); setDetail(EMPTY_DETAIL); }}>
                  <ArrowLeft size={13} aria-hidden="true" />
                  {t("composer.capsuleBack")}
                </button>
                <strong className="capsule-panel__detail-title">{selected.name || selected.ref}</strong>
              </header>
              <div
                className="capsule-panel__history"
                data-capsule-history={selected.ref}
                data-capsule-history-state={detail.loading ? "loading" : detail.failed ? "failed" : detailItems.length === 0 ? "empty" : "ready"}
              >
                {detail.loading ? (
                  <div className="capsule-panel__history-empty">{t("composer.capsuleHistoryLoading")}</div>
                ) : detail.failed ? (
                  <div className="capsule-panel__history-empty">{t("composer.capsuleHistoryFailed")}</div>
                ) : detailItems.length === 0 ? (
                  <div className="capsule-panel__history-empty">{t("composer.capsuleHistoryEmpty")}</div>
                ) : (
                  <Suspense fallback={<div className="capsule-panel__history-empty">{t("composer.capsuleHistoryLoading")}</div>}>
                    <LazyTranscript items={detailItems} onPrompt={() => {}} questionNavigator={false} rewindDisabled />
                  </Suspense>
                )}
              </div>
            </>
          ) : (
            <>
              <header className="capsule-panel__header">{t("composer.capsuleTitle")}</header>
              {hasRunning || ended.length > 0 ? (
                <div className="capsule-panel__list">
                  {renderRunningGroup(t("composer.capsuleAgents"), groups.agents, "agent")}
                  {renderRunningGroup(t("composer.capsuleCommands"), groups.commands, "terminal")}
                  {ended.length > 0 && (
                    <div className="capsule-panel__group" data-capsule-group="ended">
                      <div className="capsule-panel__group-title">{t("composer.capsuleEnded", { n: ended.length })}</div>
                      {endedRows}
                    </div>
                  )}
                </div>
              ) : (
                <div className="capsule-panel__empty" data-capsule-empty="true">
                  {endedLoaded ? t("composer.capsuleEmpty") : t("composer.capsuleHistoryLoading")}
                </div>
              )}
            </>
          )}
        </section>
      </AnchoredPopover>
    </span>
  );
}
