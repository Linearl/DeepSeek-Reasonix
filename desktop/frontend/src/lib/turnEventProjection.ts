import { asArray } from "./array";
import { app } from "./bridge";
import { recordFrontendDiagnostic } from "./frontendDiagnosticBridge";
import type { TurnEventEnvelope, TurnEventReplayView, WireEvent } from "./types";

type WireHandler = (event: WireEvent) => void;
type ResetHandler = (tabId: string, replay: TurnEventReplayView) => Promise<boolean>;

const MAX_REPLAY_PAGES = 32;

// 任务469: a prompt-kind event stranded in the gap queue is a silently dead
// ask/approval/mcp panel — the same "arrived at the webview but never
// presented" shape the wire fences had. The stranded callback lets the owner
// trigger the authoritative prompt replay (seq-less, so it bypasses this
// projector entirely) instead of leaving the panel blocked on a stalled
// sequence repair.
function isPromptKindEvent(event: WireEvent): boolean {
  return event.kind === "ask_request" || event.kind === "approval_request" || event.kind === "mcp_interaction";
}

// TurnEventProjector is the per-tab ordered projection boundary. While a gap or
// checkpoint reset is being repaired, live events are held and applied only
// after the durable page and transcript prefix agree.
export class TurnEventProjector {
  private readonly sequenceByTab = new Map<string, number>();
  private readonly repairByTab = new Map<string, Promise<void>>();
  private readonly pendingRepairByTab = new Map<string, { afterSeq: number; runtimeEpoch?: string }>();
  private readonly gapQueueByTab = new Map<string, WireEvent[]>();
  private readonly epochByTab = new Map<string, string>();
  private readonly generationByTab = new Map<string, number>();
  private readonly projectingReplayByTab = new Set<string>();
  // tl1739: a voided cursor (epoch flip) loses the truth "the surface already
  // folded everything up to here". The next accepted live event then exposes a
  // fake gap from 0 and the repair re-pours the WHOLE durable ledger — every
  // painted row re-applied past any watermark that a rebuild reset in the same
  // window (the 20261120 duplicate-render incident). Keeping the pre-void
  // cursor as the repair's starting floor bounds the repair to genuinely
  // unseen events on the continuing-numbering shape; the restarted-numbering
  // shape is adjudicated inside replayGap from the response itself.
  private readonly preVoidCursorByTab = new Map<string, number>();
  private strandedHandler?: (tabId: string) => void;
  private handler: WireHandler = () => {};
  private resetHandler?: ResetHandler;

  bind(handler: WireHandler) { this.handler = handler; }
  unbind(handler: WireHandler) { if (this.handler === handler) this.handler = () => {}; }
  bindReset(handler: ResetHandler) { this.resetHandler = handler; }
  unbindReset(handler: ResetHandler) { if (this.resetHandler === handler) this.resetHandler = undefined; }
  onPromptsStranded(handler?: (tabId: string) => void) { this.strandedHandler = handler; }
  // tl1739: whether a ledger replay is mid-flight on this tab. The wire layer
  // uses it to dispatch replayed text deltas ONE SEGMENT EACH instead of into
  // the shared rAF batch — a batch would merge a re-projected folded delta
  // with the next new delta under the max seq, so the watermark could no
  // longer drop the folded prefix and it re-streamed onto the painted bubble
  // (the 20261120 duplicate-render incident).
  isProjectingReplay(tabId: string): boolean {
    return this.projectingReplayByTab.has(tabId);
  }

  // Runs at every repair terminal that can leave prompt-kind events sitting in
  // the gap queue (failed/abandoned repair, page budget exhausted, epoch
  // mismatch early-return). Firing the handler triggers the backend prompt
  // replay, which re-presents the panel without depending on this projector.
  private reportStrandedPrompts(tabId: string): void {
    if (!this.strandedHandler) return;
    const queued = this.gapQueueByTab.get(tabId);
    if (!queued?.some(isPromptKindEvent)) return;
    this.strandedHandler(tabId);
  }

