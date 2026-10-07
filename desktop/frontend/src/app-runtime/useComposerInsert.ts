// Owner of the composer-bound insertion channels (task 38 batch B1). The four
// request states, their active-tab projections and every command that writes
// them live here so the monolith consumes one API instead of reaching into
// per-tab state maps. Fork-owned on purpose: the upstream twin
// (useComposerInsertCommands) routes terminal-output insertion through the
// session operations authority, which App.tsx does not run yet — wiring that
// in would change behavior, so this owner keeps the direct bridge port and
// revisits when the operations pipeline lands.
import { useCallback, useRef, useState } from "react";
import { formatTerminalOutputForComposer } from "../lib/terminalOutput";
import { formatSelectionReference, type SelectedTextInsertRequest } from "../lib/selectedTextContext";
import type { ComposerInsertRequest } from "../lib/types";
import type { Translator } from "../lib/i18n";
// Type home stays with the upstream file until the two owners consolidate.
import type { WorkspaceInsertTarget } from "./useComposerInsertCommands";

export type ComposerInsertOwnerInput = {
  activeTabId: string | undefined;
  approval: { id: string; tool: string } | undefined | null;
  t: Translator;
  showToast: (message: string, kind: "info" | "warn" | "error") => void;
  ports: {
    terminalOutput(tabId: string, sessionId: string): Promise<string>;
  };
};

export function useComposerInsertOwner(input: ComposerInsertOwnerInput) {
  const { activeTabId, approval, t, showToast, ports } = input;
  const [composerInsertRequestsByTab, setComposerInsertRequestsByTab] = useState<Record<string, ComposerInsertRequest>>({});
  const [selectedTextRequestsByTab, setSelectedTextRequestsByTab] = useState<Record<string, SelectedTextInsertRequest>>({});
  const selectedTextRequestIdRef = useRef(0);
  const [planRevisionInsertRequest, setPlanRevisionInsertRequest] = useState<{
    tabId: string;
    approvalId: string;
    request: ComposerInsertRequest;
  } | null>(null);
  const [workspaceInsertTarget, setWorkspaceInsertTarget] = useState<WorkspaceInsertTarget>("composer");

  const activePlanRevisionInsertRequest =
    planRevisionInsertRequest &&
    planRevisionInsertRequest.tabId === activeTabId &&
    planRevisionInsertRequest.approvalId === approval?.id
      ? planRevisionInsertRequest.request
      : null;
  const composerInsertRequest = activeTabId ? composerInsertRequestsByTab[activeTabId] ?? null : null;
  const selectedTextRequest = activeTabId ? selectedTextRequestsByTab[activeTabId] ?? null : null;

  const setInsertTarget = useCallback((target: WorkspaceInsertTarget) => setWorkspaceInsertTarget(target), []);
  const handleRevisionActiveChange = useCallback((active: boolean) => {
    setWorkspaceInsertTarget(active ? "planRevision" : "composer");
  }, []);
  // Mode "replace" is the rewind channel: undoing a rewind restores the
  // pre-rewind prompt and an edit-prompt flow swaps in the edited text. The
  // useSessionUndo composeInsert port binds to this single API (task 38 R1).
  const replaceComposerInsert = useCallback((tabId: string, text: string) => {
    setComposerInsertRequestsByTab((current) => ({ ...current, [tabId]: { id: Date.now(), text, mode: "replace" } }));
  }, []);
  const prefillSubagentCommand = useCallback((command: string) => {
    if (!activeTabId) return;
    setComposerInsertRequestsByTab((current) => ({
      ...current,
      [activeTabId]: { id: Date.now(), text: command, mode: "prefix" },
    }));
  }, [activeTabId]);
  // Quick-command snippets (#18) insert at the caret; the user still sends.
  const insertQuickCommand = useCallback((text: string) => {
    if (!activeTabId || !text) return;
    setComposerInsertRequestsByTab((current) => ({
      ...current,
      [activeTabId]: { id: Date.now(), text, mode: "insert" },
    }));
  }, [activeTabId]);

  const addWorkspaceTextToComposer = useCallback((text: string) => {
    if (activeTabId && workspaceInsertTarget === "planRevision" && approval?.tool === "exit_plan_mode") {
      setPlanRevisionInsertRequest({
        tabId: activeTabId,
        approvalId: approval.id,
        request: { id: Date.now(), text },
      });
      return;
    }
    if (activeTabId) {
      setComposerInsertRequestsByTab((current) => ({
        ...current,
        [activeTabId]: { id: Date.now(), text },
      }));
    }
  }, [activeTabId, approval, workspaceInsertTarget]);

  const addTerminalOutputToComposer = useCallback(async (sessionId: string) => {
    if (!activeTabId) return;
    try {
      const output = await ports.terminalOutput(activeTabId, sessionId);
      const formatted = formatTerminalOutputForComposer(output);
      if (!formatted) {
        showToast(t("terminal.noOutput"), "info");
        return;
      }
      addWorkspaceTextToComposer(formatted);
    } catch (error) {
      showToast(error instanceof Error ? error.message : String(error), "error");
    }
  }, [activeTabId, addWorkspaceTextToComposer, ports, showToast, t]);

  const addSelectedTextToComposer = useCallback((text: string, source?: SelectedTextInsertRequest["source"]) => {
    const selected = text.trim();
    if (!activeTabId || !selected) return;
    selectedTextRequestIdRef.current += 1;
    setSelectedTextRequestsByTab((current) => ({
      ...current,
      [activeTabId]: { id: selectedTextRequestIdRef.current, text: selected, ...(source ? { source } : {}) },
    }));
  }, [activeTabId]);

  const addTerminalSelectionToComposer = useCallback((text: string) => addSelectedTextToComposer(text, "terminal"), [addSelectedTextToComposer]);

  const addWorkspaceCodeToComposer = useCallback((path: string, code: string) => {
    if (!activeTabId || !code.trim()) return;
    if (workspaceInsertTarget === "planRevision" && approval?.tool === "exit_plan_mode") {
      // The plan-revision input is plain text and only consumes request.text,
      // so hand it the fenced rendering instead of a structured reference.
      setPlanRevisionInsertRequest({
        tabId: activeTabId,
        approvalId: approval.id,
        request: { id: Date.now(), text: formatSelectionReference(path, code) },
      });
      return;
    }
    selectedTextRequestIdRef.current += 1;
    setSelectedTextRequestsByTab((current) => ({
      ...current,
      [activeTabId]: { id: selectedTextRequestIdRef.current, text: code, path },
    }));
  }, [activeTabId, approval, workspaceInsertTarget]);

  return {
    activePlanRevisionInsertRequest,
    composerInsertRequest,
    selectedTextRequest,
    setInsertTarget,
    handleRevisionActiveChange,
    replaceComposerInsert,
    prefillSubagentCommand,
    insertQuickCommand,
    addWorkspaceTextToComposer,
    addTerminalOutputToComposer,
    addSelectedTextToComposer,
    addTerminalSelectionToComposer,
    addWorkspaceCodeToComposer,
  };
}
