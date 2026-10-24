import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { CheckCheck, ChevronDown, ChevronRight, ClipboardCopy, ClipboardPaste, Eye, EyeOff, SendHorizontal, Undo2 } from "lucide-react";
import { writeClipboardText } from "../lib/clipboard";
import { countUnhandled, markHandledTurns, readHandledTurns, useUnfiledInputs } from "../lib/unfiledInputs";
import { useToast } from "../lib/toast";
import { useI18n } from "../lib/i18n";
import { ModalCloseButton } from "./ModalCloseButton";
import type { UnfiledInputView } from "../lib/types";

// 任务 1023 阶段 0：未入档输入（已从检查点恢复）横幅 + 面板。
// 呈现与操作都只读后端：复制走剪贴板，转草稿走 composer 插入通道，
// 重发走与 transcript 输入相同的提交通道；「已处理」仅存前端 localStorage，
// 不回写 transcript、不改事件日志。
// tl 1127：「查看」从内联展开改为大窗口模态——41 条实例中内联列表被
// .main 的 overflow:hidden 裁掉且无滚动，后面的条目必须先处理前面的才可见。
// 大窗口 portal 到 body、列表独立滚动（详见 .unfiled-viewer 样式段注释）。

const COPY = {
  en: {
    banner: (n: number) => `${n} input ${n === 1 ? "entry" : "entries"} never reached the conversation`,
    bannerSuffix: "recovered from checkpoints",
    view: "View",
    collapse: "Collapse",
    markAllHandled: "Mark all handled",
    handledAllDone: "All entries marked handled",
    expandEntry: "Expand",
    collapseEntry: "Collapse",
    emptyBody: "The checkpoint sidecar kept these prompts, but no matching user message exists in the transcript.",
    copy: "Copy full text",
    draft: "Use as draft",
    resend: "Resend",
    handled: "Mark handled",
    copied: "Copied",
    draftDone: "Inserted into the composer as a draft",
    resent: "Resubmitted — the entry is marked handled",
    handledDone: "Marked handled",
    resendUnavailable: "Resend unavailable here",
    turnLabel: (turn: number) => `Turn ${turn}`,
    timeUnknown: "time unknown",
    close: "Close",
  },
  zh: {
    banner: (n: number) => `检测到 ${n} 条输入未进入对话`,
    bannerSuffix: "已从检查点恢复",
    view: "查看",
    collapse: "收起",
    markAllHandled: "全部标记已处理",
    handledAllDone: "已全部标记处理",
    expandEntry: "展开",
    collapseEntry: "收起",
    emptyBody: "检查点旁路保留了这些输入原文，但 transcript 中没有对应用户消息。",
    copy: "复制全文",
    draft: "转为草稿",
    resend: "重发",
    handled: "已处理",
    copied: "已复制全文",
    draftDone: "已作为草稿插入输入框",
    resent: "已重新提交，条目标记为已处理",
    handledDone: "已标记处理",
    resendUnavailable: "当前不可重发",
    turnLabel: (turn: number) => `轮次 ${turn}`,
    timeUnknown: "时间未知",
    close: "关闭",
  },
  "zh-TW": {
    banner: (n: number) => `偵測到 ${n} 條輸入未進入對話`,
    bannerSuffix: "已從檢查點恢復",
    view: "查看",
    collapse: "收起",
    markAllHandled: "全部標記已處理",
    handledAllDone: "已全部標記處理",
    expandEntry: "展開",
    collapseEntry: "收起",
    emptyBody: "檢查點旁路保留了這些輸入原文，但 transcript 中沒有對應使用者訊息。",
    copy: "複製全文",
    draft: "轉為草稿",
    resend: "重發",
    handled: "已處理",
    copied: "已複製全文",
    draftDone: "已作為草稿插入輸入框",
    resent: "已重新提交，條目標記為已處理",
    handledDone: "已標記處理",
    resendUnavailable: "目前不可重發",
    turnLabel: (turn: number) => `輪次 ${turn}`,
    timeUnknown: "時間未知",
    close: "關閉",
  },
} as const;