  release(tabId: string) {
    this.generationByTab.set(tabId, (this.generationByTab.get(tabId) ?? 0) + 1);
    this.sequenceByTab.delete(tabId);
    this.gapQueueByTab.delete(tabId);
    this.epochByTab.delete(tabId);
    this.preVoidCursorByTab.delete(tabId);
    this.projectingReplayByTab.delete(tabId);
    this.pendingRepairByTab.delete(tabId);
    this.repairByTab.delete(tabId);
  }

  // `replayAfter` is the backend's durable floor of events this webview has not
  // provably seen (Ledger.ProjectionCursor: turnStartSeq-1 while a turn is
  // live, latest once terminal/compacted). 任务675: seeding a fresh cursor at
  // `latest` when that floor is known is SELF-SEALING — the seed jumps past
  // turn-head events that were never delivered (session rebind while the
  // backend turn already runs, the one-click-analysis shape), and every later
  // observeRuntime sees latest == projected so the `latest > projected`
  // back-fill can never fire again. The floor outranks the active flag: an
  // idle session's floor IS latest (terminal/compacted), so trusting it costs
  // no replay on the paths the active gate used to protect.
  observeRuntime(tabId: string, runtimeEpoch: string | undefined, latest: number, replayAfter: number | undefined, _active = false) {
    if (runtimeEpoch && runtimeEpoch !== this.epochByTab.get(tabId)) {
      this.generationByTab.set(tabId, (this.generationByTab.get(tabId) ?? 0) + 1);
      this.epochByTab.set(tabId, runtimeEpoch);
      this.sequenceByTab.delete(tabId);
      this.gapQueueByTab.delete(tabId);
      // tl1739: a fresh authoritative seed from the snapshot supersedes the
      // pre-void hint — the snapshot's durable floor is the better truth.
      this.preVoidCursorByTab.delete(tabId);
      this.pendingRepairByTab.delete(tabId);
      this.repairByTab.delete(tabId);
    }
    let projected = this.sequenceByTab.get(tabId);
    if (projected === undefined) {
      projected = Math.min(replayAfter ?? latest, latest);
      this.sequenceByTab.set(tabId, projected);
    }
    if (latest > projected) this.requestReplay(tabId, projected, runtimeEpoch);
  }

  acceptLive(tabId: string, event: WireEvent, runtimeEpoch?: string): boolean {
    if (this.projectingReplayByTab.has(tabId)) return true;
    if (typeof event.seq !== "number" || event.seq <= 0) return true;
    const voided = !this.sequenceByTab.has(tabId);
    const last = this.sequenceByTab.get(tabId) ?? 0;
    if (event.seq <= last) return false;
    if (this.repairByTab.has(tabId) || event.seq > last + 1) {
      const queued = this.gapQueueByTab.get(tabId) ?? [];
      queued.push(event);
      this.gapQueueByTab.set(tabId, queued);
      if (!this.repairByTab.has(tabId)) {
        // tl1739: a voided cursor must not blind-start the repair at 0 — the
        // pre-void cursor is the floor of what the surface already folded.
        const repairFrom = voided ? (this.preVoidCursorByTab.get(tabId) ?? last) : last;
        this.requestReplay(tabId, repairFrom, event.runtimeEpoch ?? runtimeEpoch);
      }
      return false;
    }
    this.sequenceByTab.set(tabId, event.seq);
    return true;
  }

