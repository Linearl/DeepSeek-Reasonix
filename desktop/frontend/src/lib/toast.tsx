import { createContext, useCallback, useContext, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { recordNotification } from "./notifyArchive";
import { isNotifyKindEnabled, subscribeNotifyDisplayPrefs } from "./notifyDisplayPrefs";
import { ensureRtNoticeStack } from "./rtNotice";

export interface Toast {
  id: number;
  text: string;
  level: "info" | "warn" | "error";
  actionLabel?: string;
  onAction?: () => void;
}

export interface ToastOptions {
  actionLabel?: string;
  onAction?: () => void;
  durationMs?: number;
}

export interface ToastContextValue {
  toasts: Toast[];
  showToast: (text: string, level?: Toast["level"], options?: ToastOptions) => void;
}

const ToastContext = createContext<ToastContextValue>({ toasts: [], showToast: () => {} });

export function useToast() {
  return useContext(ToastContext);
}

let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());
  // tl1464：右下角统一堆叠容器（#rt-notice-stack，任务 910 骨架）。挂载时取/建
  // 一次；info 档 toast 经 createPortal 进该容器与 C 类诊断告警同住，靠
  // data-kind 区分。容器由模块级 ensureRtNoticeStack 幂等创建，StrictMode 双跑
  // 无副作用。
  const [stackEl, setStackEl] = useState<HTMLElement | null>(null);
  useEffect(() => {
    setStackEl(ensureRtNoticeStack());
  }, []);

  const showToast = useCallback((text: string, level: Toast["level"] = "info", options: ToastOptions = {}) => {
    const id = nextId++;
    // tl1458：toast 落通知留档（本 provider 是 28 个 useToast 消费者的单点汇
    // 合处，一处接入全族覆盖）。留档≠拦截——下面的 setToasts/timer 通路原样，
    // toast 照常弹出、照常自动消失；tl1464 起留档也与展示路由无关（开关关闭
    // 只抑制展示，留档照写）。
    recordNotification({ level, source: "toast", text });
    setToasts((prev) => [...prev, { id, text, level, actionLabel: options.actionLabel, onAction: options.onAction }]);
    const timer = setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
      timers.current.delete(id);
    }, options.durationMs ?? (options.actionLabel ? 8000 : 2500));
    timers.current.set(id, timer);
  }, []);

  const dismissToast = useCallback((id: number) => {
    const timer = timers.current.get(id);
    if (timer) clearTimeout(timer);
    timers.current.delete(id);
    setToasts((prev) => prev.filter((t) => t.id !== id));
  }, []);

  // tl1464 展示路由（用户修正1）：error/warn 档留顶部居中原样——位置/样式/
  // 时长零变化。warn 不迁的原因：28 个调用方里大量「失败走 warn 档」的操作
  // 反馈（sendFailed / inbox 错误 / 截图与附件失败等），迁右下会把本该醒目的
  // 失败挪走；info 档全量核对均为成功/中性反馈（导出成功、主题应用、复制成
  // 功等），才收编进右下角 A 类瞬时面。
  // A 类开关只管 info 档的展示（右下）；开关关闭时右下面隐藏，留档照写
  // （showToast 里的 recordNotification 在路由之前），错误/警告不受开关影响。
  const actionFeedbackOn = useSyncExternalStore(subscribeNotifyDisplayPrefs, () =>
    isNotifyKindEnabled("actionFeedback"),
  );
  const topToasts = toasts.filter((t) => t.level === "warn" || t.level === "error");
  const cornerToasts = actionFeedbackOn ? toasts.filter((t) => t.level === "info") : [];

  const renderToastBody = (t: Toast) => (
    <>
      {/* 任务 910: 图标字形与 .rt-notice 徽章同族（语言中立文本字形，
          随 token 着色；emoji 彩色块与统一后的 token 视觉脱节）。 */}
      {t.level === "info" && <span className="toast__icon">ℹ</span>}
      {t.level === "warn" && <span className="toast__icon">⚠</span>}
      {t.level === "error" && <span className="toast__icon">✕</span>}
      <span className="toast__text">{t.text}</span>
      {t.actionLabel && t.onAction && (
        <button
          type="button"
          className="toast__action"
          onClick={(event) => {
            event.stopPropagation();
            dismissToast(t.id);
            t.onAction?.();
          }}
        >
          {t.actionLabel}
        </button>
      )}
    </>
  );

  return (
    <ToastContext.Provider value={{ toasts, showToast }}>
      {children}
      <div className="toast-container" role="status" aria-live="polite">
        {topToasts.map((t) => (
          <div key={t.id} className={`toast toast--${t.level}`} onClick={() => dismissToast(t.id)}>
            {renderToastBody(t)}
          </div>
        ))}
      </div>
      {stackEl && cornerToasts.length > 0 &&
        createPortal(
          cornerToasts.map((t) => (
            // tl1464：A 类操作反馈（info 档）进右下统一堆叠，data-kind="action"
            // 与 C 类 data-kind="diagnostic" 区分；role="status" 逐条保留原容器
            // aria-live=polite 的播报语义。样式/时长/点击关闭全沿用 .toast 现状。
            <div
              key={t.id}
              className={`toast toast--${t.level}`}
              data-kind="action"
              role="status"
              onClick={() => dismissToast(t.id)}
            >
              {renderToastBody(t)}
            </div>
          )),
          stackEl,
        )}
    </ToastContext.Provider>
  );
}
