export type ViewportIntent = "tail" | "reader";

export type LogicalAnchor =
  | { kind: "tail" }
  | { kind: "block"; blockKey: string; offsetPx: number };

export type ScrollTransactionKind =
  | "jump"
  | "restore"
  | "prepend"
  | "display-change"
  | "selection"
  | "composer-resize"
  | "tail-sync";

export type ScrollTransaction = {
  id: number;
  generation: number;
  geometryRevision: number;
  kind: ScrollTransactionKind;
  status: "active" | "committed" | "cancelled" | "expired";
};

export type TranscriptScrollOwner =
  | "tail-follow"
  | "question-jump"
  | "restore"
  | "history-prepend"
  | "display-change"
  | "selection-edge-scroll"
  | "composer-resize"
  | "custom-scrollbar"
  | "nested-scroll"
  | "block-window-prepend";

export type TranscriptScrollMode = "tail-follow" | "manual" | "selection" | "restoring";

export type TranscriptViewportGeometry = {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
};

export type TranscriptVisibleBlock = {
  key: string;
  top: number;
  bottom: number;
};

export type TranscriptViewportSnapshot = TranscriptViewportGeometry & {
  visibleBlocks: readonly TranscriptVisibleBlock[];
};

export type TranscriptWriteRequest = {
  session: string;
  generation: number;
  transactionId: number;
  geometryRevision: number;
  owner: TranscriptScrollOwner;
  intent: ViewportIntent;
  offset: number;
};

export type TranscriptWriteResult = {
  accepted: boolean;
  offset: number;
  reason?: string;
  changed?: boolean;
};

export type TranscriptKernelClock = {
  now: () => number;
  requestAnimationFrame: (callback: FrameRequestCallback) => number;
  cancelAnimationFrame: (handle: number) => void;
  setTimeout: (callback: () => void, delay: number) => ReturnType<typeof setTimeout>;
  clearTimeout: (handle: ReturnType<typeof setTimeout>) => void;
};

export type TranscriptKernelEvent = {
  session: string;
  generation: number;
  transaction: number;
  owner?: TranscriptScrollOwner;
  intent: ViewportIntent;
  geometryRevision: number;
  requestedOffset?: number;
  acceptedOffset?: number;
  outcome: string;
};

type ActiveTransaction = {
  settleFrame?: number;
  listeners?: Set<() => void>;
  transaction: ScrollTransaction;
  anchor: LogicalAnchor;
  /** Task 931: passive corrections (the streaming anchor-restore path) face
   * the drift gate on their FIRST alignment too; explicit intents do not. */
  passive: boolean;
  correctionRevision: number;
  retryUsed: boolean;
  timeout: ReturnType<typeof setTimeout>;
};

type DeferredStructuralTransaction = {
  generation: number;
  kind: "restore" | "prepend" | "display-change" | "composer-resize";
  passive: boolean;
};

const TRANSACTION_TTL_MS = 1_000;
// tl 1204: exported as the single source of truth for "counts as at the
// bottom". Every reader (kernel verdict, hook isAtBottom, diagnostics panels)
// must derive from this constant - a second copy of the formula is exactly the
// "anchor semantics set in the render layer" failure mode this ticket removes.
export const TRANSCRIPT_BOTTOM_THRESHOLD_PX = 4;
const BOTTOM_THRESHOLD_PX = TRANSCRIPT_BOTTOM_THRESHOLD_PX;
// Task 753 (root cause 2): an anchored restore that would move the viewport
// further than this (or one viewport height, whichever is larger) is a yank,
// not a correction - the reader's anchored block drifted (large collapse or
// expansion above/at the anchor) and re-applying the stale anchor teleports
// the view back to e.g. the last user message.
const ANCHOR_DRIFT_MIN_PX = 320;

/** Task 753: current viewport facts for the anchor-drift guard in
 * correctAnchor. Absent - the correction keeps its pre-753 behavior. */
