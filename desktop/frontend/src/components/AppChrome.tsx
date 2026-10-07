import { PanelLeft, PanelRight, Search } from "lucide-react";
import type { ReactNode } from "react";
import { TabBar } from "./TabBar";
import type { LastSessionWorkspaceInfo, TabMeta } from "../lib/types";
import { useT } from "../lib/i18n";

type DesktopPlatform = "darwin" | "windows" | "linux";

export interface AppChromeProps {
  platform: DesktopPlatform;
  browserPreviewChrome: boolean;
  workbenchChrome?: boolean;
  tabs: TabMeta[];
  activeTabId?: string;
  revealActiveSignal: number;
  commandCompact: boolean;
  sidebarTogglePressed: boolean;
  sidebarExpandBlocked: boolean;
  sidebarCollapsed: boolean;
  sidebarToggleTitle: string;
  workspacePanelMaximized: boolean;
  workspacePanelRenderable: boolean;
  workspacePanelLabel: string;
  onToggleSidebar: () => void;
  onToggleWorkspacePanel: () => void;
  onTabChange: (tabId: string) => void;
  onTabClose: (tabId: string) => void;
  onTabsClose: (tabIds: string[], nextActiveTabId?: string) => void;
  onTabStopAndClose?: (tabId: string) => void;
  onTabsReorder: (tabIds: string[]) => void;
  onNewTab: () => void;
  onOpenPalette: () => void;
  /** 任务 552:标签页概览面板(自带触发按钮),渲染在放大镜按钮左侧;三套 chrome 各一处。 */
  tabOverview?: ReactNode;
  /** Split view (task 70): the tab in the secondary pane, and its toggle. */
  splitTabId?: string | null;
  onToggleSplit?: (tabId: string) => void;
  /** 任务546：新建会话「沿用最近会话的目录」（透传 TabBar；缺省则不渲染下拉）。 */
  onFetchLastSessionWorkspace?: () => Promise<LastSessionWorkspaceInfo | null>;
  onNewTabInWorkspace?: (hint: LastSessionWorkspaceInfo) => void;
}

