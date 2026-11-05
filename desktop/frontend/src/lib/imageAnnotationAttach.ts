// tl 1412: the image-viewer annotation editor hands its finished PNG back to
// the conversation input through this window-event channel — the same seam
// displayMode/sessionExperience use for "a component deep in the transcript
// needs to reach the Composer".
//
// Why an event and not a prop chain: the viewer is hosted in two places
// (Composer draft attachments and Message/MessageAttachments transcript
// attachments). Message has no path into the composer's draft bookkeeping,
// while the Composer is a per-app singleton at the footer, so a window event
// with a single subscriber stays honest. The Composer owns the storage calls
// (SavePastedImage → AttachmentDataURL → addAttachmentToDraft) exactly like
// the paste-image and screenshot-confirm chains do; this module only carries
// the payload.

export interface AnnotatedImageAttachment {
  /** Finished composite PNG as a data URL (image/png). */
  dataUrl: string;
  /** Optional display name, derived from the annotated image's filename. */
  name?: string;
}

const ANNOTATED_IMAGE_ATTACH_EVENT = "reasonix:image-annotation-attach";

/** Fire from the annotation editor when the user confirms the annotated PNG. */
export function requestAnnotatedImageAttachment(attachment: AnnotatedImageAttachment): void {
  if (typeof window === "undefined" || typeof window.dispatchEvent !== "function") return;
  window.dispatchEvent(new CustomEvent<AnnotatedImageAttachment>(ANNOTATED_IMAGE_ATTACH_EVENT, { detail: attachment }));
}

/** Subscribe (Composer); returns the unsubscribe function. */
export function onAnnotatedImageAttachment(cb: (attachment: AnnotatedImageAttachment) => void): () => void {
  if (typeof window === "undefined" || typeof window.addEventListener !== "function") return () => {};
  const handler = (e: Event) => cb((e as CustomEvent<AnnotatedImageAttachment>).detail);
  window.addEventListener(ANNOTATED_IMAGE_ATTACH_EVENT, handler);
  return () => window.removeEventListener(ANNOTATED_IMAGE_ATTACH_EVENT, handler);
}
