// Small transcript row cards: phase lines, steer bubbles, notice cards with
// decision receipts, and compaction cards.

import { useCallback, useMemo, useState } from "react";
import { CheckCheck, ChevronDown, ChevronRight, CirclePlay, ClipboardCheck, FileSearch, GitBranch, Info, MessageSquare, TriangleAlert } from "lucide-react";
import { useT } from "../lib/i18n";
import type { CompactionItem, NoticeItem } from "../lib/transcriptRows";
import type { WireCompletionSummary } from "../lib/types";
import { turnChangeText, turnCheckState, turnCheckText } from "../lib/turnResult";
import { TurnResultSummary } from "./TurnResultSummary";
import { TurnEditList } from "./TurnEditList";
import { STEER_NOTICE_PREFIX } from "../lib/useController";
import { collabAsImSource, type CollabImSourceMessage } from "../lib/collabMessage";
import { collabDisplayLabel, useCollabContactNames } from "../lib/collabContactNames";
import { ProcessCompactIcon, ProcessPhaseIcon } from "./ProcessCard";
import { useTranscriptUserResizeIntent } from "./TranscriptLayoutIntentContext";

export function PhaseCard({ id, text }: { id: string; text: string }) {
  return <div className="phase" data-entrance={id}><ProcessPhaseIcon size={12} /><span>{text}</span></div>;
}

// 任务462: contact_id → 会话名 的显示解析（缓存有名字用名字，查不到降级
// 截短 id，绝不空白；完整 id 留在 hover）。758 的 steer 卡片与 user row 卡
// 共用同一解析管线。
function useCollabLabel(): (contactId: string) => string {
  const contactNames = useCollabContactNames();
  return useCallback(
    (contactId: string) => contactNames.get(contactId.trim()) || collabDisplayLabel(contactId),
    [contactNames],
  );
}

// 任务758: a cross-session delivery renders the same im-source card whichever
// entity carried it into the transcript (user row in Message.tsx, ↪ steer
// notice here) — one delivery text, one presentation. The ↪ mark rides the
// card head so the steer provenance stays visible; hover keeps the raw
// contact_ids (same contract as the user-row card).
function SteerCollabCard({ source }: { source: CollabImSourceMessage }) {
  const t = useT();
  const collabLabel = useCollabLabel();
  return (
    <div className="im-source-card im-source-card--steer">
      <div className="im-source-card__head" data-transcript-selection-ignore>
        <MessageSquare size={14} />
        <span>{t("msg.fromIm", { source: t("msg.fromCollab") })}</span>
        <span className="im-source-card__steer-mark" title={t("transcript.steer")} aria-hidden="true">↪</span>
      </div>
      {source.text && <div className="im-source-card__text">{source.text}</div>}
      {(source.sender || source.chat) && (
        <div
          className="im-source-card__meta"
          data-transcript-selection-ignore
          title={[source.sender, source.chat].filter(Boolean).map((id) => `contact_id=${id.trim()}`).join(" → ")}
        >
          <span>{t("msg.collabRoute", { from: collabLabel(source.sender), to: collabLabel(source.chat) })}</span>
        </div>
      )}
    </div>
  );
}

// 任务758: the deferred placeholder's one-line summary for a collab body —
// resolved route labels only, the raw `[跨会话消息] 来自 contact_id=…` header
// (and any hop/metadata tail) stays out of the collapsed row.
function SteerCollabRouteSummary({ source }: { source: CollabImSourceMessage }) {
  const t = useT();
  const collabLabel = useCollabLabel();
  return <>{t("msg.collabRoute", { from: collabLabel(source.sender), to: collabLabel(source.chat) })}</>;
}

