import {
  lazy,
  Suspense,
  useCallback,
  useDeferredValue,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type CSSProperties,
} from "react";
import { ArrowDown, Loader2 } from "lucide-react";
const ToolRecoveryPanel = lazy(() => import("./ToolRecoveryPanel").then(m => ({ default: m.ToolRecoveryPanel })));
import type { ControllerLiveStore, HistoryLoadTrigger, HistoryMutation, Item, LiveStream } from "../lib/useController";
import type { CheckpointMeta, WireCompletionSummary } from "../lib/types";
import type { InvocationMetadataMap } from "../lib/invocationDisplay";
import { useT } from "../lib/i18n";
import { acquireMarkdownWorkerClient, releaseMarkdownWorkerClient } from "../lib/markdownWorkerClient";
import { onSessionExperienceWillChange, useSessionExperience } from "../lib/sessionExperience";
import { cachedSubcallsByParent, cachedTranscriptRowBlocks, cachedTurnModels } from "../lib/transcriptDerivedCache";
import {
  allWorkProcessesCollapsed,
  EMPTY_FOLDS,
  foldMapWithReasoningOpen,
  foldMapWithToggle,
  foldSegmentStates,
  NO_LIVE,
  reconcileFoldEntries,
  type FoldMap,
  type TranscriptLiveFlags,
} from "../lib/transcriptRows";
import { projectTranscriptTimeline, transcriptRenderMode } from "../lib/transcriptTimeline";
import {
  readTranscriptFoldOverrides,
  replaceTranscriptFoldOverrides,
  writeTranscriptFoldOverride,
} from "../lib/transcriptFoldOverrides";
import {
  clearWorkProcessFoldState,
  publishWorkProcessFoldState,
} from "../lib/workProcessFoldState";
import { useTranscriptCommand } from "../lib/useTranscriptCommand";
import {
  EMPTY_FIND_INDEX,
  buildTranscriptFindIndex,
  findAttributeSelector,
  searchTranscriptFind,
  stepFindHit,
  type TranscriptFindHighlight,
  type TranscriptFindHit,
} from "../lib/transcriptFind";
import { composeDomRef } from "../lib/composeDomRef";
import { useTranscriptKernel } from "../lib/useTranscriptKernel";
import { canRequestOlderHistory, createOlderHistoryGateLogger, explainOlderHistoryGate, olderHistoryTriggerPx } from "../lib/historyOlderGates";
import { reportFrontendLog } from "../lib/frontendLog";
import { useAutoLoadOlderEnabled } from "../lib/autoLoadOlderPreference";
import { TranscriptHistoryRequest } from "../lib/transcriptHistoryRequest";
import type { TranscriptQuestionNavigatorHandle } from "./TranscriptQuestionNavigator";
import { useTranscriptQuestions } from "../lib/useTranscriptQuestionNavigation";
import { useTranscriptSelectableRows } from "../lib/useTranscriptSelectableRows";
import { useTranscriptSelectionRetention } from "../lib/useTranscriptSelectionRetention";
import { useCreationTranscriptScrollbar } from "../lib/useCreationTranscriptScrollbar";
import { hasTranscriptScrollableRange } from "../lib/transcriptScrollGeometry";
import { attachNestedScrollHandoff } from "../lib/nestedScrollHandoff";
import { useTranscriptEntranceAnimation } from "../lib/useEntranceAnimation";
import type { QuestionAnchor } from "../lib/transcriptGrouping";
import { transcriptSelectionStore } from "../lib/transcriptSelectionStore";
import { recordFrontendDiagnostic } from "../lib/frontendDiagnosticBridge";
import { beginSurfaceFrame, completeSurfaceFrame } from "../lib/sessionMonitor";
import { InvocationMetadataContext } from "./Message";
import { LiveStreamContext } from "./LiveStreamContext";
import { MarkdownImageTabContext } from "./MarkdownImageContext";
import { TranscriptLayoutIntentProvider, TranscriptScrollWriteProvider } from "./TranscriptLayoutIntentContext";
import { TranscriptViewport, type TranscriptViewportHandle } from "./TranscriptViewport";
import { Welcome } from "./Welcome";
import { useTranscriptRowRenderer } from "./useTranscriptRowRenderer";

export { NoticeCard } from "./TranscriptCards";

const EMPTY_CHECKPOINTS: CheckpointMeta[] = [];
const EMPTY_INVOCATION_METADATA: InvocationMetadataMap = {};
const QUESTION_NAV_MIN_COUNT = 2;
const TranscriptQuestionNavigator = lazy(() => import("./TranscriptQuestionNavigator"));
const QuestionSearchPanel = lazy(() => import("./QuestionSearchPanel"));
import { TranscriptFindBar } from "./TranscriptFindBar";
import { TranscriptFindContext } from "./TranscriptFindContext";
const SHOW_FRONTEND_DIAGNOSTICS = typeof __BUILD_CHANNEL__ === "undefined"
  || __BUILD_CHANNEL__ === "test"
  || __BUILD_CHANNEL__ === "preview"
  || __BUILD_CHANNEL__ === "canary"
  || Boolean(import.meta.env?.DEV);
