import { useCallback, useEffect, useLayoutEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore } from "react";
import { createPortal } from "react-dom";
import { MessageSquare, Languages, Pin, Sparkles, X } from "lucide-react";
import { useSelectionActionsEnabled } from "../lib/selectionActionsPreference";
import { ContextMenu, type ContextMenuPoint } from "./ContextMenu";
import { messageSelectionContextText, TRANSCRIPT_COPY_FAILED_EVENT } from "../lib/messageSelectionCopy";
import { writeClipboardText } from "../lib/clipboard";
import {
  detectShortcutPlatform,
  formatShortcutCombo,
  onShortcutsChanged,
  resolvedShortcutCombo,
  useGlobalShortcut,
} from "../lib/keyboardShortcuts";
import { useT } from "../lib/i18n";
import { transcriptSelectionStore } from "../lib/transcriptSelectionStore";
import { rowKeyForNode, transcriptSelectionPointClientRect } from "../lib/transcriptSelectionDom";
import { useToast } from "../lib/toast";

// Vite loads this beside the already-lazy selection menu chunk. The direct
// tsx unit-test runner has no CSS loader, so it deliberately leaves MODE unset.
if (import.meta.env?.MODE) void import("./TranscriptSelectionMenu.css");

type SelectionAction =
  | { kind: "native"; text: string; point: ContextMenuPoint }
  | { kind: "logical"; snapshotId: number; sourceTabId: string; point: ContextMenuPoint };

const ACTION_EDGE_GAP = 8;

type SelectionActionOverlayState = {
  phase: "closed" | "positioning" | "open";
  action: SelectionAction | null;
  point: ContextMenuPoint;
  revision: number;
};

type SelectionActionOverlayEvent =
  | { type: "show"; action: SelectionAction }
  | { type: "positioned"; point: ContextMenuPoint; revision: number }
  | { type: "close" }
  | { type: "reset" };

const INITIAL_ACTION_OVERLAY_STATE: SelectionActionOverlayState = {
  phase: "closed",
  action: null,
  point: { left: -10_000, top: -10_000 },
  revision: 0,
};

function selectionActionOverlayReducer(
  state: SelectionActionOverlayState,
  event: SelectionActionOverlayEvent,
): SelectionActionOverlayState {
  switch (event.type) {
    case "show":
      return {
        ...state,
        phase: "positioning",
        action: event.action,
        revision: state.revision + 1,
      };
    case "positioned":
      if (state.phase !== "positioning" || state.revision !== event.revision || !state.action) return state;
      return { ...state, phase: "open", point: event.point };
    case "close":
    case "reset":
      if (!state.action) return state;
      // Keep the last painted coordinates while hidden. Moving a detached or
      // transparent fixed layer during native selection churn is one of the
      // WebView2 stale-pixel triggers this stable host avoids.
      return { ...state, phase: "closed", action: null };
  }
}