export type AnchorCorrectionViewport = {
  scrollTop: number;
  clientHeight: number;
  visibleBlocks: readonly TranscriptVisibleBlock[];
};

function defaultClock(): TranscriptKernelClock {
  return {
    now: () => Date.now(),
    requestAnimationFrame: (callback) => requestAnimationFrame(callback),
    cancelAnimationFrame: (handle) => cancelAnimationFrame(handle),
    setTimeout: (callback, delay) => setTimeout(callback, delay),
    clearTimeout: (handle) => clearTimeout(handle),
  };
}

function transactionPriority(kind: ScrollTransactionKind): number {
  switch (kind) {
    case "selection": return 5;
    case "jump": return 4;
    case "restore":
    case "prepend":
    case "display-change":
    case "composer-resize": return 3;
    case "tail-sync": return 1;
  }
}

export class TranscriptKernel {
  private readonly clock: TranscriptKernelClock;
  private readonly emit: (event: TranscriptKernelEvent) => void;
  private write: ((request: TranscriptWriteRequest) => TranscriptWriteResult) | null = null;
  private session = "";
  private generationValue = 0;
  private geometryVersion = 0;
  private transactionSequence = 0;
  private interactionVersion = 0;
  private active: ActiveTransaction | null = null;
  private deferredStructural: DeferredStructuralTransaction | null = null;
  private intentValue: ViewportIntent = "tail";
  private anchorValue: LogicalAnchor = { kind: "tail" };
  private anchors = new Map<string, LogicalAnchor>();
  // tl 1184: the last measured "is the viewport at the bottom" verdict.
  private atBottomValue = true;
  // tl 1184: the scrollTop seen at the previous observation. Intent only ever
  // changes on a REAL displacement; content growth leaves scrollTop untouched
  // and therefore must not touch the verdict.
  private lastScrollTop = -1;
  // tl 1184: per-session snapshot of atBottomValue, written when a surface is
  // switched away from. Why a plain boolean rather than re-deriving it from the
  // stored anchor: while output streams the content keeps growing, so a scroll
  // event landing after the user reached the bottom compares an OLD scrollTop
  // against a NEW scrollHeight and concludes atBottom=false. That demotes the
  // intent to "reader" and stores a block anchor, so switching back parks the
  // view mid-transcript instead of at the bottom. A boolean captured at
  // switch-away time cannot be corrupted by growth that happens afterwards.
  private bottomBySession = new Map<string, boolean>();
  private userGesture = false;
  private tailFrame: number | null = null;
  private anomalyCount = 0;
  private safeModeValue = false;
  private writeTop: number | null = null;
  private nativeGestureTimer: ReturnType<typeof setTimeout> | null = null;
  // Task 753: the block this kernel last aligned the viewport to, and the
  // top it had at that moment. Same key + a large top delta at correction
  // time = the anchored block drifted and the stale restore must be refused.
  private alignedAnchor: { key: string; top: number } | null = null;
  // Task 931 (AC8 corner): the block anchor restored by replaceSurface. Its
  // FIRST correction stays exempt from the drift gate even when a later
  // passive restore carries it (the explicit restore may have expired while
  // the block was still unmeasurable). Consumed by the first viewport-present
  // correction for that key, or superseded by real user displacement.
  private surfaceRestoreKey: string | null = null;

  constructor(options: { clock?: TranscriptKernelClock; emit?: (event: TranscriptKernelEvent) => void } = {}) {
    this.clock = options.clock ?? defaultClock();
    this.emit = options.emit ?? (() => {});
  }

  get generation(): number { return this.generationValue; }
  get interactionRevision(): number { return this.interactionVersion; }
  get geometryRevision(): number { return this.geometryVersion; }
  get intent(): ViewportIntent { return this.intentValue; }
  get anchor(): LogicalAnchor { return this.anchorValue; }
  get safeMode(): boolean { return this.safeModeValue; }
  get userGestureActive(): boolean { return this.userGesture; }
  get nativeGestureLeaseActive(): boolean { return this.nativeGestureTimer !== null; }
  get activeTransaction(): ScrollTransaction | null { return this.active?.transaction ?? null; }

