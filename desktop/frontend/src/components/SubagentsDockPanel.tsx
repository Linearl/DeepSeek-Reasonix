import { lazy, memo, Suspense, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowLeft,
  Ban,
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  CircleDashed,
  FoldHorizontal,
  LoaderCircle,
  UnfoldHorizontal,
} from "lucide-react";
import { useT, type DictKey } from "../lib/i18n";
import { historyMessagesToItems } from "../lib/useController";
import { subjectOf } from "../lib/tools";
import type { HistoryMessage } from "../lib/types";
import {
  SUBAGENT_DIRECTORY_PAGE_SIZE,
  mergeEndedSubagentRecords,
  type SubagentDirectory,
  type SubagentDirectoryEntry,
} from "../lib/subagentDirectory";
import type { SubagentArtifactView } from "../lib/types";

// 任务440: the dock detail's live transcript loads lazily — the same pattern
// as the capsule detail, and the chunk is already in the bundle (main
// transcript).
const LazyTranscript = lazy(async () => ({ default: (await import("./Transcript")).Transcript }));

// 任务440: transcript read states for the dock detail view. loading/failed/
// empty mirror the capsule history view; a running entry re-pulls every 2s so
// the message stream keeps up with the child's work (the same cadence the
// capsule's live clock uses), an ended entry reads once.
interface DockLiveTranscriptState {
  loading: boolean;
  failed: boolean;
  messages: HistoryMessage[];
}

const EMPTY_DOCK_LIVE: DockLiveTranscriptState = { loading: false, failed: false, messages: [] };

export const SUBAGENT_LIVE_POLL_MS = 2000;

// Task 495: right-dock "子代理" tab body, modeled on the zcode subagent
// directory (SubagentDirectorySidePane): a running section and an ended
// section, rows folded by default, click expands a small inline detail —
// plus the 20-per-page reveal on the ended list. The data source is the
// session transcript itself (lib/subagentDirectory), so nothing here fetches.
//
// Task 507 dual state (experimental_subagent_detail):
// - off (default, plan C): the row click keeps the exact 495 inline preview
//   expansion, plus the optional widen/narrow dock-width toolbar;
// - on (plan A): a row click switches the dock body to a read-only detail
//   view with a back button. Hard boundary: subagents have no input pipeline
//   (task.go: "fail with a precise question instead of guessing"), so the
//   detail view renders no input surface and says so. The selection lives in
//   component state only — nothing is persisted, a restart lands back on the
//   list (the progress preview itself is never on disk).

function rowTitle(entry: SubagentDirectoryEntry): string {
  const item = entry.item;
  return item.subject || subjectOf(item.name, item.args) || item.resolvedName || item.name;
}

function statusLabelKey(entry: SubagentDirectoryEntry): DictKey {
  if (entry.phase) return `subagent.phase.${entry.phase}` as DictKey;
  switch (entry.status) {
    case "done": return "subagent.phase.completed";
    case "error": return "subagent.phase.failed";
    case "stopped": return "subagent.phase.cancelled";
    default: return "subagent.phase.running";
  }
}

function StatusIcon({ entry }: { entry: SubagentDirectoryEntry }) {
  if (entry.running) return <LoaderCircle size={13} aria-hidden="true" className="subagents-panel__spin" />;
  const terminal = entry.phase ?? entry.status;
  if (terminal === "failed" || terminal === "error" || terminal === "partial") {
    return <CircleAlert size={13} aria-hidden="true" className="subagents-panel__icon subagents-panel__icon--failed" />;
  }
  if (terminal === "cancelled" || terminal === "stopped") {
    return <Ban size={13} aria-hidden="true" className="subagents-panel__icon subagents-panel__icon--cancelled" />;
  }
  if (terminal === "queued") {
    return <CircleDashed size={13} aria-hidden="true" className="subagents-panel__icon" />;
  }
  return <CheckCircle2 size={13} aria-hidden="true" className="subagents-panel__icon subagents-panel__icon--done" />;
}

