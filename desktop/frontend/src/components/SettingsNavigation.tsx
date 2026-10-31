import { useMemo, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import {
  Activity,
  ChartNoAxesColumn,
  Bot,
  Box,
  Cable,
  Database,
  FlaskConical,
  HardDrive,
  Keyboard,
  LockKeyhole,
  Network,
  Package,
  Palette,
  Plug,
  Radio,
  RefreshCw,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Sparkles,
  Users,
  Webhook,
  X,
} from "lucide-react";
import { useT, type DictKey } from "../lib/i18n";
import { clearSearchHistory, readSearchHistory, recordSearchTerm, removeSearchTerm } from "../lib/searchHistory";
import type { SettingsTab } from "../lib/types";
import { SearchHistoryMenu } from "./SearchHistoryMenu";

export const SETTINGS_NAV_TABS: SettingsTab[] = [
  "general", "bots", "models", "providers", "model-stats", "mcp", "remote", "localserver",
  "skills", "subagents", "plugins", "experimental", "memory",
  "hooks", "diagnostics", "shortcuts", "permissions", "sandbox", "network", "appearance", "storage", "updates",
];

const SETTINGS_TAB_GROUPS: { labelKey: DictKey; tabs: SettingsTab[] }[] = [
  // The fork's own grouping pass left a duplicate of the preferences and
  // connections groups in front of upstream's, so the panel rendered 通用 /
  // 模型偏好 twice. Upstream's grouping wins; the fork-only localserver tab is
  // folded into the connections group instead of getting a group of its own.
  { labelKey: "settings.navGroup.preferences", tabs: ["general"] },
  { labelKey: "settings.tab.models", tabs: ["models", "providers", "model-stats"] },
  { labelKey: "settings.navGroup.connections", tabs: ["bots", "mcp", "remote", "localserver"] },
  { labelKey: "settings.navGroup.capabilities", tabs: ["skills", "subagents", "plugins", "experimental"] },
  { labelKey: "settings.navGroup.context", tabs: ["memory"] },
  { labelKey: "settings.navGroup.automation", tabs: ["hooks", "diagnostics"] },
  { labelKey: "settings.navGroup.security", tabs: ["permissions", "sandbox", "network"] },
  { labelKey: "settings.navGroup.application", tabs: ["appearance", "shortcuts", "storage", "updates"] },
];

export type SettingsNavigationItem = {
  id: SettingsTab;
  label: string;
  meta: string;
  /* 任务 1134：searchTerms 支持惰性 getter——重量级词表（如实验室 63+ 条目
   * 的三语标签拼接）只在首次真正过滤（normalized 非空）时构建；空查询短路
   * 分支不触碰 getter，设置面板首挂零索引成本。 */
  searchTerms?: string | (() => string);
};

export function SettingsNavigation({
  items,
  activeTab,
  onSelect,
}: {
  items: SettingsNavigationItem[];
  activeTab: SettingsTab;
  /* 任务 764：点击时带上当前查询词——宿主可据此把「搜索命中实验室条目」
   * 解析为直达预选（非空才传语义，无查询时为 undefined，旧签名兼容）。 */
  onSelect: (tab: SettingsTab, query?: string) => void;
}) {
  const t = useT();
  const [query, setQuery] = useState("");
  // tl1282: 检索历史——与侧栏「搜索项目或会话」共用一份 LRU（lib/searchHistory，
  // localStorage 持久化跨重启）。默认收起：仅聚焦且无查询词时展示，默认视图零改动。
  const [history, setHistory] = useState<string[]>(readSearchHistory);
  const [historyOpen, setHistoryOpen] = useState(false);
  const historyVisible = historyOpen && query.trim() === "" && history.length > 0;
  const itemById = useMemo(() => new Map(items.map((item) => [item.id, item])), [items]);
  const filteredGroups = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    if (!normalized) return SETTINGS_TAB_GROUPS;
    return SETTINGS_TAB_GROUPS.map((group) => {
      const groupMatches = t(group.labelKey).toLocaleLowerCase().includes(normalized);
      const tabs = group.tabs.filter((id) => {
        if (groupMatches) return true;
        const item = itemById.get(id);
        if (!item) return false;
        const terms = typeof item.searchTerms === "function" ? item.searchTerms() : (item.searchTerms ?? "");
        return `${item.label} ${item.meta} ${terms}`.toLocaleLowerCase().includes(normalized);
      });
      return { ...group, tabs };
    }).filter((group) => group.tabs.length > 0);
  }, [itemById, query, t]);

  const commitQuery = () => {
    if (!query.trim()) return;
    setHistory(recordSearchTerm(query));
  };
  const handleSearchKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    if (event.key === "Enter") commitQuery();
    if (event.key === "Escape") setHistoryOpen(false);
  };
  const selectHistoryTerm = (term: string) => {
    setQuery(term);
    setHistory(recordSearchTerm(term));
    setHistoryOpen(false);
  };

  return (
    <nav className="settings-center__nav" aria-label={t("settings.title")}>
      <label className="settings-center__search">
        <Search size={15} aria-hidden="true" />
        <input
          value={query}
          onChange={(event) => setQuery(event.currentTarget.value)}
          onFocus={() => setHistoryOpen(true)}
          onBlur={() => setHistoryOpen(false)}
          onKeyDown={handleSearchKeyDown}
          placeholder={t("settings.searchPlaceholder")}
          aria-label={t("settings.searchPlaceholder")}
        />
        {query && (
          <button type="button" onClick={() => setQuery("")} aria-label={t("settings.searchClear")}>
            <X size={14} aria-hidden="true" />
          </button>
        )}
        {historyVisible && (
          <SearchHistoryMenu
            entries={history}
            onSelect={selectHistoryTerm}
            onRemove={(term) => setHistory(removeSearchTerm(term))}
            onClear={() => { setHistory(clearSearchHistory()); setHistoryOpen(false); }}
          />
        )}
      </label>
      <div className="settings-center__navgroups">
        {filteredGroups.map((group) => (
          <section className="settings-center__navgroup" key={group.labelKey}>
            <div className="settings-center__navgroup-label">{t(group.labelKey)}</div>
            <div className="settings-center__navitems">
              {group.tabs.map((id) => {
                const item = itemById.get(id);
                if (!item) return null;
                return (
                  <button
                    key={id}
                    className={`settings-center__navitem${activeTab === id ? " settings-center__navitem--active" : ""}`}
                    aria-current={activeTab === id ? "page" : undefined}
                    title={item.meta ? `${item.label} · ${item.meta}` : item.label}
                    onClick={() => {
                      // tl1282: 带查询词点中导航项 = 一次有效检索（提交点复用任务 764
                      // 已有的 onSelect(query) 语义，零新增事件）。
                      commitQuery();
                      onSelect(id, query);
                    }}
                  >
                    <span className="settings-center__navitem-main">
                      {settingsTabIcon(id)}
                      <span>{item.label}</span>
                    </span>
                    {item.meta && query.trim() && <small>{item.meta}</small>}
                  </button>
                );
              })}
            </div>
          </section>
        ))}
        {filteredGroups.length === 0 && (
          <div className="settings-center__navempty" role="status">{t("settings.searchNoResults")}</div>
        )}
      </div>
    </nav>
  );
}

