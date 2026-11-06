import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactElement } from "react";
import { createPortal } from "react-dom";
import { app, onAskBackground, onEvent, type AskBackgroundNotice } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { recordNotification } from "../lib/notifyArchive";
import { isNotifyKindEnabled, subscribeNotifyDisplayPrefs } from "../lib/notifyDisplayPrefs";
import { ensureRtNoticeStack } from "../lib/rtNotice";

// 任务991: 后台 tab 的 ask 感知 toast。面板挂在一个用户看不见的 tab 里时，
// 806/844/849 的交互信号整体失效（鼠标够不着），45s 一到 autopilot 就替答——
// 观感是「我明明在用电脑，它自己把问题答了」。这里补一个右下角限时 toast：
//   · 计时与后端 askWaitFor 同源（事件携带 waitMs，不硬编码 15s），
//     剩余 3s 起按比例渐隐，到期消失——与后端自动决策同刻；
//     tl1464（用户修正2）：展示窗口下限 10s——waitMs 低于 10s 时抬到 10s
//     （「停留3~5s→延长10s」）；高于 10s 的窗口（15s/10min）原样跟随，
//     只延长不缩短。ask 终态（prompt_answered/closed）仍权威移除——不给
//     已被自动决策的问题留「待答」假提醒。
//   · 渐隐期 hover = 「我在看」→ 恢复满亮并同步重置后端窗口（844 信号，
//     hover 预算封顶照旧）；本地计时同样按含下限的满窗口重置。
//   · 点击 = 切 tab（未打开自动打开）+ 重置后端窗口（178 re-presented 语义，
//     非停表——防「点了就走」挂死 run，读完点选项 806 才停表）；
//   · 手动关闭 = 仅移除 toast，零后端调用（关闭≠撤回决策，后端窗口继续）。
// ask 被回答/关闭（含后端自动决策）时 toast 一并消失——prompt_answered /
// prompt_closed 事件是权威的终态信号，countdown 只是它的乐观镜像。
// tl1464：渲染面迁入右下角统一堆叠（#rt-notice-stack，任务 910 骨架），
// data-kind="action" 与 C 类诊断告警区分；.ask-bg-toast-container 容器退役。

export interface AskBackgroundToastItem {
  key: string;
  tabId: string;
  askId: string;
  waitMs: number;
  summary: string;
  /** 绝对到期时刻（Date.now() 基准）；null = attended ask，无自动到期。 */
  deadlineAt: number | null;
}

const FADE_MS = 3000;
const TICK_MS = 200;

/** tl1464：后台 ask toast 展示窗口下限（用户修正2：3~5s → 10s）。 */
export const ASK_BG_TOAST_MIN_MS = 10_000;

/** 测试缝：harness 用小窗口行为实证「跟随 waitMs / 下限抬升」双语义后复位。 */
let minDisplayMsForTest: number | null = null;

/** 测试缝：临时改写下限（null = 复位为 ASK_BG_TOAST_MIN_MS）。 */
export function setAskBgToastMinMsForTest(ms: number | null): void {
  minDisplayMsForTest = ms;
}

/**
 * 展示窗口的唯一起点：waitMs>0 时取 max(waitMs, 下限)（只延长不缩短），
 * attended ask（waitMs=0）无自动到期。到达与 hover 重置共用，保证两处
 * 语义永不漂移。
 */
export function askBgDeadlineAt(nowMs: number, waitMs: number): number | null {
  if (waitMs <= 0) return null;
  const min = minDisplayMsForTest ?? ASK_BG_TOAST_MIN_MS;
  return nowMs + Math.max(waitMs, min);
}

function toastKey(tabId: string, askId: string): string {
  return `${tabId}::${askId}`;
}