function entryDurationSeconds(entry: SubagentDirectoryEntry): number {
  const progress = entry.item.subagentProgress;
  return Math.round((progress?.durationMs ?? entry.item.durationMs ?? 0) / 1000);
}

function entryStartedClock(entry: SubagentDirectoryEntry): string {
  return entry.item.startedAt
    ? new Date(entry.item.startedAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })
    : "";
}

/** One folded row (requirement 495 ①: ended subagents default to the row
 *  state); clicking expands the inline detail — or, under the 507 plan-A
 *  switch, asks the panel to open the read-only detail view. */
const DirectoryRow = memo(function DirectoryRow({
  entry,
  onSelect,
}: {
  entry: SubagentDirectoryEntry;
  onSelect?: () => void;
}) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const title = rowTitle(entry);
  const summary = entry.item.summary || entry.item.error || "";
  const durationSeconds = entryDurationSeconds(entry);
  const startedClock = entryStartedClock(entry);
  const handleClick = () => {
    if (onSelect) {
      onSelect();
      return;
    }
    setOpen((value) => !value);
  };
  return (
    <li className="subagents-panel__row-wrap">
      <button
        type="button"
        className={`subagents-panel__row${open ? " subagents-panel__row--open" : ""}`}
        aria-expanded={onSelect ? undefined : open}
        onClick={handleClick}
      >
        <StatusIcon entry={entry} />
        <span className="subagents-panel__row-title" title={title}>{title}</span>
        <span className="subagents-panel__row-status">{t(statusLabelKey(entry))}</span>
        <ChevronRight size={12} aria-hidden="true" className="subagents-panel__chevron" />
      </button>
      {open && (
        <div className="subagents-panel__detail">
          {summary ? <p className="subagents-panel__detail-summary">{summary}</p> : null}
          <p className="subagents-panel__detail-meta">
            {startedClock ? <span>{t("subagentPanel.startedAt", { time: startedClock })}</span> : null}
            {durationSeconds > 0 ? <span>{t("subagent.phase.elapsed", { n: durationSeconds })}</span> : null}
          </p>
        </div>
      )}
    </li>
  );
});

/** Task 507 plan A: the read-only in-dock detail view. Same content the
 *  plan-C inline preview carries (summary/error + the in-memory progress
 *  preview), in the wider dock body, with a back button and an explicit
 *  read-only notice — no input surface by design.
 *
 *  任务440: when the entry carries a persisted transcript ref and the App
 *  injected the read bridge, the view renders the child's actual work
 *  process (message stream / tool calls) instead of the truncated preview —
 *  re-pulling every SUBAGENT_LIVE_POLL_MS while the child is still running so
 *  the stream keeps up live (this is the supplement's "运行中也能看" next to
 *  zcode). Without a ref (ephemeral run) or without the bridge the view falls
 *  back to the memory-only preview, byte for byte the 507 behavior. */
