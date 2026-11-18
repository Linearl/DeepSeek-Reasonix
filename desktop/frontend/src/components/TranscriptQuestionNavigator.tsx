import { forwardRef, useImperativeHandle } from "react";
import { useT } from "../lib/i18n";
import { useTranscriptHistoryNavigation } from "../lib/useTranscriptHistoryNavigation";
import type { QuestionAnchor } from "../lib/transcriptGrouping";
import { QuestionJumpBar } from "./QuestionJumpBar";

export type TranscriptQuestionNavigatorHandle = { retry: () => void };
export default forwardRef<TranscriptQuestionNavigatorHandle,
  Parameters<typeof useTranscriptHistoryNavigation>[0] & {
    questions: QuestionAnchor[]; totalQuestions: number; activeTurn: number | null;
    /** tl1698：跳到转录底部（末刻度）。 */
    onJumpBottom?: () => void;
  }
>(function TranscriptQuestionNavigator({ questions, totalQuestions, activeTurn, onJumpBottom, ...ownership }, ref) {
  const t = useT();
  const { pendingQuestion, navigate, retry } = useTranscriptHistoryNavigation(ownership);
  useImperativeHandle(ref, () => ({ retry }), [retry]);
  return <>
    {/* tl1698：末刻度 = 转录底部。onJumpBottom 由 Transcript 传入（内核的
        scrollToBottom 是 hook 级函数，不在 kernel 类上）。 */}
    <QuestionJumpBar loadedQuestions={questions} totalQuestions={totalQuestions} activeTurn={activeTurn} onJump={navigate}
      onJumpBottom={onJumpBottom} />
    {pendingQuestion && <div className="transcript-navigation-overlay transcript-question-jump-overlay" data-question-jump-mask="true" data-question-jump-phase="loading" role="status" aria-live="polite"><span className="transcript-navigation-overlay__spinner" aria-hidden="true" /><span>{t("common.loading")}</span></div>}
  </>;
});