export function AskBackgroundToasts({ activeTabId, onSwitchTab }: {
  activeTabId?: string;
  onSwitchTab: (tabId: string) => void;
}): ReactElement | null {
  const t = useT();
  const [items, setItems] = useState<AskBackgroundToastItem[]>([]);
  const [hoveredKey, setHoveredKey] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  // tl1464：右下统一堆叠容器（任务 910 骨架），挂载时取/建一次。
  const [stackEl, setStackEl] = useState<HTMLElement | null>(null);
  const hoveredKeyRef = useRef<string | null>(null);
  hoveredKeyRef.current = hoveredKey;

  useEffect(() => {
    setStackEl(ensureRtNoticeStack());
  }, []);

  // 到期清理 + 渐隐 tick。单一 interval 驱动全部 toast 的剩余时间，
  // 数量个位数（后台同时有 ask 的 tab 极少），200ms 精度足够。
  useEffect(() => {
    if (items.length === 0) return;
    const timer = setInterval(() => {
      const clock = Date.now();
      setNow(clock);
      setItems((current) => current.filter((item) => item.deadlineAt === null || item.deadlineAt > clock));
    }, TICK_MS);
    return () => clearInterval(timer);
  }, [items.length === 0]);

  // 后端通知到达：新增或刷新（同 key 只更新到期时刻，不重复堆叠）。
  // tl1464：A 类开关关闭时只跳过展示态的堆叠（留档在上面照写——留档≠展示）；
  // 渲染处的实时闸门负责「开着时弹出的 toast 在关闭后立即隐藏」。
  useEffect(() => {
    return onAskBackground((notice: AskBackgroundNotice) => {
      if (!notice?.tabId || !notice?.askId) return;
      const key = toastKey(notice.tabId, notice.askId);
      const waitMs = Number(notice.waitMs) || 0;
      // tl1458：后台 ask 落通知留档（warn 档——需要用户处置的挂起问题）。
      // 留档≠拦截：下方 toast 通路原样（计时/hover/点击语义零变化）。
      recordNotification({
        level: "warn",
        source: "ask-bg",
        text: String(notice.summary ?? "") || t("ask.bgToast"),
        detail: `tab ${notice.tabId} · ask ${notice.askId}`,
      });
      if (!isNotifyKindEnabled("actionFeedback")) return;
      setItems((current) => {
        const next = current.filter((item) => item.key !== key);
        next.push({
          key,
          tabId: notice.tabId,
          askId: notice.askId,
          waitMs,
          summary: String(notice.summary ?? ""),
          // tl1464：展示窗口 = max(waitMs, 10s 下限)（用户修正2，只延长不缩短）。
          deadlineAt: askBgDeadlineAt(Date.now(), waitMs),
        });
        return next;
      });
    });
  }, []);

  // 终态信号：ask 被回答/关闭（含后端 806 自动决策、109 拒答、取消）时
  // toast 必须一并消失——countdown 与后端同源，但 hover 重置后两者可能短暂
  // 错位，prompt_* 事件是唯一权威的收尾。
  useEffect(() => {
    return onEvent((e) => {
      if (e.kind !== "prompt_answered" && e.kind !== "prompt_closed") return;
      const askId = e.itemId ?? (e.ask as { id?: string } | undefined)?.id;
      if (!askId) return;
      setItems((current) => current.filter((item) => item.askId !== askId));
    });
  }, []);

  // 用户自己切到了该 tab：toast 完成使命，静默退场。
  useEffect(() => {
    if (!activeTabId) return;
    setItems((current) => current.filter((item) => item.tabId !== activeTabId));
  }, [activeTabId]);

  // hover = 「我在看」：恢复满亮 + 本地计时重置为满窗口（tl1464 起同样含 10s
  // 下限，与到达口径同源）+ 同步重置后端窗口（844 activity 信号，重置而非停
  // 表；预算封顶在后端）。
  const handleHover = useCallback((item: AskBackgroundToastItem) => {
    setHoveredKey((current) => (current === item.key ? current : item.key));
    if (item.waitMs > 0) {
      setItems((current) => current.map((entry) =>
        entry.key === item.key ? { ...entry, deadlineAt: askBgDeadlineAt(Date.now(), entry.waitMs) } : entry));
      try {
        app.NotifyAskActivityForTab(item.tabId, item.askId).catch(() => {});
      } catch {
        // best effort
      }
    }
  }, []);

  const handleMouseLeave = useCallback((item: AskBackgroundToastItem) => {
    setHoveredKey((current) => (current === item.key ? null : current));
  }, []);

  // 点击 = 切 tab + 重置后端窗口（178 re-presented：fresh window + fresh budget，
  // 非停表）+ 移除 toast。
  const handleClick = useCallback((item: AskBackgroundToastItem) => {
    setItems((current) => current.filter((entry) => entry.key !== item.key));
    try {
      app.NotifyAskReattachForTab(item.tabId, item.askId).catch(() => {});
    } catch {
      // best effort
    }
    onSwitchTab(item.tabId);
  }, [onSwitchTab]);

  // 手动关闭：仅移除，零后端调用（关闭≠撤回决策）。
  const handleClose = useCallback((item: AskBackgroundToastItem) => {
    setItems((current) => current.filter((entry) => entry.key !== item.key));
  }, []);

  const fades = useMemo(() => items.map((item) => {
    if (item.deadlineAt === null) return { item, opacity: 1 };
    const remaining = item.deadlineAt - now;
    if (hoveredKey === item.key) return { item, opacity: 1 };
    return { item, opacity: Math.max(0, Math.min(1, remaining / FADE_MS)) };
  }), [items, now, hoveredKey]);

  // tl1464：迁入右下统一堆叠（#rt-notice-stack）。每条自带 role="status" 保留
  // 原容器 aria-live=polite 的播报语义；data-kind="action" 与 C 类区分。
  // A 类开关实时闸门（useSyncExternalStore 订阅）：关闭 = 不展示，留档照写。
  const actionFeedbackOn = useSyncExternalStore(subscribeNotifyDisplayPrefs, () =>
    isNotifyKindEnabled("actionFeedback"),
  );
  if (fades.length === 0 || !stackEl || !actionFeedbackOn) return null;
  return createPortal(
    fades.map(({ item, opacity }) => (
      <div
        key={item.key}
        className="ask-bg-toast"
        data-kind="action"
        role="status"
        style={{ opacity }}
        onClick={() => handleClick(item)}
        onMouseEnter={() => handleHover(item)}
        onMouseLeave={() => handleMouseLeave(item)}
      >
        <span className="ask-bg-toast__icon">⚠</span>
        <div className="ask-bg-toast__body">
          <div className="ask-bg-toast__title">{t("ask.bgToast")}</div>
          {item.summary && <div className="ask-bg-toast__summary">{item.summary}</div>}
        </div>
        <button
          type="button"
          className="ask-bg-toast__close"
          aria-label={t("common.close")}
          title={t("common.close")}
          onClick={(event) => {
            event.stopPropagation();
            handleClose(item);
          }}
        >
          ✕
        </button>
      </div>
    )),
    stackEl,
  );
}