function settingsTabIcon(id: SettingsTab): ReactNode {
  const props = { size: 17, strokeWidth: 1.8, "aria-hidden": true as const };
  switch (id) {
    case "general": return <Settings2 {...props} />;
    case "models": return <Box {...props} />;
    case "model-stats": return <ChartNoAxesColumn {...props} />;
    case "providers": return <Cable {...props} />;
    case "bots": return <Bot {...props} />;
    case "mcp": return <Plug {...props} />;
    case "remote": return <Server {...props} />;
    case "localserver": return <Radio {...props} />;
    case "skills": return <Sparkles {...props} />;
    case "subagents": return <Users {...props} />;
    case "plugins": return <Package {...props} />;
    case "experimental": return <FlaskConical {...props} />;
    case "memory": return <Database {...props} />;
    case "hooks": return <Webhook {...props} />;
    case "diagnostics": return <Activity {...props} />;
    case "shortcuts": return <Keyboard {...props} />;
    case "permissions": return <ShieldCheck {...props} />;
    case "sandbox": return <LockKeyhole {...props} />;
    case "network": return <Network {...props} />;
    case "appearance": return <Palette {...props} />;
    case "storage": return <HardDrive {...props} />;
    case "updates": return <RefreshCw {...props} />;
    // Every tab needs a glyph: a missing case renders nothing, which leaves that
    // nav item visibly misaligned against its neighbours (how "experimental" looked).
    default: return <Settings2 {...props} />;
  }
}
