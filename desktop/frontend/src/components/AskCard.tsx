import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useT } from "../lib/i18n";
import { reportFrontendLog } from "../lib/frontendLog";
import { ChevronDown, ChevronLeft, ChevronRight, ChevronUp, X } from "lucide-react";
import type { QuestionAnswer, WireAsk, WireAskQuestion } from "../lib/types";
import {
  DecisionConfirmBar,
  PromptAction,
  PromptDescriptionDisclosure,
  PromptHeaderAction,
  PromptShelf,
} from "./PromptShelf";

const askDrafts = new Map<string, AskDraft>();
const askDraftKey = (scope: string, ask: WireAsk) =>
  JSON.stringify([scope, ask.runtimeEpoch ?? "", ask.turnId ?? "", ask.id]);
// 任务844: minimum spacing between panel-hover signals. One hop per second is
// far below the auto-decide window scale (default 15s) and keeps the bridge
// traffic negligible while still resetting the window long before it expires.
const askActivityThrottleMs = 1000;
type AnswerMode = "option" | "custom";
type AskDraft = {
  sel: Record<string, string[]>;
  custom: Record<string, string>;
  answerMode: Record<string, AnswerMode>;
  active: number;
  selectedIndex: number;
};

function readAskDraft(key: string): AskDraft | undefined {
  const draft = askDrafts.get(key);
  return draft ? { ...draft, sel: { ...draft.sel }, custom: { ...draft.custom }, answerMode: { ...draft.answerMode } } : undefined;
}

function clearAskDraft(key: string): void {
  askDrafts.delete(key);
}

// AskCard renders the `ask` tool as a decision shelf near the composer. It
// walks multi-question asks one at a time. Single-select choices advance to
// the next question immediately; multi-select and custom answers wait for an
// explicit confirm, and the final question still requires submission.
type AskCardProps = {
  ask: WireAsk;
  onAnswer: (id: string, answers: QuestionAnswer[]) => void | Promise<void>;
  onDismiss?: () => void | Promise<void>;
  draftScope?: string;
  onStop: () => void;
  // 任务806 要素①: fired once, on the user's first selection or typed edit on
  // the panel. The backend stops the autopilot auto-decide timer on it, so the
  // run cannot take the decision away mid-choice. Optional: surfaces without a
  // local bridge (remote sessions) simply never fire it and keep the plain
  // wait/timeout behavior.
  onInteract?: (id: string) => void;
  // 任务844: fired while the pointer moves over the panel area. The backend
  // resets (not stops) the autopilot auto-decide window on it — "the user is
  // reading" — bounded by the backend's absolute hover budget. Optional like
  // onInteract: remote surfaces keep the plain wait/timeout behavior.
  onActivity?: (id: string) => void;
  // 任务849: fired on the takeover toggle. true parks the backend auto-decide
  // (timer and absolute hover budget suspended; the ask waits indefinitely);
  // false releases it and re-arms a fresh window. Resolves false when no
  // timed ask consumed the toggle, which rolls the local button state back —
  // the panel must never show a takeover the backend is not honoring.
  onTakeover?: (id: string, taking: boolean) => Promise<boolean>;
  // 任务849: the autopilot auto-decide window in ms (>0 arms the countdown
  // display and the takeover button). The App mirrors the backend's
  // autopilotAskWaitFor from the tab's collaboration mode plus the task-477
  // settings snapshot; 0/undefined keeps the plain panel (non-autopilot tabs,
  // settings unknown, remote surfaces without a local bridge).
  countdownMs?: number;
};

export function AskCard(props: AskCardProps) {
  const draftKey = askDraftKey(props.draftScope ?? "default", props.ask);
  // The same identity owns both cached drafts and live component state. A
  // controller can reuse prompt IDs, so changing runtime or turn remounts it.
  return <AskCardBody key={draftKey} {...props} draftKey={draftKey} />;
}