export function UnfiledInputsBanner({
  tabId,
  sessionKey,
  onDraft,
  onResend,
  disabled,
  onHandled,
}: {
  tabId: string | undefined;
  sessionKey: string;
  /** Inserts the recovered text into the composer draft (task 1023). */
  onDraft?: (text: string) => void;
  /** Resubmits the recovered text through the transcript prompt channel. */
  onResend?: (text: string) => void;
  disabled?: boolean;
  /** Notifies the owner after the handled set changed (in-place cards re-filter). */
  onHandled?: () => void;
}) {
  const { locale } = useI18n();
  const t = COPY[locale];
  const { showToast } = useToast();
  const { entries } = useUnfiledInputs(tabId);
  const [handled, setHandled] = useState<Set<number>>(() => readHandledTurns(sessionKey));
  const [viewerOpen, setViewerOpen] = useState(false);
  useEffect(() => {
    setHandled(readHandledTurns(sessionKey));
  }, [sessionKey]);

  const unhandledCount = countUnhandled(entries, handled);
  const pending = useMemo(
    () => entries.filter((entry) => !handled.has(entry.turn)),
    [entries, handled],
  );

  const markHandled = useCallback(
    (turn: number, toast?: string) => {
      markHandledTurns(sessionKey, [turn]);
      setHandled(readHandledTurns(sessionKey));
      onHandled?.();
      if (toast) showToast(toast, "info");
    },
    [sessionKey, showToast],
  );

  // 任务 1045 批量面：数据层 markHandledTurns 本就收 turns 数组（localStorage 一次
  // 写入），这里一次把全部未处理 turn 交过去——41 条逐条点的痛点由此清空。
  const markAllHandled = useCallback(() => {
    const turns = entries.filter((entry) => !handled.has(entry.turn)).map((entry) => entry.turn);
    if (turns.length === 0) return;
    markHandledTurns(sessionKey, turns);
    setHandled(readHandledTurns(sessionKey));
    onHandled?.();
    showToast(t.handledAllDone, "info");
  }, [entries, handled, sessionKey, showToast, t, onHandled]);

  // tl 1127 空态铁律：全部处理完（unhandledCount===0）时横幅与大窗口一起消失，
  // 不残留空白窗口。App.tsx 外层还有 unfiledUnhandledCount>0 的闸门双保险。
  if (unhandledCount === 0) return null;

  return (
    <section className="composer-guidance-shelf" role="status" aria-label={t.banner(unhandledCount)}>
      <div className="banner banner--warning banner--actionable" style={{ flexWrap: "wrap" }}>
        <CheckCheck size={17} aria-hidden="true" />
        <span className="banner__msg">
          <strong>{t.banner(unhandledCount)}</strong>
          {` — ${t.bannerSuffix}`}
        </span>
        <span className="banner__spacer" />
        {pending.length > 1 && (
          <button className="btn btn--secondary btn--small" type="button" onClick={markAllHandled}>
            <CheckCheck size={13} aria-hidden="true" />
            <span>{t.markAllHandled}</span>
          </button>
        )}
        <button
          className="btn btn--secondary btn--small"
          type="button"
          aria-haspopup="dialog"
          onClick={() => setViewerOpen(true)}
        >
          <Eye size={13} aria-hidden="true" />
          <span>{t.view}</span>
        </button>
      </div>
      {viewerOpen && (
        <UnfiledInputsViewer
          count={unhandledCount}
          pending={pending}
          t={t}
          disabled={disabled}
          onDraft={onDraft}
          onResend={onResend}
          onMarkHandled={markHandled}
          onMarkAllHandled={markAllHandled}
          onClose={() => setViewerOpen(false)}
        />
      )}
    </section>
  );
}

// tl 1127 大窗口浏览：点「查看」打开的大模态（portal 到 body，不受 .main
// overflow:hidden 裁剪）。结构与焦点管理照 BlankProjectDialog 的项目规范：
// Esc 关闭、Tab 在窗口内循环（焦点陷阱）、关闭后焦点还给横幅上的触发按钮；
// 尺寸与独立滚动见 styles.css 的 .unfiled-viewer 段（参照 events-repair-dialog
// 的 flex 高度链：列表 min-height:0 + overflow-y:auto，滚动链 contain 不带动
// 底层 transcript）。条目卡片复用 UnfiledEntryCard，操作行为与横幅面完全一致。
function UnfiledInputsViewer({
  count,
  pending,
  t,
  disabled,
  onDraft,
  onResend,
  onMarkHandled,
  onMarkAllHandled,
  onClose,
}: {
  count: number;
  pending: UnfiledInputView[];
  t: (typeof COPY)[keyof typeof COPY];
  disabled?: boolean;
  onDraft?: (text: string) => void;
  onResend?: (text: string) => void;
  onMarkHandled: (turn: number, toast?: string) => void;
  onMarkAllHandled: () => void;
  onClose: () => void;
}) {
  const dialogRef = useRef<HTMLDivElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);
  const restoreFocusRef = useRef<HTMLElement | null>(null);

  useLayoutEffect(() => {
    restoreFocusRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    closeButtonRef.current?.focus();
    return () => {
      if (restoreFocusRef.current?.isConnected) restoreFocusRef.current.focus();
    };
  }, []);

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        onClose();
        return;
      }
      if (event.key !== "Tab") return;
      const focusable = Array.from(dialogRef.current?.querySelectorAll<HTMLElement>(
        'button:not(:disabled), [href], input:not(:disabled), [tabindex]:not([tabindex="-1"])',
      ) ?? []);
      if (focusable.length === 0) return;
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKeyDown, { capture: true });
    return () => document.removeEventListener("keydown", onKeyDown, { capture: true });
  }, [onClose]);

  return createPortal(
    <div
      className="modal-backdrop unfiled-viewer-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div
        ref={dialogRef}
        className="modal unfiled-viewer"
        role="dialog"
        aria-modal="true"
        aria-label={t.banner(count)}
      >
        <div className="unfiled-viewer__head">
          <span className="unfiled-viewer__head-title">
            <strong>{t.banner(count)}</strong>
            {` — ${t.bannerSuffix}`}
          </span>
          {pending.length > 1 && (
            <button className="btn btn--secondary btn--small" type="button" onClick={onMarkAllHandled}>
              <CheckCheck size={13} aria-hidden="true" />
              <span>{t.markAllHandled}</span>
            </button>
          )}
          <ModalCloseButton ref={closeButtonRef} label={t.close} onClick={onClose} />
        </div>
        <div className="unfiled-viewer__list">
          {pending.map((entry) => (
            <UnfiledEntryCard
              key={entry.turn}
              entry={entry}
              t={t}
              disabled={disabled}
              onDraft={onDraft}
              onResend={onResend}
              onMarkHandled={onMarkHandled}
            />
          ))}
          {pending.length === 0 && <div className="banner banner--info"><span className="banner__msg">{t.emptyBody}</span></div>}
        </div>
      </div>
    </div>,
    document.body,
  );
}