  connectWriter(writer: (request: TranscriptWriteRequest) => TranscriptWriteResult): () => void {
    this.write = writer;
    return () => {
      if (this.write === writer) this.write = null;
    };
  }

  detachSurface(): void {
    this.clearNativeGestureLease();
    this.cancelActive("surface-detached");
    this.deferredStructural = null;
    this.alignedAnchor = null;
    this.surfaceRestoreKey = null;
    if (this.tailFrame !== null) this.clock.cancelAnimationFrame(this.tailFrame);
    this.tailFrame = null;
    this.generationValue += 1;
    this.userGesture = false;
    this.writeTop = null;
  }

  replaceSurface(session: string): { generation: number; anchor: LogicalAnchor } {
    if (this.session) {
      this.anchors.set(this.session, this.anchorValue);
      // tl 1184: capture the bottom verdict alongside the anchor (the anchor
      // alone can be a growth artifact - see bottomBySession).
      this.bottomBySession.set(this.session, this.atBottomValue);
    }
    this.clearNativeGestureLease();
    this.cancelActive("surface-replaced");
    this.deferredStructural = null;
    if (this.tailFrame !== null) this.clock.cancelAnimationFrame(this.tailFrame);
    this.tailFrame = null;
    this.session = session;
    this.generationValue += 1;
    this.geometryVersion = 0;
    this.userGesture = false;
    this.writeTop = null;
    this.anomalyCount = 0;
    this.safeModeValue = false;
    // A restored anchor has never been aligned against THIS surface's
    // geometry - its first correction is a legitimate restore, not a drift.
    this.alignedAnchor = null;
    // tl 1184: if the surface was parked at the bottom when it was switched
    // away from, restore "follow the tail" unconditionally. A block anchor in
    // that case is an artifact of streaming growth, not a deliberate reader
    // position, so honouring it would drop the user mid-transcript on every
    // tab switch made while output streams.
    const storedAnchor = this.anchors.get(session) ?? { kind: "tail" };
    if (this.bottomBySession.get(session) === true) {
      this.anchorValue = { kind: "tail" };
      this.intentValue = "tail";
      this.atBottomValue = true;
    } else {
      this.anchorValue = storedAnchor;
      this.intentValue = storedAnchor.kind === "tail" ? "tail" : "reader";
      // tl 1184b: derive the bottom verdict from the restored anchor. Without
      // this the field keeps the PREVIOUS surface's value (lastScrollTop is a
      // single global, so a neighbouring session's bottom state leaks in), and
      // the second tab switch back wrongly believes the reader is parked at the
      // tail -> the view gets yanked to the bottom.
      this.atBottomValue = storedAnchor.kind === "tail";
    }
    // Task 931 (AC8): record the restored block key so its first correction
    // keeps the pre-931 exemption even when it rides a passive restore (the
    // explicit restore transaction may have expired while the block was still
    // unmeasurable). Real displacement supersedes it.
    this.surfaceRestoreKey = this.anchorValue.kind === "block" ? this.anchorValue.blockKey : null;
    return { generation: this.generationValue, anchor: this.anchorValue };
  }

  advanceGeometry(generation = this.generationValue): number {
    if (generation !== this.generationValue) return this.geometryVersion;
    this.geometryVersion += 1;
    return this.geometryVersion;
  }

  capture(snapshot: TranscriptViewportSnapshot): LogicalAnchor {
    if (this.intentValue === "tail") return { kind: "tail" };
    const first = snapshot.visibleBlocks.find((block) => block.bottom > snapshot.scrollTop + 0.5);
    if (!first) return this.anchorValue;
    return { kind: "block", blockKey: first.key, offsetPx: snapshot.scrollTop - first.top };
  }

