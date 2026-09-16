import { memo, useContext, useMemo } from "react";
import type { AssistantItem } from "../lib/transcriptRows";
import { parseCompletionReport } from "../lib/completionReport";
import { AssistantMessage } from "./Message";
import { CompletionReportCard } from "./CompletionReportCard";
import { LiveStreamContext } from "./LiveStreamContext";

export const LiveAssistantMessage = memo(function LiveAssistantMessage({
  item,
  creationMode = false,
}: {
  item: AssistantItem;
  creationMode?: boolean;
}) {
  const live = useContext(LiveStreamContext);
  const streamingLive = Boolean(live && live.id === item.id);
  const shown = {
    ...item,
    ...(streamingLive
      ? {
          text: live!.text,
          reasoning: "",
          streaming: true,
          reasoningComplete: true,
          reasoningDurationMs: undefined,
        }
      : { reasoning: "", reasoningComplete: true, reasoningDurationMs: undefined }),
  };
  // Task 112: peel the trailing structured completion report out of the
  // answer body so it renders as a dedicated hand-off card. Skip while
  // streaming — the block is incomplete until the turn settles.
  const report = useMemo(
    () => (shown.streaming ? null : parseCompletionReport(shown.text)),
    [shown.streaming, shown.text],
  );
  const displayItem = report ? { ...shown, text: report.body || shown.text } : shown;
  return (
    <>
      <AssistantMessage item={displayItem} defaultExpanded={false} expandWhileStreaming={false} creationMode={creationMode} />
      {report ? <CompletionReportCard fields={report.fields} id={`${item.id}-report`} /> : null}
    </>
  );
});
