import { CloudOff, Loader2, TriangleAlert } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { useT } from "../lib/i18n";
import { historyLoadPhase, historyLoadPhaseKey, sessionLoadNow } from "../lib/sessionLoadPhase";
import type { SessionAvailability } from "../lib/sessionAvailability";

/** A persistent status region, outside the collapsible transcript. Key by session identity. */
export function SessionRecoveryBanner({ availability, onRetry, tabId }: {
  availability: SessionAvailability;
  onRetry?: () => Promise<unknown>;
  /** 任务 560：本地历史加载时据此轮询后端阶段名，分阶段显示主标题。 */
  tabId?: string;
}) {
  const t = useT();
  const detailId = useId();
  const mounted = useRef(false);
  const inFlight = useRef(false);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState("");
  const [expanded, setExpanded] = useState(false);
  // 任务 560：本地历史加载期间轮询后端阶段名（1s 一拍），把主标题分成
  // 「读取会话索引」「还原消息历史」两态；同时累计等待秒数，≥10 秒追加
  // 「已等待 n 秒」行。远程连接类不轮询（远程会话不经过本地
  // HistorySliceForTab，轮询只会拿到空串，届时自然回落通用文案）。
  // 阶段名/时钟取自 sessionLoadPhase 的注入点，测试可替换。
  // 注意：hooks 必须全部位于下方 early return 之前。
  const [phase, setPhase] = useState("");
  const [waitedSeconds, setWaitedSeconds] = useState(0);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => { setActionError(""); setExpanded(false); }, [availability.kind, availability.detail]);
  const retry = async () => {
    if (!onRetry || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setActionError("");
    try {
      await onRetry();
    } catch (error) {
      if (mounted.current) setActionError(error instanceof Error ? error.message : String(error));
    } finally {
      inFlight.current = false;
      if (mounted.current) setBusy(false);
    }
  };
  const loading = busy || availability.kind === "loading";
  const connection = availability.source === "connection";
  const historyLoading = loading && !connection;
  useEffect(() => {
    if (!historyLoading) {
      setPhase("");
      setWaitedSeconds(0);
      return;
    }
    let alive = true;
    const startedAt = sessionLoadNow();
    setPhase("");
    setWaitedSeconds(0);
    const tick = () => {
      setWaitedSeconds(Math.max(0, Math.floor((sessionLoadNow() - startedAt) / 1000)));
      // 空串=阶段未知/无进行中的读：清回兜底文案，保证「阶段未知→兜底」。
      historyLoadPhase(tabId ?? "").then((name) => {
        if (alive) setPhase(name);
      }).catch(() => { /* 阶段名是锦上添花：轮询失败保留当前显示 */ });
    };
    tick();
    const timer = window.setInterval(tick, 1000);
    return () => { alive = false; window.clearInterval(timer); };
  }, [historyLoading, tabId]);
  const phaseKey = historyLoadPhaseKey(phase);
  // Runtime setup errors already have their own startup/lease recovery controls.
  if (availability.kind === "ready" || availability.source === "runtime") return null;
  const detail = actionError || availability.detail;
  const Icon = loading ? Loader2 : connection ? CloudOff : TriangleAlert;
  return (
    <section className={`session-recovery${loading ? " session-recovery--loading" : ""}`} role={loading ? "status" : "alert"} aria-busy={loading}>
      <Icon size={28} aria-hidden="true" className={loading ? "session-recovery__spinner" : undefined} />
      <div className="session-recovery__copy">
        <strong>{t(loading
          ? connection ? "remoteSurface.connecting" : phaseKey
          : connection ? "sessionRecovery.connectionLost" : "sessionRecovery.historyFailed")}</strong>
        {/* 任务 560：loading 态副文案改用专属的预期时长提示 loadingHint，
            不再误用失败态的 historyHint（「未完成，请重试」）；失败态文案逐字不变。 */}
        <span>{t(actionError ? "sessionRecovery.retryFailed"
          : connection ? "sessionRecovery.connectionHint"
          : loading ? "sessionRecovery.loadingHint"
          : "sessionRecovery.historyHint")}</span>
        {historyLoading && waitedSeconds >= 10
          && <span className="session-recovery__wait">{t("sessionRecovery.waitedSeconds", { n: waitedSeconds })}</span>}
      </div>
      {onRetry && <button type="button" className="btn btn--primary btn--small" disabled={loading} onClick={() => void retry()}>
        {t(loading ? "common.loading" : connection ? "remoteSurface.reconnect" : "sessionRecovery.retryHistory")}
      </button>}
      {detail && <button type="button" className="btn btn--ghost btn--small" aria-expanded={expanded} aria-controls={detailId} onClick={() => setExpanded(value => !value)}>
        {t("sessionRecovery.details")}
      </button>}
      {detail && expanded && <pre className="session-recovery__detail" id={detailId}>{detail}</pre>}
    </section>
  );
}

export function SessionRecoveryPlaceholder({ availability }: { availability: SessionAvailability }) {
  const t = useT();
  const loading = availability.kind === "loading";
  const Icon = loading ? Loader2 : availability.source === "connection" ? CloudOff : TriangleAlert;
  return <div className="session-recovery-placeholder">
    <Icon size={30} aria-hidden="true" className={loading ? "session-recovery__spinner" : undefined} />
    <span>{t(loading ? "common.loading" : "sessionRecovery.contentAfterRecovery")}</span>
  </div>;
}
