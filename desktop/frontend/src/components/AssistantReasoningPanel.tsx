import { useEffect, useRef, useState } from "react";
import { ChevronRight } from "lucide-react";
import { useWorkProcessPresentation } from "../lib/sessionExperience";
import { useCollapseAnimation } from "../lib/useCollapseAnimation";
import { useT } from "../lib/i18n";
import type { Item } from "../lib/useController";
import { Markdown } from "./Markdown";
import { ProcessBrainIcon } from "./ProcessCard";
import { ReasoningSummary } from "./ReasoningSummary";
import { StreamingReasoningText } from "./StreamingReasoningText";

type AssistantItem = Extract<Item, { kind: "assistant" }>;

function reasoningDurationLabel(durationMs: number | undefined, t: ReturnType<typeof useT>): string {
  if (typeof durationMs !== "number" || !Number.isFinite(durationMs) || durationMs <= 0) return t("msg.thinkingDone");
  return t("msg.thinkingDuration", { s: Math.max(1, Math.round(durationMs / 1000)) });
}

export function AssistantReasoningPanel({
  item,
  defaultExpanded,
  explicitFold,
  onExplicitFoldChange,
}: {
  item: AssistantItem;
  defaultExpanded: boolean;
  /** 任务 765: the user's persisted fold intent for this panel, if any.
   * Outranks every derived initial state and survives remounts. */
  explicitFold?: boolean;
  onExplicitFoldChange?: (open: boolean) => void;
}) {
  const t = useT();
  const presentation = useWorkProcessPresentation();
  const running = item.streaming && !item.reasoningComplete;
  // Task 753 (R4 hardening): there is no caller-side bypass anymore. The old
  // `showWhileStreaming || expandWhileStreaming` let any future caller silently
  // override the concise tier; production always passed false (dead path with
  // live foot-gun potential), so the prop is gone and concise semantics are
  // decided by the presentation alone.
  const followsWhileStreaming = presentation.showWhileRunning;
  const keepExpanded = presentation.keepExpandedAfterCompletion;
  // A caller-hinted defaultExpanded must not outrank concise either.
  const startExpanded = defaultExpanded && presentation.experience !== "concise";
  const [open, setOpen] = useState(explicitFold ?? (startExpanded || keepExpanded || (followsWhileStreaming && item.streaming)));
  const bodyRef = useRef<HTMLDivElement>(null);
  // 任务 765: the ref dies with the instance; seed it from the persisted
  // intent so the streaming auto-expand branch cannot override the user's
  // choice after a remount (virtualization, tab switch and back, live→history
  // handoff). A fresh streaming cycle keeps honoring it too: the intent is
  // the user's explicit word about this panel, streaming must not flip it.
  const hasPersistedIntent = explicitFold !== undefined;
  const userOverridden = useRef(hasPersistedIntent);
  const previousStreaming = useRef(item.streaming);
  const previousComplete = useRef(item.reasoningComplete ?? false);
  const previousExperience = useRef(presentation.experience);

  useEffect(() => {
    const wasStreaming = previousStreaming.current;
    const wasComplete = previousComplete.current;
    const complete = item.reasoningComplete ?? false;
    const modeChanged = previousExperience.current !== presentation.experience;
    previousStreaming.current = item.streaming;
    previousComplete.current = complete;
    previousExperience.current = presentation.experience;
    if (modeChanged) {
      userOverridden.current = false;
      setOpen(startExpanded || keepExpanded || (followsWhileStreaming && item.streaming));
    } else if (item.streaming) {
      // 任务 765: a fresh streaming cycle clears a session-local toggle, but a
      // persisted intent (survived a remount) stays — streaming updates must
      // not overturn what the user explicitly chose for this panel.
      if (!wasStreaming) userOverridden.current = hasPersistedIntent;
      if (startExpanded || keepExpanded) setOpen(true);
      else if (!userOverridden.current && followsWhileStreaming) setOpen(true);
    } else if ((complete && !wasComplete) || wasStreaming) {
      if (!startExpanded && !keepExpanded && !userOverridden.current) setOpen(false);
    }
  }, [followsWhileStreaming, keepExpanded, item.reasoningComplete, item.streaming, presentation.experience, startExpanded]);

  const toggle = () => {
    userOverridden.current = true;
    onExplicitFoldChange?.(!open);
    setOpen((value) => !value);
  };
  useCollapseAnimation(bodyRef, open);
  const meta = running ? "" : reasoningDurationLabel(item.reasoningDurationMs, t);
  return (
    <div className="reasoning">
      <button type="button" className="reasoning__head" data-running={running ? "" : undefined} onClick={toggle} aria-expanded={open}>
        <ProcessBrainIcon size={12} />
        <span data-creation-label={t("creation.reasoningLabel")}>{running ? t("msg.thinkingRunning") : t("msg.thinking")}</span>
        {meta && <span className="reasoning__meta">{meta}</span>}
        <ChevronRight className={`reasoning__chevron${open ? " reasoning__chevron--open" : ""}`} size={12} />
      </button>
      {open ? (
        <div ref={bodyRef} className="reasoning__body" data-transcript-selectable="reasoning" data-nested-scroll>
          {running
            ? <StreamingReasoningText text={item.reasoning} />
            : <Markdown text={item.reasoning} streaming={false} cacheKey={item.id} wasStreamed={item.wasStreamed} />}
        </div>
      ) : (
        <ReasoningSummary text={item.reasoning} streaming={running} onOpen={toggle} />
      )}
    </div>
  );
}
