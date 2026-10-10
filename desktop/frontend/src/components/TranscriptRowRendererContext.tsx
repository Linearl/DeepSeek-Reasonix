import { createContext, createElement, useContext, type ReactNode } from "react";
import type { TranscriptRow } from "../lib/transcriptRows";

export type TranscriptRowRenderer = (row: TranscriptRow) => ReactNode;

// Task 735 (issue #43): renderRow used to ride into every block as a prop and
// into every row view as eagerly built children, so a single renderer identity
// change re-rendered every mounted row of a transcript — the delete-topic
// unmount storm logged one 958ms long task whose top frames were transcript
// geometry callbacks (39 samples) plus passive unmount (7 samples). The
// renderer now travels through context — the same shape as the task 399
// find-highlight context — so the memoized blocks/rows only re-render when
// their own row data actually changed.
export const TranscriptRowRendererContext = createContext<TranscriptRowRenderer | null>(null);

export function TranscriptRowRendererProvider({ renderRow, children }: {
  renderRow: TranscriptRowRenderer;
  children: ReactNode;
}) {
  return createElement(TranscriptRowRendererContext.Provider, { value: renderRow }, children);
}

export function useTranscriptRowRendererFn(): TranscriptRowRenderer {
  const renderRow = useContext(TranscriptRowRendererContext);
  if (!renderRow) {
    throw new Error("TranscriptRowRendererContext: row rendering used outside its provider");
  }
  return renderRow;
}
