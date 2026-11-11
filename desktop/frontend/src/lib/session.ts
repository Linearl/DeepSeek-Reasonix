import type { HistorySearchHit, ProjectNode, SessionMeta } from "./types";

export function sessionActivityTime(session: SessionMeta): number {
  return session.lastActivityAt ?? session.modTime;
}

// tl1547 (C1): the FE chains used to prefer topicTitle over title — the exact
// reverse of the Go sidebar chain (session_display_label.go: title >
// topicTitle > preview > timestamp > topicLabel > default). One session with
// both a custom title and a topic title then showed two different names in
// the sidebar vs the history panel / command palette. The FE now reads the
// same order as the Go chain's first three tiers; the timestamp/topicLabel/
// default tiers stay backend-owned.
export function historySessionDisplayTitle(session: Pick<SessionMeta, "preview" | "title" | "topicTitle">, fallback: string): string {
  return session.title?.trim() || session.topicTitle?.trim() || session.preview?.trim() || fallback;
}

// tl1547 (C5): the last tier used to fall back to sessionPath, leaking the
// internal transcript path into search results for sessions with neither a
// topic title nor a session title (tl1330 residual). Callers pass their own
// localized safe text instead.
export function historySearchHitDisplayTitle(
  hit: Pick<HistorySearchHit, "sessionPath" | "sessionTitle" | "topicTitle">,
  fallback: string,
): string {
  return hit.topicTitle?.trim() || hit.sessionTitle?.trim() || fallback;
}

export function paletteSessionDisplayTitle(session: Pick<SessionMeta, "preview" | "title" | "topicTitle">, fallback: string): string {
  return session.title?.trim() || session.topicTitle?.trim() || session.preview?.trim() || fallback;
}

// tl1547 (C4): during startup indexing the backend leaves `preview` as the
// file-name stamp only (the English "History is being indexed" literal is
// gone from the payload), and the card renders this localized placeholder
// with the stamp appended when one exists — the same slot the Go side used to
// fill with a hardcoded English string.
export function historyIndexedTitle(preview: string | undefined, indexing: string): string {
  const stamp = preview?.trim();
  return stamp ? `${indexing} — ${stamp}` : indexing;
}

export function paletteSessionHint(
  session: Pick<SessionMeta, "preview" | "title" | "topicTitle" | "topicId" | "workspaceRoot">,
): string | undefined {
  const primary = paletteSessionDisplayTitle(session, "");
  const title = session.title?.trim();
  const preview = session.preview?.trim();
  const workspace = session.workspaceRoot?.trim();
  const secondary = session.topicId
    ? preview && preview !== primary ? preview : ""
    : title && title !== primary ? title : preview && preview !== primary ? preview : "";
  const hint = [secondary, workspace].filter(Boolean).join(" · ");
  return hint || undefined;
}

export function paletteSessionKeywords(session: Pick<SessionMeta, "preview" | "title" | "topicId">): string[] {
  return [session.topicId ? undefined : session.title?.trim(), session.preview?.trim()].filter((value): value is string => Boolean(value));
}

// topicActivityTime returns the last-activity timestamp for a sidebar topic
// node. Falls back to the topic's creation time so blank topics (no session
// files yet) are still visible under time-based filters.
export function topicActivityTime(node: ProjectNode): number {
  return node.lastActivityAt || node.createdAt || 0;
}
