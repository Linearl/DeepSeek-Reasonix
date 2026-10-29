import type { TranscriptKernel } from "./transcriptKernel";

/** tl 1243: what changed in the observed element's content box. The scroller
 * commit branches on it — a WIDTH change rewraps every line (sidebar/dock
 * toggles, window resize) and needs the structural viewport compensation,
 * while a height-only change keeps the guarded tail subsidy (task 267 R3
 * semantics: never yank a reader). */
export type TranscriptGeometryChange = {
  widthChanged: boolean;
  heightChanged: boolean;
};

export const TRANSCRIPT_GEOMETRY_UNCHANGED: TranscriptGeometryChange = { widthChanged: false, heightChanged: false };

/** tl 1243: classify a ResizeObserver delivery against the previous one.
 * The FIRST delivery (the initial observation ResizeObserver always emits)
 * classifies as unchanged — it reports the state the commit path already
 * saw, so it must not fabricate a resize. An empty or malformed delivery
 * (bare notify() in tests/platform quirks) also classifies as unchanged. */
export function classifyTranscriptGeometryChange(
  previous: { width: number; height: number } | null,
  entries: readonly { contentRect?: { width?: number; height?: number } }[],
): { change: TranscriptGeometryChange; previous: { width: number; height: number } } {
  const entry = entries[0];
  const rect = entry?.contentRect;
  const width = typeof rect?.width === "number" ? rect.width : previous?.width ?? 0;
  const height = typeof rect?.height === "number" ? rect.height : previous?.height ?? 0;
  if (!previous) return { change: TRANSCRIPT_GEOMETRY_UNCHANGED, previous: { width, height } };
  return {
    change: {
      widthChanged: width !== previous.width,
      heightChanged: height !== previous.height,
    },
    previous: { width, height },
  };
}

/** Disconnect alone does not revoke already queued ResizeObserver deliveries. */
export function observeTranscriptGeometry(
  kernel: Pick<TranscriptKernel, "generation" | "afterCurrentGenerationPaint">,
  element: Element,
  before: () => unknown,
  /** `change` is optional on purpose: custom observers (tests, platform
   * quirks) may deliver bare notifications, which classify as unchanged. */
  commit: (change?: TranscriptGeometryChange) => void,
  createObserver: (notify: (change: TranscriptGeometryChange) => void) => Pick<ResizeObserver, "observe" | "disconnect"> = (notify) => {
    let previous: { width: number; height: number } | null = null;
    return new ResizeObserver((entries) => {
      const classified = classifyTranscriptGeometryChange(previous, entries);
      previous = classified.previous;
      notify(classified.change);
    });
  },
  options?: {
    /** tl 1255: deliver matching changes synchronously instead of deferring
     * them to the next paint frame. ResizeObserver callbacks run AFTER layout
     * and BEFORE paint, so a synchronous commit compensates the very reflow
     * that triggered the delivery — the browser never paints the shifted
     * position. The default (deferred) delivery suits changes whose reaction
     * is cosmetic (the 267 R3 tail subsidy); a width rewrap during the
     * sidebar's 0.16s grid-column transition repaints EVERY frame, and a
     * 1-2 frame-late correction is exactly the visible jump tl 1255 removes. */
    deliverSync?: (change?: TranscriptGeometryChange) => boolean;
  },
): () => void {
  const generation = kernel.generation;
  let disposed = false;
  let cancelFrame: (() => void) | null = null;
  const current = () => !disposed && generation === kernel.generation;
  const observer = createObserver((change) => {
    if (!current()) return;
    before();
    // tl 1255: a synchronously delivered change must not be dropped by a
    // pending deferred commit — the two cannot conflict (the sync commit runs
    // inside this rendering frame; the deferred one flushes at the next rAF
    // and re-reads the geometry it finds, so the kernel's transaction
    // dedupe + fresh-geometry re-read keep both idempotent).
    if (options?.deliverSync?.(change)) {
      commit(change);
      return;
    }
    if (cancelFrame) return;
    cancelFrame = kernel.afterCurrentGenerationPaint(() => {
      cancelFrame = null;
      if (current()) commit(change);
    });
  });
  observer.observe(element);
  return () => {
    disposed = true;
    observer.disconnect();
    cancelFrame?.();
  };
}
