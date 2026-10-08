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
// Task 558: the ended directory defaults to collapsed (failed/interrupted
// count as ended; running sections stay expanded), the list is height-capped
// with internal scrolling (styles.css), and records can be deleted singly or
// cleared when ended — DeleteSubagentRecord / ClearEndedSubagents remove the
// persisted transcript, sidecars, and metadata, not just the row. The panel
// height cap and the collapse state keep the panel from wallpapering the
// screen as the ended directory grows.
//
// The two bridge calls are injected as props (onListSubagents /
// onReadSubagent) instead of importing lib/bridge here — the same pattern as
// onCancelJob — so the panel stays a pure component and the tsx test harness
// can stub them without loading the bridge module graph.
//
// This file is intentionally independent of task 440's unmerged
// RunningTasksPanel: same design language, separate component/CSS names. The
// main conversation ruled (2026-10-02, option a) that this capsule absorbs
// task 440 — it is a verified superset — so that panel stays retired instead
// of merging.

import { lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import { Activity, ArrowLeft, Bot, ChevronDown, SendHorizontal, Sparkles, Square, TerminalSquare } from "lucide-react";
import { AnchoredPopover } from "./AnchoredPopover";
import { InlineConfirmButton } from "./InlineConfirmButton";
import { useT, type DictKey } from "../lib/i18n";
import { historyMessagesToItems } from "../lib/useController";
import type { BackgroundRuntimeView, HistoryMessage, JobView, SubagentArtifactView, SubagentSendReceiptView } from "../lib/types";

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

// CapsuleWorkEntry is one flattened running row: the job plus the runtime it
// came from. A tabId of "" marks the active controller's own snapshot (the
// legacy source) — those rows stop through onCancelJob; rows with a tabId stop
// through onCancelRuntimeJob (App.CancelJobForTab), which reaches detached and
// background tabs the active controller knows nothing about (task 440).
export interface CapsuleWorkEntry {
  job: JobView;
  tabId: string;
  origin: string;
  detached: boolean;
}

// mergeCapsuleWork flattens the active controller's jobs plus every
// process-local runtime's jobs (visible and detached tabs, from
// App.BackgroundRuntimes) into one deduplicated running list. The active
// snapshot leads (current session first); runtime rows keep their snapshot
// order. Job ids seen in any runtime group are skipped in the active
// snapshot — the same controller surfaces in both sources, and a duplicate
// row would double-count the badge and render twice.
export function mergeCapsuleWork(
  jobs: readonly JobView[],
  runtimes: readonly BackgroundRuntimeView[],
  unknownOriginLabel: string,
): CapsuleWorkEntry[] {
  const entries: CapsuleWorkEntry[] = [];
  const seen = new Set<string>();
  for (const job of jobs) {
    if (seen.has(job.id)) continue;
    seen.add(job.id);
    entries.push({ job, tabId: "", origin: "", detached: false });
  }
  for (const runtime of runtimes) {
    for (const job of runtime.jobs) {
      if (seen.has(job.id)) continue;
      seen.add(job.id);
      entries.push({ job, tabId: runtime.tabId, origin: runtime.title || unknownOriginLabel, detached: runtime.detached });
    }
  }
  return entries;
}

// splitCapsuleEntries groups flattened running rows into the two sections,
// same bash/agent rule as groupCapsuleJobs.
export function splitCapsuleEntries(entries: readonly CapsuleWorkEntry[]): { agents: CapsuleWorkEntry[]; commands: CapsuleWorkEntry[] } {
  const agents: CapsuleWorkEntry[] = [];
  const commands: CapsuleWorkEntry[] = [];
  for (const entry of entries) {
    if (entry.job.kind === "bash") commands.push(entry);
    else agents.push(entry);
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

// 任务616: the capsule's message composer for one sub-agent. Persist-first:
// the backend writes the mailbox file before attempting the live steer, so
// every receipt (steered / queued / parked) means the message is on disk;
// disabled means the messaging switch is off and nothing was written.
function CapsuleMailComposer({
  sessionPath,
  target,
  onSendMessage,
}: {
  sessionPath?: string;
  target: SubagentArtifactView;
  onSendMessage?: (sessionPath: string, ref: string, summary: string, text: string) => Promise<SubagentSendReceiptView>;
}) {
  const t = useT();
  const [summary, setSummary] = useState("");
  const [text, setText] = useState("");
  const [sending, setSending] = useState(false);
  const [receipt, setReceipt] = useState<SubagentSendReceiptView | null>(null);
  const [error, setError] = useState<string | null>(null);
  const canSend = Boolean(onSendMessage && sessionPath && text.trim() && !sending);
  const send = async () => {
    if (!onSendMessage || !sessionPath || !text.trim() || sending) return;
    setSending(true);
    setError(null);
    try {
      const view = await onSendMessage(sessionPath, target.ref, summary.trim(), text.trim());
      setReceipt(view);
      if (view.disposition !== "disabled") setText("");
      setSummary("");
    } catch (e) {
      setReceipt(null);
      setError(String((e as Error)?.message ?? e));
    } finally {
      setSending(false);
    }
  };
  const receiptKey: DictKey | null = receipt
    ? receipt.disposition === "steered" ? "composer.capsuleMailSteered"
    : receipt.disposition === "queued" ? "composer.capsuleMailQueued"
    : receipt.disposition === "parked" ? "composer.capsuleMailParked"
    : "composer.capsuleMailDisabled"
    : null;
  return (
    <div className="capsule-panel__mail" data-capsule-mail-target={target.ref}>
      <div className="capsule-panel__mail-row">
        <input
          type="text"
          className="capsule-panel__mail-summary"
          value={summary}
          placeholder={t("composer.capsuleMailSummary")}
          aria-label={t("composer.capsuleMailSummary")}
          disabled={sending}
          onChange={(e) => setSummary(e.target.value)}
        />
      </div>
      <div className="capsule-panel__mail-row">
        <textarea
          className="capsule-panel__mail-text"
          value={text}
          rows={2}
          placeholder={t("composer.capsuleMailPlaceholder")}
          aria-label={t("composer.capsuleMailSend")}
          disabled={sending}
          onChange={(e) => setText(e.target.value)}
        />
        <button
          type="button"
          className="capsule-panel__mail-send"
          disabled={!canSend}
          aria-label={t("composer.capsuleMailSend")}
          title={t("composer.capsuleMailSend")}
          onClick={() => void send()}
        >
          <SendHorizontal size={13} aria-hidden="true" />
          <span>{sending ? t("composer.capsuleMailSending") : t("composer.capsuleMailSend")}</span>
        </button>
      </div>
      {receiptKey && (
        <div className="capsule-panel__mail-receipt" role="status" data-capsule-mail-disposition={receipt?.disposition}>
          {t(receiptKey)}
        </div>
      )}
      {error && (
        <div className="capsule-panel__mail-receipt capsule-panel__mail-receipt--error" role="alert">
          {t("composer.capsuleMailError")} {error}
        </div>
      )}
    </div>
  );
}

export function CapsuleIndicator({
  jobs = [],
  runtimes = [],
  onCancelJob,
  onCancelRuntimeJob,
  sessionPath,
  onListSubagents,
  onReadSubagent,
  onDeleteSubagent,
  onClearEndedSubagents,
  onSendMessage,
}: {
  jobs?: readonly JobView[];
  // Task 440: every process-local runtime's running jobs (visible and
  // detached tabs), from App.BackgroundRuntimes — the same source the
  // status-bar jobs chip uses. The panel lists ALL running sub-agents and
  // background commands, not just the active tab's.
  runtimes?: readonly BackgroundRuntimeView[];
  // Existing stop chain (App.CancelJobForTab -> Controller.CancelJob ->
  // jobs.KillForSession); shared with the status-bar jobs chip. Stops rows
  // from the active snapshot (tabId "").
  onCancelJob?: (jobID: string) => Promise<boolean>;
  // Per-tab stop chain (App.CancelJobForTab(tabId, jobId)) for rows that
  // belong to another visible or detached runtime (task 440 stop wiring).
  onCancelRuntimeJob?: (tabId: string, jobID: string) => Promise<boolean>;
  // Active tab's session path; owner filter for the ended sub-agents
  // directory. Empty (no bound session) simply yields an empty directory.
  sessionPath?: string;
  // Read-only Wails surface (desktop/subagents_app.go), injected by App.
  onListSubagents?: (sessionPath: string) => Promise<SubagentArtifactView[]>;
  onReadSubagent?: (sessionPath: string, ref: string) => Promise<HistoryMessage[]>;
  // Task 558 delete surface. onDeleteSubagent removes one ended record
  // (transcript + sidecars + meta); onClearEndedSubagents removes every ended
  // record and resolves how many went. Both re-list through onListSubagents
  // afterwards — the directory reload is the source of truth, not a local
  // optimistic splice.
  onDeleteSubagent?: (sessionPath: string, ref: string) => Promise<void>;
  onClearEndedSubagents?: (sessionPath: string) => Promise<number>;
  // 任务616: persist-first message send into one sub-agent's mailbox. The
  // receipt carries the disposition the composer renders inline.
  onSendMessage?: (sessionPath: string, ref: string, summary: string, text: string) => Promise<SubagentSendReceiptView>;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [stopping, setStopping] = useState<Set<string>>(() => new Set());
  const [now, setNow] = useState(() => Date.now());
  const [ended, setEnded] = useState<SubagentArtifactView[]>([]);
  // 任务616: running artifact views (ref-bearing) so a live sub-agent is
  // messageable from the same directory the ended ones live in.
  const [runningArtifacts, setRunningArtifacts] = useState<SubagentArtifactView[]>([]);
  const [endedLoaded, setEndedLoaded] = useState(false);
  const [selected, setSelected] = useState<SubagentArtifactView | null>(null);
  const [detail, setDetail] = useState<CapsuleDetailState>(EMPTY_DETAIL);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const detailSeq = useRef(0);
  // Task 558 default collapse rule: ended sub-agents start collapsed (a
  // finished directory must not wallpaper the panel — the screenshot that
  // motivated this task showed 12 flat rows filling the screen), running
  // sections stay expanded. A manual toggle is remembered for the app session
  // via this state — the CapsuleIndicator stays mounted across panel
  // open/close, so re-opening keeps the user's choice — and deliberately NOT
  // persisted: a restart returns to the default. failed/interrupted count as
  // ended: a failed run does not deserve a permanently expanded row either.
  const [endedExpanded, setEndedExpanded] = useState(false);
  // Bumped after a successful delete/clear so the directory reload effect
  // re-pulls without re-opening the panel.
  const [listNonce, setListNonce] = useState(0);
  const [deletingRef, setDeletingRef] = useState<string | null>(null);
  const [clearing, setClearing] = useState(false);
  const [recordError, setRecordError] = useState<string | null>(null);
  // Task 440 empty state: a panel the user explicitly opened while idle stays
  // open showing the empty state instead of snapping shut. The 447 anti-noise
  // auto-close only applies to panels that HAD content and drained to empty.
  const hadContentRef = useRef(false);
  const runningEntries = useMemo(
    () => mergeCapsuleWork(jobs, runtimes, t("runtime.unknownTask")),
    [jobs, runtimes, t],
  );
  const groups = useMemo(() => splitCapsuleEntries(runningEntries), [runningEntries]);
  const runningCount = runningEntries.length;
  const hasRunning = runningCount > 0;
  // Task 447c2 event-driven refresh: the controller pushes a fresh jobs
  // snapshot on notice/turn_done events, so the value-keyed running identity
  // below changes exactly when a job enters or leaves the snapshot. Keying
  // the directory reload on it (instead of the boolean "anything running")
  // lets a single sub-agent that finishes move into the ended directory
  // immediately while the panel stays open — previously it stayed invisible
  // until every job drained or the panel was reopened. Value keying (joined
  // ids, not array identity) keeps same-set re-renders from re-pulling.
  // Task 440: the identity spans every runtime's jobs, so a detached tab's
  // job finishing also refreshes the directory while the panel is open.
  const runningKey = useMemo(
    () => runningEntries.map((entry) => `${entry.tabId}:${entry.job.id}`).join("\n"),
    [runningEntries],
  );

  // Load the ended directory every time the panel opens and whenever the
  // running set changes while open (a just-finished batch writes its sidecars
  // at completion). Read-only and cheap; a failure renders an empty directory.
  // Task 558: listNonce also re-pulls after a delete/clear resolves — the
  // fresh listing is the source of truth for what remains.
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    onListSubagents?.(sessionPath ?? "").then((views) => {
        if (cancelled) return;
        // Still-running invocations belong to the live sections above, not
        // the ended directory; their transcripts are unfinished anyway.
        setEnded(views.filter((view) => view.status !== "running"));
        setRunningArtifacts(views.filter((view) => view.status === "running"));
        setEndedLoaded(true);
      })
      .catch(() => {
        if (cancelled) return;
        setEnded([]);
        setRunningArtifacts([]);
        setEndedLoaded(true);
      });
    return () => {
      cancelled = true;
    };
    // onListSubagents is a stable useCallback in App; re-listing keys on the
    // open state, the running-set identity, and the post-delete nonce — not
    // on callback identity.
  }, [open, runningKey, sessionPath, onListSubagents, listNonce]);

  // Live elapsed clock only while the panel is open and something is still
  // running (same rule as the task-440 panel).
  useEffect(() => {
    if (!open || !hasRunning) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [open, hasRunning]);

  // Track "this open session had content" for the drain auto-close: an
  // explicitly opened idle panel keeps its empty state (task 440 ③); a panel
  // whose running sections and directory drained closes itself (447 anti-noise).
  useEffect(() => {
    if (open && (hasRunning || ended.length > 0)) hadContentRef.current = true;
  }, [open, hasRunning, ended.length]);
  useEffect(() => {
    if (!open || !endedLoaded) return;
    if (hadContentRef.current && !hasRunning && ended.length === 0) {
      setOpen(false);
      setSelected(null);
      setDetail(EMPTY_DETAIL);
    }
  }, [open, endedLoaded, hasRunning, ended.length]);

  const close = () => {
    setOpen(false);
    hadContentRef.current = false;
    setSelected(null);
    setDetail(EMPTY_DETAIL);
    setRecordError(null);
  };

  const stop = async (entry: CapsuleWorkEntry) => {
    const key = `${entry.tabId}:${entry.job.id}`;
    // Route by origin: active-snapshot rows go through the shared cancel
    // chain; foreign-runtime rows must go through the per-tab chain —
    // cancelling a foreign job id on the active controller would silently
    // hit the wrong job manager and stop nothing.
    const handler = entry.tabId ? onCancelRuntimeJob : onCancelJob;
    if (!handler || stopping.has(key)) return;
    setStopping((current) => new Set(current).add(key));
    try {
      if (entry.tabId && onCancelRuntimeJob) await onCancelRuntimeJob(entry.tabId, entry.job.id);
      else if (onCancelJob) await onCancelJob(entry.job.id);
    } finally {
      setStopping((current) => {
        const next = new Set(current);
        next.delete(key);
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

  // Task 558: remove one ended record (files included) or every ended record
  // at once. Success re-lists through the nonce; failure surfaces inline —
  // the row stays, so nothing silently vanishes on an error. (The detail view
  // replaces the list, so a delete always originates from the list view.)
  const deleteRecord = async (view: SubagentArtifactView) => {
    if (!onDeleteSubagent || deletingRef !== null || clearing) return;
    setDeletingRef(view.ref);
    setRecordError(null);
    try {
      await onDeleteSubagent(sessionPath ?? "", view.ref);
      setListNonce((n) => n + 1);
    } catch (e) {
      setRecordError(String((e as Error)?.message ?? e));
    } finally {
      setDeletingRef(null);
    }
  };

  const clearEnded = async () => {
    if (!onClearEndedSubagents || clearing || deletingRef !== null) return;
    setClearing(true);
    setRecordError(null);
    try {
      await onClearEndedSubagents(sessionPath ?? "");
      setListNonce((n) => n + 1);
    } catch (e) {
      setRecordError(String((e as Error)?.message ?? e));
    } finally {
      setClearing(false);
    }
  };

  const renderRunningGroup = (title: string, rows: readonly CapsuleWorkEntry[], icon: "agent" | "terminal") => {
    if (rows.length === 0) return null;
    return (
      <div className="capsule-panel__group" data-capsule-group={icon}>
        <div className="capsule-panel__group-title">{title}</div>
        {rows.map((entry) => {
          const pending = stopping.has(`${entry.tabId}:${entry.job.id}`);
          // A foreign-runtime row can only stop through the per-tab chain; if
          // App didn't wire it, the button renders disabled rather than lying
          // (cancelling a foreign id on the active controller stops nothing).
          const canStop = entry.tabId ? Boolean(onCancelRuntimeJob) : Boolean(onCancelJob);
          return (
            <div className="capsule-panel__row" key={`${entry.tabId}:${entry.job.id}`} data-capsule-job-id={entry.job.id} data-capsule-job-kind={entry.job.kind} data-capsule-job-tab={entry.tabId}>
              <span className="capsule-panel__row-icon" aria-hidden="true">
                {icon === "agent" ? <Bot size={14} /> : <TerminalSquare size={14} />}
              </span>
              <span className="capsule-panel__copy">
                <strong title={entry.job.label || entry.job.kind}>{entry.job.label || entry.job.kind}</strong>
                <small className="capsule-panel__elapsed" data-elapsed={formatCapsuleElapsed(entry.job.startedAt, now)}>
                  {entry.origin && <span className="capsule-panel__origin">{entry.origin} · </span>}
                  {formatCapsuleElapsed(entry.job.startedAt, now)}
                </small>
              </span>
              {canStop && (
                <button
                  type="button"
                  className="capsule-panel__stop"
                  disabled={pending}
                  aria-label={`${t("status.jobStop")} ${entry.job.label || entry.job.kind}`}
                  onClick={() => void stop(entry)}
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

  // Task 558: an ended row is a div holding the open button (transcript
  // preview) plus its own delete button — a button inside a button is invalid
  // HTML, so the open affordance moved to `.capsule-panel__row-main`. A record
  // without a transcript keeps its open button disabled but can still be
  // deleted: removing a junk meta-only entry is exactly what the user wants.
  const endedRows = ended.map((view) => {
    const statusKey = endedCapsuleStatusKey(view.status);
    const statusLabel = statusKey ? t(statusKey) : view.status;
    const metaBits = [statusLabel, view.model, capsuleTimeLabel(view.createdAt)].filter(Boolean);
    return (
      <div
        className="capsule-panel__row capsule-panel__row--ended"
        key={view.ref}
        data-capsule-ended-id={view.ref}
        data-capsule-ended-status={view.status}
        data-capsule-ended-openable={view.hasTranscript}
      >
        <button
          type="button"
          className="capsule-panel__row-main"
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
          {view.pendingMail > 0 && (
            <span className="capsule-panel__mail-badge" title={t("composer.capsuleMailPending", { n: view.pendingMail })}>
              {t("composer.capsuleMailPending", { n: view.pendingMail })}
            </span>
          )}
        </button>
        {onDeleteSubagent && (
          <InlineConfirmButton
            label={t("composer.capsuleDelete")}
            confirmLabel={t("composer.capsuleDeleteConfirm")}
            cancelLabel={t("common.cancel")}
            disabled={deletingRef !== null || clearing}
            danger
            onConfirm={() => void deleteRecord(view)}
          />
        )}
      </div>
    );
  });

  // 任务616: ref-bearing rows for this session's RUNNING sub-agents — the
  // only targets a mid-turn message can reach. Same open affordance as the
  // ended rows; the detail composer does the sending.
  const runningArtifactRows = runningArtifacts.map((view) => (
    <div className="capsule-panel__row capsule-panel__row--ended" key={view.ref} data-capsule-running-id={view.ref}>
      <button
        type="button"
        className="capsule-panel__row-main"
        disabled={!view.hasTranscript}
        title={view.hasTranscript ? view.name || view.ref : t("composer.capsuleNotOpenable")}
        onClick={() => openEnded(view)}
      >
        <span className="capsule-panel__row-icon" aria-hidden="true">
          {view.kind === "skill" ? <Sparkles size={14} /> : <Bot size={14} />}
        </span>
        <span className="capsule-panel__copy">
          <strong>{view.name || view.kind || view.ref}</strong>
          <small>{[t("subagent.phase.running"), view.model, capsuleTimeLabel(view.createdAt)].filter(Boolean).join(" · ")}</small>
        </span>
        {view.pendingMail > 0 && (
          <span className="capsule-panel__mail-badge" title={t("composer.capsuleMailPending", { n: view.pendingMail })}>
            {t("composer.capsuleMailPending", { n: view.pendingMail })}
          </span>
        )}
      </button>
    </div>
  ));

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
        {/* 任务 497：徽标只计运行中。已结束子代理数只出现在面板内的
            「已结束子代理（n）」节标题里（自带口径），不再回填到这个无标注
            的入口徽标——否则空闲时徽标显示已结束数，会被误读成仍在运行。 */}
        {hasRunning && (
          <span className="capsule__badge" data-capsule-badge="running">
            {runningCount}
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
              {onSendMessage && sessionPath && (
                <CapsuleMailComposer sessionPath={sessionPath} target={selected} onSendMessage={onSendMessage} />
              )}
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
              {hasRunning || ended.length > 0 || runningArtifactRows.length > 0 ? (
                <div className="capsule-panel__list">
                  {/* Task 440 ③: an open panel with nothing running states it
                      explicitly instead of silently shrinking to the ended
                      directory (or closing). */}
                  {!hasRunning && (
                    <div className="capsule-panel__running-empty" data-capsule-running-empty="true">
                      {t("composer.capsuleNoRunning")}
                    </div>
                  )}
                  {renderRunningGroup(t("composer.capsuleAgents"), groups.agents, "agent")}
                  {renderRunningGroup(t("composer.capsuleCommands"), groups.commands, "terminal")}
                  {runningArtifactRows.length > 0 && (
                    <div className="capsule-panel__group" data-capsule-group="running-subagents">
                      <div className="capsule-panel__group-title">{t("composer.capsuleMailRunning")}</div>
                      {runningArtifactRows}
                    </div>
                  )}
                  {ended.length > 0 && (
                    <div className="capsule-panel__group" data-capsule-group="ended" data-capsule-ended-expanded={endedExpanded}>
                      {/* 任务 558 折叠规则：已结束目录默认折叠（failed/interrupted 也算已结束），
                          运行区不受影响永远展开；手动展开会话内记住、不持久化。 */}
                      <div className="capsule-panel__group-title capsule-panel__ended-head">
                        <button
                          type="button"
                          className="capsule-panel__ended-toggle"
                          aria-expanded={endedExpanded}
                          title={endedExpanded ? t("composer.capsuleEndedCollapse") : t("composer.capsuleEndedExpand")}
                          onClick={() => setEndedExpanded((v) => !v)}
                        >
                          <ChevronDown size={11} aria-hidden="true" className="capsule-panel__ended-chevron" />
                          {t("composer.capsuleEnded", { n: ended.length })}
                        </button>
                        {onClearEndedSubagents && (
                          <InlineConfirmButton
                            label={t("composer.capsuleClear")}
                            confirmLabel={t("composer.capsuleClearConfirm")}
                            cancelLabel={t("common.cancel")}
                            disabled={clearing || deletingRef !== null}
                            danger
                            primary
                            onConfirm={() => void clearEnded()}
                          />
                        )}
                      </div>
                      {recordError && (
                        <div className="capsule-panel__record-error" role="alert" data-capsule-record-error="true">
                          {t("composer.capsuleRecordError")}
                          {recordError}
                        </div>
                      )}
                      {endedExpanded && endedRows}
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
