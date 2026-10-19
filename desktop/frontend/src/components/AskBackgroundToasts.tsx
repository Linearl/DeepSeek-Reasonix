import { useCallback, useEffect, useMemo, useRef, useState, type ReactElement } from "react";
import { app, onAskBackground, onEvent, type AskBackgroundNotice } from "../lib/bridge";
import { useT } from "../lib/i18n";

// 任务991: 后台 tab 的 ask 感知 toast。面板挂在一个用户看不见的 tab 里时，
// 806/844/849 的交互信号整体失效（鼠标够不着），45s 一到 autopilot 就替答——
// 观感是「我明明在用电脑，它自己把问题答了」。这里补一个右下角限时 toast：
//   · 计时与后端 askWaitFor 同源（事件携带 waitMs，不硬编码 15s），
//     剩余 3s 起按比例渐隐，到期消失——与后端自动决策同刻；
//   · 渐隐期 hover = 「我在看」→ 恢复满亮并同步重置后端窗口（844 信号，
//     hover 预算封顶照旧）；
//   · 点击 = 切 tab（未打开自动打开）+ 重置后端窗口（178 re-presented 语义，
//     非停表——防「点了就走」挂死 run，读完点选项 806 才停表）；
//   · 手动关闭 = 仅移除 toast，零后端调用（关闭≠撤回决策，后端窗口继续）。
// ask 被回答/关闭（含后端自动决策）时 toast 一并消失——prompt_answered /
// prompt_closed 事件是权威的终态信号，countdown 只是它的乐观镜像。

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
  const hoveredKeyRef = useRef<string | null>(null);
  hoveredKeyRef.current = hoveredKey;

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
  useEffect(() => {
    return onAskBackground((notice: AskBackgroundNotice) => {
      if (!notice?.tabId || !notice?.askId) return;
      const key = toastKey(notice.tabId, notice.askId);
      const waitMs = Number(notice.waitMs) || 0;
      setItems((current) => {
        const next = current.filter((item) => item.key !== key);
        next.push({
          key,
          tabId: notice.tabId,
          askId: notice.askId,
          waitMs,
          summary: String(notice.summary ?? ""),
          deadlineAt: waitMs > 0 ? Date.now() + waitMs : null,
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

  // hover = 「我在看」：恢复满亮 + 本地计时重置为满窗口 + 同步重置后端窗口
  // （844 activity 信号，重置而非停表；预算封顶在后端）。
  const handleHover = useCallback((item: AskBackgroundToastItem) => {
    setHoveredKey((current) => (current === item.key ? current : item.key));
    if (item.waitMs > 0) {
      setItems((current) => current.map((entry) =>
        entry.key === item.key ? { ...entry, deadlineAt: Date.now() + entry.waitMs } : entry));
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

  if (fades.length === 0) return null;
  return (
    <div className="ask-bg-toast-container" role="status" aria-live="polite">
      {fades.map(({ item, opacity }) => (
        <div
          key={item.key}
          className="ask-bg-toast"
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
      ))}
    </div>
  );
}
