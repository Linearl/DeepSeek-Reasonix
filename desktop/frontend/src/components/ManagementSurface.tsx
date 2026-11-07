import { Component, Suspense, lazy, useCallback, useEffect, useRef, useState, type ComponentType, type ReactNode } from "react";
import { useT } from "../lib/i18n";
import { reportFrontendLog } from "../lib/frontendLog";
class SurfaceBoundary extends Component<{ fallback: ReactNode; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  // tl1449（症状④）：错误边界此前静默吞掉渲染抛错——页面上只剩「设置加载失败」，
  // desktop.log 与前端日志环里零痕迹，排查无门。抛错必须留痕。
  componentDidCatch(error: unknown) {
    reportFrontendLog("management-surface", "surface render crashed into the error boundary", error instanceof Error ? error.message : String(error), "error");
  }
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
  // tl1449：attempt 变化（首次挂载/重试换新 lazy 分包）时两个标志都重置——旧
  // settled 结论对未就绪的新分包不再适用。
  useEffect(() => {
    setStalled(false);
    setSettled(false);
  }, [attempt.key]);
  // tl1449（症状①根因）：active 变化只重置 stalled 重新计时，绝不重置 settled。
  // 分包的解析状态与页面显隐无关——lazy 已解析，离开再进入仍是就绪的；而
  // SettledProbe 只在挂载时报一次（active 翻转不重挂 Suspense 子树），这里一旦
  // 把 settled 清零就再没有人把它置回，12s 后看门狗会把已渲染的页面（含进行中
  // 的清空回收站确认框）整页误翻成「设置加载失败」，清空从未执行。
  useEffect(() => {
    if (!active) return undefined;
    setStalled(false);
    const timer = window.setTimeout(() => setStalled(true), SURFACE_LOAD_STALL_MS);
    return () => window.clearTimeout(timer);
  }, [active, attempt.key]);
  // tl1449（症状④）：看门狗真的把静默挂起升级为失败态的瞬间落一条日志。只在
  // 不可见→可见的沿上记一次，重复渲染不刷屏；页面隐藏时不渲染不算升级。
  const failureVisible = active && stalled && !settled;
  const failureSeen = useRef(false);
  useEffect(() => {
    if (!failureVisible) { failureSeen.current = false; return; }
    if (failureSeen.current) return;
    failureSeen.current = true;
    reportFrontendLog("management-surface", "watchdog escalated a stalled surface to the load-failed fallback", `no chunk settlement within ${SURFACE_LOAD_STALL_MS}ms`, "warn");
  }, [failureVisible]);
  const fallback = (failed: boolean) => active ? <section aria-label={t("settings.title")} style={{ position: "fixed", inset: 0, zIndex: "var(--z-modal)", background: "var(--bg-soft)", padding: "60px 24px", color: "var(--fg)" }}>
    <button className="btn" onClick={onBack}>{t("settings.backToWorkspace")}</button>
    <p role={failed ? "alert" : "status"}>{t(failed ? "settings.loadFailed" : "common.loading")}</p>
    {failed && <button className="btn btn--secondary" onClick={() => setAttempt((value) => ({ key: value.key + 1, View: lazy(loader) }))}>{t("common.retry")}</button>}
  </section> : null;
  if (stalled && !settled) return fallback(true);
  return <SurfaceBoundary key={attempt.key} fallback={fallback(true)}><Suspense fallback={fallback(false)}><SurfaceSettledProbe onSettled={markSettled} /><View {...surfaceProps} /></Suspense></SurfaceBoundary>;
}