  // tl1653x: fence-anchor/projector epoch single-point alignment. The wire
  // fence anchor (runtimeEpochByTabRef) is re-armed by every meta refresh
  // (loadMetaForTab), but this projector's epochByTab only moves through
  // observeRuntime — which runs solely inside dispatchRuntimeStatusForTab. A
  // controller rebuild (resume, Diet/self-heal rewrite, effort switch) flips
  // the runtime epoch; the fence reconcile re-arms the ANCHOR, the next event
  // passes the fence into acceptLive, exposes its seq gap, and requests a
  // replay — which then early-returns at replayGap's epoch check because the
  // PROJECTOR still holds the old epoch (requestedEpoch=new vs currentEpoch=old).
  // Every reasoning/text delta dropped by the fence stays un-filled until a tab
  // switch reseeds the cursor — the "thinking block appears only after
  // switching tabs" report. Re-arming the anchor and the projector in the same
  // breath keeps the two epoch authorities from diverging; the teardown matches
  // observeRuntime's epoch-flip branch byte for byte (an epoch flip voids the
  // old epoch's cursor, queues, and in-flight repair).
  reanchorEpoch(tabId: string, runtimeEpoch: string | undefined) {
    if (!runtimeEpoch || runtimeEpoch === this.epochByTab.get(tabId)) return;
    // tl1739: preserve the folded truth across the void — the pre-void cursor
    // becomes the next repair's floor (see preVoidCursorByTab). Cleared on
    // release and on the next authoritative flip (observeRuntime), kept here:
    // this flip IS the one that created the void it describes.
    const preVoid = this.sequenceByTab.get(tabId);
    if (preVoid !== undefined) this.preVoidCursorByTab.set(tabId, preVoid);
    this.generationByTab.set(tabId, (this.generationByTab.get(tabId) ?? 0) + 1);
    this.epochByTab.set(tabId, runtimeEpoch);
    this.sequenceByTab.delete(tabId);
    this.gapQueueByTab.delete(tabId);
    this.pendingRepairByTab.delete(tabId);
    this.repairByTab.delete(tabId);
  }

  private requestReplay(tabId: string, afterSeq: number, runtimeEpoch?: string) {
    if (typeof app.TurnEventsForTab !== "function") return;
    if (this.repairByTab.has(tabId)) {
      this.pendingRepairByTab.set(tabId, { afterSeq, runtimeEpoch });
      return;
    }
    const generation = this.generationByTab.get(tabId) ?? 0;
    const repair = this.replayGap(tabId, afterSeq, runtimeEpoch, generation)
      .catch((error) => recordFrontendDiagnostic("runtime", "turn-events-gap-repair-failed", {
        afterSeq: this.sequenceByTab.get(tabId) ?? afterSeq,
        error: error instanceof Error ? error.message : String(error),
      }))
      .finally(() => {
        if (this.repairByTab.get(tabId) !== repair) return;
        this.repairByTab.delete(tabId);
        const pending = this.pendingRepairByTab.get(tabId);
        // 任务469: a repair that ended without draining the gap queue must not
        // strand a pending ask/approval panel behind a stalled sequence. Only
        // at the true terminal — a follow-up repair continuation gets first
        // crack at the queue.
        if (!pending) this.reportStrandedPrompts(tabId);
        if (!pending) return;
        this.pendingRepairByTab.delete(tabId);
        this.requestReplay(tabId, pending.afterSeq, pending.runtimeEpoch);
      });
    this.repairByTab.set(tabId, repair);
  }

