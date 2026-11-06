// tl1456 资源管理器面板：内存总览（Go runtime memstats + OS 系统内存）+
// 进程表（本进程 + 直接子进程，非全系统枚举）+ 运行中子代理（进程内，
// 复用 RunningSubagents 注册表）。
//
// 形态：独立覆盖层面板（wf-panel 同款 backdrop + Esc/点 backdrop 关闭），
// 挂在命令面板「工具与诊断 → 资源管理器」，不新增 tab、不动默认布局。
// 刷新节流：面板开着才轮询（2s/3s/5s 三档可配，默认 3s），interval 挂在
// [open, refreshMs] 上、关闭/换档即清——关闭后零定时器、零在途轮询；
// Go 侧 ResourceSnapshot 是纯按需读，不落任何采样器。
// 测量诚实性：Measured/Available=false 的字段一律显示「—」/「平台未提供」，
// 绝不用 0 冒充测量值（与 task 184 采样器同纪律）。

import { useCallback, useEffect, useRef, useState } from "react";
import { Cpu, Gauge, List, MemoryStick, RefreshCw, Users, X } from "lucide-react";
import { app, type ResourceSnapshotView } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { formatDurationLabel } from "../lib/durationFormat";
import { useMountTransition } from "../lib/useMountTransition";

const REFRESH_OPTIONS = [2000, 3000, 5000] as const;
const DEFAULT_REFRESH_MS = 3000;