  observeNativeScroll(snapshot: TranscriptViewportSnapshot): boolean {
    const writerTop = this.writeTop;
    if (writerTop !== null && Math.abs(snapshot.scrollTop - writerTop) <= BOTTOM_THRESHOLD_PX) {
      this.writeTop = null;
      return false;
    }
    // Scroll also fires for layout/clamping and delayed writer delivery. Only
    // an input owner can turn that geometry observation into a new intent.
    if (!this.userGesture) return false;
    this.writeTop = null;
    const atBottom = this.isViewportAtBottom(snapshot);
    // tl 1184: a reader who stays put while output streams keeps the SAME
    // scrollTop while scrollHeight grows - the naive atBottom above then flips
    // to false even though the user never moved. Only a real scrollTop change
    // expresses user intent, so gate the verdict on displacement; otherwise
    // streaming silently corrupts "was at the bottom" and the next tab switch
    // restores a stale block anchor instead of the tail.
    const displaced = snapshot.scrollTop !== this.lastScrollTop;
    if (displaced) {
      this.lastScrollTop = snapshot.scrollTop;
      this.atBottomValue = atBottom;
    }
    this.intentValue = atBottom ? "tail" : "reader";
    this.anchorValue = this.intentValue === "tail" ? { kind: "tail" } : this.capture(snapshot);
    this.anchors.set(this.session, this.anchorValue);
    // Task 931: real displacement supersedes any pending surface-restore
    // exemption — the reader has moved, the restored key is no longer special.
    this.surfaceRestoreKey = null;
    return true;
  }

  /** tl 1204: the authoritative "is this viewport at the bottom" verdict.
   * The same formula used to live in the hook's render-time isAtBottom and in
   * two diagnostics sites — four places that could (and did) disagree with the
   * kernel's own verdict. This method is the one copy. */
  isViewportAtBottom(snapshot: { scrollTop: number; scrollHeight: number; clientHeight: number }): boolean {
    return snapshot.scrollHeight - snapshot.clientHeight - snapshot.scrollTop <= BOTTOM_THRESHOLD_PX;
  }

  /** tl 1204: the authoritative restore-landing entry — the ONE place that
   * decides how a freshly restored surface is positioned, derived from the
   * authoritative anchor alone. A tail anchor lands on the bottom synchronously
   * (the 1187 template) with scheduleTailSync as the single rAF fallback; a
   * block anchor opens the restore transaction and lands it in the same tick
   * when the caller can supply block geometry (the hydrate-complete frame can,
   * the DOM is mounted by then). Callers — the replaceSurface effect and the
   * hydrate-complete frame — must not re-derive tail/block semantics; asking
   * this method IS the protocol. Returns whether the position was applied
   * synchronously. */
  restoreLanding(blockTop?: (blockKey: string) => number | undefined): boolean {
    if (this.anchorValue.kind === "block") {
      const transaction = this.begin("restore", this.anchorValue);
      if (!transaction) return false;
      return blockTop ? this.correctAnchor(transaction, blockTop) : true;
    }
    const transaction = this.begin("tail-sync", this.anchorValue);
    const landed = transaction ? this.correctAnchor(transaction, () => undefined) : false;
    if (!landed) this.scheduleTailSync();
    return landed;
  }