  private async replayGap(tabId: string, afterSeq: number, requestedEpoch: string | undefined, generation: number) {
    let cursor = afterSeq;
    for (let page = 0; page < MAX_REPLAY_PAGES; page += 1) {
      if ((this.generationByTab.get(tabId) ?? 0) !== generation) return;
      const replay = await app.TurnEventsForTab!(tabId, cursor);
      if ((this.generationByTab.get(tabId) ?? 0) !== generation) return;
      const currentEpoch = this.epochByTab.get(tabId);
      if ((requestedEpoch && currentEpoch && requestedEpoch !== currentEpoch) ||
        (replay.runtimeEpoch && currentEpoch && replay.runtimeEpoch !== currentEpoch)) {
        // 任务469: this early return used to be fully silent — the repair gave
        // up and every queued event (prompt cards included) stayed stranded
        // with no trace and no recovery. Name the stall; the repair's finally
        // terminal reports stranded prompts (single reporting point).
        recordFrontendDiagnostic("runtime", "turn-events-gap-repair-epoch-mismatch", {
          afterSeq: cursor,
          requestedEpoch: requestedEpoch ?? "",
          currentEpoch: currentEpoch ?? "",
          replayEpoch: replay.runtimeEpoch ?? "",
        });
        return;
      }

      const envelopes = asArray(replay.events).slice().sort((a, b) => a.seq - b.seq);
      // tl1739: a repair that starts from a pre-void floor (or any blind high
      // guess) can land on a ledger whose numbering RESTARTED beneath it (new
      // session head, crash-analysis rebind): every envelope sits at or below
      // the floor and the page comes back empty. The empty page alone would
      // end the repair and drain nothing; the queued small-seq events would
      // then be dropped as duplicates of a numbering the surface never folded
      // (the 657 live-tail loss shape). Adjudicate on page 0: the ledger's
      // latest sitting strictly behind the requested position IS the restart
      // signal — drop the floor to the checkpoint boundary and re-fetch.
      if (page === 0 && !replay.resetRequired && envelopes.length === 0) {
        const restartFloor = Math.max(0, replay.floorSeq - 1);
        const behind = replay.latestSeq > 0
          ? replay.latestSeq < afterSeq
          : restartFloor < afterSeq;
        if (behind && restartFloor < cursor) {
          cursor = restartFloor;
          this.sequenceByTab.set(tabId, cursor);
          continue;
        }
      }

      if (replay.resetRequired) {
        if (!this.resetHandler || !(await this.resetHandler(tabId, replay))) {
          throw new Error("turn event checkpoint reset could not hydrate the transcript");
        }
        if ((this.generationByTab.get(tabId) ?? 0) !== generation) return;
        cursor = Math.max(0, replay.floorSeq - 1);
        this.sequenceByTab.set(tabId, cursor);
      }

      for (const envelope of envelopes) {
        if (envelope.seq <= cursor) continue;
        if (envelope.seq !== cursor + 1) throw new Error(`turn event replay gap after ${cursor}`);
        this.projectEnvelope(tabId, envelope, requestedEpoch);
        cursor = envelope.seq;
        this.sequenceByTab.set(tabId, cursor);
      }
      if (replay.hasMore) {
        const next = replay.nextAfterSeq;
        if (next !== cursor) throw new Error(`turn event replay cursor mismatch: projected ${cursor}, backend ${next}`);
        if (envelopes.length === 0) throw new Error("turn event replay made no progress");
        continue;
      }

      const pending = asArray(this.gapQueueByTab.get(tabId)).slice().sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
      this.gapQueueByTab.delete(tabId);
      const remaining: WireEvent[] = [];
      for (const live of pending) {
        if (typeof live.seq !== "number" || live.seq <= 0) {
          this.handler(live);
          continue;
        }
        if (live.seq <= cursor) continue;
        if (live.seq !== cursor + 1) {
          remaining.push(live);
          continue;
        }
        this.handler(live);
        cursor = live.seq;
        this.sequenceByTab.set(tabId, cursor);
      }
      if (remaining.length === 0) return;
      this.gapQueueByTab.set(tabId, remaining);
    }
    recordFrontendDiagnostic("runtime", "turn-events-gap-repair-incomplete", {
      afterSeq: this.sequenceByTab.get(tabId) ?? cursor,
    });
    this.reportStrandedPrompts(tabId);
  }

  private projectEnvelope(tabId: string, envelope: TurnEventEnvelope, runtimeEpoch?: string) {
    const durable = envelope?.event;
    if (!durable || typeof envelope.seq !== "number") return;
    this.projectingReplayByTab.add(tabId);
    try {
      this.handler({
        ...durable,
        turnId: envelope.turnId || durable.turnId,
        seq: envelope.seq,
        status: (envelope.status || durable.status) as WireEvent["status"],
        tabId,
        runtimeEpoch: envelope.runtimeEpoch ?? runtimeEpoch,
      });
    } finally {
      this.projectingReplayByTab.delete(tabId);
    }
  }
}