function SubagentDetailView({
  entry,
  onBack,
  sessionPath,
  onReadSubagent,
}: {
  entry: SubagentDirectoryEntry;
  onBack: () => void;
  sessionPath?: string;
  onReadSubagent?: (sessionPath: string, ref: string) => Promise<HistoryMessage[]>;
}) {
  const t = useT();
  const title = rowTitle(entry);
  const summary = entry.item.summary || "";
  const error = entry.item.error || "";
  const progress = entry.item.subagentProgress;
  const previewReasoning = progress?.reasoning || "";
  const previewText = progress?.text || "";
  const previewNotice = progress?.notice || "";
  const hasPreview = Boolean(previewReasoning || previewText || previewNotice);
  const durationSeconds = entryDurationSeconds(entry);
  const startedClock = entryStartedClock(entry);
  const ref = entry.ref;
  const liveEnabled = Boolean(ref && onReadSubagent && sessionPath);
  const [live, setLive] = useState<DockLiveTranscriptState>(liveEnabled ? { loading: true, failed: false, messages: [] } : EMPTY_DOCK_LIVE);
  const liveSeq = useRef(0);
  useEffect(() => {
    if (!ref || !onReadSubagent || !sessionPath) return;
    const seq = ++liveSeq.current;
    let cancelled = false;
    const pull = () => {
      onReadSubagent(sessionPath, ref)
        .then((messages) => {
          if (cancelled || seq !== liveSeq.current) return;
          setLive({ loading: false, failed: false, messages });
        })
        .catch(() => {
          if (cancelled || seq !== liveSeq.current) return;
          setLive({ loading: false, failed: true, messages: [] });
        });
    };
    pull();
    // A running child keeps the stream fresh; an ended one reads once — its
    // transcript is final (the parent re-render supplies the new running
    // flag, and the effect re-subscribes on that transition).
    if (!entry.running) {
      return () => {
        cancelled = true;
      };
    }
    const timer = window.setInterval(pull, SUBAGENT_LIVE_POLL_MS);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [ref, sessionPath, onReadSubagent, entry.running]);
  // Value-keyed projection: a poll returning identical content re-renders the
  // wrapper but not the transcript tree.
  const liveItems = useMemo(() => historyMessagesToItems(live.messages, "subagent-dock").items, [live.messages]);
  return (
    <div className="subagents-panel__detailview" role="region" aria-label={t("subagentPanel.detailTitle")}>
      <div className="subagents-panel__detailview-head">
        <button type="button" className="subagents-panel__back" onClick={onBack}>
          <ArrowLeft size={13} aria-hidden="true" />
          <span>{t("subagentPanel.back")}</span>
        </button>
      </div>
      <div className="subagents-panel__detailview-title" title={title}>
        <StatusIcon entry={entry} />
        <span className="subagents-panel__detailview-name">{title}</span>
      </div>
      <p className="subagents-panel__detailview-meta">
        <span>{t(statusLabelKey(entry))}</span>
        {startedClock ? <span>{t("subagentPanel.startedAt", { time: startedClock })}</span> : null}
        {durationSeconds > 0 ? <span>{t("subagent.phase.elapsed", { n: durationSeconds })}</span> : null}
      </p>
      {liveEnabled ? (
        <div
          className="subagents-panel__live"
          data-subagents-live={entry.running ? "running" : "ended"}
          data-subagents-live-state={live.loading ? "loading" : live.failed ? "failed" : liveItems.length === 0 ? "empty" : "ready"}
        >
          {live.loading ? (
            <div className="subagents-panel__detailview-note">{t("composer.capsuleHistoryLoading")}</div>
          ) : live.failed ? (
            <div className="subagents-panel__detailview-note">{t("composer.capsuleHistoryFailed")}</div>
          ) : liveItems.length === 0 ? (
            <div className="subagents-panel__detailview-note">{t("composer.capsuleHistoryEmpty")}</div>
          ) : (
            <Suspense fallback={<div className="subagents-panel__detailview-note">{t("composer.capsuleHistoryLoading")}</div>}>
              <LazyTranscript items={liveItems} onPrompt={() => {}} questionNavigator={false} rewindDisabled />
            </Suspense>
          )}
        </div>
      ) : (
        <>
          {error ? (
            <div className="subagents-panel__detailview-block subagents-panel__detailview-block--error">
              <div className="subagents-panel__detailview-label">{t("subagent.preview.notice")}</div>
              <pre className="subagents-panel__detailview-text">{error}</pre>
            </div>
          ) : null}
          {summary ? (
            <div className="subagents-panel__detailview-block">
              <div className="subagents-panel__detailview-label">{t("subagentPanel.detailSummary")}</div>
              <pre className="subagents-panel__detailview-text">{summary}</pre>
            </div>
          ) : null}
          {previewReasoning ? (
            <div className="subagents-panel__detailview-block">
              <div className="subagents-panel__detailview-label">{t("subagent.preview.reasoning")}</div>
              <pre className="subagents-panel__detailview-text">{previewReasoning}</pre>
            </div>
          ) : null}
          {previewText ? (
            <div className="subagents-panel__detailview-block">
              <div className="subagents-panel__detailview-label">{t("subagent.preview.text")}</div>
              <pre className="subagents-panel__detailview-text">{previewText}</pre>
            </div>
          ) : null}
          {previewNotice ? (
            <div className="subagents-panel__detailview-block">
              <div className="subagents-panel__detailview-label">{t("subagent.preview.notice")}</div>
              <pre className="subagents-panel__detailview-text">{previewNotice}</pre>
            </div>
          ) : null}
          {progress?.truncated ? <div className="subagents-panel__detailview-note">{t("subagent.preview.truncated")}</div> : null}
          {!summary && !error && !hasPreview ? (
            <p className="subagents-panel__detailview-note">{t("subagentPanel.detailNoPreview")}</p>
          ) : null}
        </>
      )}
      <p className="subagents-panel__readonly">{t("subagentPanel.detailReadOnly")}</p>
    </div>
  );
}

export function SubagentsDockPanel({
  directory,
  detailEnabled = false,
  wide = false,
  onToggleWide,
  sessionPath,
  onReadSubagent,
  onListPersisted,
}: {
  directory: SubagentDirectory;
  /** Task 507 plan-A gate: row clicks open the read-only detail view. */
  detailEnabled?: boolean;
  /** Task 507 plan-C widen affordance state (session-local, App-owned). */
  wide?: boolean;
  /** Provided only where the dock width commands are wired; without it the
   *  toolbar is not rendered and the panel is byte-for-byte the 495 body. */
  onToggleWide?: (next: boolean) => void;
  /** 任务440: the active tab's session path — owner scope for the live
   *  transcript reads. Empty (no bound session) keeps every entry
   *  preview-only, exactly the 507 behavior. */
  sessionPath?: string;
  /** 任务440: read-only bridge (desktop ReadSubagentSession), injected by App
   *  with the same callback the capsule detail uses. Without it the live
   *  view stays off and rows render the preview blocks. */
  onReadSubagent?: (sessionPath: string, ref: string) => Promise<HistoryMessage[]>;
  /** 任务495 剩余项: the persisted ended directory (desktop
   *  ListSubagentsByParent — the sidecar records survive restart, compaction
   *  and archived outputs). Merged into the ended section; without it the
   *  panel stays the transcript-only projection (the 备用视图 passes nothing,
   *  byte-for-byte the 495 body there). */
  onListPersisted?: (sessionPath: string) => Promise<SubagentArtifactView[]>;
}) {
  const t = useT();
  // Task 495 ④: reveal the ended list 20 rows at a time (zcode's PAGE_SIZE).
  // The parent keys this panel per session, so a session switch remounts and
  // the reveal resets; live transcript updates must NOT reset it.
  const [revealed, setRevealed] = useState(SUBAGENT_DIRECTORY_PAGE_SIZE);
  // Task 507 plan A: the open detail's item id. Component state only — never
  // persisted, so a restart lands back on the list (语义诚实: the preview
  // content itself is memory-only).
  const [selectedId, setSelectedId] = useState<string | null>(null);
  // 任务495 剩余项: the persisted sidecar records for this session. Reloads
  // on session switch and whenever the transcript's ended count moves (a
  // just-finished child writes its sidecar at completion — the same refresh
  // shape the capsule directory uses). A failure simply yields no extras.
  const [persisted, setPersisted] = useState<readonly SubagentArtifactView[]>([]);
  const endedCountKey = directory.ended.length;
  useEffect(() => {
    if (!onListPersisted || !sessionPath) {
      setPersisted([]);
      return;
    }
    let cancelled = false;
    onListPersisted(sessionPath)
      .then((views) => {
        if (!cancelled) setPersisted(views);
      })
      .catch(() => {
        if (!cancelled) setPersisted([]);
      });
    return () => {
      cancelled = true;
    };
  }, [onListPersisted, sessionPath, endedCountKey]);
  // 任务495 剩余项: the ended section is transcript ∪ persisted, transcript
  // entries winning on ref collisions (they carry the richer live state).
  const mergedEnded = useMemo(
    () => mergeEndedSubagentRecords(directory, persisted),
    [directory, persisted],
  );
  const ended = mergedEnded.ended;
  const visibleEnded = ended.slice(0, revealed);
  const hiddenEnded = ended.length - visibleEnded.length;
  const empty = directory.running.length === 0 && ended.length === 0;
  const selected = selectedId
    ? directory.running.find((entry) => entry.item.id === selectedId)
      ?? ended.find((entry) => entry.item.id === selectedId)
    : undefined;
  const widenToolbar = onToggleWide ? (
    <div className="subagents-panel__toolbar">
      <button
        type="button"
        className="subagents-panel__wide"
        aria-pressed={wide}
        onClick={() => onToggleWide(!wide)}
      >
        {wide
          ? <FoldHorizontal size={12} aria-hidden="true" />
          : <UnfoldHorizontal size={12} aria-hidden="true" />}
        <span>{t(wide ? "subagentPanel.narrow" : "subagentPanel.widen")}</span>
      </button>
    </div>
  ) : null;
  if (empty) {
    return (
      <div className="subagents-panel" aria-label={t("workspace.subagentsTab")}>
        {/* 任务495 剩余项 (用法引导): the bare "no subagents" line could not
            answer "怎么用 / 只能看活跃么" — the guide line states where rows
            come from, the two sections, the click-to-expand interaction and
            restart persistence. Same faint-text class, no new CSS. */}
        <p className="subagents-panel__empty">{t("subagentPanel.empty")}</p>
        <p className="subagents-panel__empty">{t("subagentPanel.emptyGuide")}</p>
      </div>
    );
  }
  if (detailEnabled && selected) {
    return (
      <div className="subagents-panel" aria-label={t("workspace.subagentsTab")}>
        {widenToolbar}
        {/* 任务440: keyed per selection so the live transcript state resets
            cleanly when the user switches rows — no stale stream of the
            previous child bleeds into the next detail view. */}
        <SubagentDetailView
          key={selected.item.id}
          entry={selected}
          onBack={() => setSelectedId(null)}
          sessionPath={sessionPath}
          onReadSubagent={onReadSubagent}
        />
      </div>
    );
  }
  return (
    <div className="subagents-panel" aria-label={t("workspace.subagentsTab")}>
      {widenToolbar}
      <section className="subagents-panel__section">
        <h3 className="subagents-panel__section-title">
          {t("subagent.phase.running")} · {directory.running.length}
        </h3>
        {directory.running.length === 0 ? (
          <p className="subagents-panel__section-empty">{t("subagentPanel.runningEmpty")}</p>
        ) : (
          <ul className="subagents-panel__list">
            {directory.running.map((entry) => (
              <DirectoryRow
                key={entry.item.id}
                entry={entry}
                onSelect={detailEnabled ? () => setSelectedId(entry.item.id) : undefined}
              />
            ))}
          </ul>
        )}
      </section>
      <section className="subagents-panel__section">
        <h3 className="subagents-panel__section-title">
          {t("subagentPanel.ended")} · {ended.length}
        </h3>
        {visibleEnded.length === 0 ? (
          <p className="subagents-panel__section-empty">{t("subagentPanel.endedEmpty")}</p>
        ) : (
          <ul className="subagents-panel__list">
            {visibleEnded.map((entry) => (
              <DirectoryRow
                key={entry.item.id}
                entry={entry}
                onSelect={detailEnabled ? () => setSelectedId(entry.item.id) : undefined}
              />
            ))}
          </ul>
        )}
        {hiddenEnded > 0 && (
          <div className="subagents-panel__foot">
            <button type="button" className="btn btn--small" onClick={() => setRevealed((value) => value + SUBAGENT_DIRECTORY_PAGE_SIZE)}>
              {t("subagentPanel.showMore", { count: hiddenEnded })}
            </button>
          </div>
        )}
      </section>
    </div>
  );
}