const FrontendDiagnosticsPanel = SHOW_FRONTEND_DIAGNOSTICS
  ? lazy(() => import("./FrontendDiagnosticsPanel"))
  : null;

export type TranscriptProps = {
  items: Item[];
  live?: LiveStream;
  liveStore?: ControllerLiveStore;
  tabId?: string;
  geometrySessionKey?: string;
  footerHeight?: number;
  onPrompt: (text: string) => void;
  onDeliveryContinue?: () => void;
  onAcceptDelivery?: () => void;
  onOpenChanges?: (summary?: WireCompletionSummary) => void;
  onConsolidateRecovery?: () => void;
  onOpenVerification?: (summary: WireCompletionSummary) => void;
  /** Task 658: the concurrent-writer notice's inline route into the version dialog. */
  onViewVersions?: () => void;
  onEditPrompt?: (turn: number, displayText: string, submitText?: string) => boolean | void | Promise<boolean | void>;
  /** 任务461-P3: one-click resend for a failed submission (same channel as onEditPrompt). */
  onResendPrompt?: (turn: number, displayText: string, submitText?: string) => boolean | void | Promise<boolean | void>;
  onRewind?: (turn: number, scope: string) => void;
  checkpoints?: CheckpointMeta[];
  actionPending?: boolean;
  rewindDisabled?: boolean;
  running?: boolean;
  questionNavigator?: boolean;
  questionSearchOpen?: boolean;
  onCloseQuestionSearch?: () => void;
  /** Task 399: in-session Ctrl+F find. Open state is owned by App so the two
   *  split Transcripts don't each register their own global listener. */
  findOpen?: boolean;
  onCloseFind?: () => void;
  /** Bumped by every Ctrl+F; forwarded so a repeat chord re-selects the query. */
  findPulse?: number;
  welcomeVariant?: "default" | "creation";
  creationMode?: boolean;
  actionHoverMenus?: boolean;
  rewindSignal?: number;
  revealSignal?: number;
  hydrating?: boolean;
  hasOlderHistory?: boolean;
  historyStartTurn?: number;
  historyTotalTurns?: number;
  loadingOlderHistory?: boolean;
  olderHistoryError?: string;
  olderHistoryExhausted?: boolean;
  onLoadOlderHistory?: (targetTurn?: number, trigger?: HistoryLoadTrigger) => boolean | Promise<boolean>;
  turnStartAt?: number;
  contentRevision?: number;
  invocationMetadata?: InvocationMetadataMap;
  historyMutation?: HistoryMutation;
  surfaceCommitToken?: string;
  onSurfacePaintReady?: (token: string, outcome: "ready" | "degraded") => void;
};

