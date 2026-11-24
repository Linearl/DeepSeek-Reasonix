import { noteTranscriptScrollWrite, recordTranscriptScrollDiagnostic } from "./transcriptScrollProbe";
import { labFlagEnabled } from "./labFlags";
import { SmoothTailFollower } from "./smoothTailFollow";
import type { TranscriptWriteRequest, TranscriptWriteResult } from "./transcriptKernel";

/** The only production owner allowed to mutate the native Transcript scroll
 * position. Every adapter, gesture, and transaction routes through this class.
 *
 * tl 1165: tail-class writes (the `tail-follow` owner positioning at the
 * measured floor, requested as a non-finite offset) may ride the smooth
 * scroll follow when the lab switch is on — native smooth scrollTo behind
 * the DSH ScrollFollow guards. Every other write (anchor restores, prepends,
 * question jumps, reader edges) keeps the exact pre-1165 instant assignment;
 * with the switch off even tail writes do (byte-for-byte). */
export class TranscriptViewportWriter {
  private element: HTMLElement | null = null;
  private generation = 0;
  private frozen = false;
  private readonly follower = new SmoothTailFollower();
  private readonly smoothFollow: () => boolean;
  // tl 1776: the floor this writer last aimed a tail-class write at (the
  // measured maximum at that moment). This is the streaming tail-follow race
  // witness: while output streams, the follow engine pins the floor once per
  // chunk, and the scroll events those writes emit can land in the window
  // where scrollHeight has already grown past it — an observation taken there
  // reads "not at the bottom" purely because of that growth, not because the
  // reader moved. The facts sampler (capturePreReflowFacts) consults this
  // floor: a viewport still sitting on it (within the bottom tolerance) while
  // the kernel intent is tail is a tail view in follow-lag, so the sample must
  // record at-bottom. Cleared on attach: another surface's floor says nothing
  // about this one.
  private tailFloor: number | null = null;

  constructor(options: { smoothFollow?: () => boolean } = {}) {
    this.smoothFollow = options.smoothFollow ?? (() => labFlagEnabled("smoothScrollFollow"));
  }

  /** tl 1776: the floor of the most recent tail-follow-class write (null
   * before the first one / after a surface re-attach). Read only by the
   * pre-reflow facts sampler; the write path stays the single owner. */
  get lastTailFloor(): number | null { return this.tailFloor; }

  attach(element: HTMLElement | null, generation: number): void {
    this.element = element;
    this.generation = generation;
    this.tailFloor = null;
    if (!element) this.follower.reset();
  }

  freeze(value: boolean): void {
    this.frozen = value;
  }

  /** Cancel an outstanding smooth follow before reader motion owns the
   * viewport (gesture entries). Inert with the switch off or while idle. */
  interruptViewport = (): void => {
    const element = this.element;
    if (!element || this.frozen || !this.smoothFollow()) return;
    this.follower.interrupt(element);
  };