  beginUserGesture(snapshot: TranscriptViewportSnapshot, owner: "selection" | "native" = "native"): void {
    this.interactionVersion += 1;
    this.clearNativeGestureLease();
    this.cancelTailFrame();
    this.userGesture = true;
    // Task 267 (R1b): a gesture no longer demotes intent/anchor by itself.
    // Every gesture-time write is already gated on this.userGesture alone,
    // and observeNativeScroll re-derives the intent from a REAL displacement —
    // so a non-displacing tap (copy button, expand reasoning, text select)
    // keeps the tail follow alive instead of parking the view on the captured
    // block forever ("point at the bottom, click once, the view stops
    // following and bounces back to the last user message").
    if (owner === "selection") {
      // Selection scrolling still needs a concrete edge anchor. Capture under
      // the CURRENT intent: a tail view stays a tail anchor, a reader keeps
      // its block — either way the gesture itself changes nothing.
      this.anchorValue = this.capture(snapshot);
      this.begin("selection", this.anchorValue);
    } else {
      this.cancelActive("user-gesture");
    }
  }

  endUserGesture(): ScrollTransaction | null {
    this.clearNativeGestureLease();
    this.userGesture = false;
    if (this.active?.transaction.kind === "selection") this.finish(this.active.transaction.id, "committed", "selection-ended");
    const deferred = this.deferredStructural;
    this.deferredStructural = null;
    let resumed: ScrollTransaction | null = null;
    // Task 931: resume with the CURRENT anchor, not the one frozen at defer
    // time. The user may have kept scrolling through this gesture
    // (observeNativeScroll already advanced anchorValue past the defer point);
    // re-applying the frozen anchor here used to revert the logical anchor and
    // hand the next correction a stale target — a teleport back to where the
    // gesture started instead of where the reader actually is.
    if (deferred && deferred.generation === this.generationValue) resumed = this.begin(deferred.kind, this.anchorValue, { passive: deferred.passive });
    // Task 267: tail-sync requests arriving during the gesture were dropped
    // outright (only structural kinds defer), and nothing ever retried them.
    // With the gate open and the view still owning the tail, re-arm the
    // subsidy so streaming growth the gesture swallowed catches up next frame.
    // A reader intent (real upward displacement) is never touched here.
    if (this.intentValue === "tail") this.scheduleTailSync();
    return resumed;
  }

  renewNativeGesture(
    snapshot: TranscriptViewportSnapshot,
    idleMs: number,
    onEnd: (resumed: ScrollTransaction | null) => void,
  ): void {
    this.clearNativeGestureLease();
    if (!this.userGesture) this.beginUserGesture(snapshot, "native");
    const generation = this.generationValue;
    const timer = this.clock.setTimeout(() => {
      if (generation !== this.generationValue || this.nativeGestureTimer !== timer) return;
      this.nativeGestureTimer = null;
      onEnd(this.endUserGesture());
    }, Math.max(0, idleMs));
    this.nativeGestureTimer = timer;
  }

  afterCurrentGenerationPaint(callback: () => void): () => void {
    const generation = this.generationValue;
    let cancelled = false;
    const handle = this.clock.requestAnimationFrame(() => {
      if (!cancelled && generation === this.generationValue) callback();
    });
    return () => { cancelled = true; this.clock.cancelAnimationFrame(handle); };
  }

  begin(kind: ScrollTransactionKind, anchor = this.anchorValue, options?: { passive?: boolean }): ScrollTransaction | null {
    if (this.userGesture && kind !== "selection") {
      if (kind === "restore" || kind === "prepend" || kind === "display-change" || kind === "composer-resize") {
        this.deferredStructural = { generation: this.generationValue, kind, passive: options?.passive === true };
      }
      return null;
    }
    if (this.active && transactionPriority(this.active.transaction.kind) > transactionPriority(kind)) return null;
    if (kind !== "tail-sync") this.cancelTailFrame();
    this.cancelActive("superseded");
    const transaction: ScrollTransaction = {
      id: ++this.transactionSequence,
      generation: this.generationValue,
      geometryRevision: this.geometryVersion,
      kind,
      status: "active",
    };
    const timeout = this.clock.setTimeout(() => this.finish(transaction.id, "expired", "deadline"), TRANSACTION_TTL_MS);
    this.active = { transaction, anchor, passive: options?.passive === true, correctionRevision: -1, retryUsed: false, timeout };
    this.anchorValue = anchor;
    this.intentValue = anchor.kind === "tail" ? "tail" : "reader";
    this.anchors.set(this.session, anchor);
    this.emitEvent(transaction, undefined, undefined, undefined, "active");
    return transaction;
  }

