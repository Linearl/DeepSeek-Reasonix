// 任务 552:标签页概览面板——顶栏放大镜左侧的新入口。
//
// 三要素:「搜索标签页…」输入框 + 「打开的标签页」组(图标+标题+相对时间+
// 行内 × 关闭,当前 tab 高亮)+「最近关闭的标签页」组(点击重开)。
// 面板壳复用 fork 的 AnchoredPopover(与模型切换/胶囊面板同一弹层体系),
// 搜索算法在 lib/tabOverviewSearch.ts(AND 过滤+五档加权+稳定排序),
// 最近关闭栈在 lib/tabOverviewModel.ts(上限 8/同身份去重置顶/重开即剪除)。
// 相对时间复用 heartbeat 的 formatRelativeTime;刷新 interval 只在面板打开时
// 挂载(60s),关闭即清。
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent } from "react";
import { ChevronsDown, FileText, MessageSquare, X } from "lucide-react";
import { ANCHORED_POPOVER_CLOSE_MS, AnchoredPopover } from "./AnchoredPopover";
import { useT } from "../lib/i18n";
import { useHeartbeatT } from "../custom/features/heartbeat/heartbeat.i18n";
import { formatRelativeTime } from "../custom/features/heartbeat/heartbeat.presentation";
import {
  buildTabSearchFields,
  filterAndRankTabSearchItems,
  normalizeTabSearchQuery,
} from "../lib/tabOverviewSearch";
import {
  tabOverviewSearchHint,
  tabOverviewTitle,
  tabOverviewTypeLabel,
  type RecentClosedTab,
} from "../lib/tabOverviewModel";
import type { TabMeta } from "../lib/types";

interface TabOverviewRow {
  key: string;
  title: string;
  timeLabel: string;
  icon: "session" | "file";
  active: boolean;
  searchFields: ReturnType<typeof buildTabSearchFields>;
  run: () => void;
  onClose?: () => void;
}