export function Transcript(props: TranscriptProps) {
  const {
    items, live: liveProp, liveStore, tabId, geometrySessionKey, footerHeight = 0,
    onPrompt, onDeliveryContinue, onAcceptDelivery, onOpenChanges, onOpenVerification, onConsolidateRecovery,
    onViewVersions, onEditPrompt, onResendPrompt, onRewind, checkpoints = EMPTY_CHECKPOINTS, actionPending = false,
    rewindDisabled = false, running = false, questionNavigator = true,
    questionSearchOpen = false, onCloseQuestionSearch,
    findOpen = false, onCloseFind, findPulse = 0,
    welcomeVariant = "default", creationMode = false, actionHoverMenus = false,
    rewindSignal = 0, revealSignal = 0, hydrating = false, hasOlderHistory = false,
    historyStartTurn = 0, historyTotalTurns = 0, loadingOlderHistory = false,
    olderHistoryError, olderHistoryExhausted, onLoadOlderHistory, turnStartAt, contentRevision = 0,
    invocationMetadata = EMPTY_INVOCATION_METADATA, historyMutation,
    surfaceCommitToken, onSurfacePaintReady,
  } = props;
  const t = useT();
  const subscribeLive = useCallback((listener: () => void) => liveStore?.subscribe(tabId, listener) ?? (() => {}), [liveStore, tabId]);
  const getLiveSnapshot = useCallback(() => liveStore?.getSnapshot(tabId) ?? liveProp, [liveProp, liveStore, tabId]);
  const live = useSyncExternalStore(subscribeLive, getLiveSnapshot, getLiveSnapshot);
  const resolvedSessionKey = geometrySessionKey || `tab:${tabId ?? "preview"}`;
  const surfaceKey = `${resolvedSessionKey}:${revealSignal}`;
  const entranceRef = useTranscriptEntranceAnimation<HTMLDivElement>(tabId, revealSignal, items);
  const viewportRef = useRef<TranscriptViewportHandle>(null);
  const committedSurfaceRef = useRef("");
  const experience = useSessionExperience();
  const liveFlags = useMemo<TranscriptLiveFlags>(() => live?.id ? {
    id: live.id,
    hasAnswerText: Boolean(live.text.trim()),
    hasReasoning: Boolean(live.reasoning),
    reasoningComplete: live.reasoningComplete,
  } : NO_LIVE, [live?.id, live?.reasoning, live?.reasoningComplete, live?.text]);
  // Derived structures are reused across tab switches (see transcriptDerivedCache):
  // switching back hands over the same items array, so the rebuild short-circuits.
  const turnModels = cachedTurnModels(items, liveFlags, running, false);
  // Capture stable commands, never the per-render hook result: a memoized
  // callback holding that result can chain older render/selection contexts.
  // Fork (task 160): the scroll-driven "load older" trigger is an experiment; the
  // "load older" button in the viewport is the default path and ignores this flag.
  const autoLoadOlder = useAutoLoadOlderEnabled();
  const requestOlderAtTopRef = useRef<(() => void) | null>(null);
  const { kernel: transcriptKernel, setScroller: setKernelScroller, snapshot,
    beginGesture, beginStructural, scrollElement, scrollToBottom, scheduleTailSync, safeMode, scrollRef, setScrollMode, writeOffset, jumpToBlock, onScroll, endGesture, commitViewportGeometry, onWheelCapture, isAtBottom, intent, onTouchStartCapture, onTouchEndCapture, onKeyDownCapture, onPointerDownCapture, beginAnchorRestore,
  } = useTranscriptKernel({
    sessionKey: surfaceKey,
    geometryRevision: `${contentRevision}:${footerHeight}:${experience}:${historyMutation?.seq ?? 0}`,
    // Fork (task 160): switch-gated scroll trigger. Reading through a ref keeps the
    // callback stable across renders while `requestOlder` is declared below.
    autoLoadOlderAtTop: autoLoadOlder ? () => requestOlderAtTopRef.current?.() : undefined,
  });
  const [
    questions, loadedByTurn, totalQuestions, activeQuestion, setActiveQuestion,
    scheduleActiveQuestionSync, turnForUser, lastTurn,
  ] = useTranscriptQuestions(items, historyStartTurn, historyTotalTurns, scrollElement, scheduleTailSync);

  const segmentStates = useMemo(() => foldSegmentStates(turnModels, experience === "deep"), [experience, turnModels]);
  const [folds, setFolds] = useState<FoldMap>(EMPTY_FOLDS);
  // Null until the first reconcile: a fresh run has not observed a tier change yet, so
  // the first pass must be treated as one. Otherwise a restarted app reconciles with
  // preferenceChanged === false and the concise tier's "collapse the work process"
  // branch never runs for sessions whose folds were restored (task 124).
  const experienceRef = useRef<typeof experience | null>(null);
  const foldSurfaceRef = useRef("");
  useLayoutEffect(() => {
    if (foldSurfaceRef.current === resolvedSessionKey) return;
    foldSurfaceRef.current = resolvedSessionKey;
    setFolds(readTranscriptFoldOverrides(resolvedSessionKey, segmentStates));
  }, [resolvedSessionKey, segmentStates]);
  useEffect(() => onSessionExperienceWillChange(() => {
    beginStructural("display-change");
  }), [beginStructural]);
  useEffect(() => {
    const preferenceChanged = experienceRef.current !== experience;
    experienceRef.current = experience;
    setFolds((previous) => {
      const next = reconcileFoldEntries(previous, segmentStates, experience, preferenceChanged);
      if (next) replaceTranscriptFoldOverrides(resolvedSessionKey, next);
      return next ?? previous;
    });
  }, [experience, resolvedSessionKey, segmentStates]);

  // 任务 463：「收起/展开全部工作过程」按钮是双向开关，方向必须跟随真实折叠
  // 状态——这里把「当前是否全折叠」按 tabId 上报给共享 store，composer 据此
  // 切换按钮外观。tabId 缺失的预览面（历史面板等）不上报，避免污染真实会话
  // 的按钮方向。
  const workProcessesHasFoldables = segmentStates.length > 0;
  const workProcessesCollapsed = useMemo(
    () => allWorkProcessesCollapsed(folds, segmentStates, experience),
    [folds, segmentStates, experience],
  );
  useEffect(() => {
    if (!tabId) return;
    publishWorkProcessFoldState(tabId, {
      hasFoldables: workProcessesHasFoldables,
      allCollapsed: workProcessesCollapsed,
    });
  }, [tabId, workProcessesHasFoldables, workProcessesCollapsed]);
  // 卸载（或换绑 tabId）时注销上报：陈旧表面不得让按钮停在「展开」。
  useEffect(() => {
    if (!tabId) return;
    const boundTabId = tabId;
    return () => clearWorkProcessFoldState(boundTabId);
  }, [tabId]);

  const subcallsByParent = cachedSubcallsByParent(items);
  const checkpointsByTurn = useMemo(() => new Map(checkpoints.map((checkpoint) => [checkpoint.turn, checkpoint])), [checkpoints]);
  const blocks = cachedTranscriptRowBlocks(turnModels, {
    folds,
    sessionExperience: experience,
    hasOlderHistory: false,
    creationMode,
    turnForUser,
    hasCheckpointForTurn: (turn) => checkpointsByTurn.has(turn),
    subcallsByParent,
  }, [checkpointsByTurn, creationMode, experience, folds, subcallsByParent, turnForUser]);
  const projection = useMemo(() => projectTranscriptTimeline(blocks, hasOlderHistory), [blocks, hasOlderHistory]);
  const renderMode = transcriptRenderMode(projection.completedBlocks.length, safeMode);
  const allRows = useMemo(() => blocks.flatMap((block) => block.rows), [blocks]);
  const empty = items.length === 0;
  const rowIndexByKey = useMemo(() => new Map(allRows.map((row, index) => [String(row.key), index])), [allRows]);
  const [selectableRows, liveSelectableRows] = useTranscriptSelectableRows(allRows, live);
  const cancelStreamingScroll = useCallback(() => beginGesture("selection"), [beginGesture]);
  const { clear: clearSelection, onPointerDownCapture: onSelectionPointerDown, endStaleGesture } = useTranscriptSelectionRetention({
    tabId,
    revealSignal,
    rowIndexByKey,
    selectableRows,
    selectableRowOverrides: liveSelectableRows,
    scrollRef: scrollRef,
    setScrollMode: setScrollMode,
    writeOffset: writeOffset,
    cancelStreamingScroll,
  });

  // ── Task 399: in-session Ctrl+F find ──────────────────────────────────
  // Query lives here (not in App) so split panes keep independent searches;
  // the open flag is lifted so a single global shortcut can't double-fire.
  const [findQuery, setFindQuery] = useState("");
  const [findCursor, setFindCursor] = useState(0);
  // Deferred keeps typing responsive on 万行 sessions: the scan runs after
  // the input paint instead of blocking the keystroke.
  const deferredFindQuery = useDeferredValue(findQuery);
  const findIndexEntries = useMemo(
    () => (findOpen ? buildTranscriptFindIndex(blocks) : EMPTY_FIND_INDEX),
    [findOpen, blocks],
  );
  const findResult = useMemo(
    () => searchTranscriptFind(findIndexEntries, deferredFindQuery),
    [findIndexEntries, deferredFindQuery],
  );
  // Cursor may drift out of range when rows stream in/out; clamp on read so
  // highlights never point at a stale index (and no effect loop is needed).
  const activeFindIndex = findResult.hits.length === 0
    ? -1
    : Math.min(Math.max(findCursor, 0), findResult.hits.length - 1);
  const activeFindHit = activeFindIndex >= 0 ? findResult.hits[activeFindIndex] : null;
  const findNoMatches = deferredFindQuery.trim() !== "" && findResult.hits.length === 0;

  // Mount the hit's block first (windowed mode may not have it), then measure
  // the row against its block and jump with that offset so deep rows land
  // visible, not just their block header. Falls back to a plain block jump
  // when measurement isn't possible yet; the kernel re-anchors on geometry
  // changes either way.
  const jumpToFindHit = useTranscriptCommand((hit: TranscriptFindHit) => {
    const element = scrollRef.current;
    if (!element) return false;
    document.getSelection()?.removeAllRanges();
    clearSelection("find-navigation");
    viewportRef.current?.mountBlock(hit.blockKey);
    const measure = (): number | null => {
      const blockEl = element.querySelector<HTMLElement>(findAttributeSelector("data-transcript-block-key", hit.blockKey));
      const rowEl = element.querySelector<HTMLElement>(findAttributeSelector("data-row-key", hit.rowKey));
      if (!blockEl || !rowEl) return null;
      // Land the row a little below the viewport top so the find bar doesn't
      // cover the hit; clamp at 0 (kernel never scrolls to a negative offset).
      const rowOffset = Math.round(rowEl.getBoundingClientRect().top - blockEl.getBoundingClientRect().top);
      return Math.max(0, rowOffset - 56);
    };
    const offset = measure();
    if (offset !== null) return jumpToBlock(hit.blockKey, offset);
    // Pinned block hasn't painted yet — jump after the next frame, once the
    // row exists; offset 0 (block top) is the honest fallback if it never does.
    requestAnimationFrame(() => {
      const lateOffset = measure();
      jumpToBlock(hit.blockKey, lateOffset ?? 0);
    });
    return true;
  });

  const stepFind = useTranscriptCommand((direction: 1 | -1) => {
    const total = findResult.hits.length;
    if (total === 0) return;
    const next = stepFindHit(activeFindIndex, total, direction);
    setFindCursor(next);
    const hit = findResult.hits[next];
    if (hit) jumpToFindHit(hit);
  });

  // Jump to the first hit when the QUERY changes — not when the match list
  // refreshes from streaming/paging (that would yank the viewport while the
  // reader is watching). lastJumpedQuery latches per committed query.
  const lastJumpedQueryRef = useRef("");
  useEffect(() => {
    if (!findOpen) {
      lastJumpedQueryRef.current = "";
      return;
    }
    const query = deferredFindQuery.trim();
    // Clearing the query re-arms the latch so retyping the same term jumps again.
    if (!query) {
      lastJumpedQueryRef.current = "";
      return;
    }
    if (query === lastJumpedQueryRef.current) return;
    lastJumpedQueryRef.current = query;
    setFindCursor(0);
    const first = findResult.hits[0];
    if (first) jumpToFindHit(first);
  }, [findOpen, deferredFindQuery, findResult, jumpToFindHit]);

  // Closing the bar clears the query so the next Ctrl+F starts fresh and no
  // highlight survives an invisible bar.
  useEffect(() => {
    if (findOpen) return;
    setFindQuery("");
    setFindCursor(0);
  }, [findOpen]);

  const findHighlight = useMemo<TranscriptFindHighlight>(() => {
    if (!findOpen || findResult.hits.length === 0) return null;
    return {
      hits: new Set(findResult.hits.map((hit) => hit.rowKey)),
      active: activeFindHit?.rowKey ?? null,
    };
  }, [findOpen, findResult, activeFindHit]);

  const handleFoldToggle = useTranscriptCommand((segmentKey: string, open: boolean) => {
    beginStructural("display-change");
    setFolds((previous) => {
      const next = foldMapWithToggle(previous, segmentKey, open);
      const entry = next.get(segmentKey);
      if (entry) writeTranscriptFoldOverride(resolvedSessionKey, segmentKey, entry);
      return next;
    });
  });
  const handleReasoningManualOpen = useTranscriptCommand((segmentKey: string) => {
    beginStructural("display-change");
    const active = segmentStates.find((segment) => segment.key === segmentKey)?.hasRunningWork ?? false;
    setFolds((previous) => {
      const next = foldMapWithReasoningOpen(previous, segmentKey, active);
      const entry = next.get(segmentKey);
      if (entry) writeTranscriptFoldOverride(resolvedSessionKey, segmentKey, entry);
      return next;
    });
  });
  // Task 269 B: "collapse all work processes" — the composer's chevron button
  // reaches the transcript through this window event (the two are not in a
  // parent/child relation; the session-experience event is the existing
  // convention). userOverridden=true is load-bearing: without it the running
  // branch of the next reconcile tick would re-open every fold (the R3
  // deliberate-open semantics would undo the collapse a moment later). The
  // B-layer component states need no individual clearing: they are invisible
  // once the header is closed and re-derive from presentation on next open.
  const handleCollapseAll = useTranscriptCommand(() => {
    beginStructural("display-change");
    setFolds((previous) => {
      const next = new Map(previous);
      for (const segment of segmentStates) {
        next.set(segment.key, {
          open: false,
          userOverridden: true,
          running: segment.hasRunningWork,
          keepReasoningExpanded: segment.keepReasoningExpanded,
        });
      }
      replaceTranscriptFoldOverrides(resolvedSessionKey, next);
      return next;
    });
  });
  // 任务 463：与收起对偶的「全部展开」。userOverridden=true 同样是承重件：
  // 没有它，运行中的 reconcile tick 会把刚展开的块按默认规则改回去。
  const handleExpandAll = useTranscriptCommand(() => {
    beginStructural("display-change");
    setFolds((previous) => {
      const next = new Map(previous);
      for (const segment of segmentStates) {
        next.set(segment.key, {
          open: true,
          userOverridden: true,
          running: segment.hasRunningWork,
          keepReasoningExpanded: segment.keepReasoningExpanded,
        });
      }
      replaceTranscriptFoldOverrides(resolvedSessionKey, next);
      return next;
    });
  });
  useEffect(() => {
    const onCollapseAll = () => handleCollapseAll();
    const onExpandAll = () => handleExpandAll();
    window.addEventListener("reasonix:collapse-all-folds", onCollapseAll);
    window.addEventListener("reasonix:expand-all-folds", onExpandAll);
    return () => {
      window.removeEventListener("reasonix:collapse-all-folds", onCollapseAll);
      window.removeEventListener("reasonix:expand-all-folds", onExpandAll);
    };
  }, [handleCollapseAll, handleExpandAll]);
  const renderRow = useTranscriptRowRenderer({
    tabId, checkpoints, subcallsByParent, creationMode, running, actionPending,
    rewindDisabled, actionHoverMenus, turnStartAt, lastTurn,
    onFoldToggle: handleFoldToggle, onReasoningManualOpen: handleReasoningManualOpen,
    onPrompt, onDeliveryContinue, onAcceptDelivery, onOpenChanges, onOpenVerification, onConsolidateRecovery,
    onViewVersions, onEditPrompt, onResendPrompt, onRewind,
  });

  const jumpToLoadedQuestion = useTranscriptCommand((question: QuestionAnchor) => {
    const block = blocks.find((candidate) => candidate.questionAnchor === `u:${question.id}`);
    if (!block) return false;
    document.getSelection()?.removeAllRanges();
    clearSelection("question-navigation");
    setActiveQuestion(question.turn);
    viewportRef.current?.mountBlock(block.key);
    return jumpToBlock(block.key);
  });
  const questionNavigatorRef = useRef<TranscriptQuestionNavigatorHandle>(null);
  const history = useMemo(() => new TranscriptHistoryRequest(transcriptKernel), [transcriptKernel]);
  // 任务 448 收尾（445 调研 §1.5-2）：组件层拒绝此前零日志，装机取证只能靠
  // `history.older-request` 缺席反推。转换式留痕：同一原因连续拒绝只记首条，
  // 翻回允许后重置——4MB 滚动日志装不下每滚动帧一条（frontendLog 纪律）。
  const reportOlderGateBlock = useRef<ReturnType<typeof createOlderHistoryGateLogger> | null>(null);
  if (!reportOlderGateBlock.current) {
    reportOlderGateBlock.current = createOlderHistoryGateLogger((message, detail) => reportFrontendLog("history-paging", message, detail, "info"));
  }
  const requestOlder = useTranscriptCommand((turn?: number, trigger: HistoryLoadTrigger = "viewport-user") => {
    // 任务 448（384 收尾）：闸只剩 hasOlder + loading 两态，与 controller 层同口径。
    // 旧代码在这里还有 `|| running` —— 会话跑着时滚动到顶、按钮、横条跳转全部静默
    // 拒绝（零日志），把 384 在 controller 层的解锁挡在了组件层之后（445 调研 §2.3）。
    if (!onLoadOlderHistory || !canRequestOlderHistory({ hasOlderHistory, loadingOlderHistory })) {
      reportOlderGateBlock.current?.(
        onLoadOlderHistory ? explainOlderHistoryGate({ hasOlderHistory, loadingOlderHistory }) : { allowed: false },
        trigger,
      );
      return Promise.resolve(false);
    }
    reportOlderGateBlock.current?.({ allowed: true }, trigger);
    if (trigger !== "question-jump" && trigger !== "retry") beginStructural("prepend");
    return history.load(() => onLoadOlderHistory(turn, trigger));
  });
  // Assigned during render so the kernel's wheel/key handlers always call the gate
  // the button above uses: the switch widens where the trigger comes from, never
  // which loads are allowed.
  requestOlderAtTopRef.current = () => void requestOlder(undefined, "viewport-user");
  const retry = useTranscriptCommand(() => {
    if (questionNavigatorRef.current) questionNavigatorRef.current.retry();
    else void requestOlder(undefined, "retry");
  });
  useEffect(() => {
    if (rewindSignal <= 0) return;
    const last = questions[questions.length - 1];
    if (last) jumpToLoadedQuestion(last);
  }, [jumpToLoadedQuestion, questions, rewindSignal]);

  const handleScroll = useTranscriptCommand(() => {
    const towardHistory = onScroll();
    if (towardHistory === null) return;
    scheduleActiveQuestionSync();
    const element = scrollRef.current;
    // 任务 448（B1 预取）：到顶 64px 改为两个视口（`olderHistoryTriggerPx`），
    // 读者抵达边界前一页已在本地；请求侧的 in-flight 复用与 loading 闸照旧单飞。
    if (towardHistory && element && element.scrollTop <= olderHistoryTriggerPx(element.clientHeight)) void requestOlder(undefined, "viewport-user");
  });
  const {
    state: creationScrollbar,
    handleScroll: handleCreationScroll,
    onThumbPointerDown: handleCreationScrollbarThumbPointerDown,
    onRailPointerDown: handleCreationScrollbarRailPointerDown,
  } = useCreationTranscriptScrollbar({
    enabled: creationMode,
    contentRevision,
    scrollRef: scrollRef,
    onScroll: handleScroll,
    setScrollMode: setScrollMode,
    writeOffset: writeOffset,
    finishProgrammaticScroll: endGesture,
  });

  const setScroller = useMemo(() => composeDomRef(setKernelScroller, entranceRef), [setKernelScroller, entranceRef]);
  const previousFooterHeight = useRef(footerHeight);
  useLayoutEffect(() => {
    if (previousFooterHeight.current === footerHeight) return;
    previousFooterHeight.current = footerHeight;
    beginStructural("composer-resize");
    commitViewportGeometry();
  }, [footerHeight, beginStructural, commitViewportGeometry]);

  useEffect(() => {
    acquireMarkdownWorkerClient();
    return () => releaseMarkdownWorkerClient();
  }, []);
  useEffect(() => {
    const parent = scrollElement;
    if (!parent) return;
    return attachNestedScrollHandoff({
      parent,
      onParentScrollIntent: () => onWheelCapture(),
      writeParentOffset: (top) => writeOffset("nested-scroll", top),
    }).detach;
  }, [onWheelCapture, scrollElement, writeOffset]);
  useEffect(() => {
    recordFrontendDiagnostic("transcript", "transcript.surface", {
      generation: transcriptKernel.generation,
      completedBlocks: projection.completedBlocks.length,
      renderMode,
    });
  }, [projection.completedBlocks.length, renderMode, surfaceKey, transcriptKernel.generation]);
  // Task 125: first-frame + geometry-measure. The clock starts when a surface
  // with content appears (covers startup and tab switch) and stops on the
  // kernel's post-paint callback, which is the same gate the paint-ready
  // receipt uses. Overhead outside the window is one key compare per render.
  const frameSurfaceRef = useRef("");
  useEffect(() => {
    if (empty || hydrating) {
      frameSurfaceRef.current = "";
      return;
    }
    if (frameSurfaceRef.current === surfaceKey) return;
    frameSurfaceRef.current = surfaceKey;
    beginSurfaceFrame(surfaceKey);
    return transcriptKernel.afterCurrentGenerationPaint(() => {
      if (frameSurfaceRef.current !== surfaceKey) return;
      frameSurfaceRef.current = "";
      if (tabId) completeSurfaceFrame(tabId, surfaceKey);
    });
  }, [empty, hydrating, surfaceKey, tabId, transcriptKernel]);
  useEffect(() => {
    if (!surfaceCommitToken || !onSurfacePaintReady || hydrating) return;
    const commitKey = `${transcriptKernel.generation}:${surfaceCommitToken}`;
    if (committedSurfaceRef.current === commitKey) return;
    return transcriptKernel.afterCurrentGenerationPaint(() => {
      const geometry = snapshot();
      if (!geometry || (!empty && geometry.visibleBlocks.length === 0)) return;
      if (committedSurfaceRef.current === commitKey) return;
      committedSurfaceRef.current = commitKey;
      // Fork (#9567): the remounted surface can park wherever its first paint
      // lands — resetScroll's tail intent was observed lost after
      // model-takeover remounts, leaving the newest turns above the viewport.
      // Enforce the tail once the incoming surface has committed its paint.
      setScrollMode("tail-follow");
      scrollToBottom();
      onSurfacePaintReady(surfaceCommitToken, safeMode ? "degraded" : "ready");
    });
  }, [empty, hydrating, safeMode, snapshot, onSurfacePaintReady, projection, surfaceCommitToken, transcriptKernel]);
  const autoFillRef = useRef({ surface: "", pages: 0 });
  useEffect(() => {
    if (autoFillRef.current.surface !== surfaceKey) autoFillRef.current = { surface: surfaceKey, pages: 0 };
    // 任务 448（B2+B3）：自动填充的闸与滚动/按钮同口径 —— `running` 不再停摆
    // （384 收尾），`olderHistoryError` 也不再是永久死锁（zcode 借鉴：失败不进
    // error 态）。失败后 loading 翻回 false 触发本 effect 重跑，于是下一轮直接
    // 重试；预算仍是每 surface 最多 3 页，绝不会无限重发。
    if (hydrating || !canRequestOlderHistory({ hasOlderHistory, loadingOlderHistory }) || autoFillRef.current.pages >= 3) return;
    return transcriptKernel.afterCurrentGenerationPaint(() => {
      const geometry = snapshot();
      if (!geometry || geometry.clientHeight <= 0 || geometry.scrollHeight > geometry.clientHeight + 4) return;
      autoFillRef.current.pages += 1;
      void requestOlder(undefined, "auto-fill");
    });
  }, [hasOlderHistory, hydrating, snapshot, loadingOlderHistory, projection.completedBlocks.length, requestOlder, surfaceKey, transcriptKernel]);

  const showQuestionNav = questionNavigator && totalQuestions >= QUESTION_NAV_MIN_COUNT;
  const selectionSnapshot = useSyncExternalStore(transcriptSelectionStore.subscribe, transcriptSelectionStore.getSnapshot, transcriptSelectionStore.getSnapshot);
  const protectedBlockKeys = useMemo(() => {
    const keys = new Set<string>();
    if (transcriptKernel.anchor.kind === "block") keys.add(transcriptKernel.anchor.blockKey);
    const endpoints = selectionSnapshot.mode.startsWith("logical")
      ? [selectionSnapshot.anchor?.rowKey, selectionSnapshot.focus?.rowKey]
      : [];
    for (const block of blocks) {
      if (block.rows.some((row) => endpoints.includes(row.key))) keys.add(block.key);
    }
    return keys;
  }, [blocks, selectionSnapshot, transcriptKernel.anchor]);
  const jumpBottomVisible = Boolean(
    !isAtBottom
      && scrollElement
      && hasTranscriptScrollableRange(scrollElement),
  );

  return (
    <InvocationMetadataContext.Provider value={invocationMetadata}>
    <MarkdownImageTabContext.Provider value={tabId ?? ""}>
    <TranscriptLayoutIntentProvider value={() => { beginStructural("display-change"); }}>
    <TranscriptScrollWriteProvider value={writeOffset}>
    <TranscriptFindContext.Provider value={findHighlight}>
      <div className="transcript-shell" aria-busy={loadingOlderHistory || undefined} data-protected-blocks={protectedBlockKeys.size}>
        {tabId && <Suspense fallback={null}><ToolRecoveryPanel key={resolvedSessionKey} tabId={tabId} sessionKey={resolvedSessionKey} running={running} refreshKey={items.length} /></Suspense>}
        {empty ? (
          <div className={`transcript transcript--empty${creationMode ? " transcript--creation-scrollbar" : ""}`} ref={setScroller} aria-busy={hydrating || undefined}>
            {hydrating ? <div className="transcript__loading" role="status" aria-live="polite"><Loader2 className="transcript__loading-icon" aria-hidden="true" /><span>{t("common.loading")}</span></div>
              : <Welcome onPrompt={onPrompt} variant={welcomeVariant} />}
          </div>
        ) : (
          <LiveStreamContext.Provider value={live}>
            <div
              ref={setScroller}
              className={`transcript${creationMode ? " transcript--creation-scrollbar" : ""}${creationMode && creationScrollbar.hot ? " transcript--scrollbar-hot" : ""}`}
              data-transcript-hydrating={hydrating ? "true" : "false"}
              data-transcript-generation={transcriptKernel.generation}
              data-transcript-intent={intent}
              data-transcript-row-count={allRows.length}
              data-transcript-block-count={blocks.length}
              data-scroll-mode={selectionSnapshot.mode !== "none" ? "selection" : intent === "tail" ? "tail-follow" : "manual"}
              onScroll={creationMode ? handleCreationScroll : handleScroll}
              onWheelCapture={() => onWheelCapture()}
              onTouchStartCapture={() => onTouchStartCapture()}
              onTouchEndCapture={() => onTouchEndCapture()}
              onTouchCancelCapture={() => onTouchEndCapture()}
              onKeyDownCapture={onKeyDownCapture}
              onPointerDownCapture={(event) => {
                onPointerDownCapture(event);
                onSelectionPointerDown(event);
              }}
              onMouseDownCapture={onPointerDownCapture}
            >
              <TranscriptViewport
                key={surfaceKey}
                ref={viewportRef}
                projection={projection}
                mode={renderMode}
                tabId={tabId}
                scrollElement={scrollElement}
                renderRow={renderRow}
                loadingOlderHistory={loadingOlderHistory}
                olderHistoryError={olderHistoryError}
                olderHistoryExhausted={olderHistoryExhausted}
                onRetryOlderHistory={retry}
                onLoadOlder={onLoadOlderHistory ? () => void requestOlder(undefined, "viewport-user") : undefined}
                onGeometryWillChange={beginAnchorRestore}
                onGeometryChange={commitViewportGeometry}
                kernel={transcriptKernel}
                protectedBlockKeys={protectedBlockKeys}
                running={running}
                turnStartAt={turnStartAt}
              />
            </div>
          </LiveStreamContext.Provider>
        )}
        {creationMode && creationScrollbar.visible && <div className={`transcript__scrollbar${creationScrollbar.hot ? " transcript__scrollbar--hot" : ""}`} onPointerDown={handleCreationScrollbarRailPointerDown} aria-hidden="true">
          <div className="transcript__scrollbar-thumb" style={{ top: creationScrollbar.thumbTop, height: creationScrollbar.thumbHeight } as CSSProperties} onPointerDown={handleCreationScrollbarThumbPointerDown} />
        </div>}
        {!empty && showQuestionNav && <Suspense fallback={null}><TranscriptQuestionNavigator ref={questionNavigatorRef} kernel={transcriptKernel}
          requestOlder={requestOlder} loadingOlderHistory={loadingOlderHistory} loadedByTurn={loadedByTurn}
          jump={jumpToLoadedQuestion} questions={questions} totalQuestions={totalQuestions} activeTurn={activeQuestion} /></Suspense>}
      {showQuestionNav && (
        <Suspense fallback={null}>
          <QuestionSearchPanel
            open={Boolean(questionSearchOpen)}
            onClose={() => onCloseQuestionSearch?.()}
            questions={questions}
            totalQuestions={totalQuestions}
            onJump={jumpToLoadedQuestion}
          />
        </Suspense>
      )}
        <TranscriptFindBar
          open={findOpen}
          query={findQuery}
          onQueryChange={setFindQuery}
          activeIndex={activeFindIndex + 1}
          matchCount={findResult.hits.length}
          capped={findResult.capped}
          noMatches={findNoMatches}
          onPrev={() => stepFind(-1)}
          onNext={() => stepFind(1)}
          onClose={() => onCloseFind?.()}
          focusSignal={findPulse}
        />
        {!empty && <button type="button" className="transcript__jump-bottom" hidden={!jumpBottomVisible} onClick={() => { endStaleGesture(); scrollToBottom(); }} aria-label={t("transcript.jumpToBottom")} title={t("transcript.jumpToBottom")}><ArrowDown size={18} strokeWidth={2.2} aria-hidden="true" /></button>}
        {FrontendDiagnosticsPanel && <Suspense fallback={null}><FrontendDiagnosticsPanel scrollElement={scrollElement} totalRows={allRows.length} /></Suspense>}
      </div>
    </TranscriptFindContext.Provider>
    </TranscriptScrollWriteProvider>
    </TranscriptLayoutIntentProvider>
    </MarkdownImageTabContext.Provider>
    </InvocationMetadataContext.Provider>
  );
}
