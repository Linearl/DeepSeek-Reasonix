import { memo, useEffect, type CSSProperties } from "react";
import { getTranscriptStore } from "../lib/transcriptStore";
import {
  historyEntryIdForRow,
  transcriptRowMeasurementVersion,
  estimateTranscriptRowSize,
  type TranscriptRow,
} from "../lib/transcriptRows";
import { transcriptRowLayoutVariant } from "../lib/transcriptRowGeometry";
import type { TimelineBlock } from "../lib/transcriptTimeline";
import { useTranscriptFindHighlight } from "./TranscriptFindContext";
import { useTranscriptRowRendererFn } from "./TranscriptRowRendererContext";

const TranscriptRowView = memo(function TranscriptRowView({
  row,
  tabId,
}: {
  row: TranscriptRow;
  tabId?: string;
}) {
  const entryId = historyEntryIdForRow(row);
  const estimate = estimateTranscriptRowSize(row);
  // Task 399: find highlight rides context so the hit set can change per
  // keystroke without re-threading props through Viewport/ProjectionView and
  // busting every block's memo. null (bar closed) is a stable identity, so
  // rows re-render only while a search is actually active.
  const find = useTranscriptFindHighlight();
  const findHit = find?.hits.has(String(row.key)) ?? false;
  const findActive = find?.active === String(row.key);
  useEffect(() => {
    if (entryId) getTranscriptStore().requestEntryFullContent(tabId, entryId);
  }, [entryId, tabId]);
  // Task 735: the row content builds here, behind the memo gate — building it
  // eagerly as a children prop used to defeat this memo on every renderer
  // identity change (each row's component tree was rebuilt even when the row
  // data was untouched).
  const content = useTranscriptRowRendererFn()(row);
  return (
    <div
      className={`transcript__row${findHit ? " transcript__row--find" : ""}${findActive ? " transcript__row--find-active" : ""}`}
      data-row-key={String(row.key)}
      data-row-kind={row.kind}
      data-find-hit={findHit || undefined}
      data-find-active={findActive || undefined}
      data-layout-version={transcriptRowMeasurementVersion(row)}
      data-transcript-layout-variant={transcriptRowLayoutVariant(row)}
      style={{ "--transcript-row-estimate": `${estimate}px` } as CSSProperties}
    >
      {content}
    </div>
  );
});

export const TranscriptBlockView = memo(function TranscriptBlockView({
  block,
  tabId,
  placement,
}: {
  block: TimelineBlock;
  tabId?: string;
  placement?: { index: number; top: number };
}) {
  return (
    <div
      className={`transcript__block${placement ? " transcript__window-item" : ""}`}
      data-index={placement?.index}
      style={placement ? { position: "absolute", top: placement.top, left: 0, width: "100%" } : undefined}
      data-transcript-block-key={block.key}
      data-transcript-block-phase={block.phase}
      data-transcript-content-revision={block.contentRevision}
      data-transcript-measurement-revision={block.measurementRevision}
    >
      {block.rows.map((row) => (
        <TranscriptRowView key={row.key} row={row} tabId={tabId} />
      ))}
    </div>
  );
});
