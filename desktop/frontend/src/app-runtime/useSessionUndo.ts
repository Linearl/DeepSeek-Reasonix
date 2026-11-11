import { useState } from "react";
import { useCommittedCommand } from "../lib/useCommittedCommand";
import type { Item } from "../lib/useController";
import type { RewindUndoState } from "../lib/rewindTypes";
import type { RewindResultView } from "../lib/types";

export type SessionUndoInput = {
  activeTabId: string | undefined;
  activeTabReadOnly: boolean;
  items: readonly Item[];
  hydratePlaceholderActive: boolean;
  controllerReady: boolean;
  running: boolean;
  messageActionOpen: boolean;
  approvalOpen: boolean;
  askOpen: boolean;
  clearContextPending: boolean;
  // tl1544: the lab-switch state for the RUNNING-state edit entry. Off
  // (default) keeps the existing refuse-while-running behaviour byte-for-byte.
  editResendEnabled: boolean;
  ports: {
    rewindForTab(tabId: string, turn: number, scope: string): Promise<boolean>;
    rewindForTabDetailed(tabId: string, turn: number, scope: string): Promise<RewindResultView>;
    refreshTabMetas(): void;
    undoRewindForTab(tabId: string, transactionId: string): Promise<boolean>;
    sendToTab(tabId: string, display: string, submit: string, original: string): Promise<void>;
    // tl1544: the atomic edit-resend bridge (stop → treatment → fork → resend).
    editAndResendForTab(tabId: string, turn: number, display: string, submit: string, original: string): Promise<{ ok: boolean; error?: string; branch?: string }>;
    composeInsert(tabId: string, text: string): void;
    refreshDock(): void;
    refreshProject(): void;
  };
};

/**
 * Owns the undo/rewind lifecycle: per-tab rewind state and committing flags,
 * message-action rewinds (fork/code/summarize/full), edit-prompt rewinds and
 * the committed-session revert handler. The undo banner still reads
 * `rewindState`/`setRewindStateForTab` through this hook's return; only the
 * banner identity and its DOM live in the footer region.
 */
