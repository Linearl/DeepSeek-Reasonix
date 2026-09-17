// Task 151 (round 3): the transcript surface key must be derivable for a tab that is
// not active. A resident (kept-alive) pane is keyed by its own tab's surface identity,
// so becoming visible does not change the key and does not pay for a fresh surface
// replace — which is exactly the cost residency exists to avoid. Same inputs as the
// active tab's key, so both agree on the value for a given tab.
export type TranscriptGeometryKeyInput = {
  sessionPath?: string;
  sessionGeneration?: number;
  scope?: string;
  workspaceRoot?: string;
  topicId?: string;
  tabId?: string;
};

export function transcriptGeometryKeyFor(input: TranscriptGeometryKeyInput): string {
  const sessionPath = (input.sessionPath ?? "").trim();
  if (sessionPath) return ["session", sessionPath, String(input.sessionGeneration ?? 0)].join("\u0000");
  return ["topic", input.scope ?? "", input.workspaceRoot ?? "", input.topicId ?? "", input.tabId ?? ""].join("\u0000");
}
