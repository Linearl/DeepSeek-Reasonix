// tl1746 phase 1 — 任务面板（桌面内嵌只读直读 tasklist.db）。
//
// 形态：独立覆盖层面板（ResourcePanel 同款 wf-panel backdrop + Esc/点
// backdrop 关闭），命令面板 Ctrl+K「工具与诊断 → 任务面板」直达；实验室
// 可观测性组「任务面板」卡承载开关与数据库路径。
//
// 只读红线：数据通道只有 GetTasklistOverview / QueryTasks（Go 侧 mode=ro、
// 仅 SELECT）；面板无任何写 UI——任务增改引导走 tl.py（SSOT，含 changelog
// +backup）。读数口径与 tl.py 对齐：优先级 = prio_mark 列；待办 = 非
// done/verified/cancelled；完成 = done+verified。
//
// 性能：总览 30s 缓存在 Go 侧（cached=true 标记命中），刷新按钮 refresh=true
// 强制重查；列表查询按筛选即查（1697 行级 <10ms 量级，不缓存）。
// 错误态：库不存在/路径无效 → 面板内错误卡（原因 + 去实验室设置 + 重试），
// 不白屏不崩溃。
//
// 测试注入缝：bindings 缺省走真实 bridge；harness 传 fixture（ResourcePanel
// load / CollabHub inboxBindings 同款先例）。

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Activity, RefreshCw, Settings2, X } from "lucide-react";
import {
  app,
  type TasklistCount,
  type TasklistOverview,
  type TasklistQueryFilters,
  type TasklistQueryResult,
  type TasklistTaskRow,
} from "../lib/bridge";
import type { SettingsView } from "../lib/settingsViewTypes";
import { useAppNavigationStore } from "../store/appNavigation";
import { useT } from "../lib/i18n";
import { useMountTransition } from "../lib/useMountTransition";

/** 数据通道注入缝：harness 传 fixture；生产不传 = 真实 bridge（行为零变化）。 */
export interface TasklistPanelBindings {
  getOverview(path: string, refresh: boolean): Promise<TasklistOverview>;
  queryTasks(path: string, filters: TasklistQueryFilters): Promise<TasklistQueryResult>;
  /** 面板每次打开读一次设置快照拿 tasklistDbPath（方案 §三.2：免重启动态）。 */
  settings(): Promise<SettingsView>;
}

const REAL_BINDINGS: TasklistPanelBindings = {
  getOverview: (path, refresh) => app.GetTasklistOverview(path, refresh),
  queryTasks: (path, filters) => app.QueryTasks(path, filters),
  settings: () => app.Settings(),
};

/** tl.py 的 8 态（固定序；状态分布按此展示）。 */
const STATUS_KEYS = ["pending", "in_progress", "partial", "watching", "blocked", "done", "verified", "cancelled"] as const;

/** 8 态 → 三语字典键（显式映射；t() 的 DictKey 是逐键验字典的 union，
 * 模板字面量动态键类型过不了——显式表既类型安全又保未知态原样展示）。 */
const STATUS_DICT: Record<string, "tasklist.status.pending" | "tasklist.status.in_progress" | "tasklist.status.partial" | "tasklist.status.watching" | "tasklist.status.blocked" | "tasklist.status.done" | "tasklist.status.verified" | "tasklist.status.cancelled"> = {
  pending: "tasklist.status.pending",
  in_progress: "tasklist.status.in_progress",
  partial: "tasklist.status.partial",
  watching: "tasklist.status.watching",
  blocked: "tasklist.status.blocked",
  done: "tasklist.status.done",
  verified: "tasklist.status.verified",
  cancelled: "tasklist.status.cancelled",
};

function statusLabel(status: string, t: ReturnType<typeof useT>): string {
  const key = STATUS_DICT[status];
  return key ? t(key) : status;
}

type Tab = "overview" | "list";