function AskCardBody({ ask, onAnswer, onInteract, onActivity, onTakeover, countdownMs, onStop, draftKey }: AskCardProps & { draftKey: string }) {
  const t = useT();
  // Per-question state: selected option labels, and an optional typed answer.
  const [sel, setSel] = useState<Record<string, string[]>>(() => readAskDraft(draftKey)?.sel ?? {});
  const [custom, setCustom] = useState<Record<string, string>>(() => readAskDraft(draftKey)?.custom ?? {});
  const [answerMode, setAnswerMode] = useState<Record<string, AnswerMode>>(() => readAskDraft(draftKey)?.answerMode ?? {});
  const [customOpen, setCustomOpen] = useState(false);
  const [active, setActive] = useState(() => readAskDraft(draftKey)?.active ?? 0);
  // Extra decision row after option labels: custom answer. Skip is a
  // secondary footer action rather than an answer choice.
  const [selectedIndex, setSelectedIndex] = useState(() => readAskDraft(draftKey)?.selectedIndex ?? 0);
  const [expandedDescriptionId, setExpandedDescriptionId] = useState<string | null>(null);
  const [descriptionTruncated, setDescriptionTruncated] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  // A newly delivered Ask always starts expanded, matching harness. Collapse
  // is presentation state and must not leak from an earlier request.
  const [collapsed, setCollapsed] = useState(false);
  const shelfRef = useRef<HTMLDivElement | null>(null);
  const customInputRef = useRef<HTMLInputElement | null>(null);
  const initializedActiveRef = useRef(false);
  const interactedRef = useRef(false);
  const instanceId = useId();

  // 任务806 要素①: idempotent — the backend consumes the first touch only,
  // so one fire per panel is enough and keeps the bridge traffic at zero for
  // panels nobody has touched. The state twin drives the UI: once the user
  // started choosing, the backend has stopped the timer, so the countdown and
  // the takeover button step aside for an attended wait.
  const [interacted, setInteracted] = useState(false);
  const fireInteract = () => {
    if (interactedRef.current) return;
    interactedRef.current = true;
    setInteracted(true);
    try {
      onInteract?.(ask.id);
    } catch {
      // The signal is best effort; the auto-decide timer firing early is the
      // pre-806 behavior, never a crash.
    }
  };

  // 任务849: the takeover toggle — the explicit "I am here, stop the clock"
  // declaration, one step stronger than hover (844). Held, the panel shows a
  // settled "no auto-answer" badge instead of a ticking countdown; released,
  // the countdown restarts from the full window. Optimistic with rollback:
  // a false reply (attended/high-risk ask, unknown/expired ID) un-renders a
  // takeover the backend is not honoring.
  const takeoverArmed = (countdownMs ?? 0) > 0 && Boolean(onTakeover);
  const [takeoverHeld, setTakeoverHeld] = useState(false);
  const [takeoverPending, setTakeoverPending] = useState(false);
  const toggleTakeover = () => {
    if (!onTakeover || takeoverPending) return;
    const next = !takeoverHeld;
    setTakeoverPending(true);
    setTakeoverHeld(next);
    void onTakeover(ask.id, next)
      .then((applied) => {
        if (!applied) setTakeoverHeld(!next);
      })
      .catch(() => setTakeoverHeld(!next))
      .finally(() => setTakeoverPending(false));
  };

  // 任务849: the local countdown over the auto-decide window. Rendered from a
  // local timer — no event schema change, no per-second backend traffic (the
  // concern that shelved the visual countdown in 844 was exactly the backend
  // pushed-event variant). The backend clock stays authoritative: this display
  // is advisory, and its window start simply mirrors where the backend's
  // timer begins (the panel mounts a beat after the Ask registration, so the
  // last shown second can overshoot the real expiry by a beat). mousemove
  // re-arms both clocks — the throttled hop below resets the backend window,
  // and the same tick resets the local deadline, so the two stay in step.
  const countdownLive = takeoverArmed && !takeoverHeld && !interacted;
  const [countdownDeadline, setCountdownDeadline] = useState(() => Date.now() + (countdownMs ?? 0));
  const [countdownNow, setCountdownNow] = useState(() => Date.now());
  useEffect(() => {
    if (!countdownLive) return;
    const timer = window.setInterval(() => setCountdownNow(Date.now()), 250);
    return () => window.clearInterval(timer);
  }, [countdownLive]);
  const countdownSeconds = countdownLive ? Math.max(0, Math.ceil((countdownDeadline - countdownNow) / 1000)) : null;

  // 任务844: pointer movement over the panel resets the auto-decide window on
  // the backend ("the user is reading"). mousemove fires at pointer-poll rate,
  // so the signal is throttled well below the window scale — one hop per
  // second is plenty for a 15s window and keeps the bridge quiet. 任务849:
  // the same tick re-arms the local countdown, and while a takeover is held
  // the clock is parked on the backend, so neither the hop nor the local
  // reset happens.
  const lastActivityRef = useRef(0);
  const fireActivity = () => {
    if (takeoverArmed && (takeoverHeld || interacted)) return;
    const now = Date.now();
    if (now - lastActivityRef.current < askActivityThrottleMs) return;
    lastActivityRef.current = now;
    if (countdownLive) {
      setCountdownDeadline(now + (countdownMs ?? 0));
      setCountdownNow(now);
    }
    try {
      onActivity?.(ask.id);
    } catch {
      // Best effort like onInteract: a lost hover only means the window keeps
      // the plain countdown, never a crash.
    }
  };

  const questions = ask.questions;
  const q = questions[Math.min(active, questions.length - 1)];
  const isLast = active >= questions.length - 1;
  const progress = `${Math.min(active + 1, questions.length)}/${questions.length}`;
  const hasMultipleQuestions = questions.length > 1;

  // Row layout: [options...] [custom]
  const optionCount = q?.options.length ?? 0;
  const customRowIndex = optionCount;
  const rowCount = optionCount + 1;
  const selectedOption = selectedIndex >= 0 && selectedIndex < optionCount
    ? q?.options[selectedIndex]
    : undefined;
  const selectedDescriptionId = selectedOption
    ? `${instanceId}-description-${selectedIndex}`
    : undefined;
  const descriptionExpanded = selectedDescriptionId !== undefined && expandedDescriptionId === selectedDescriptionId;

  useEffect(() => {
    // 任务461-P16: the mount is the last link of the ask trail — with the Go
    // emit line and the reducer receipt line this pinpoints whether a
    // "no popup" report died before the event, in the reducer, or at render.
    reportFrontendLog("ask-panel", "ask card mounted", `ask=${ask.id}`);
    shelfRef.current?.focus();
  }, []);

  useEffect(() => {
    try {
      askDrafts.set(draftKey, { sel: { ...sel }, custom: { ...custom }, answerMode: { ...answerMode }, active, selectedIndex });
    } catch {
      // Draft storage is best effort; the live pending ask remains authoritative.
    }
  }, [active, answerMode, custom, draftKey, selectedIndex, sel]);

  useEffect(() => {
    setCustomOpen(false);
    if (initializedActiveRef.current) setSelectedIndex(0);
    initializedActiveRef.current = true;
  }, [active]);

  useEffect(() => {
    setExpandedDescriptionId(null);
  }, [active, ask.id]);

  useEffect(() => {
    if (customOpen) customInputRef.current?.focus();
  }, [customOpen]);

  const answersFrom = (
    nextSel: Record<string, string[]> = sel,
    nextCustom: Record<string, string> = custom,
  ): QuestionAnswer[] =>
    questions.map((question) => ({
      questionId: question.id,
      selected: answerMode[question.id] === "custom" && nextCustom[question.id]?.trim()
        ? [nextCustom[question.id].trim()]
        : (nextSel[question.id] ?? []),
    }));

  const answerLabel = (question: WireAskQuestion) => {
    if (answerMode[question.id] === "custom") return custom[question.id]?.trim() ?? "";
    return (sel[question.id] ?? []).join(", ");
  };

  const answered = (question: WireAskQuestion) => answerMode[question.id] === "custom"
    ? (custom[question.id]?.trim() ?? "") !== ""
    : (sel[question.id]?.length ?? 0) > 0;

  const currentAnswered = q ? answered(q) : false;

  const submitAction = (action: () => void | Promise<void>) => {
    if (submitting) return;
    setSubmitting(true);
    void Promise.resolve()
      .then(action)
      .catch(() => setSubmitting(false));
  };

  const finishOrAdvance = (nextSel = sel, nextCustom = custom) => {
    if (submitting) return;
    if (isLast) {
      submitAction(() => Promise.resolve(onAnswer(ask.id, answersFrom(nextSel, nextCustom))).then(() => {
        clearAskDraft(draftKey);
      }));
      return;
    }
    setActive((i) => Math.min(i + 1, questions.length - 1));
  };

  const toggleOption = (question: WireAskQuestion, label: string) => {
    if (submitting) return;
    const cur = sel[question.id] ?? [];
    const nextSel = question.multi
      ? { ...sel, [question.id]: cur.includes(label) ? cur.filter((x) => x !== label) : [...cur, label] }
      : { ...sel, [question.id]: [label] };

    setAnswerMode((m) => ({ ...m, [question.id]: "option" }));
    setSel(nextSel);
    setCustomOpen(false);
  };

  const setTyped = (question: WireAskQuestion, text: string) => {
    fireInteract();
    setSelectedIndex(customRowIndex);
    setAnswerMode((m) => ({ ...m, [question.id]: "custom" }));
    setCustom((c) => ({ ...c, [question.id]: text }));
    if (text.trim()) setSel((s) => ({ ...s, [question.id]: [] }));
  };

  const goBack = () => {
    if (submitting) return;
    setActive((i) => Math.max(0, i - 1));
  };

  const stopAsk = () => {
    clearAskDraft(draftKey);
    onStop();
  };

  const skipCurrentQuestion = () => {
    if (submitting || !q) return;
    const nextSel = { ...sel, [q.id]: [] };
    const nextCustom = { ...custom, [q.id]: "" };
    setSel(nextSel);
    setCustom(nextCustom);
    setCustomOpen(false);
    if (!isLast) {
      setActive((i) => i + 1);
      return;
    }
    submitAction(() => Promise.resolve(onAnswer(ask.id, answersFrom(nextSel, nextCustom))).then(() => {
      clearAskDraft(draftKey);
    }));
  };

  const selectRow = (index: number) => {
    if (submitting || !q) return;
    fireInteract();
    setSelectedIndex(index);
    if (index < optionCount) {
      const option = q.options[index];
      if (!option) return;
      if (q.multi) {
        toggleOption(q, option.label);
      } else {
        // Single-select follows harness behavior: choose and advance, while
        // keeping the answer in the draft so Back can revise it.
        setAnswerMode((m) => ({ ...m, [q.id]: "option" }));
        setSel((s) => ({ ...s, [q.id]: [option.label] }));
        setCustomOpen(false);
        if (active < questions.length - 1) setActive((i) => i + 1);
      }
    } else if (index === customRowIndex) {
      // Opening custom clears option picks for this question.
      setAnswerMode((m) => ({ ...m, [q.id]: "custom" }));
      setCustomOpen(true);
      setSel((s) => ({ ...s, [q.id]: [] }));
    }
  };

  const canConfirm = (): boolean => {
    if (!q || submitting) return false;
    if (selectedIndex === customRowIndex) {
      return Boolean(custom[q.id]?.trim());
    }
    // Multi-select: answers come from checked options / typed custom, not the
    // keyboard cursor alone.
    if (q.multi) return currentAnswered;
    // Single-select: the keyboard cursor is authoritative for option rows so
    // initial Enter and ArrowDown+Enter work without a prior click.
    if (selectedIndex >= 0 && selectedIndex < optionCount) return true;
    return (sel[q.id]?.length ?? 0) > 0;
  };

  const confirmSelected = () => {
    if (!q || submitting || !canConfirm()) return;
    if (selectedIndex === customRowIndex) {
      finishOrAdvance();
      return;
    }
    // Ensure the highlighted option is reflected for single-select.
    if (!q.multi && selectedIndex < optionCount) {
      const option = q.options[selectedIndex];
      if (option) {
        const nextSel = { ...sel, [q.id]: [option.label] };
        const nextCustom = { ...custom, [q.id]: "" };
        setSel(nextSel);
        setCustom(nextCustom);
        finishOrAdvance(nextSel, nextCustom);
        return;
      }
    }
    finishOrAdvance();
  };

  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (submitting || !q) return;
      const target = event.target instanceof Element ? event.target : null;
      const tag = target?.tagName.toLowerCase();
      if (tag === "input" || tag === "textarea" || (target instanceof HTMLElement && target.isContentEditable)) return;

      if (event.key === "Escape") {
        event.preventDefault();
        stopAsk();
        return;
      }
      if (event.key === "ArrowUp") {
        event.preventDefault();
        setSelectedIndex((i) => (i - 1 + rowCount) % rowCount);
        return;
      }
      if (event.key === "ArrowDown") {
        event.preventDefault();
        setSelectedIndex((i) => (i + 1) % rowCount);
        return;
      }
      if (event.key === "Enter") {
        event.preventDefault();
        confirmSelected();
        return;
      }
      if ((event.key === "ArrowLeft" || event.key === "Backspace") && active > 0) {
        event.preventDefault();
        goBack();
        return;
      }

      const index = Number(event.key) - 1;
      if (!Number.isInteger(index) || index < 0 || index >= optionCount) return;
      event.preventDefault();
      selectRow(index);
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  });

  const answeredSummary = useMemo(
    () =>
      questions
        .slice(0, active)
        .map((question) => answerLabel(question))
        .filter(Boolean),
    [active, custom, questions, sel],
  );

  if (!q) return null;

  const confirmLabel = isLast
      ? t("common.submit")
      : t("ask.next");

  return (
    <PromptShelf
      decision
      className="prompt-shelf--ask"
      cardCollapsible
      collapsed={collapsed}
      onToggleCollapse={() => setCollapsed((value) => !value)}
      onMouseMove={fireActivity}
      barRef={shelfRef}
      titleId="ask-shelf-title"
      title={q.header ?? t("ask.title")}
      badges={
        <span className="ask-shelf__header-meta">
          {hasMultipleQuestions && (
            <span className="ask-shelf__header-text ask-shelf__header-text--progress">
              {t("ask.questionProgress", { progress })}
            </span>
          )}
          {countdownSeconds !== null && (
            <span className="ask-shelf__header-text ask-shelf__header-text--progress">
              {t("ask.autoDecideCountdown", { seconds: countdownSeconds })}
            </span>
          )}
          {takeoverArmed && takeoverHeld && (
            <span className="ask-shelf__header-text">{t("ask.takeoverHeld")}</span>
          )}
        </span>
      }
      meta={q.prompt}
      headerActions={
        <>
          {takeoverArmed && !interacted && (
            <PromptHeaderAction
              onClick={toggleTakeover}
              ariaLabel={takeoverHeld ? t("ask.releaseTakeover") : t("ask.takeover")}
              disabled={takeoverPending}
            >
              {takeoverHeld ? t("ask.releaseTakeover") : t("ask.takeover")}
            </PromptHeaderAction>
          )}
          <PromptHeaderAction
            onClick={() => setCollapsed((value) => !value)}
            ariaLabel={collapsed ? t("common.expand") : t("common.collapse")}
            disabled={submitting}
          >
            {collapsed ? <ChevronUp size={15} aria-hidden="true" /> : <ChevronDown size={15} aria-hidden="true" />}
          </PromptHeaderAction>
          <PromptHeaderAction onClick={stopAsk} ariaLabel={t("decision.stopTask")} disabled={submitting}>
            <X size={16} aria-hidden="true" />
          </PromptHeaderAction>
        </>
      }
      actions={
        <>
          {q.options.map((o, index) => {
            const on = (sel[q.id] ?? []).includes(o.label);
            const cursor = selectedIndex === index;
            return (
              <PromptAction
                key={o.label}
                actionId={`${instanceId}-row-${index}`}
                keyLabel={q.options.length <= 9 ? String(index + 1) : ""}
                label={o.label}
                description={o.description}
                descriptionId={`${instanceId}-description-${index}`}
                descriptionDisclosure
                onDescriptionOverflowChange={selectedIndex === index ? setDescriptionTruncated : undefined}
                onClick={() => selectRow(index)}
                // Single-select: cursor owns selection. Multi-select: selected
                // means checked; active is the keyboard cursor only.
                selected={q.multi ? on : cursor}
                active={q.multi ? cursor : false}
                disabled={submitting}
              />
            );
          })}
          <div
            className={`ask-shelf__custom-row${custom[q.id]?.trim() ? " ask-shelf__custom-row--active" : ""}`}
            role="group"
            onClick={(event) => {
              // The card toggles collapse on clicks outside its controls; this row is a control.
              event.stopPropagation();
              fireInteract();
              setSelectedIndex(customRowIndex);
              setAnswerMode((m) => ({ ...m, [q.id]: "custom" }));
              setCustomOpen(true);
              customInputRef.current?.focus();
            }}
          >
            <span className="ask-shelf__custom-indicator" aria-hidden="true">✎</span>
            <input
              ref={customInputRef}
              className="ask-shelf__custom"
              aria-label={t("ask.customAnswer")}
              placeholder={t("ask.customPlaceholder")}
              value={answerMode[q.id] === "custom" ? custom[q.id] ?? "" : ""}
              disabled={submitting}
              onFocus={() => {
                fireInteract();
                setSelectedIndex(customRowIndex);
              }}
              onChange={(e) => setTyped(q, e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && canConfirm()) {
                  e.preventDefault();
                  confirmSelected();
                }
                e.stopPropagation();
              }}
            />
          </div>
        </>
      }
      crumbs={
        answeredSummary.length > 0 && (
          <div className="ask-shelf__crumbs">
            {answeredSummary.map((answer, index) => (
              <span className="ask-shelf__crumb" key={`${index}-${answer}`}>
                {index + 1}. {answer}
              </span>
            ))}
          </div>
        )
      }
      note={
        <>
          {selectedDescriptionId && descriptionTruncated && (
            <PromptDescriptionDisclosure
              descriptionId={`${selectedDescriptionId}-detail`}
              label={selectedOption?.label}
              description={selectedOption?.description}
              expanded={descriptionExpanded}
              onToggle={() => setExpandedDescriptionId((current) => current === selectedDescriptionId ? null : selectedDescriptionId)}
              disabled={submitting}
            />
          )}
        </>
      }
      footer={
        <div className="ask-shelf__footer-layout">
          <div className="ask-shelf__pager" aria-label={t("ask.questionProgress", { progress })}>
            <button type="button" className="ask-shelf__pager-button" aria-label={t("ask.back")} disabled={active === 0 || submitting} onClick={goBack}>
              <ChevronLeft size={16} aria-hidden="true" />
            </button>
            <span>{progress}</span>
            <button type="button" className="ask-shelf__pager-button" aria-label={t("ask.next")} disabled={isLast || submitting} onClick={() => setActive((i) => Math.min(i + 1, questions.length - 1))}>
              <ChevronRight size={16} aria-hidden="true" />
            </button>
          </div>
          <DecisionConfirmBar
            hint={t("decision.selectHint")}
            confirmLabel={confirmLabel}
            onConfirm={confirmSelected}
            secondaryLabel={t("ask.skipQuestion")}
            onSecondary={skipCurrentQuestion}
            disabled={submitting}
            confirmDisabled={!canConfirm()}
          />
        </div>
      }
    />
  );
}
