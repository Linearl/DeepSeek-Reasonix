// tl1387 (user rulings 20261104 13:5x / 14:0x): the sidebar session row's
// model hover. The row never renders provider/model names inline (the
// tl1330/Task 274① inline tag is retired); the model lives ONLY on the row
// hover title, and the hover shows the CURRENTLY EFFECTIVE model — the same
// value the composer selector displays (the backend stamps node.modelRef
// from the live tab model, so the two are one source). Fallback order when
// no runtime is visible: the last known live value > the session sidecar's
// creation-time model (annotated 「创建时」 via its own locale key). Pure and
// structured so tests need no React and no locale.

export type SessionModelHover =
  | { kind: "live" | "lastKnown"; model: string }
  | { kind: "createdAt"; model: string };

/** Module-level last-known-live memory: whenever a row renders with a live
 *  modelRef, remember it per session key; a later render without a visible
 *  runtime falls back to it before the creation-time value. Session keys are
 *  paths/ids (stable per session), values tiny strings; the cap bounds a
 *  long-lived window's memory. */
const lastKnownLiveModel = new Map<string, string>();
const LAST_KNOWN_CAP = 2048;

export function rememberLiveSessionModel(key: string, modelRef: string | undefined): void {
  const trimmed = (modelRef ?? "").trim();
  if (!key || !trimmed) return;
  if (lastKnownLiveModel.size >= LAST_KNOWN_CAP && !lastKnownLiveModel.has(key)) {
    lastKnownLiveModel.clear();
  }
  lastKnownLiveModel.set(key, trimmed);
}

export function lastKnownLiveSessionModel(key: string): string | undefined {
  return lastKnownLiveModel.get(key) || undefined;
}

export interface SessionModelHoverInput {
  /** Live, selector-source model ref (host sees the runtime). */
  modelRef?: string;
  /** Creation-time sidecar model (host sees no live runtime). */
  metaModel?: string;
}

/** The hover value for one row, or undefined when nothing is known (the row
 *  then renders with no model hover at all — never a guess). */
export function sessionModelHover(key: string, input: SessionModelHoverInput): SessionModelHover | undefined {
  const live = (input.modelRef ?? "").trim();
  if (live) return { kind: "live", model: live };
  const lastKnown = lastKnownLiveSessionModel(key);
  if (lastKnown) return { kind: "lastKnown", model: lastKnown };
  const createdAt = (input.metaModel ?? "").trim();
  if (createdAt) return { kind: "createdAt", model: createdAt };
  return undefined;
}

/** The composed title line for one row: the base title, then the model line
 *  when a hover value exists (locale keys projectTree.modelHover.*). */
export function titleWithSessionModel(
  title: string,
  hover: SessionModelHover | undefined,
  format: (key: "projectTree.modelHover.current" | "projectTree.modelHover.createdAt", values: { model: string }) => string,
): string {
  if (!hover) return title;
  const key = hover.kind === "createdAt" ? "projectTree.modelHover.createdAt" : "projectTree.modelHover.current";
  const line = format(key, { model: hover.model });
  return title ? `${title}\n${line}` : line;
}
