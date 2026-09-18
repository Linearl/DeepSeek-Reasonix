import { useEffect, useRef, type ReactNode } from "react";

/**
 * ⚠️ NOT ACTIVE — kept for a later attempt, wired to nothing in the app.
 *
 * Task 151 (round 3) shipped this as the tab-switch fix; it was reverted the next day.
 * With one pane per resident tab the app-level runtime contract broke: the composer
 * stopped returning to a writable state (send, model switch) and the build had to be
 * pulled back to the data-layer-only state. What has to be solved before this can be
 * wired in again:
 *   1. Every mounted Transcript does real work. A pane per tab multiplies the
 *      backend ToolRecoveryPanel query, the live/markdown/geometry subscriptions and
 *      the diagnostics, and document-level lookups (`document.querySelector(".transcript")`,
 *      `querySelectorAll` over rows) start returning the WRONG surface once two exist.
 *   2. A pane that becomes visible must shed every trace of its hidden state. The inert
 *      flag used to stick (the ref callback was only called with null on the way back,
 *      and it bailed out on null) — which turns a live transcript into an unclickable
 *      one. Silent, and exactly the kind of failure that ships.
 *   3. The app's "one surface owns the transcript" invariants need an explicit
 *      multi-surface contract first: `scripts/check-single-scroll-writer.mjs`, the
 *      app-lifecycle probe's singular subscription/operation counts, and the
 *      navigation-surface ticket handshake (surfaceCommitToken) all assume a single
 *      Transcript instance.
 *
 * The measurement tooling (bench/tab-render.mjs) and the pure helpers
 * (lib/transcriptResidency.ts, lib/transcriptGeometryKey.ts) are unaffected and stay.
 */

export type TranscriptPaneResidencyProps = {
  /** Resident tab ids, most recent first. Each one gets its own pane. */
  panes: readonly string[];
  /** The tab whose pane is on screen — during a navigation transition still the outgoing tab. */
  visibleTabId: string | undefined;
  renderPane: (tabId: string, resident: boolean) => ReactNode;
};

/**
 * One transcript pane per resident tab, all mounted, only one visible. Switching tabs
 * stops being "replace the item set of the single Transcript instance" (which makes
 * React rebuild the incoming tab's window node by node, measured at 174 ms
 * click-to-DOM-complete on two 100-turn tabs) and becomes "toggle which pane is
 * visible" (7 ms, bench/tab-render.mjs).
 *
 * The pane is keyed by tab id, and so is everything below it, which is what makes React
 * reuse the incoming pane's DOM instead of remounting it.
 */
export function TranscriptPaneResidency(props: TranscriptPaneResidencyProps) {
  return (
    <>
      {props.panes.map((tabId) => (
        <TranscriptPane key={tabId} tabId={tabId} resident={tabId !== props.visibleTabId}>
          {props.renderPane(tabId, tabId !== props.visibleTabId)}
        </TranscriptPane>
      ))}
    </>
  );
}

function TranscriptPane({ tabId, resident, children }: { tabId: string; resident: boolean; children: ReactNode }) {
  const ref = useRef<HTMLDivElement | null>(null);
  // Both directions on purpose: a pane that becomes visible has to have its hidden
  // state removed, and "the ref callback got null" is not a signal that it happened.
  useEffect(() => {
    const node = ref.current;
    if (!node) return;
    node.inert = resident;
  }, [resident]);
  return (
    <div
      ref={ref}
      className={resident ? "transcript-pane transcript-pane--resident" : "transcript-pane"}
      data-transcript-pane={tabId}
      data-transcript-resident={resident ? "true" : undefined}
      aria-hidden={resident || undefined}
    >
      {children}
    </div>
  );
}
