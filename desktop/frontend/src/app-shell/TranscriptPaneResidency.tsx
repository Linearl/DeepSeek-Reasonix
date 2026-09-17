import { type ReactNode } from "react";

export type TranscriptPaneResidencyProps = {
  /** Resident tab ids, most recent first. Each one gets its own pane. */
  panes: readonly string[];
  /** The tab whose pane is on screen — during a navigation transition still the outgoing tab. */
  visibleTabId: string | undefined;
  renderPane: (tabId: string, resident: boolean) => ReactNode;
};

/**
 * Task 151 (round 3): one transcript pane per resident tab, all mounted, only one
 * visible. Switching tabs stops being "replace the item set of the single
 * Transcript instance" (which makes React rebuild the incoming tab's window node by
 * node, measured at 174 ms click-to-DOM-complete on two 100-turn tabs) and becomes
 * "toggle which pane is visible" (7 ms, bench/tab-render.mjs).
 *
 * The pane is keyed by tab id, and so is everything below it, which is what makes
 * React reuse the incoming pane's DOM instead of remounting it.
 */
export function TranscriptPaneResidency(props: TranscriptPaneResidencyProps) {
  return (
    <>
      {props.panes.map((tabId) => {
        const resident = tabId !== props.visibleTabId;
        return (
          <div
            key={tabId}
            className={resident ? "transcript-pane transcript-pane--resident" : "transcript-pane"}
            data-transcript-pane={tabId}
            data-transcript-resident={resident ? "true" : undefined}
            aria-hidden={resident || undefined}
            ref={resident ? hideFromAssistiveTech : undefined}
          >
            {props.renderPane(tabId, resident)}
          </div>
        );
      })}
    </>
  );
}

// A resident pane stays in the document, so it has to leave the tab order and the
// accessibility tree as well — hidden from sight is not the same as unreachable.
function hideFromAssistiveTech(node: HTMLDivElement | null): void {
  if (!node) return;
  (node as HTMLDivElement & { inert?: boolean }).inert = true;
}