export function TranscriptSelectionMenu({
  enabled = true,
  resetKey,
  onAddToChat,
  onQuickAction,
  quickActionsEnabled: quickActionsEnabledProp,
}: {
  enabled?: boolean;
  resetKey?: string | number;
  onAddToChat?: (text: string) => void;
  /** Task 369: runs the one-shot side query; result renders in the floating card. */
  onQuickAction?: (action: string, text: string, contextText: string) => Promise<string>;
  /** Task 369: experimental_selection_actions gate (store-fed; prop overrides for tests). */
  quickActionsEnabled?: boolean;
}) {
  const t = useT();
  const { showToast } = useToast();
  const logicalSnapshot = useSyncExternalStore(
    transcriptSelectionStore.subscribe,
    transcriptSelectionStore.getSnapshot,
    transcriptSelectionStore.getSnapshot,
  );
  const [menu, setMenu] = useState<SelectionAction | null>(null);
  const [actionOverlay, dispatchActionOverlay] = useReducer(
    selectionActionOverlayReducer,
    INITIAL_ACTION_OVERLAY_STATE,
  );
  const action = actionOverlay.action;
  const actionOverlayStateRef = useRef(actionOverlay);
  actionOverlayStateRef.current = actionOverlay;
  const actionRef = useRef<HTMLDivElement>(null);
  const dismissedRef = useRef<string | number | null>(null);
  const previousResetKeyRef = useRef(resetKey);
  const activeResetKeyRef = useRef(resetKey);
  activeResetKeyRef.current = resetKey;
  const shortcutPlatform = useMemo(() => detectShortcutPlatform(), []);
  const [shortcutRevision, setShortcutRevision] = useState(0);
  useEffect(() => onShortcutsChanged(() => setShortcutRevision((value) => value + 1)), []);

  const closeAction = useCallback(() => {
    dispatchActionOverlay({ type: "close" });
  }, []);

  const showAction = useCallback((nextAction: SelectionAction) => {
    dispatchActionOverlay({ type: "show", action: nextAction });
  }, []);

  const resetAction = useCallback(() => {
    dispatchActionOverlay({ type: "reset" });
  }, []);

  const reportCopyFailure = useCallback(() => {
    showToast(t("diag.copyFailed"), "error");
  }, [showToast, t]);

  useEffect(() => {
    const onFailure = () => reportCopyFailure();
    document.addEventListener(TRANSCRIPT_COPY_FAILED_EVENT, onFailure);
    return () => document.removeEventListener(TRANSCRIPT_COPY_FAILED_EVENT, onFailure);
  }, [reportCopyFailure]);

  useEffect(() => {
    if (previousResetKeyRef.current === resetKey) return;
    previousResetKeyRef.current = resetKey;
    dismissedRef.current = null;
    setMenu(null);
    resetAction();
    document.getSelection()?.removeAllRanges();
    transcriptSelectionStore.clear("tab-switch");
  }, [resetAction, resetKey]);

  const resolveLogical = useCallback(async (selection: Extract<SelectionAction, { kind: "logical" }>) => {
    const text = await transcriptSelectionStore.resolveText(selection.snapshotId);
    if (
      !text
      || !transcriptSelectionStore.isCurrent(selection.snapshotId, selection.sourceTabId)
      || String(activeResetKeyRef.current ?? "") !== selection.sourceTabId
    ) return null;
    return text;
  }, []);
  // Task 369: selection quick-actions (translate/explain) behind the
  // experimental_selection_actions switch — off = byte-identical legacy menu.
  const quickActionsEnabledStore = useSelectionActionsEnabled();
  const quickActionsEnabled = quickActionsEnabledProp ?? quickActionsEnabledStore;
  const [quickAction, setQuickAction] = useState<"translate" | "explain" | null>(null);
  const [quickResult, setQuickResult] = useState("");
  const [quickError, setQuickError] = useState(false);
  const [quickPinned, setQuickPinned] = useState(false);
  const quickActionRef = useRef<((action: string, text: string, contextText: string) => Promise<string>) | null>(null);
  const addShortcut = useMemo(
    () => formatShortcutCombo(resolvedShortcutCombo("selection.addToChat", shortcutPlatform), shortcutPlatform),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [shortcutPlatform, shortcutRevision],
  );

  const QUICK_TEXT_CAP = 9000; // ~3k tokens CJK-heavy; longer selections truncate
  const QUICK_CONTEXT_CAP = 3000; // ~3k CJK tokens (task 369 spec)

  const runQuickAction = useCallback(async (kind: "translate" | "explain", selection: SelectionAction) => {
    const text = selection.kind === "native"
      ? selection.text
      : await resolveLogical(selection);
    if (!text) {
      // Task 514: a selection that no longer resolves (logical snapshot
      // cleared or superseded between toolbar-open and the click) used to
      // end here silently — the click did nothing at all ("clicked, no
      // floating window", the last silent path of the 369 chain). Every
      // quick-action click must land in a visible card; fail visibly.
      setQuickAction(kind);
      setQuickError(true);
      setQuickResult(t("selection.quickAction.unavailable"));
      return;
    }
    const truncated = text.length > QUICK_TEXT_CAP;
    const body = truncated ? text.slice(0, QUICK_TEXT_CAP) : text;
    // Neighbor context: the selection's own row text already rides in `body`;
    // the surrounding-window scrape is DOM-local and stays within the cap.
    let contextText = "";
    try {
      const container = actionRef.current?.closest(".transcript") ?? document.querySelector("[data-surface=\"transcript\"]");
      contextText = truncated ? "" : (container?.textContent ?? "").slice(0, QUICK_CONTEXT_CAP);
    } catch { /* context scrape is best-effort */ }
    setQuickAction(kind);
    setQuickResult("");
    setQuickError(false);
    try {
      const runner = onQuickAction ?? quickActionRef.current ?? (async () => "");
      const result = await runner(kind, body, contextText);
      let next = result || t("selection.quickAction.empty");
      if (truncated) next = next + "\n\n" + t("selection.quickAction.truncated");
      setQuickResult(next);
      if (quickError) setQuickError(false);
    } catch {
      setQuickError(true);
      setQuickResult(t("selection.quickAction.failed"));
    }
  }, [onQuickAction, resolveLogical, t]);

  const closeQuickCard = useCallback(() => {
    setQuickAction(null);
    setQuickResult("");
    setQuickError(false);
    setQuickPinned(false);
  }, []);

  useEffect(() => {
    if (quickAction == null) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") { closeQuickCard(); closeAction(); }
    };
    const onDown = (event: MouseEvent) => {
      if (quickPinned) return;
      if (actionRef.current?.contains(event.target as Node)) return;
      if ((event.target as HTMLElement)?.closest?.(".transcript-selection-result-card")) return;
      closeQuickCard();
    };
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onDown);
    return () => {
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onDown);
    };
  }, [quickAction, quickPinned, closeQuickCard, closeAction]);

  const copySelection = useCallback(async (selection: SelectionAction) => {
    if (selection.kind === "native") {
      const native = document.getSelection();
      const rangeSnapshot = native && !native.isCollapsed ? {
        anchorNode: native.anchorNode,
        anchorOffset: native.anchorOffset,
        focusNode: native.focusNode,
        focusOffset: native.focusOffset,
      } : null;
      const copied = await writeClipboardText(selection.text);
      const current = document.getSelection();
      if (!copied) {
        reportCopyFailure();
        return;
      }
      if (
        rangeSnapshot
        && current
        && !current.isCollapsed
        && current.anchorNode === rangeSnapshot.anchorNode
        && current.anchorOffset === rangeSnapshot.anchorOffset
        && current.focusNode === rangeSnapshot.focusNode
        && current.focusOffset === rangeSnapshot.focusOffset
      ) current.removeAllRanges();
      return;
    }
    const text = await resolveLogical(selection);
    if (!text) return;
    const copied = await writeClipboardText(text);
    if (!transcriptSelectionStore.isCurrent(selection.snapshotId, selection.sourceTabId)) return;
    if (!copied) {
      reportCopyFailure();
      return;
    }
    transcriptSelectionStore.clear("copy");
    closeAction();
  }, [closeAction, reportCopyFailure, resolveLogical]);

  const addSelectionToChat = useCallback(async () => {
    if (!action || !onAddToChat) return;
    if (action.kind === "native") {
      document.getSelection()?.removeAllRanges();
      closeAction();
      onAddToChat(action.text);
      return;
    }
    const text = await resolveLogical(action);
    if (!text) return;
    transcriptSelectionStore.clear("add-to-chat");
    closeAction();
    onAddToChat(text);
  }, [action, closeAction, onAddToChat, resolveLogical]);

  useGlobalShortcut(
    "selection.addToChat",
    () => { void addSelectionToChat(); },
    [],
    Boolean(action) && enabled && Boolean(onAddToChat),
  );

  useLayoutEffect(() => {
    if (actionOverlay.phase !== "positioning" || !action) return;
    const revision = actionOverlay.revision;
    const rect = actionRef.current?.getBoundingClientRect();
    if (!rect) {
      dispatchActionOverlay({ type: "positioned", point: action.point, revision });
      return;
    }
    dispatchActionOverlay({
      type: "positioned",
      revision,
      point: {
        left: Math.min(
          Math.max(ACTION_EDGE_GAP, action.point.left),
          Math.max(ACTION_EDGE_GAP, window.innerWidth - rect.width - ACTION_EDGE_GAP),
        ),
        top: Math.min(
          Math.max(ACTION_EDGE_GAP, action.point.top),
          Math.max(ACTION_EDGE_GAP, window.innerHeight - rect.height - ACTION_EDGE_GAP),
        ),
      },
    });
  }, [action, actionOverlay.phase, actionOverlay.revision]);

  useEffect(() => {
    if (!enabled || !onAddToChat || logicalSnapshot.mode !== "logical-settled") {
      if (action?.kind === "logical") closeAction();
      return;
    }
    if (logicalSnapshot.tabId !== String(resetKey ?? "") || !logicalSnapshot.focus) return;
    if (dismissedRef.current === logicalSnapshot.id) return;
    const rect = transcriptSelectionPointClientRect(logicalSnapshot.focus);
    showAction({
      kind: "logical",
      snapshotId: logicalSnapshot.id,
      sourceTabId: logicalSnapshot.tabId,
      point: rect ? { left: rect.right, top: rect.bottom + 8 } : { left: 12, top: 12 },
    });
  }, [action?.kind, closeAction, enabled, logicalSnapshot, onAddToChat, resetKey, showAction]);

  useEffect(() => {
    const onContextMenu = (event: MouseEvent) => {
      if (!enabled || typeof window === "undefined" || !window.runtime) return;
      const snapshot = transcriptSelectionStore.getSnapshot();
      const rowKey = rowKeyForNode(event.target instanceof Node ? event.target : null);
      if (
        rowKey
        && (snapshot.mode === "logical-dragging" || snapshot.mode === "logical-settled")
        && transcriptSelectionStore.isRowSelected(snapshot.id, rowKey)
      ) {
        event.preventDefault();
        setMenu({
          kind: "logical",
          snapshotId: snapshot.id,
          sourceTabId: snapshot.tabId,
          point: menuPointFromEvent(event),
        });
        return;
      }
      const selected = messageSelectionContextText(document, event.target);
      if (selected == null) return;
      event.preventDefault();
      setMenu({ kind: "native", text: selected, point: menuPointFromEvent(event) });
    };
    document.addEventListener("contextmenu", onContextMenu);
    return () => document.removeEventListener("contextmenu", onContextMenu);
  }, [enabled]);

  useEffect(() => {
    if (enabled && onAddToChat) return;
    setMenu(null);
    resetAction();
    document.getSelection()?.removeAllRanges();
    transcriptSelectionStore.clear("selection-actions-disabled");
  }, [enabled, onAddToChat, resetAction]);

  useEffect(() => {
    if (!enabled || !onAddToChat) return;
    let frame: number | null = null;
    const showForTarget = (target: EventTarget | null) => {
      if (transcriptSelectionStore.isLogical()) return;
      const selected = messageSelectionContextText(document, target);
      const selection = document.getSelection();
      const range = selection?.rangeCount ? selection.getRangeAt(selection.rangeCount - 1) : null;
      if (selected == null || !range) {
        dismissedRef.current = null;
        if (actionOverlayStateRef.current.action?.kind === "native") closeAction();
        return;
      }
      if (dismissedRef.current === selected) return;
      dismissedRef.current = null;
      const rect = typeof range.getBoundingClientRect === "function" ? range.getBoundingClientRect() : null;
      showAction({
        kind: "native",
        text: selected,
        point: rect && (rect.width > 0 || rect.height > 0)
          ? { left: rect.right, top: rect.bottom + 8 }
          : { left: 12, top: 12 },
      });
    };
    const scheduleShow = (target: EventTarget | null) => {
      if (frame !== null) cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        frame = null;
        showForTarget(target);
      });
    };
    const onPointerUp = (event: PointerEvent) => {
      if (event.button !== 0) return;
      dismissedRef.current = null;
      scheduleShow(event.target);
    };
    const onPointerDown = (event: PointerEvent) => {
      if (event.button !== 0) return;
      const target = event.target instanceof Element ? event.target : null;
      if (target?.closest(".transcript-selection-action")) return;
      const snapshot = transcriptSelectionStore.getSnapshot();
      if (snapshot.mode === "logical-dragging" || snapshot.mode === "logical-settled") {
        transcriptSelectionStore.clear("new-pointer");
      }
      const selection = document.getSelection();
      if (selection && !selection.isCollapsed) selection.removeAllRanges();
      dismissedRef.current = null;
      closeAction();
    };
    const onKeyUp = (event: KeyboardEvent) => {
      const selection = document.getSelection();
      const target = selection?.focusNode instanceof Element
        ? selection.focusNode
        : selection?.focusNode?.parentElement ?? event.target;
      scheduleShow(target);
    };
    const onSelectionChange = () => {
      if (transcriptSelectionStore.isLogical()) return;
      const selection = document.getSelection();
      if (!selection || selection.isCollapsed || selection.toString().trim() === "") {
        dismissedRef.current = null;
        if (actionOverlayStateRef.current.action?.kind === "native") closeAction();
      }
    };
    const onKeyDown = (event: KeyboardEvent) => {
      const currentAction = actionOverlayStateRef.current.action;
      if (event.key !== "Escape" || !currentAction) return;
      dismissedRef.current = currentAction.kind === "native" ? currentAction.text : currentAction.snapshotId;
      if (currentAction.kind === "logical") transcriptSelectionStore.clear("escape");
      closeAction();
    };
    const closeNative = () => {
      if (actionOverlayStateRef.current.action?.kind === "native") closeAction();
    };
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("pointerup", onPointerUp);
    document.addEventListener("keyup", onKeyUp);
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("selectionchange", onSelectionChange);
    window.addEventListener("resize", closeNative);
    window.addEventListener("scroll", closeNative, true);
    return () => {
      if (frame !== null) cancelAnimationFrame(frame);
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("pointerup", onPointerUp);
      document.removeEventListener("keyup", onKeyUp);
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("selectionchange", onSelectionChange);
      window.removeEventListener("resize", closeNative);
      window.removeEventListener("scroll", closeNative, true);
    };
  }, [closeAction, enabled, onAddToChat, showAction]);

  return <>
    <ContextMenu
      open={menu != null}
      point={menu?.point ?? null}
      minWidth={140}
      ariaLabel={t("common.copy")}
      items={[{
        key: "copy",
        label: t("common.copy"),
        shortcut: formatShortcutCombo(
          shortcutPlatform === "darwin" ? { key: "c", meta: true } : { key: "c", ctrl: true },
          shortcutPlatform,
        ),
        onSelect: () => {
          if (menu) void copySelection(menu);
          setMenu(null);
        },
      },
      ...(quickActionsEnabled && onQuickAction ? [
        {
          key: "quick-translate",
          label: t("selection.quickAction.translate"),
          onSelect: () => {
            if (menu) void runQuickAction("translate", menu);
            setMenu(null);
          },
        },
        {
          key: "quick-explain",
          label: t("selection.quickAction.explain"),
          onSelect: () => {
            if (menu) void runQuickAction("explain", menu);
            setMenu(null);
          },
        },
      ] : [])]}
      onClose={() => setMenu(null)}
    />
    {typeof document !== "undefined" && createPortal(
      <div
        ref={actionRef}
        className="transcript-selection-action"
        data-surface="transcript"
        data-state={actionOverlay.phase}
        role="toolbar"
        aria-hidden={actionOverlay.phase !== "open"}
        aria-label={t("selection.actions")}
        style={{
          left: actionOverlay.point.left,
          top: actionOverlay.point.top,
          visibility: actionOverlay.phase === "open" ? "visible" : "hidden",
          opacity: actionOverlay.phase === "open" ? 1 : 0,
          pointerEvents: actionOverlay.phase === "open" ? "auto" : "none",
          animation: actionOverlay.phase === "open" ? undefined : "none",
        }}
        onMouseDown={(event) => event.preventDefault()}
      >
        <button
          type="button"
          disabled={actionOverlay.phase !== "open"}
          tabIndex={actionOverlay.phase === "open" ? 0 : -1}
          onClick={() => void addSelectionToChat()}
        >
          <MessageSquare size={14} aria-hidden="true" />
          <span>{t("selection.addToChat")}</span>
          <kbd>{addShortcut}</kbd>
        </button>
        {quickActionsEnabled && onQuickAction && (
          <>
            <button
              type="button"
              disabled={actionOverlay.phase !== "open"}
              tabIndex={actionOverlay.phase === "open" ? 0 : -1}
              onClick={() => {
                if (actionOverlay.action) void runQuickAction("translate", actionOverlay.action);
              }}
            >
              <Languages size={14} aria-hidden="true" />
              <span>{t("selection.quickAction.translate")}</span>
            </button>
            <button
              type="button"
              disabled={actionOverlay.phase !== "open"}
              tabIndex={actionOverlay.phase === "open" ? 0 : -1}
              onClick={() => {
                if (actionOverlay.action) void runQuickAction("explain", actionOverlay.action);
              }}
            >
              <Sparkles size={14} aria-hidden="true" />
              <span>{t("selection.quickAction.explain")}</span>
            </button>
          </>
        )}
      </div>,
      document.body,
    )}
    {/*
     * Task 369: the floating result card — action title, pin, close, streamed
     * content region. Dismisses on click-away / new selection / Escape; the
     * pin only keeps the card on screen, it never changes the discard
     * semantics (close = drop; reopening re-requests).
     */}
    {quickAction != null && createPortal(
      <div
        className="transcript-selection-result-card"
        data-pinned={quickPinned ? "true" : undefined}
        role="dialog"
        aria-label={t(quickAction === "translate" ? "selection.quickAction.translate" : "selection.quickAction.explain")}
        style={{
          left: actionOverlay.point.left,
          top: actionOverlay.point.top,
        }}
      >
        <div className="transcript-selection-result-card__head">
          <span className="transcript-selection-result-card__title">
            {t(quickAction === "translate" ? "selection.quickAction.translate" : "selection.quickAction.explain")}
          </span>
          <button
            type="button"
            className="transcript-selection-result-card__pin"
            aria-pressed={quickPinned}
            aria-label={t("selection.quickAction.pin")}
            onClick={() => setQuickPinned((value) => !value)}
          >
            <Pin size={13} aria-hidden="true" />
          </button>
          <button
            type="button"
            className="transcript-selection-result-card__close"
            aria-label={t("common.close")}
            onClick={closeQuickCard}
          >
            <X size={13} aria-hidden="true" />
          </button>
        </div>
        <div className="transcript-selection-result-card__body" data-state={quickError ? "error" : quickResult ? "done" : "loading"}>
          {quickError
            ? quickResult
            : quickResult || t("selection.quickAction.thinking")}
        </div>
      </div>,
      document.body,
    )}
  </>;
}

function menuPointFromEvent(event: MouseEvent): ContextMenuPoint {
  if (event.clientX > 0 || event.clientY > 0) return { left: event.clientX, top: event.clientY };
  const range = document.getSelection()?.rangeCount ? document.getSelection()?.getRangeAt(0) : null;
  const rect = typeof range?.getBoundingClientRect === "function" ? range.getBoundingClientRect() : null;
  if (rect && (rect.width > 0 || rect.height > 0)) return { left: rect.left, top: rect.bottom + 4 };
  return { left: 12, top: 12 };
}
