import { Component, Suspense, lazy, useCallback, useEffect, useState, type ComponentType, type ReactNode } from "react";
import { useT } from "../lib/i18n";
class SurfaceBoundary extends Component<{ fallback: ReactNode; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() { return this.state.failed ? this.props.fallback : this.props.children; }
}
// 任务 739：Suspense 挂起没有终态——分包迟迟解析不了（主线程被长任务饿死）时
// 「加载中」会永远停在那里。看门狗到点把静默等待升级为可操作的失败态
// （提示 + 重试）；期间分包若已就绪，重试是瞬时完成。阈值取宽（本地资源 +
// 大分包首载），只在真挂起时才触发。
const SURFACE_LOAD_STALL_MS = 12_000;
// 任务 739：Suspense 解析探针——随分包一起挂载，挂载即代表分包就绪。看门狗
// 定时器在分包就绪后并不撤表，必须靠这个锁存挡住「面板正常用了 12 秒后被
// 定时器误翻成失败态」。
function SurfaceSettledProbe({ onSettled }: { onSettled: () => void }) {
  useEffect(() => { onSettled(); }, [onSettled]);
  return null;
}
/** Recreate the lazy loader on retry; a rejected React.lazy promise is cached. */
export function ManagementSurface<P extends object>({ loader, surfaceProps, active, onBack }: {
  loader: () => Promise<{ default: ComponentType<P> }>; surfaceProps: P; active: boolean; onBack: () => void;
}) {
  const t = useT();
  const [attempt, setAttempt] = useState(() => ({ key: 0, View: lazy(loader) }));
  const [stalled, setStalled] = useState(false);
  const [settled, setSettled] = useState(false);
  const markSettled = useCallback(() => setSettled(true), []);
  const { View } = attempt;
  useEffect(() => {
    if (!active) return undefined;
    setStalled(false);
    setSettled(false);
    const timer = window.setTimeout(() => setStalled(true), SURFACE_LOAD_STALL_MS);
    return () => window.clearTimeout(timer);
  }, [active, attempt.key]);
  const fallback = (failed: boolean) => active ? <section aria-label={t("settings.title")} style={{ position: "fixed", inset: 0, zIndex: "var(--z-modal)", background: "var(--bg-soft)", padding: "60px 24px", color: "var(--fg)" }}>
    <button className="btn" onClick={onBack}>{t("settings.backToWorkspace")}</button>
    <p role={failed ? "alert" : "status"}>{t(failed ? "settings.loadFailed" : "common.loading")}</p>
    {failed && <button className="btn btn--secondary" onClick={() => setAttempt((value) => ({ key: value.key + 1, View: lazy(loader) }))}>{t("common.retry")}</button>}
  </section> : null;
  if (stalled && !settled) return fallback(true);
  return <SurfaceBoundary key={attempt.key} fallback={fallback(true)}><Suspense fallback={fallback(false)}><SurfaceSettledProbe onSettled={markSettled} /><View {...surfaceProps} /></Suspense></SurfaceBoundary>;
}