export function AppChrome({
  platform,
  browserPreviewChrome,
  workbenchChrome = false,
  tabs,
  activeTabId,
  revealActiveSignal,
  commandCompact,
  sidebarTogglePressed,
  sidebarExpandBlocked,
  sidebarCollapsed,
  sidebarToggleTitle,
  workspacePanelMaximized,
  workspacePanelRenderable,
  workspacePanelLabel,
  onToggleSidebar,
  onToggleWorkspacePanel,
  onTabChange,
  onTabClose,
  onTabsClose,
  onTabStopAndClose,
  onTabsReorder,
  onNewTab,
  onOpenPalette,
  tabOverview,
  splitTabId = null,
  onToggleSplit,
  onFetchLastSessionWorkspace,
  onNewTabInWorkspace,
}: AppChromeProps) {
  const t = useT();
  const darwinChrome = platform === "darwin";
  const titlebarDragRail = darwinChrome || platform === "windows";
  const chromeClassName = [
    "app-chrome",
    "app-chrome--tabs",
    darwinChrome ? "app-chrome--darwin-tabs" : "app-chrome--native-tabs",
    workbenchChrome ? "app-chrome--workbench" : "",
    !darwinChrome ? "app-chrome--identityless" : "",
    `app-chrome--platform-${platform}`,
  ].filter(Boolean).join(" ");
  const tabBar = (
    <TabBar
      tabs={tabs}
      activeTabId={activeTabId}
      revealActiveSignal={revealActiveSignal}
      onTabChange={onTabChange}
      onTabClose={onTabClose}
      onTabsClose={onTabsClose}
      onTabStopAndClose={onTabStopAndClose}
      onTabsReorder={onTabsReorder}
      onNewTab={onNewTab}
      onOpenPalette={undefined}
      commandCompact={commandCompact}
      splitTabId={splitTabId}
      onToggleSplit={onToggleSplit}
      onFetchLastSessionWorkspace={onFetchLastSessionWorkspace}
      onNewTabInWorkspace={onNewTabInWorkspace}
    />
  );

  return (
    <header className={chromeClassName}>
      {browserPreviewChrome && darwinChrome && (
        <div className="app-chrome__traffic" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      )}
      {titlebarDragRail && <span className="app-chrome__drag-rail" aria-hidden="true" />}
      <button
        className={[
          "app-chrome__panel-toggle",
          "app-chrome__panel-toggle--left",
          sidebarTogglePressed ? "app-chrome__panel-toggle--pressed" : "",
          sidebarExpandBlocked ? "app-chrome__panel-toggle--blocked" : "",
        ].filter(Boolean).join(" ")}
        type="button"
        onClick={sidebarExpandBlocked ? undefined : onToggleSidebar}
        aria-label={sidebarToggleTitle}
        aria-pressed={!sidebarCollapsed}
        aria-disabled={sidebarExpandBlocked}
      >
        <PanelLeft size={16} />
      </button>
      {workbenchChrome && (
        <>
          {/* 任务 552:标签页概览入口,位于放大镜左侧(CSS 绝对定位再左移一位)。 */}
          {tabOverview}
          <button
            className="app-chrome__workbench-search"
            type="button"
            onClick={onOpenPalette}
            aria-label={t("palette.placeholder")}
          >
            <Search size={18} />
          </button>
        </>
      )}

      {workbenchChrome ? (
        <span className="app-chrome__spacer" aria-hidden="true" />
      ) : darwinChrome ? (
        <>
          <div className="app-chrome__tab-strip app-chrome__tab-strip--darwin">
            {tabBar}
          </div>
          <div
            className={[
              "app-chrome__tools",
              "app-chrome__tools--fixed",
            ].filter(Boolean).join(" ")}
            aria-label={t("tabBar.commandSearch")}
          >
            {/* 任务 552:标签页概览入口,DOM 顺序在放大镜前 = 视觉在其左侧(flex 行)。 */}
            {tabOverview}
            <button
              className={[
                "tabbar__command",
                "tabbar__command--compact",
                "app-chrome__command",
              ].filter(Boolean).join(" ")}
              type="button"
              onClick={onOpenPalette}
              aria-label={t("palette.placeholder")}
              title={t("palette.placeholder")}
            >
              <Search size={16} className="tabbar__command-icon" />
            </button>
          </div>
        </>
      ) : (
        <>
          <div className="app-chrome__tab-strip app-chrome__tab-strip--native">
            {tabBar}
          </div>
          <div
            className={[
              "app-chrome__tools",
            ].filter(Boolean).join(" ")}
            aria-label={t("tabBar.commandSearch")}
          >
            {/* 任务 552:标签页概览入口,DOM 顺序在放大镜前 = 视觉在其左侧(flex 行)。 */}
            {tabOverview}
            <button
              className={[
                "tabbar__command",
                "tabbar__command--compact",
                "app-chrome__command",
              ].filter(Boolean).join(" ")}
              type="button"
              onClick={onOpenPalette}
              aria-label={t("palette.placeholder")}
              title={t("palette.placeholder")}
            >
              <Search size={16} className="tabbar__command-icon" />
            </button>
          </div>
        </>
      )}

      {!workspacePanelMaximized && (
        <button
          className={[
            "app-chrome__panel-toggle",
            "app-chrome__panel-toggle--right",
            workspacePanelRenderable ? "app-chrome__panel-toggle--active" : "",
          ].filter(Boolean).join(" ")}
          type="button"
          onClick={onToggleWorkspacePanel}
          aria-label={workspacePanelLabel}
          aria-pressed={workspacePanelRenderable}
        >
          <PanelRight size={16} />
        </button>
      )}
    </header>
  );
}