// 任务 1045: 恢复卡片本体——头部行（标题 + 全部操作按钮）恒可见不随正文沉底，
// 正文默认折叠到 FOLDED_LINES 行、超长时内部滚动，展开/收起是显式点击。
// 这样 50KB+ 的 skill md 全文既挡不死 transcript，处理按钮也永远点得到。
function UnfiledEntryCard({
  entry,
  t,
  disabled,
  onDraft,
  onResend,
  onMarkHandled,
}: {
  entry: UnfiledInputView;
  t: (typeof COPY)[keyof typeof COPY];
  disabled?: boolean;
  onDraft?: (text: string) => void;
  onResend?: (text: string) => void;
  onMarkHandled: (turn: number, toast?: string) => void;
}) {
  const { showToast } = useToast();
  const [expanded, setExpanded] = useState(false);
  const formatTime = entry.time > 0 ? new Date(entry.time).toLocaleString() : t.timeUnknown;
  return (
    <div className="banner banner--info" style={{ flexDirection: "column", alignItems: "stretch", gap: 6 }}>
      {/* 头部行：标题 + 展开/收起 + 四个操作按钮。position: sticky 保证正文内部
          滚动时头部（含按钮）仍钉在卡片顶端，绝不随 50KB 正文沉到视口外。 */}
      <div className="unfiled-card__head">
        <button
          type="button"
          className="notice-line__summary-toggle"
          aria-expanded={expanded}
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? <ChevronDown className="notice-line__chevron" size={14} aria-hidden="true" /> : <ChevronRight className="notice-line__chevron" size={14} aria-hidden="true" />}
          <span className="notice-line__title">{`${t.turnLabel(entry.turn)} · ${formatTime}`}</span>
        </button>
        <span className="banner__spacer" />
        <button className="btn btn--secondary btn--small" type="button" aria-expanded={expanded} onClick={() => setExpanded((v) => !v)}>
          {expanded ? <EyeOff size={13} aria-hidden="true" /> : <Eye size={13} aria-hidden="true" />}
          <span>{expanded ? t.collapseEntry : t.expandEntry}</span>
        </button>
        <button
          className="btn btn--secondary btn--small"
          type="button"
          onClick={() => {
            void writeClipboardText(entry.prompt).then((ok) => showToast(ok ? t.copied : String(entry.prompt), ok ? "info" : "warn"));
          }}
        >
          <ClipboardCopy size={13} aria-hidden="true" />
          <span>{t.copy}</span>
        </button>
        {onDraft && (
          <button
            className="btn btn--secondary btn--small"
            type="button"
            onClick={() => {
              onDraft(entry.prompt);
              onMarkHandled(entry.turn, t.draftDone);
            }}
          >
            <ClipboardPaste size={13} aria-hidden="true" />
            <span>{t.draft}</span>
          </button>
        )}
        <button
          className="btn btn--primary btn--small"
          type="button"
          disabled={disabled || !onResend}
          title={disabled ? t.resendUnavailable : undefined}
          onClick={() => {
            if (!onResend) return;
            onResend(entry.prompt);
            onMarkHandled(entry.turn, t.resent);
          }}
        >
          <SendHorizontal size={13} aria-hidden="true" />
          <span>{t.resend}</span>
        </button>
        <button className="btn btn--small" type="button" onClick={() => onMarkHandled(entry.turn, t.handledDone)}>
          <Undo2 size={13} aria-hidden="true" />
          <span>{t.handled}</span>
        </button>
      </div>
      {/* 正文：默认折叠（max-height 由 CSS 按 FOLDED_LINES 行算），展开后若仍超长
          由 max-height + overflow-y 兜底——卡片永远不越过自身边界去挡 transcript。 */}
      <div className={`unfiled-card__body${expanded ? " unfiled-card__body--open" : ""}`}>{entry.prompt}</div>
    </div>
  );
}