  cancelActive(outcome = "cancelled"): void {
    if (this.active) this.finish(this.active.transaction.id, "cancelled", outcome);
  }

  onTransactionEnd(transaction: ScrollTransaction, listener: () => void): void {
    if (this.active?.transaction !== transaction) { listener(); return; }
    (this.active.listeners ??= new Set()).add(listener);
  }

  finish(id: number, status: Exclude<ScrollTransaction["status"], "active">, outcome: string = status): boolean {
    const active = this.active;
    if (!active || active.transaction.id !== id) return false;
    this.clock.clearTimeout(active.timeout);
    if (active.settleFrame != null) this.clock.cancelAnimationFrame(active.settleFrame);
    active.transaction.status = status;
    this.emitEvent(active.transaction, undefined, undefined, undefined, outcome);
    this.active = null;
    active.listeners?.forEach((listener) => listener());
    return true;
  }

  correctAnchor(
    transaction: ScrollTransaction,
    blockTop: (blockKey: string) => number | undefined,
    viewport?: AnchorCorrectionViewport,
  ): boolean {
    const active = this.active;
    if (!active || active.transaction.id !== transaction.id || transaction.generation !== this.generationValue) return false;
    if (this.userGesture || active.transaction.kind === "selection") return false;
    if (active.correctionRevision === this.geometryVersion) return false;
    active.correctionRevision = this.geometryVersion;
    if (active.anchor.kind === "tail") return this.writeAndFinish(active, "tail-follow", Number.POSITIVE_INFINITY);
    const top = blockTop(active.anchor.blockKey);
    if (top == null || !Number.isFinite(top)) {
      if (!active.retryUsed) {
        active.retryUsed = true;
        return false;
      }
      this.reportAnomaly("missing-anchor");
      this.finish(transaction.id, "cancelled", "anchor-missing");
      return false;
    }
    // Task 753 (root cause 2): the anchored block is allowed to move between
    // alignments, but if it moved further than a viewport from where this
    // kernel last aligned it, re-applying the anchor is a teleport back to a
    // message the reader left long ago ("jumps to my message, never follows
    // the bottom"). Refuse the restore, re-anchor to the block the reader is
    // actually looking at, and keep the current pixels.
    //
    // Task 931: the same teleport rides the FIRST alignment when the anchor is
    // stale — 753's exemption covered fresh captures / explicit jumps / surface
    // restores, but a passive streaming restore can carry an anchor that was
    // never aligned against this viewport (native momentum walked the viewport
    // past the gesture lease, or a gesture-deferred structural resume reverted
    // the logical anchor). The gate now also covers passive corrections on
    // their first alignment; explicit intents keep the exemption unchanged:
    // jump (user navigation), prepend (position compensation may legitimately
    // move far), and non-passive restores (explicit jump/surface restore keep
    // pre-931 first-alignment semantics byte-for-byte). The surface-restore
    // key (AC8) additionally exempts the restored block's first correction.
    const sameAnchorAligned = this.alignedAnchor?.key === active.anchor.blockKey;
    const surfaceRestoreFirst = active.anchor.kind === "block" && this.surfaceRestoreKey === active.anchor.blockKey;
    if (surfaceRestoreFirst && viewport) this.surfaceRestoreKey = null;
    const passiveFirstAlignment = active.passive
      && !surfaceRestoreFirst
      && active.transaction.kind !== "jump"
      && active.transaction.kind !== "prepend";
    if (viewport && (sameAnchorAligned || passiveFirstAlignment)) {
      const target = top + active.anchor.offsetPx;
      if (Math.abs(target - viewport.scrollTop) > Math.max(ANCHOR_DRIFT_MIN_PX, viewport.clientHeight)) {
        const first = viewport.visibleBlocks.find((block) => block.bottom > viewport.scrollTop + 0.5);
        this.alignedAnchor = null;
        if (first) {
          const replacement: LogicalAnchor = { kind: "block", blockKey: first.key, offsetPx: viewport.scrollTop - first.top };
          this.anchorValue = replacement;
          this.anchors.set(this.session, replacement);
        }
        this.finish(transaction.id, "cancelled", sameAnchorAligned ? "anchor-drift" : "anchor-stale");
        return false;
      }
    }
    const owner: TranscriptScrollOwner = active.transaction.kind === "jump" ? "question-jump"
      : active.transaction.kind === "prepend" ? "history-prepend"
        : active.transaction.kind === "display-change" ? "display-change"
          : active.transaction.kind === "composer-resize" ? "composer-resize"
            : "restore";
    const written = this.writeAndFinish(active, owner, top + active.anchor.offsetPx);
    if (written) this.alignedAnchor = { key: active.anchor.blockKey, top };
    return written;
  }