export function TabOverviewPanel({
  tabs,
  activeTabId,
  recentClosedTabs,
  onActivateTab,
  onCloseTab,
  onReopenClosedTab,
  variant = "tools",
}: {
  tabs: TabMeta[];
  activeTabId?: string;
  recentClosedTabs: RecentClosedTab[];
  onActivateTab: (tabId: string) => void;
  onCloseTab: (tabId: string) => void;
  onReopenClosedTab: (entry: RecentClosedTab) => void;
  /** tools = 经典/darwin 顶栏工具区;workbench = 工作台顶栏(绝对定位在放大镜左侧)。 */
  variant?: "tools" | "workbench";
}) {
  const t = useT();
  const hbT = useHeartbeatT();
  const [open, setOpen] = useState(false);
  const [closing, setClosing] = useState(false);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(-1);
  const [now, setNow] = useState(() => Date.now());
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const closeTimerRef = useRef<number | null>(null);

  const clearCloseTimer = useCallback(() => {
    if (closeTimerRef.current !== null) {
      window.clearTimeout(closeTimerRef.current);
      closeTimerRef.current = null;
    }
  }, []);

  const openPanel = useCallback(() => {
    clearCloseTimer();
    setClosing(false);
    setQuery("");
    setActive(-1);
    setNow(Date.now());
    setOpen(true);
  }, [clearCloseTimer]);

  const closePanel = useCallback(() => {
    clearCloseTimer();
    setClosing(true);
    window.requestAnimationFrame(() => setOpen(false));
    const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    closeTimerRef.current = window.setTimeout(() => {
      closeTimerRef.current = null;
      setClosing(false);
      setQuery("");
      setActive(-1);
    }, reduceMotion ? 0 : ANCHORED_POPOVER_CLOSE_MS);
  }, [clearCloseTimer]);

  useEffect(() => () => clearCloseTimer(), [clearCloseTimer]);

  // 相对时间刷新只在面板打开期间运行(60s),关闭即清。
  useEffect(() => {
    if (!open) return;
    setNow(Date.now());
    const interval = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(interval);
  }, [open]);

  const openRows = useMemo<TabOverviewRow[]>(() => {
    return tabs.map((tab) => ({
      key: `open-${tab.id}`,
      title: tabOverviewTitle(tab),
      timeLabel: "",
      icon: tab.tabType === "file" || tab.scope === "file" ? "file" : "session",
      active: tab.id === activeTabId,
      searchFields: buildTabSearchFields(
        tabOverviewTitle(tab),
        tabOverviewSearchHint(tab),
        tabOverviewTypeLabel(tab, t("tabOverview.scopeProject"), t("tabOverview.scopeGlobal")),
      ),
      run: () => {
        onActivateTab(tab.id);
        closePanel();
      },
      onClose: () => onCloseTab(tab.id),
    }));
  }, [activeTabId, closePanel, onActivateTab, onCloseTab, t, tabs]);

  const closedRows = useMemo<TabOverviewRow[]>(() => {
    return recentClosedTabs.map((entry) => ({
      key: `closed-${tabReopenKey(entry)}`,
      title: tabOverviewTitle(entry.tab),
      timeLabel: formatRelativeTime(entry.closedAt, now, hbT),
      icon: entry.tab.tabType === "file" || entry.tab.scope === "file" ? "file" : "session",
      active: false,
      searchFields: buildTabSearchFields(
        tabOverviewTitle(entry.tab),
        tabOverviewSearchHint(entry.tab),
        tabOverviewTypeLabel(entry.tab, t("tabOverview.scopeProject"), t("tabOverview.scopeGlobal")),
      ),
      run: () => {
        onReopenClosedTab(entry);
        closePanel();
      },
    }));
  }, [closePanel, hbT, now, onReopenClosedTab, recentClosedTabs, t]);

  const queryParts = useMemo(() => normalizeTabSearchQuery(query), [query]);
  const filteredOpenRows = useMemo(() => filterAndRankTabSearchItems(openRows, queryParts), [openRows, queryParts]);
  const filteredClosedRows = useMemo(() => filterAndRankTabSearchItems(closedRows, queryParts), [closedRows, queryParts]);
  const flatRows = useMemo(() => [...filteredOpenRows, ...filteredClosedRows], [filteredClosedRows, filteredOpenRows]);
  const hasAnyResult = flatRows.length > 0;

  // 结果集缩小(搜索词变化/行内关闭)时收敛高亮,防止悬空。
  useEffect(() => {
    setActive((current) => (current >= 0 && current >= flatRows.length ? Math.max(0, flatRows.length - 1) : current));
  }, [flatRows.length]);

  // 打开时聚焦搜索框;先等弹层完成定位再聚焦,避免布局期抢焦点。
  useLayoutEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  const onInputKeyDown = useCallback((event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive((current) => (flatRows.length === 0 ? -1 : current < 0 ? 0 : (current + 1) % flatRows.length));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive((current) => (flatRows.length === 0 ? -1 : current <= 0 ? flatRows.length - 1 : current - 1));
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      const row = flatRows[active];
      if (row) row.run();
    }
  }, [active, flatRows]);

  const renderRow = (row: TabOverviewRow, index: number, isOn: boolean) => (
    <div
      key={row.key}
      role="option"
      aria-selected={isOn}
      className={[
        "tab-overview__item",
        isOn ? "tab-overview__item--on" : "",
        row.active ? "tab-overview__item--active" : "",
      ].filter(Boolean).join(" ")}
      onMouseEnter={() => setActive(index)}
      onClick={() => row.run()}
    >
      <span className="tab-overview__item-icon" aria-hidden="true">
        {row.icon === "file" ? <FileText size={14} /> : <MessageSquare size={14} />}
      </span>
      <span className="tab-overview__item-title" title={row.title}>{row.title}</span>
      {row.timeLabel && <span className="tab-overview__item-time">{row.timeLabel}</span>}
      {row.onClose && (
        <button
          type="button"
          className="tab-overview__item-close"
          aria-label={t("tabOverview.closeTabTitle", { title: row.title })}
          title={t("tabOverview.close")}
          // 防误触(对齐 zcode):指针按下先吞掉,避免冒泡触发行级「切换」。
          onPointerDown={(event) => {
            event.preventDefault();
            event.stopPropagation();
          }}
          onClick={(event) => {
            event.preventDefault();
            event.stopPropagation();
            row.onClose?.();
          }}
        >
          <X size={12} />
        </button>
      )}
    </div>
  );

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        className={[
          "tabbar__command",
          "tabbar__command--compact",
          "app-chrome__command",
          "tab-overview__trigger",
          variant === "workbench" ? "tab-overview__trigger--workbench" : "",
        ].filter(Boolean).join(" ")}
        aria-label={t("tabOverview.title")}
        title={t("tabOverview.title")}
        aria-expanded={open && !closing}
        onClick={() => (open || closing ? closePanel() : openPanel())}
      >
        <ChevronsDown size={16} className="tabbar__command-icon" />
      </button>
      <AnchoredPopover
        open={open}
        closing={closing}
        anchorRef={triggerRef}
        onClose={closePanel}
        className="tab-overview__panel"
        align="end"
      >
        <div className="tab-overview" role="dialog" aria-label={t("tabOverview.title")}>
          <div className="tab-overview__searchrow">
            <input
              ref={inputRef}
              className="tab-overview__input"
              value={query}
              onChange={(event) => { setQuery(event.target.value); setActive(0); }}
              onKeyDown={onInputKeyDown}
              placeholder={t("tabOverview.searchPlaceholder")}
              spellCheck={false}
              autoComplete="off"
              aria-label={t("tabOverview.searchPlaceholder")}
            />
          </div>
          <div className="tab-overview__list" role="listbox" aria-label={t("tabOverview.title")}>
            {!hasAnyResult ? (
              <div className="tab-overview__empty">{t("tabOverview.noResults")}</div>
            ) : (
              <>
                {filteredOpenRows.length > 0 && (
                  <div className="tab-overview__group">
                    <div className="tab-overview__group-title">{t("tabOverview.openTabs")}</div>
                    {filteredOpenRows.map((row, index) => renderRow(row, index, index === active))}
                  </div>
                )}
                {filteredClosedRows.length > 0 && (
                  <div className="tab-overview__group">
                    <div className="tab-overview__group-title">{t("tabOverview.recentlyClosed")}</div>
                    {filteredClosedRows.map((row, index) => renderRow(row, filteredOpenRows.length + index, filteredOpenRows.length + index === active))}
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      </AnchoredPopover>
    </>
  );
}

function tabReopenKey(entry: RecentClosedTab): string {
  return `${entry.tab.scope}\u0000${entry.tab.workspaceRoot}\u0000${entry.tab.topicId}\u0000${entry.tab.sessionPath ?? ""}\u0000${entry.closedAt}`;
}