export function useSessionUndo(input: SessionUndoInput) {
  const { activeTabId, items, ports } = input;
  const [rewindStatesByTab, setRewindStatesByTab] = useState<Record<string, RewindUndoState>>({});
  const [rewindCommittingByTab, setRewindCommittingByTab] = useState<Record<string, boolean>>({});
  const [rewindSignal, setRewindSignal] = useState(0);

  const setRewindStateForTab = useCommittedCommand((tabId: string, nextState: RewindUndoState | null) => {
    if (!tabId) return;
    setRewindStatesByTab(current => {
      if (!nextState && !current[tabId]) return current;
      const next = { ...current };
      if (nextState) next[tabId] = nextState;
      else delete next[tabId];
      return next;
    });
  });

  const setRewindCommittingForTab = useCommittedCommand((tabId: string, committing: boolean) => {
    setRewindCommittingByTab((current) => {
      const next = { ...current };
      if (committing) next[tabId] = true;
      else delete next[tabId];
      return next;
    });
  });

  const bumpRewindSignal = useCommittedCommand(() => setRewindSignal((value) => value + 1));

  const handleSessionRevertCommitted = useCommittedCommand((sourceTabId: string, outcome: RewindResultView) => {
    if (!sourceTabId || !outcome.ok) return;
    setRewindStateForTab(sourceTabId, {
      turnDiff: 0,
      transactionId: outcome.transactionId,
      undoAvailable: outcome.undoAvailable,
      filesRestored: outcome.written ?? [],
      filesRemoved: outcome.deleted ?? [],
    });
    ports.refreshDock();
    ports.refreshProject();
  });

  const rewindState = activeTabId ? rewindStatesByTab[activeTabId] ?? null : null;
  const rewindCommitting = Boolean(activeTabId && rewindCommittingByTab[activeTabId]);

  const handleMessageAction = useCommittedCommand((turn: number, scope: string) => {
    const sourceTabId = activeTabId;
    if (!sourceTabId || input.activeTabReadOnly) return;
    if (input.hydratePlaceholderActive) return;
    if (scope === "fork") {
      // Fork still goes through the controller (not optimistic).
      ports.rewindForTab(sourceTabId, turn, scope).then((ok) => {
        if (!ok) return;
        ports.refreshTabMetas();
        ports.refreshProject();
      });
      return;
    }

    // Code-only rewind only affects files — no message truncation,
    // no optimistic UI needed.  Execute immediately.
    if (scope === "code") {
      setRewindCommittingForTab(sourceTabId, true);
      void ports.rewindForTabDetailed(sourceTabId, turn, scope).then((outcome) => {
        setRewindCommittingForTab(sourceTabId, false);
        if (!outcome.ok) return;
        setRewindStateForTab(sourceTabId, {
          turnDiff: 0,
          transactionId: outcome.transactionId,
          undoAvailable: outcome.undoAvailable,
          filesRestored: outcome.written ?? [],
          filesRemoved: outcome.deleted ?? [],
        });
        ports.refreshDock();
        ports.refreshProject();
      });
      return;
    }

    // Summarize only compresses the conversation log — no files touched,
    // no optimistic UI needed. Execute immediately like code-only rewind.
    if (scope === "summ-from" || scope === "summ-upto") {
      ports.rewindForTab(sourceTabId, turn, scope).then((ok) => {
        if (!ok) return;
        ports.refreshDock();
        ports.refreshProject();
      });
      return;
    }

    const hasCheckpointTurns = items.some((it) => it.kind === "user" && it.checkpointTurn != null);
    let boundaryIdx = -1;
    let userCount = 0;
    let targetUserCount = -1;
    for (let i = 0; i < items.length; i++) {
      if (items[i].kind === "user") {
        const item = items[i] as Extract<Item, { kind: "user" }>;
        const matches = hasCheckpointTurns ? item.checkpointTurn === turn : userCount === turn;
        if (matches) {
          boundaryIdx = i;
          targetUserCount = userCount;
          break;
        }
        userCount++;
      }
    }
    if (boundaryIdx < 0) {
      ports.rewindForTab(sourceTabId, turn, scope).then((ok) => {
        if (!ok) return;
        if (scope === "both") {
          ports.refreshDock();
          ports.refreshProject();
        }
      });
      return;
    }

    const prevUserCount = items.filter((it) => it.kind === "user").length;
    const turnDiff = prevUserCount - targetUserCount;
    const userItem = items[boundaryIdx]?.kind === "user" ? items[boundaryIdx] as Extract<Item, { kind: "user" }> : undefined;
    const prompt = userItem?.text ?? "";

    // Immediate backend commit — only update UI after success.
    setRewindCommittingForTab(sourceTabId, true);
    void ports.rewindForTabDetailed(sourceTabId, turn, scope).then((outcome) => {
      setRewindCommittingForTab(sourceTabId, false);
      if (!outcome.ok) {
        // Keep conversation/files as-is; notices already carry the reason.
        return;
      }
      const targetTabId = outcome.tabId || sourceTabId;
      setRewindStateForTab(targetTabId, {
        turnDiff: outcome.tabId ? 0 : turnDiff,
        transactionId: outcome.transactionId,
        undoAvailable: outcome.undoAvailable,
        undoTabId: sourceTabId,
        filesRestored: outcome.written ?? [],
        filesRemoved: outcome.deleted ?? [],
      });
      ports.composeInsert(targetTabId, prompt);
      bumpRewindSignal();
      if (scope === "both" || scope === "code") {
        ports.refreshDock();
        ports.refreshProject();
      }
    });
  });

  const handleUndoRewind = useCommittedCommand(() => {
    const tabId = activeTabId;
    const state = rewindState;
    if (!tabId || !state) return;
    const tx = state.transactionId;
    const undoTabId = state.undoTabId || tabId;
    const undo = tx && state.undoAvailable ? ports.undoRewindForTab(undoTabId, tx) : Promise.resolve(true);
    void undo.then((ok) => {
      if (!ok) return;
      setRewindStateForTab(tabId, null);
      ports.composeInsert(tabId, "");
      bumpRewindSignal();
      ports.refreshDock();
      ports.refreshProject();
    });
  });

  const handleEditPrompt = useCommittedCommand(async (turn: number, displayText: string, submitText?: string): Promise<boolean> => {
    const sourceTabId = activeTabId;
    // tl1544: with the lab switch ON the running gate opens into the atomic
    // edit-resend (stop + treatment + fork-at-turn + resend in one call).
    // Every other gate keeps its meaning; with the switch OFF this branch is
    // unreachable and the behaviour below is byte-for-byte the old chain.
    if (input.running) {
      if (!sourceTabId || input.editResendEnabled !== true || input.activeTabReadOnly
        || !input.controllerReady || input.hydratePlaceholderActive
        || input.messageActionOpen || input.approvalOpen || input.askOpen
        || input.clearContextPending) return false;
      const nextRunning = displayText.trim();
      if (!nextRunning) return false;
      const submitRunning = (submitText ?? displayText).trim();
      const originalRunning = originalPromptForTurn(turn);
      if (!originalRunning) return false;
      try {
        const outcome = await ports.editAndResendForTab(sourceTabId, turn, nextRunning, submitRunning, originalRunning);
        if (!outcome.ok) return false;
        bumpRewindSignal();
        ports.refreshDock();
        ports.refreshProject();
        return true;
      } catch {
        return false;
      }
    }
    if (!sourceTabId || input.activeTabReadOnly || !input.controllerReady || input.hydratePlaceholderActive
      || rewindStatesByTab[sourceTabId] || input.messageActionOpen
      || input.approvalOpen || input.askOpen || input.clearContextPending) return false;
    const next = displayText.trim();
    if (!next) return false;
    const submit = (submitText ?? displayText).trim();
    // tl1544 parity note: the idle chain keeps its original shape — an empty
    // reverse-lookup result flows through exactly as before (no new gate).
    const original = originalPromptForTurn(turn);
    const outcome = await ports.rewindForTabDetailed(sourceTabId, turn, "conversation");
    if (!outcome.ok) return false;
    bumpRewindSignal();
    const targetTabId = outcome.tabId || sourceTabId;
    try {
      await ports.sendToTab(targetTabId, next, submit, original);
      return true;
    } catch {
      return false;
    }
  });

  // tl1544 (shared): resolve the historical prompt text for a checkpoint turn
  // from the transcript items — the same reverse mapping the rewind picker
  // uses (CheckpointTurnsByMessageIndex on the Go side).
  function originalPromptForTurn(turn: number): string {
    const hasCheckpointTurns = items.some((it) => it.kind === "user" && it.checkpointTurn != null);
    let original = "";
    let userCount = 0;
    for (const item of items) {
      if (item.kind !== "user") continue;
      const matches = hasCheckpointTurns ? item.checkpointTurn === turn : userCount === turn;
      if (matches) {
        original = (item.submitText ?? item.text).trim();
        break;
      }
      userCount++;
    }
    return original;
  }

  return {
    rewindState,
    rewindCommitting,
    rewindSignal,
    setRewindStateForTab,
    setRewindCommittingForTab,
    bumpRewindSignal,
    handleSessionRevertCommitted,
    handleMessageAction,
    handleUndoRewind,
    handleEditPrompt,
  };
}