  // offsetPx (task 399): row-level find jumps land mid-block; the default 0
  // keeps every existing block-top caller's behavior byte-identical.
  jumpToBlock(blockKey: string, blockTop: (blockKey: string) => number | undefined, offsetPx = 0): boolean {
    const transaction = this.begin("jump", { kind: "block", blockKey, offsetPx });
    return transaction ? this.correctAnchor(transaction, blockTop) : false;
  }

  stageJumpToBlock(blockKey: string, offsetPx = 0): ScrollTransaction | null {
    return this.begin("jump", { kind: "block", blockKey, offsetPx });
  }

  scrollToTail(): boolean {
    this.interactionVersion += 1;
    this.intentValue = "tail";
    this.anchorValue = { kind: "tail" };
    this.anchors.set(this.session, this.anchorValue);
    const transaction = this.begin("tail-sync", this.anchorValue);
    return Boolean(transaction && this.active && this.writeAndFinish(this.active, "tail-follow", Number.POSITIVE_INFINITY));
  }

  scheduleTailSync(): void {
    if (this.intentValue !== "tail" || this.userGesture || this.tailFrame !== null) return;
    const generation = this.generationValue;
    this.tailFrame = this.clock.requestAnimationFrame(() => {
      this.tailFrame = null;
      if (generation !== this.generationValue || this.intentValue !== "tail" || this.userGesture) return;
      const transaction = this.begin("tail-sync", { kind: "tail" });
      if (!transaction || !this.active) return;
      const written = this.writeAndFinish(this.active, "tail-follow", Number.POSITIVE_INFINITY);
      if (!written) {
        // tl 1204: this rAF pass is THE async fallback of the restore protocol
        // (chain b). When even it cannot land the write the recovery is
        // exhausted — say so on the diagnostic stream instead of failing
        // silently, so a "stopped mid-way" report has a traceable outcome.
        this.emit({
          session: this.session,
          generation: this.generationValue,
          transaction: this.active.transaction.id,
          owner: "tail-follow",
          intent: this.intentValue,
          geometryRevision: this.geometryVersion,
          outcome: "tail-sync-fallback-exhausted",
        });
      }
    });
  }

  writeUserControlled(owner: "selection-edge-scroll" | "custom-scrollbar" | "nested-scroll", offset: number): boolean {
    if (!this.write) return false;
    let active = this.active;
    if (!active || active.transaction.kind !== "selection") {
      const transaction = this.begin("selection", this.anchorValue);
      active = transaction ? this.active : null;
    }
    if (!active) return false;
    const result = this.write({
      session: this.session,
      generation: this.generationValue,
      transactionId: active.transaction.id,
      geometryRevision: this.geometryVersion,
      owner,
      intent: "reader",
      offset,
    });
    this.emitEvent(active.transaction, owner, offset, result.offset, result.accepted ? "accepted" : result.reason ?? "rejected");
    return result.accepted;
  }

