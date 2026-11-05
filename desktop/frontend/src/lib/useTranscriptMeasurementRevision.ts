import { useCallback, useRef, useState } from "react";

/**
 * tl1416 (issue #82): the transcript measurement revision drives TranscriptWindow's
 * prepaint measurement pass. Before this gate, EVERY MarkdownHistory row whose
 * parsed blocks changed inside one commit called geometryChanged, and each call
 * chained a synchronous layout-effect re-render plus a full mounted-block
 * measurement sweep — W rows multiplied the identical DOM sweep W times within a
 * single prepaint window (the tab-switch freeze's write-back half; the read half
 * is the per-key geometry cache in useTranscriptKernel).
 *
 * Contract:
 *  - signalGeometryChanged collapses same-commit signals: the first call
 *    increments, later calls before the next measurement pass are absorbed. One
 *    pass observes the whole committed DOM anyway — the extra signals carry no
 *    extra information, only extra synchronous re-renders.
 *  - beginMeasurementPass is the consumption point (top of the measurement
 *    effect): after it runs, the next signal increments again.
 *  - bumpForPublishedSizes is the measurement effect's own publication increment
 *    (new sizes entered the prefix); it is deliberately UNGATED — absorbing it
 *    would drop a published batch's follow-up pass. It converges by itself: the
 *    follow-up pass re-stages nothing (the ledger dedupes at ±0.5px), so the
 *    revision chain is bounded at a small constant per geometry change instead of
 *    scaling with the number of rows that changed.
 */
export function useTranscriptMeasurementRevision(): {
  revision: number;
  signalGeometryChanged: () => void;
  beginMeasurementPass: () => void;
  bumpForPublishedSizes: () => void;
} {
  const [revision, setRevision] = useState(0);
  const pendingSignalRef = useRef(false);
  const signalGeometryChanged = useCallback(() => {
    if (pendingSignalRef.current) return;
    pendingSignalRef.current = true;
    setRevision((value) => value + 1);
  }, []);
  const beginMeasurementPass = useCallback(() => {
    pendingSignalRef.current = false;
  }, []);
  const bumpForPublishedSizes = useCallback(() => {
    setRevision((value) => value + 1);
  }, []);
  return { revision, signalGeometryChanged, beginMeasurementPass, bumpForPublishedSizes };
}