/** bytes → 人类可读大小（PerfMemorySection 同款形态；0/缺失 → "—"）。 */
function formatBytes(n: number | undefined): string {
  if (!n) return "—";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v >= 100 || i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

/** 子代理行时长：startedAt(ms) → 相对快照时刻的绝对时长标签。 */
function subagentElapsed(startedAtMs: number, snapshotTsMs: number, t: ReturnType<typeof useT>): string {
  if (!startedAtMs) return "—";
  return formatDurationLabel(Math.max(0, Math.round((snapshotTsMs - startedAtMs) / 1000)), t);
}

/** 进程时长：秒（OS 读数）；缺失 → "—"。 */
function processElapsed(seconds: number | undefined, measured: boolean, t: ReturnType<typeof useT>): string {
  if (!measured || seconds === undefined || seconds <= 0) return "—";
  return formatDurationLabel(Math.round(seconds), t);
}

function StatItem({ label, value, title }: { label: string; value: string; title?: string }) {
  return (
    <div className="res-panel__stat" title={title}>
      <span className="res-panel__stat-label">{label}</span>
      <span className="res-panel__stat-value">{value}</span>
    </div>
  );
}

export function ResourcePanel({
  open,
  onClose,
  load,
}: {
  open: boolean;
  onClose: () => void;
  /** 快照加载口（测试注入）；缺省走 app.ResourceSnapshot()。 */
  load?: () => Promise<ResourceSnapshotView>;
}) {
  const t = useT();
  const { mounted, status } = useMountTransition(open, 200);
  const [refreshMs, setRefreshMs] = useState<number>(DEFAULT_REFRESH_MS);
  const [snapshot, setSnapshot] = useState<ResourceSnapshotView | null>(null);
  const [error, setError] = useState("");
  // generation fence（Transcript 同款纪律）：轮询/手动刷新发出后，面板可能
  // 已关闭或换档——只有当前代号的响应才允许落状态，过期回包零写入。
  const requestSeqRef = useRef(0);
  const mountedRef = useRef(false);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      requestSeqRef.current += 1; // 卸载即吊销在途请求的落案权
    };
  }, []);

  const refresh = useCallback(async () => {
    const seq = ++requestSeqRef.current;
    try {
      const view = await (load ? load() : app.ResourceSnapshot());
      if (!mountedRef.current || seq !== requestSeqRef.current) return; // 过期回包
      setSnapshot(view);
      setError("");
    } catch (err) {
      if (!mountedRef.current || seq !== requestSeqRef.current) return;
      setError(err instanceof Error ? err.message : String(err));
    }
  }, [load]);

  // 首开即拉一次（不等第一个 tick）。
  useEffect(() => {
    if (!open) return;
    void refresh();
  }, [open, refresh]);

  // 节流轮询：interval 严格挂在 [open, refreshMs]——关闭即清、换档重建。
  // 关闭后本组件由 App 条件卸载，这里是第二重保险（零常驻定时器契约）。
  useEffect(() => {
    if (!open) return;
    const timer = window.setInterval(() => void refresh(), refreshMs);
    return () => window.clearInterval(timer);
  }, [open, refreshMs, refresh]);

  // Esc 关闭（document 级，焦点漂移也生效）。
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  const close = useCallback(() => onClose(), [onClose]);
  const onBackdropPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) close();
    },
    [close],
  );

  if (!mounted) return null;

  const sys = snapshot?.system;
  const sysUsed = sys?.available ? Math.max(0, sys.totalBytes - sys.availableBytes) : 0;
  const sysPercent = sys?.available && sys.totalBytes > 0 ? Math.min(100, Math.round((sysUsed / sys.totalBytes) * 100)) : 0;
  const runtime = snapshot?.runtime;
  const rows = snapshot ? [snapshot.main, ...snapshot.children] : [];
  const snapshotTsMs = snapshot ? Date.parse(snapshot.ts) : 0;

  return (
    <div className="drawer-backdrop wf-panel-backdrop res-panel-backdrop" data-state={status} onPointerDown={onBackdropPointerDown}>
      <div className="wf-panel res-panel" role="dialog" aria-modal="true" aria-label={t("resourcePanel.title")} data-testid="res-panel">
        <div className="wf-panel__toolbar">
          <span className="wf-panel__title">
            <Gauge size={15} />
            <span className="wf-panel__name">{t("resourcePanel.title")}</span>
          </span>
          {snapshot ? (
            <span className="wf-panel__runstate res-panel__tick" data-testid="res-panel-updated-at">
              {new Date(snapshotTsMs || Date.now()).toLocaleTimeString()}
            </span>
          ) : null}
          <span className="res-panel__spacer" />
          <span className="res-panel__interval" role="group" aria-label={t("resourcePanel.interval")}>
            {REFRESH_OPTIONS.map((ms) => (
              <button
                key={ms}
                type="button"
                className={`set-seg__btn${refreshMs === ms ? " set-seg__btn--on" : ""}`}
                data-testid={`res-interval-${ms}`}
                onClick={() => setRefreshMs(ms)}
              >
                {t("resourcePanel.intervalOption", { s: Math.round(ms / 1000) })}
              </button>
            ))}
          </span>
          <button type="button" className="btn btn--secondary btn--sm" data-testid="res-refresh" onClick={() => void refresh()}>
            <RefreshCw size={13} />
            {t("resourcePanel.refresh")}
          </button>
          <button type="button" className="palette__esc" onClick={close} aria-label={t("resourcePanel.close")}>
            <X size={13} />
          </button>
        </div>

        <div className="res-panel__body">
          {error ? (
            <p className="settings-field__hint-line res-panel__error" role="alert" data-testid="res-error">
              {t("resourcePanel.loadFailed", { error })}
            </p>
          ) : null}
          {!snapshot && !error ? (
            <p className="settings-field__hint-line" data-testid="res-loading">{t("resourcePanel.loading")}</p>
          ) : null}

          {snapshot ? (
            <>
              {/* —— 系统内存（OS API；平台未提供时诚实降级） —— */}
              <section className="res-panel__section" data-testid="res-system">
                <h3 className="res-panel__section-title">
                  <MemoryStick size={14} />
                  {t("resourcePanel.system.title")}
                </h3>
                {sys?.available ? (
                  <>
                    <div className="res-panel__sysbar" role="img" aria-label={t("resourcePanel.system.used", { percent: sysPercent })}>
                      <div className="res-panel__sysbar-fill" data-testid="res-system-fill" style={{ width: `${sysPercent}%` }} />
                    </div>
                    <p className="settings-field__hint-line" data-testid="res-system-label">
                      {t("resourcePanel.system.summary", {
                        used: formatBytes(sysUsed),
                        total: formatBytes(sys.totalBytes),
                        available: formatBytes(sys.availableBytes),
                        percent: sysPercent,
                      })}
                    </p>
                  </>
                ) : (
                  <p className="settings-field__hint-line">{t("resourcePanel.system.unavailable")}</p>
                )}
              </section>

              {/* —— Go runtime 内存面（本进程堆的真实读数） —— */}
              <section className="res-panel__section" data-testid="res-runtime">
                <h3 className="res-panel__section-title">
                  <Cpu size={14} />
                  {t("resourcePanel.runtime.title")}
                </h3>
                <div className="res-panel__stats">
                  <StatItem label={t("resourcePanel.runtime.heapAlloc")} value={formatBytes(runtime?.heapAllocBytes)} />
                  <StatItem label={t("resourcePanel.runtime.heapSys")} value={formatBytes(runtime?.heapSysBytes)} />
                  <StatItem label={t("resourcePanel.runtime.heapObjects")} value={runtime?.heapObjects ? String(runtime.heapObjects) : "—"} />
                  <StatItem label={t("resourcePanel.runtime.totalSys")} value={formatBytes(runtime?.sysBytes)} />
                  <StatItem label={t("resourcePanel.runtime.goroutines")} value={runtime?.goroutines ? String(runtime.goroutines) : "—"} />
                  <StatItem
                    label={t("resourcePanel.runtime.gc")}
                    title={t("resourcePanel.runtime.gcPause")}
                    value={runtime?.numGC ? `${runtime.numGC} · ${runtime.lastGCPauseMs.toFixed(2)}ms` : "—"}
                  />
                </div>
              </section>

              {/* —— 进程表：本进程 + 直接子进程（非全系统枚举） —— */}
              <section className="res-panel__section" data-testid="res-processes">
                <h3 className="res-panel__section-title">
                  <List size={14} />
                  {t("resourcePanel.processes.title")}
                </h3>
                <table className="res-panel__table">
                  <thead>
                    <tr>
                      <th scope="col">{t("resourcePanel.col.name")}</th>
                      <th scope="col">{t("resourcePanel.col.pid")}</th>
                      <th scope="col">{t("resourcePanel.col.memory")}</th>
                      <th scope="col">{t("resourcePanel.col.uptime")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row) => (
                      <tr key={row.kind === "main" ? "main" : row.pid} data-testid={row.kind === "main" ? "res-row-main" : undefined} data-res-pid={row.pid}>
                        <td className="res-panel__name">
                          {row.name}
                          {row.kind === "main" ? <span className="res-panel__badge">{t("resourcePanel.processes.mainBadge")}</span> : null}
                        </td>
                        <td className="res-panel__mono">{row.pid > 0 ? row.pid : "—"}</td>
                        <td className="res-panel__mono" data-testid={row.kind === "main" ? "res-main-memory" : undefined}>
                          {row.measured ? formatBytes(row.memoryBytes) : "—"}
                        </td>
                        <td>{processElapsed(row.uptimeSeconds, row.kind === "main" || row.measured, t)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {snapshot.children.length === 0 ? (
                  <p className="settings-field__hint-line">{t("resourcePanel.processes.noChildren")}</p>
                ) : null}
              </section>

              {/* —— 运行中子代理（进程内 goroutine，无独立 PID/内存） —— */}
              <section className="res-panel__section" data-testid="res-subagents">
                <h3 className="res-panel__section-title">
                  <Users size={14} />
                  {t("resourcePanel.subagents.title")}
                </h3>
                {snapshot.subagents.length === 0 ? (
                  <p className="settings-field__hint-line">{t("resourcePanel.subagents.empty")}</p>
                ) : (
                  <ul className="res-panel__subagent-list">
                    {snapshot.subagents.map((sub) => (
                      <li key={sub.ref} data-testid="res-subagent-row" data-res-subagent-ref={sub.ref} className="res-panel__subagent">
                        <span className="res-panel__subagent-name">{sub.name || sub.ref}</span>
                        {sub.description ? <span className="res-panel__subagent-desc">{sub.description}</span> : null}
                        {sub.origin ? <span className="res-panel__subagent-origin">{sub.origin}</span> : null}
                        <span className="res-panel__subagent-elapsed">{subagentElapsed(sub.startedAt, snapshotTsMs, t)}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            </>
          ) : null}
        </div>
      </div>
    </div>
  );
}