export function TasklistPanel({
  open,
  onClose,
  bindings,
}: {
  open: boolean;
  onClose: () => void;
  /** 缺省走真实 bridge；harness 注入 fixture。 */
  bindings?: TasklistPanelBindings;
}) {
  const t = useT();
  const b = bindings ?? REAL_BINDINGS;
  const { mounted, status } = useMountTransition(open, 200);
  const [tab, setTab] = useState<Tab>("overview");
  const [dbPath, setDbPath] = useState(""); // 设置里的覆盖路径（"" = 默认）
  const [overview, setOverview] = useState<TasklistOverview | null>(null);
  const [overviewError, setOverviewError] = useState("");
  const [list, setList] = useState<TasklistQueryResult | null>(null);
  const [listError, setListError] = useState("");
  const [filters, setFilters] = useState<TasklistQueryFilters>({});
  const [loadingOverview, setLoadingOverview] = useState(false);
  // generation fence（ResourcePanel 同款）：面板关闭后过期回包零写入。
  const seqRef = useRef(0);
  const mountedRef = useRef(false);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      seqRef.current += 1;
    };
  }, []);

  const loadOverview = useCallback(async (refresh: boolean) => {
    const seq = ++seqRef.current;
    setLoadingOverview(true);
    try {
      const ov = await b.getOverview(dbPath, refresh);
      if (!mountedRef.current || seq !== seqRef.current) return;
      setOverview(ov);
      setOverviewError("");
    } catch (err) {
      if (!mountedRef.current || seq !== seqRef.current) return;
      setOverview(null);
      setOverviewError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mountedRef.current && seq === seqRef.current) setLoadingOverview(false);
    }
  }, [b, dbPath]);

  const loadList = useCallback(async () => {
    const seq = ++seqRef.current;
    try {
      const res = await b.queryTasks(dbPath, filters);
      if (!mountedRef.current || seq !== seqRef.current) return;
      setList(res);
      setListError("");
    } catch (err) {
      if (!mountedRef.current || seq !== seqRef.current) return;
      setList(null);
      setListError(err instanceof Error ? err.message : String(err));
    }
  }, [b, dbPath, filters]);

  // 首开：读一次设置拿路径 → 拉总览（30s 缓存窗口内命中缓存，秒开）。
  useEffect(() => {
    if (!open) return;
    let alive = true;
    void b.settings()
      .then((s) => { if (alive) setDbPath(s.tasklistDbPath ?? ""); })
      .catch(() => { if (alive) setDbPath(""); });
    return () => { alive = false; };
  }, [open, b]);

  useEffect(() => {
    if (!open) return;
    void loadOverview(false);
  }, [open, loadOverview]);

  // 切到列表 tab（或筛选变化）即查；总览/列表共享代号栅栏。
  useEffect(() => {
    if (!open || tab !== "list") return;
    void loadList();
  }, [open, tab, loadList]);

  // Esc 关闭（document 级；ResourcePanel 同款）。
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

  const onBackdropPointerDown = useCallback(
    (e: React.PointerEvent<HTMLDivElement>) => {
      if (e.target === e.currentTarget) onClose();
    },
    [onClose],
  );

  const maxStatus = useMemo(() => Math.max(1, ...(overview?.byStatus ?? []).map((c) => c.count)), [overview]);
  const maxDomain = useMemo(() => Math.max(1, ...(overview?.openByDomain ?? []).map((c) => c.count)), [overview]);
  const maxPrio = useMemo(() => Math.max(1, ...(overview?.openByPriority ?? []).map((c) => c.count)), [overview]);
  const priorityOptions = useMemo(() => (overview?.byPriority ?? []).map((c) => c.key).filter((k) => k !== ""), [overview]);
  const domainOptions = useMemo(() => (overview?.byDomain ?? []).map((c) => c.key).filter((k) => k !== ""), [overview]);

  if (!mounted) return null;

  return (
    <div className="drawer-backdrop wf-panel-backdrop" data-state={status} onPointerDown={onBackdropPointerDown}>
      <div className="wf-panel res-panel tlpanel" role="dialog" aria-modal="true" aria-label={t("tasklist.title")} data-testid="tl-panel">
        <div className="wf-panel__toolbar">
          <span className="wf-panel__title">
            <Activity size={15} />
            <span className="wf-panel__name">{t("tasklist.title")}</span>
          </span>
          <span className="res-panel__interval" role="tablist" aria-label={t("tasklist.tabGroup")}>
            <button type="button" role="tab" aria-selected={tab === "overview"} data-testid="tl-tab-overview"
              className={`set-seg__btn${tab === "overview" ? " set-seg__btn--on" : ""}`} onClick={() => setTab("overview")}>
              {t("tasklist.tab.overview")}
            </button>
            <button type="button" role="tab" aria-selected={tab === "list"} data-testid="tl-tab-list"
              className={`set-seg__btn${tab === "list" ? " set-seg__btn--on" : ""}`} onClick={() => setTab("list")}>
              {t("tasklist.tab.list")}
            </button>
          </span>
          <span className="res-panel__spacer" />
          {overview && (
            <span className="wf-panel__runstate res-panel__tick" data-testid="tl-cached">
              {overview.cached ? t("tasklist.cached") : new Date(Date.parse(overview.generatedAt) || Date.now()).toLocaleTimeString()}
            </span>
          )}
          <button type="button" className="btn btn--secondary btn--sm" data-testid="tl-refresh"
            title={t("tasklist.refresh")} onClick={() => { void loadOverview(true); if (tab === "list") void loadList(); }}>
            <RefreshCw size={13} />
            {t("tasklist.refresh")}
          </button>
          <button type="button" className="palette__esc" onClick={onClose} aria-label={t("tasklist.close")}>
            <X size={13} />
          </button>
        </div>

        <div className="res-panel__body">
          {overviewError ? (
            <div className="tlpanel__error" role="alert" data-testid="tl-error">
              <p className="tlpanel__error-title">{t("tasklist.error.title")}</p>
              <p className="tlpanel__error-reason">{overviewError}</p>
              <div className="tlpanel__error-actions">
                <button
                  type="button"
                  className="btn btn--secondary btn--small"
                  data-testid="tl-error-go-settings"
                  onClick={() => useAppNavigationStore.getState().setSettingsTarget("experimental")}
                >
                  <Settings2 size={13} />
                  {t("tasklist.error.goSettings")}
                </button>
                <button type="button" className="btn btn--secondary btn--small" onClick={() => void loadOverview(true)}>
                  {t("tasklist.retry")}
                </button>
              </div>
            </div>
          ) : null}

          {!overviewError && !overview && loadingOverview && (
            <p className="settings-field__hint-line" data-testid="tl-loading">{t("tasklist.loading")}</p>
          )}

          {overview && tab === "overview" && (
            <>
              <div className="tlpanel__cards" data-testid="tl-cards">
                <StatCard label={t("tasklist.overview.total")} value={overview.total} />
                <StatCard label={t("tasklist.overview.pending")} value={overview.pending} tone="pending" />
                <StatCard label={t("tasklist.overview.done")} value={overview.done} tone="done" />
                <StatCard label={t("tasklist.overview.cancelled")} value={overview.cancelled} tone="cancelled" />
              </div>

              <section className="res-panel__section">
                <h3 className="res-panel__section-title">{t("tasklist.overview.statusDist")}</h3>
                <div className="tlpanel__bars">
                  {STATUS_KEYS.map((key) => {
                    const bucket = overview.byStatus.find((c) => c.key === key);
                    const count = bucket?.count ?? 0;
                    return (
                      <div className="tlpanel__bar-row" key={key} data-testid={`tl-status-${key}`}>
                        <span className="tlpanel__bar-label">{t(`tasklist.status.${key}`)}</span>
                        {/* 轨道/填充必须用块级元素（ResourcePanel 同款 div 契约）：
                            span 在轨道内的 inline 格式化上下文里 width/height 不生效，
                            填充零宽不渲染（tl1746 返修根因）。 */}
                        <div className="res-panel__sysbar tlpanel__bar">
                          <div className="res-panel__sysbar-fill" style={{ width: `${Math.round((count / maxStatus) * 100)}%` }} />
                        </div>
                        <span className="tlpanel__bar-count">{count}</span>
                      </div>
                    );
                  })}
                </div>
              </section>

              <section className="res-panel__section">
                <h3 className="res-panel__section-title">{t("tasklist.overview.domainDist")}</h3>
                {(overview.openByDomain.length === 0) ? (
                  <p className="settings-field__hint-line">{t("tasklist.overview.emptyOpen")}</p>
                ) : (
                  <div className="tlpanel__bars">
                    {overview.openByDomain.map((c: TasklistCount) => (
                      <div className="tlpanel__bar-row" key={c.key || "__unlabeled__"}>
                        <span className="tlpanel__bar-label">{c.key || t("tasklist.overview.unlabeled")}</span>
                        <div className="res-panel__sysbar tlpanel__bar">
                          <div className="res-panel__sysbar-fill" style={{ width: `${Math.round((c.count / maxDomain) * 100)}%` }} />
                        </div>
                        <span className="tlpanel__bar-count">{c.count}</span>
                      </div>
                    ))}
                  </div>
                )}
              </section>

              <section className="res-panel__section">
                <h3 className="res-panel__section-title">{t("tasklist.overview.prioDist")}</h3>
                {(overview.openByPriority.length === 0) ? (
                  <p className="settings-field__hint-line">{t("tasklist.overview.emptyOpen")}</p>
                ) : (
                  <div className="tlpanel__bars">
                    {overview.openByPriority.map((c: TasklistCount) => (
                      <div className="tlpanel__bar-row" key={c.key || "__unlabeled__"}>
                        <span className="tlpanel__bar-label">{c.key || t("tasklist.overview.unlabeled")}</span>
                        <div className="res-panel__sysbar tlpanel__bar">
                          <div className="res-panel__sysbar-fill" style={{ width: `${Math.round((c.count / maxPrio) * 100)}%` }} />
                        </div>
                        <span className="tlpanel__bar-count">{c.count}</span>
                      </div>
                    ))}
                  </div>
                )}
              </section>
            </>
          )}

          {overview && tab === "list" && (
            <>
              <div className="tlpanel__filters" data-testid="tl-filters">
                <input
                  type="search"
                  className="tlpanel__search"
                  placeholder={t("tasklist.list.search")}
                  value={filters.search ?? ""}
                  data-testid="tl-search"
                  onChange={(e) => setFilters((f) => ({ ...f, search: e.target.value }))}
                />
                <select value={filters.status ?? ""} data-testid="tl-status-filter"
                  onChange={(e) => setFilters((f) => ({ ...f, status: e.target.value }))}>
                  <option value="">{t("tasklist.list.allStatuses")}</option>
                  {STATUS_KEYS.map((key) => (
                    <option key={key} value={key}>{t(`tasklist.status.${key}`)}</option>
                  ))}
                </select>
                <select value={filters.priority ?? ""} data-testid="tl-priority-filter"
                  onChange={(e) => setFilters((f) => ({ ...f, priority: e.target.value }))}>
                  <option value="">{t("tasklist.list.allPriorities")}</option>
                  {priorityOptions.map((key) => (
                    <option key={key} value={key}>{key}</option>
                  ))}
                </select>
                <select value={filters.domain ?? ""} data-testid="tl-domain-filter"
                  onChange={(e) => setFilters((f) => ({ ...f, domain: e.target.value }))}>
                  <option value="">{t("tasklist.list.allDomains")}</option>
                  {domainOptions.map((key) => (
                    <option key={key} value={key}>{key}</option>
                  ))}
                </select>
                <input type="date" value={filters.createdFrom ?? ""} data-testid="tl-from"
                  title={t("tasklist.list.dateFrom")}
                  onChange={(e) => setFilters((f) => ({ ...f, createdFrom: e.target.value }))} />
                <input type="date" value={filters.createdTo ?? ""} data-testid="tl-to"
                  title={t("tasklist.list.dateTo")}
                  onChange={(e) => setFilters((f) => ({ ...f, createdTo: e.target.value }))} />
              </div>

              {listError ? (
                <p className="settings-field__hint-line tlpanel__error-reason" role="alert" data-testid="tl-list-error">{listError}</p>
              ) : null}
              {!listError && list && list.rows.length === 0 ? (
                <p className="settings-field__hint-line" data-testid="tl-empty">{t("tasklist.list.empty")}</p>
              ) : null}
              {!listError && list && list.limited ? (
                <p className="settings-field__hint-line" data-testid="tl-limited">
                  {t("tasklist.list.limited", { total: String(list.total), n: String(list.rows.length) })}
                </p>
              ) : null}

              {list && list.rows.length > 0 && (
                <div className="tlpanel__table-wrap" data-testid="tl-table">
                  <table className="tlpanel__table">
                    <thead>
                      <tr>
                        <th>#</th>
                        <th>{t("tasklist.list.col.status")}</th>
                        <th>{t("tasklist.list.col.priority")}</th>
                        <th>{t("tasklist.list.col.domain")}</th>
                        <th>{t("tasklist.list.col.title")}</th>
                        <th>{t("tasklist.list.col.created")}</th>
                        <th>{t("tasklist.list.col.done")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {list.rows.map((row: TasklistTaskRow) => (
                        <tr key={row.num} data-testid={`tl-row-${row.num}`}>
                          <td className="tlpanel__num">{row.num}</td>
                          <td>
                            <span className={`tlpanel__status tlpanel__status--${row.status}`}>
                              {statusLabel(row.status, t)}
                            </span>
                          </td>
                          <td>{row.priority || "—"}</td>
                          <td>{row.domain || "—"}</td>
                          <td className="tlpanel__title-cell">
                            {row.title}
                            {row.leftover ? <span className="tlpanel__note">{t("tasklist.list.note.leftover", { note: row.leftover })}</span> : null}
                            {row.blockReason ? <span className="tlpanel__note">{t("tasklist.list.note.blocked", { note: row.blockReason })}</span> : null}
                          </td>
                          <td className="tlpanel__date">{row.createdAt || "—"}</td>
                          <td className="tlpanel__date">{row.doneAt || "—"}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function StatCard({ label, value, tone }: { label: string; value: number; tone?: "pending" | "done" | "cancelled" }) {
  return (
    <div className={`tlpanel__card${tone ? ` tlpanel__card--${tone}` : ""}`}>
      <span className="tlpanel__card-value">{value}</span>
      <span className="tlpanel__card-label">{label}</span>
    </div>
  );
}
