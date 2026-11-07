// tl1476: presenter seam for the rewind preflight dialog.
//
// The rewind commit path (rewindCommit.commitRewindWithPreview) is shared by
// every live rewind surface (App message actions and the app-runtime session
// undo hook both funnel through useController.rewindForTabDetailed), but it is
// a non-React lib and cannot render the project's ConfirmDialog itself. The
// App root registers one presenter backed by useConfirmDialog; the commit path
// awaits it before mutating anything.
//
// With no presenter registered (tests, early boot) presentRewindPreflight
// resolves true so the rewind flow behaves exactly as before — the zero-change
// guarantee for environments that never mounted the dialog.

import type { RewindPreflightFile } from "./rewindSafety";

export interface RewindPreflightRequest {
  turn: number;
  scope: string;
  /** Full classification; unsafe entries disable the dialog's confirm button. */
  files: RewindPreflightFile[];
}

export type RewindPreflightPresenter = (request: RewindPreflightRequest) => Promise<boolean>;

let presenter: RewindPreflightPresenter | null = null;

export function registerRewindPreflightPresenter(next: RewindPreflightPresenter | null): void {
  presenter = next;
}

/**
 * Resolves true when the rewind may proceed (all files safe and confirmed),
 * false when the user cancelled or the plan has unsafe files (confirm is
 * disabled, so only cancel can resolve).
 */
export async function presentRewindPreflight(request: RewindPreflightRequest): Promise<boolean> {
  return presenter ? presenter(request) : true;
}