// A mid-turn steer is the user's own message, so it renders on the user side
// of the transcript instead of disappearing into the work fold.
//
// 任务723: when the same inboxItemId later arrives as a NEW turn's user_input
// (the steer was injected-but-unconsumed / rejected-to-followup and the
// dispatch pump admitted it as its own turn), the bubble downgrades to a
// collapsed placeholder — the tail user row is the entity, this row only
// marks where the message was first received. Collapsed shows the status
// label plus the body's first line; expanding reveals the full text. The
// dedup key is the inboxItemId carried on the item, never the text.
//
// 任务758: a body in the cross-session delivery shape (collabAsImSource hit)
// cardifies instead of rendering the raw text bubble — the exact same card a
// user row shows for the same message. The deferred placeholder keeps the 723
// semantics: collapsed route summary (no bare contact_id), expanding reveals
// the card below the toggle.
export function SteerCard({ id, text, deferred = false }: { id: string; text: string; deferred?: boolean }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const body = text.startsWith(STEER_NOTICE_PREFIX) ? text.slice(STEER_NOTICE_PREFIX.length) : text;
  const collab = useMemo(() => collabAsImSource(body), [body]);
  if (!deferred) {
    if (collab) {
      return (
        <div className="steer-line" data-entrance={id}>
          <SteerCollabCard source={collab} />
        </div>
      );
    }
    return (
      <div className="steer-line" data-entrance={id}>
        <div className="steer-line__bubble" title={t("transcript.steer")}>
          <span className="steer-line__icon" aria-hidden="true">↪</span>
          <span className="steer-line__text">{body}</span>
        </div>
      </div>
    );
  }
  const firstLine = body.split("\n", 1)[0] ?? "";
  return (
    <div className="steer-line" data-entrance={id}>
      <button
        type="button"
        className={`steer-line__bubble steer-line__bubble--deferred${expanded ? " steer-line__bubble--open" : ""}`}
        title={t("transcript.steerDeferred")}
        aria-expanded={expanded}
        onClick={() => setExpanded((open) => !open)}
      >
        <span className="steer-line__icon" aria-hidden="true">↪</span>
        <span className="steer-line__status">{t("transcript.steerDeferred")}</span>
        <span className={`steer-line__text${collab || !expanded ? " steer-line__text--summary" : ""}`}>
          {collab ? <SteerCollabRouteSummary source={collab} /> : expanded ? body : firstLine}
        </span>
        <ChevronRight size={12} className="steer-line__chevron" aria-hidden="true" />
      </button>
      {expanded && collab && <SteerCollabCard source={collab} />}
    </div>
  );
}

function DecisionReceiptLine({ receipt }: { receipt: NonNullable<NoticeItem["decisionReceipt"]> }) {
  const t = useT();
  const titleKey = receipt.kind === "ask"
    ? "notice.decisionReceiptAsk"
    : receipt.kind === "plan"
    ? "notice.decisionReceiptPlan"
    : receipt.kind === "recovery"
    ? "notice.decisionReceiptRecovery"
    : "notice.decisionReceiptTool";
  const outcomeKeys: Record<string, string> = {
    allow_once: "notice.decisionAllowOnce",
    allow_session: "notice.decisionAllowSession",
    allow_persistent: "notice.decisionAllowPersistent",
    deny: "notice.decisionDeny",
    start_execution: "notice.decisionStartExecution",
    revise_plan: "notice.decisionRevisePlan",
    exit_plan: "notice.decisionExitPlan",
    recovery_continue: "notice.decisionRecoveryContinue",
    recovery_continue_task: "notice.decisionRecoveryContinueTask",
    recovery_revise: "notice.decisionRecoveryRevise",
    answered: "notice.decisionAnswered",
  };
  const outcome = outcomeKeys[receipt.outcome]
    ? t(outcomeKeys[receipt.outcome] as never)
    : receipt.outcome || t("notice.decisionReceiptTitle");
  const showOutcome = receipt.kind !== "ask" || receipt.outcome !== "answered";
  return (
    <div className="notice-line__decision-receipt">
      <span className="notice-line__decision-title">{t(titleKey as never)}</span>
      {showOutcome && <span className="notice-line__decision-outcome">{outcome}</span>}
      {receipt.tool && <code>{receipt.tool}</code>}
      {receipt.subject && <span className="notice-line__decision-subject">{receipt.subject}</span>}
    </div>
  );
}