  write = (request: TranscriptWriteRequest): TranscriptWriteResult => {
    const element = this.element;
    if (!element) return this.reject(request, "no-viewport");
    if (request.generation !== this.generation) return this.reject(request, "stale-generation");
    if (this.frozen) return this.reject(request, "user-gesture");
    if (!Number.isFinite(element.scrollHeight) || !Number.isFinite(element.clientHeight)) {
      return this.reject(request, "invalid-geometry");
    }
    const maximum = Math.max(0, element.scrollHeight - element.clientHeight);
    const accepted = Number.isFinite(request.offset)
      ? Math.max(0, Math.min(maximum, request.offset))
      : maximum;
    const before = element.scrollTop;
    // tl 1776: a non-finite request IS the tail class — every one of them aims
    // at the current floor, instant or smooth, accepted or no-op alike. Record
    // it after the reject gates so a refused write (frozen/stale) never claims
    // a floor the viewport never received.
    if (!Number.isFinite(request.offset)) this.tailFloor = maximum;
    // tl 1165: smooth tail follow. Only the tail-class write (the non-finite
    // floor request behind the `tail-follow` owner) is eligible; everything
    // else keeps the pre-1165 instant write below.
    if (!Number.isFinite(request.offset) && this.smoothFollow()) {
      // Lazy settle first: a delivery that already reached the earlier target
      // frees the gate even without scroll events between two writes.
      this.follower.noteScroll(element);
      if (this.follower.animating) {
        // An outstanding smooth target finishes before another is issued
        // (DSH gate 2); the kernel's follow-up stream re-requests next frame.
        noteTranscriptScrollWrite({
          session: request.session,
          transaction: request.transactionId,
          owner: request.owner,
          intent: request.intent,
          requestedOffset: request.offset,
          acceptedOffset: before,
          outcome: "smooth-follow-pending",
          kind: "pinTail",
          top: request.offset,
          scrollTop: before,
          scrollHeight: element.scrollHeight,
          clientHeight: element.clientHeight,
          bottomDistance: maximum - before,
          mode: request.intent,
          generation: request.generation,
          geometryRevision: request.geometryRevision,
          transactionId: request.transactionId,
        });
        return { accepted: true, offset: before, changed: false };
      }
      const floorGap = maximum - before;
      if (floorGap > 4) {
        this.follower.follow(element);
        noteTranscriptScrollWrite({
          session: request.session,
          transaction: request.transactionId,
          owner: request.owner,
          intent: request.intent,
          requestedOffset: request.offset,
          acceptedOffset: before,
          outcome: "smooth-follow",
          kind: "pinTail",
          top: request.offset,
          scrollTop: before,
          scrollHeight: element.scrollHeight,
          clientHeight: element.clientHeight,
          bottomDistance: floorGap,
          mode: request.intent,
          generation: request.generation,
          geometryRevision: request.geometryRevision,
          transactionId: request.transactionId,
        });
        return { accepted: true, offset: before, changed: false };
      }
      // Within the near-bottom tolerance: fall through to the instant write,
      // identical to the pre-1165 path (DSH positions near arrivals directly).
    }
    const noOp = Math.abs(before - accepted) <= 0.5;
    if (!noOp) element.scrollTop = accepted;
    const landed = element.scrollTop;
    const acceptedByNative = Math.abs(landed - accepted) <= 4;
    noteTranscriptScrollWrite({
      session: request.session,
      transaction: request.transactionId,
      owner: request.owner,
      intent: request.intent,
      requestedOffset: request.offset,
      acceptedOffset: landed,
      outcome: noOp ? "no-op" : acceptedByNative ? "accepted" : "native-clamp",
      kind: request.owner === "tail-follow" ? "pinTail" : "scrollTo",
      top: request.offset,
      scrollTop: landed,
      scrollHeight: element.scrollHeight,
      clientHeight: element.clientHeight,
      bottomDistance: maximum - landed,
      mode: request.intent,
      generation: request.generation,
      geometryRevision: request.geometryRevision,
      transactionId: request.transactionId,
    });
    return { accepted: acceptedByNative, offset: landed, changed: Math.abs(landed - before) > 0.5, reason: acceptedByNative ? undefined : "native-clamp" };
  };

  private reject(request: TranscriptWriteRequest, reason: string): TranscriptWriteResult {
    const offset = this.element?.scrollTop ?? 0;
    recordTranscriptScrollDiagnostic("scroll-write-rejected", {
      session: request.session,
      generation: request.generation,
      transaction: request.transactionId,
      owner: request.owner,
      intent: request.intent,
      geometryRevision: request.geometryRevision,
      requestedOffset: request.offset,
      acceptedOffset: offset,
      outcome: reason,
    });
    noteTranscriptScrollWrite({
      session: request.session,
      transaction: request.transactionId,
      owner: request.owner,
      intent: request.intent,
      requestedOffset: request.offset,
      acceptedOffset: offset,
      outcome: reason,
      kind: "scrollTo",
      top: request.offset,
      scrollTop: offset,
      mode: request.intent,
      generation: request.generation,
      geometryRevision: request.geometryRevision,
      transactionId: request.transactionId,
      rejectedReason: reason,
    });
    return { accepted: false, offset, reason };
  }
}