  writeStructuralOffset(owner: "block-window-prepend", offset: number): boolean {
    if (!this.write || this.userGesture) return false;
    const transaction = this.begin("prepend", this.anchorValue);
    if (!transaction || !this.active) return false;
    const result = this.write({
      session: this.session,
      generation: this.generationValue,
      transactionId: transaction.id,
      geometryRevision: this.geometryVersion,
      owner,
      intent: this.intentValue,
      offset,
    });
    this.emitEvent(transaction, owner, offset, result.offset, result.accepted ? "accepted" : result.reason ?? "rejected");
    if (result.accepted) {
      if (result.changed) this.writeTop = result.offset;
      this.finish(transaction.id, "committed", "committed");
    }
    return result.accepted;
  }

  reportAnomaly(outcome: "blank-viewport" | "invalid-geometry" | "missing-anchor"): void {
    this.anomalyCount += 1;
    this.emit({
      session: this.session,
      generation: this.generationValue,
      transaction: this.active?.transaction.id ?? 0,
      intent: this.intentValue,
      geometryRevision: this.geometryVersion,
      outcome,
    });
    if (this.anomalyCount >= 2) {
      this.safeModeValue = true;
      this.cancelActive("safe-mode");
    }
  }

  reportHealthyGeometry(): void {
    this.anomalyCount = 0;
  }

  private writeAndFinish(active: ActiveTransaction, owner: TranscriptScrollOwner, requested: number): boolean {
    if (!this.write || active.transaction.generation !== this.generationValue || this.userGesture) return false;
    const result = this.write({
      session: this.session,
      generation: this.generationValue,
      transactionId: active.transaction.id,
      geometryRevision: this.geometryVersion,
      owner,
      intent: this.intentValue,
      offset: requested,
    });
    this.emitEvent(active.transaction, owner, requested, result.offset, result.accepted ? "accepted" : result.reason ?? "rejected");
    if (result.accepted) {
      if (result.changed) this.writeTop = result.offset;
      if (active.transaction.kind === "prepend") {
        if (active.settleFrame != null) this.clock.cancelAnimationFrame(active.settleFrame);
        active.settleFrame = this.clock.requestAnimationFrame(() => {
          if (this.active === active) this.finish(active.transaction.id, "committed", "committed");
        });
      } else this.finish(active.transaction.id, "committed", "committed");
    } else if (result.reason === "native-clamp") {
      // Task 753 (root cause 2): the native scroller clamped this frame (the
      // viewport physically cannot reach the requested offset right now).
      // Holding the transaction for the full TTL blocked every follow-up
      // correction - tail follow visibly detached for up to a second during
      // collapse animations. Finish immediately so the next geometry pass can
      // retry with fresh geometry.
      this.finish(active.transaction.id, "cancelled", "native-clamp");
    }
    return result.accepted;
  }

  private cancelTailFrame(): void {
    if (this.tailFrame === null) return;
    this.clock.cancelAnimationFrame(this.tailFrame);
    this.tailFrame = null;
  }

  private clearNativeGestureLease(): void {
    if (this.nativeGestureTimer !== null) this.clock.clearTimeout(this.nativeGestureTimer);
    this.nativeGestureTimer = null;
  }

  private emitEvent(
    transaction: ScrollTransaction,
    owner?: TranscriptScrollOwner,
    requestedOffset?: number,
    acceptedOffset?: number,
    outcome: string = transaction.status,
  ): void {
    this.emit({
      session: this.session,
      generation: transaction.generation,
      transaction: transaction.id,
      owner,
      intent: this.intentValue,
      geometryRevision: this.geometryVersion,
      requestedOffset,
      acceptedOffset,
      outcome,
    });
  }
}