export function NoticeCard({ item, onAction, onAccept, onOpenVerification, onUndoCode, actionDisabled = false }: { item: NoticeItem; onAction?: () => void; onAccept?: () => void; onOpenVerification?: (summary: WireCompletionSummary) => void; onUndoCode?: (turn: number) => void; actionDisabled?: boolean }) {
  const t = useT();
  const StatusIcon = item.level === "warn" ? TriangleAlert : Info;
  const ActionIcon = item.action === "view_versions" ? GitBranch : item.action === "open_changes" ? FileSearch : CirclePlay;
  const showVerification = item.variant === "completion" && Boolean(item.completionSummary && onOpenVerification);
  const result = item.variant === "completion" ? item.completionSummary : undefined;
  // 任务524: the turn-result notice must not squat on the conversation
  // viewport. It defaults to a one-line summary (title + files/± + checks);
  // expanding is an explicit click, the verdict actions (undo/review) stay
  // hidden while collapsed, and a newer result (different key) starts
  // collapsed again.
  const resultKey = result ? `${result.turnId ?? ""}:${result.checkpointTurn ?? ""}:${result.receipt?.diff?.id ?? ""}` : "";
  const [expandedKey, setExpandedKey] = useState<string | null>(null);
  const resultExpanded = Boolean(result) && expandedKey === resultKey;
  const showActions = Boolean((item.action && onAction) || onAccept || showVerification);
  const turn = result?.checkpointTurn;
  const actionsRow = showActions ? (
    <div className="notice-line__actions">
      {item.action && onAction ? (
        <button className="btn btn--primary btn--small" type="button" onClick={onAction} disabled={actionDisabled}>
          <ActionIcon size={13} aria-hidden="true" />
          <span>{item.action === "manual_continue" ? t("notice.manualContinue") : item.action === "recover_context" ? t("notice.protocolRecoveryAction") : item.action === "open_changes" ? t("notice.completionViewChanges") : item.action === "view_versions" ? t("recovery.inspectLineage") : t("notice.deliveryIncompleteContinue")}</span>
        </button>
      ) : null}
      {showVerification ? (
        <button className="btn btn--secondary btn--small" type="button" onClick={() => item.completionSummary && onOpenVerification?.(item.completionSummary)}>
          <ClipboardCheck size={13} aria-hidden="true" />
          <span>{t("notice.completionViewVerification")}</span>
        </button>
      ) : null}
      {onAccept ? (
        <button className="btn btn--primary btn--small" type="button" onClick={onAccept}>
          <CheckCheck size={13} aria-hidden="true" />
          <span>{t("notice.deliveryIncompleteAccept")}</span>
        </button>
      ) : null}
    </div>
  ) : null;
  return (
    <div className={`notice-line notice-line--${item.level}${item.variant ? ` notice-line--${item.variant}` : ""}`} data-entrance={item.id} role={item.code === "incomplete_read" ? "status" : undefined}>
      {!result && <StatusIcon className="notice-line__icon" size={14} aria-hidden="true" />}
      <div className="notice-line__text">
        {result ? (
          <>
            <button type="button" className="notice-line__summary-toggle" aria-expanded={resultExpanded} onClick={() => setExpandedKey(resultExpanded ? null : resultKey)}>
              {resultExpanded ? <ChevronDown className="notice-line__chevron" size={14} aria-hidden="true" /> : <ChevronRight className="notice-line__chevron" size={14} aria-hidden="true" />}
              <span className="notice-line__title">{t("notice.completionChangesTitle")}</span>
              <span className="notice-line__summary-change">{turnChangeText(result, t)}</span>
              <span className={`notice-line__summary-checks turn-result-summary__checks--${turnCheckState(result).status}`}>{turnCheckText(result, t)}</span>
            </button>
            {resultExpanded && (
              <div className="notice-line__expanded">
                <TurnResultSummary summary={result} />
                {result.receipt?.diff && result.receipt.diff.files.length > 0 && (
                  <TurnEditList
                    diff={result.receipt.diff}
                    turn={turn}
                    onReview={onAction || (showVerification ? () => result && onOpenVerification?.(result) : undefined)}
                    onUndoCode={onUndoCode}
                    undoDisabled={actionDisabled}
                  />
                )}
                {actionsRow}
              </div>
            )}
          </>
        ) : item.decisionReceipt ? (
          <DecisionReceiptLine receipt={item.decisionReceipt} />
        ) : (
          <>
            {item.title ? <div className="notice-line__title">{item.title}</div> : null}
            <div className="notice-line__body">{item.text}</div>
          </>
        )}
        {!result && actionsRow}
        {item.detail ? (
          <details className="notice-line__details">
            <summary>{t("notice.details")}</summary>
            <div>{item.detail}</div>
          </details>
        ) : null}
      </div>
    </div>
  );
}

export function CompactionCard({ item }: { item: CompactionItem }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const beginUserResize = useTranscriptUserResizeIntent();
  if (item.pending) {
    const label = (item.total ?? 0) > 0 ? t("compaction.progress", { done: item.done ?? 0, total: (item.total ?? 0) }) : t("compaction.working");
    return <div className="compaction compaction--pending" data-entrance={item.id} data-transcript-layout-variant="static"><ProcessCompactIcon size={12} /><span>{label}</span></div>;
  }
  return (
    <div className="compaction" data-entrance={item.id} data-transcript-layout-variant={open ? "compaction-expanded" : "compaction-collapsed"}>
      <button type="button" className="compaction__head" onClick={() => { beginUserResize(); setOpen((v) => !v); }} aria-expanded={open}>
        <ProcessCompactIcon size={12} />
        <span>{t("compaction.title")}</span>
        <span className="compaction__meta">{t("compaction.messages", { n: item.messages })}{item.trigger ? ` · ${item.trigger}` : ""}</span>
        <ChevronRight className={open ? "compaction__chevron--open" : ""} size={12} />
      </button>
      {open && <pre className="compaction__body">{item.summary}</pre>}
    </div>
  );
}
