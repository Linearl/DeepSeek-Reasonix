export interface RewindPlanView {
  planId?: string;
  turn?: number;
  scope?: string;
  coverage?: string;
  coverageGaps?: string[];
  legacy?: boolean;
  expiredFilePayload?: boolean;
  canFiles?: boolean;
  canConversation?: boolean;
  disabledReason?: string;
  conflicts?: string[];
  files?: string[];
  fileCount?: number;
  /** tl1476: per-file preflight verdict for the confirmation dialog. */
  fileSafety?: RewindFileSafety[];
  activeWriters?: number;
  path?: string;
  conversationAction?: string;
  ok?: boolean;
  error?: string;
}

/**
 * tl1476: one rewind target's preflight verdict. `reason` uses the backend's
 * conflict-reason vocabulary (path_unsafe, missing_payload, ...) plus
 * permission_denied; only set when safe is false.
 */
export interface RewindFileSafety {
  path: string;
  safe: boolean;
  reason?: string;
}

export interface RewindUndoState {
  turnDiff: number;
  transactionId?: string;
  undoAvailable?: boolean;
  undoTabId?: string;
  filesRestored?: string[];
  filesRemoved?: string[];
}
