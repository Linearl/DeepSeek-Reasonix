import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from "react";
import {
  Check,
  ChevronLeft,
  ChevronRight,
  ClipboardList,
  Copy,
  FileDown,
  FileImage,
  FileJson,
  FileText,
  Folder,
  FolderOpen,
  GitCompare,
  GitBranch,
  MessageSquareText,
  TerminalSquare,
  type LucideIcon,
} from "lucide-react";

import "./TopicbarMoreMenuContent.css";

import { writeClipboardText } from "../lib/clipboard";
import { t } from "../lib/i18n";
import { app as desktopApp } from "../lib/bridge";
import { useToast } from "../lib/toast";
import { OpenerIcon, openWorkspaceWithOpener, type ExternalOpenerBridge } from "./ExternalOpener";
import type { ExternalOpenerView, TabMeta } from "../lib/types";

// tl 1119+1120：在 tl 1100「topicbar 右侧只剩 …」的基础上，把会话更多菜单一级
// 收敛为 7 项：会话摘要 / 复制和导出 ▸ / 会话视图 ▸ / 改动 / 终端 / 打开方式 ▸ 。
// 二级可达零丢失：复制会话·导出会话（MD/JSON/PDF/图片）·路径·日志路径·会话ID
// 全并入「复制和导出 ▸」；转录·轨迹并入「会话视图 ▸」；每个含二级子菜单的一级
// 项右侧固定渲染一个展开引导（tl 1148：关闭态 ▷ 表示「有子菜单」，打开态翻为
// ◁ 指向子菜单实际的左弹方向）。
// tl 1153：二级菜单补平台标准 submenu 交互——hover 一级触发项 ~300ms 自动展开
// （不点击），鼠标移入子菜单不关闭，移出菜单区域仍走既有关闭逻辑（外点/Escape）；
// 触发项交互集中在 SubmenuTrigger + scheduleHoverOpen/cancelHoverOpen，1149 新增
// 二级菜单（分组移动/分组管理）复用同一组件即行为一致。
//
// 已知坑（方案文档踩坑清单，勿"优化"回去）：
//   · 二级菜单必须向左展开（right: calc(100% + 4px)）——菜单贴屏幕右侧，
//     向右展开会弹出屏幕外，用户点不到；
//   · 二级面板不 appendChild 移 DOM（会导致一开就关）；菜单是本地展开状态，
//     外部点击/Escape 在这一层统一关闭。
export interface TopicbarMoreMenuContentProps {
  sessionHasContent: boolean;
  getSessionMarkdown: () => string | Promise<string>;
  exportSession: (format: "markdown" | "json" | "pdf" | "image") => void;
  openChangedDock: () => void;
  toggleTerminal: () => void;
  terminalEnabled?: boolean;
  prefetchTerminal?: () => void;
  openSessionSummary: () => void;
  tasksOpen: boolean;
  copied: boolean;
  initialFocus: "first" | "last";
  onClose: (restoreFocus: boolean) => void;
  onCopied: () => void;
  /** 当前会话 tab（复制 ▸ 与打开方式 ▸ 都按它取值；缺失时对应二级项退化为空）。 */
  activeTab?: Pick<TabMeta, "id" | "scope" | "topicId" | "sessionPath">;
  /** 日志目录（复制 ▸ 的「日志路径」，由装配层给出）。 */
  logDirectory?: string;
  /** 转录|轨迹 视图（任务 704 的能力移入菜单；缺失时这两项不渲染）。 */
  surfaceView?: { value: "transcript" | "trajectory"; setView: (view: "transcript" | "trajectory") => void };
  /** 打开方式 ▸（原 ExternalOpener 黄色文件夹 ▾）：发现列表 + 打开回调。 */
  openers?: {
    views: ExternalOpenerView[];
    preferred: string;
    workspaceOpenable: boolean;
    onOpen: (opener: ExternalOpenerView, persist: boolean) => void;
  };
}

type SubmenuId = "copyExport" | "views" | "openers";

function enabledItems(menu: HTMLElement): HTMLButtonElement[] {
  return Array.from(menu.querySelectorAll<HTMLButtonElement>('[role="menuitem"],[role="menuitemradio"]'))
    .filter((item) => !item.disabled && item.closest('[role="menu"]') === menu);
}

function moveMenuFocus(event: ReactKeyboardEvent<HTMLDivElement>) {
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  event.stopPropagation();
  const items = enabledItems(event.currentTarget);
  if (items.length === 0) return;
  const current = items.indexOf(document.activeElement as HTMLButtonElement);
  const next = event.key === "Home" ? 0
    : event.key === "End" ? items.length - 1
    : event.key === "ArrowDown" ? (current + 1 + items.length) % items.length
    : (current - 1 + items.length) % items.length;
  items[next]?.focus();
}

// tl 1148：二级子菜单固定向左弹出（.topicbar__more-submenu 用
// right: calc(100% + 4px)，一级菜单贴屏幕右缘、向右弹会出屏）。指示箭头随
// aria-expanded 翻转——关闭态 ChevronRight（▷，平台惯例=此项有子菜单），打开态
// ChevronLeft（◁，指向子菜单实际出现的方向），消除「箭头朝右、菜单左弹」的方向
// 歧义。不做「优先右弹、越界左翻」的动态定位（B 方案）：本菜单一律贴右缘，向右
// 弹几乎必然越界，动态测量只会常走左翻分支还引入布局跳动。
function SubmenuIndicator({ open }: { open: boolean }) {
  const Icon = open ? ChevronLeft : ChevronRight;
  return <Icon className="topicbar__more-submenu-indicator" size={14} aria-hidden="true" />;
}

// tl 1153：二级菜单 hover 触发延迟（平台惯例 ~300ms：短暂扫过不弹，停留才弹）。
export const SUBMENU_HOVER_OPEN_DELAY_MS = 300;

// tl 1153：二级触发项组件化——aria-haspopup/aria-expanded、ArrowRight 键盘展开、
// 点击切换、tl 1148 箭头翻转与 hover ~300ms 自动展开（tl 1153）集中在此；1149 新增
// 二级菜单（分组移动/分组管理）复用本组件即获得同一套交互。hover 展开只切状态不
// 移动焦点（鼠标场景焦点留在原处），键盘/点击展开仍走 openSubmenu 的焦点进入路径；
// touch/pen 不做 hover 展开（手指按下即触发 pointerenter，展开语义仍归点击）。
function SubmenuTrigger({
  triggerRef,
  label,
  icon: Icon,
  open,
  disabled = false,
  onHoverOpen,
  onHoverCancel,
  onKeyboardOpen,
  onToggle,
}: {
  triggerRef: RefObject<HTMLButtonElement | null>;
  label: string;
  icon: LucideIcon;
  open: boolean;
  disabled?: boolean;
  onHoverOpen: () => void;
  onHoverCancel: () => void;
  onKeyboardOpen: () => void;
  onToggle: () => void;
}) {
  return (
    <button
      ref={triggerRef}
      tabIndex={-1}
      className="topicbar__menu-item"
      type="button"
      role="menuitem"
      aria-haspopup="menu"
      aria-expanded={open}
      disabled={disabled}
      onPointerEnter={(event) => {
        if (event.pointerType === "touch" || event.pointerType === "pen") return;
        onHoverOpen();
      }}
      onPointerLeave={onHoverCancel}
      onKeyDown={(event) => {
        if (event.key !== "ArrowRight") return;
        event.preventDefault();
        event.stopPropagation();
        onKeyboardOpen();
      }}
      onClick={onToggle}
    >
      <Icon size={14} />
      <span>{label}</span>
      {/* tl 1119：含二级子菜单的一级项固定渲染展开引导；tl 1148：打开态翻 ◁。 */}
      <SubmenuIndicator open={open} />
    </button>
  );
}

// 通用二级子菜单：向左展开、内部移动焦点、ArrowRight/Escape 收起并把焦点还给
// 触发项。触发项负责自己打开对应的 submenu（export / openers / copyInfo）。
function SubmenuShell({
  label,
  submenuRef,
  onCloseSubmenu,
  children,
}: {
  label: string;
  submenuRef: RefObject<HTMLDivElement | null>;
  onCloseSubmenu: () => void;
  children: ReactNode;
}) {
  const onKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === "ArrowLeft" || event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onCloseSubmenu();
      return;
    }
    moveMenuFocus(event);
  };
  return (
    <div ref={submenuRef} className="topicbar__more-submenu" role="menu" aria-label={label} onKeyDown={onKeyDown}>
      {children}
    </div>
  );
}

export function TopicbarMoreMenuContent({
  sessionHasContent,
  getSessionMarkdown,
  exportSession,
  openChangedDock,
  toggleTerminal,
  terminalEnabled = true,
  prefetchTerminal,
  openSessionSummary,
  tasksOpen,
  copied,
  initialFocus,
  onClose,
  onCopied,
  activeTab,
  logDirectory,
  surfaceView,
  openers,
}: TopicbarMoreMenuContentProps) {
  const { showToast } = useToast();
  const menuRef = useRef<HTMLDivElement>(null);
  const copyExportTriggerRef = useRef<HTMLButtonElement>(null);
  const viewsTriggerRef = useRef<HTMLButtonElement>(null);
  const openersTriggerRef = useRef<HTMLButtonElement>(null);
  const copyExportMenuRef = useRef<HTMLDivElement>(null);
  const viewsMenuRef = useRef<HTMLDivElement>(null);
  const openersMenuRef = useRef<HTMLDivElement>(null);
  const [submenu, setSubmenu] = useState<SubmenuId | null>(null);
  // tl 1153：hover 自动展开计时器——整个菜单同时最多一个挂起计时器，换目标即重排，
  // 提前移出触发项即取消（扫过不弹）。
  const hoverOpenTimerRef = useRef<number | null>(null);
  const cancelHoverOpen = useCallback(() => {
    if (hoverOpenTimerRef.current != null) {
      window.clearTimeout(hoverOpenTimerRef.current);
      hoverOpenTimerRef.current = null;
    }
  }, []);
  useEffect(() => () => cancelHoverOpen(), [cancelHoverOpen]);
  const scheduleHoverOpen = useCallback((id: SubmenuId) => {
    cancelHoverOpen();
    hoverOpenTimerRef.current = window.setTimeout(() => {
      hoverOpenTimerRef.current = null;
      // hover 展开只切状态、不移动焦点；子菜单弹出后鼠标移入子菜单不关闭（面板
      // 就挂在触发项 host 下，移出触发项只取消挂起计时器，不收已展开的面板）。
      setSubmenu(id);
    }, SUBMENU_HOVER_OPEN_DELAY_MS);
  }, [cancelHoverOpen]);

  const focusSubmenu = (id: SubmenuId) => {
    const ref = id === "copyExport" ? copyExportMenuRef : id === "views" ? viewsMenuRef : openersMenuRef;
    requestAnimationFrame(() => {
      const items = ref.current ? enabledItems(ref.current) : [];
      items[0]?.focus();
    });
  };

  useEffect(() => {
    const items = menuRef.current ? enabledItems(menuRef.current) : [];
    (initialFocus === "last" ? items[items.length - 1] : items[0])?.focus();
  }, [initialFocus]);

  const openSubmenu = (id: SubmenuId) => {
    cancelHoverOpen();
    setSubmenu(id);
    focusSubmenu(id);
  };

  const closeSubmenu = useCallback((id: SubmenuId) => {
    cancelHoverOpen();
    setSubmenu(null);
    const trigger = id === "copyExport" ? copyExportTriggerRef : id === "views" ? viewsTriggerRef : openersTriggerRef;
    trigger.current?.focus();
  }, [cancelHoverOpen]);

  const closeAndRun = (action: () => void) => {
    onClose(true);
    action();
  };

  const copySession = async () => {
    try {
      await writeClipboardText(await getSessionMarkdown());
    } catch {
      /* clipboard unavailable */
    }
    onCopied();
  };

  const copyInfoValue = async (value: string) => {
    if (!value) return;
    await writeClipboardText(value);
    onCopied();
  };

  const openWithOpener = (opener: ExternalOpenerView, persist: boolean) => {
    const tabID = activeTab?.id;
    if (!tabID) return;
    closeAndRun(() => {
      void openWorkspaceWithOpener(desktopApp as unknown as ExternalOpenerBridge, tabID, opener, persist)
        .catch((error) => showToast(t("externalOpener.failed", {
          name: opener.name,
          error: error instanceof Error ? error.message : String(error),
        }), "error"));
    });
  };

  const onMainMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      onClose(true);
      return;
    }
    moveMenuFocus(event);
  };

  // 会话视图 ▸ 的候选项（转录|轨迹）。surfaceView 缺失时视图子菜单整体不渲染
  // （与 tl 1100 行为一致，避免在无该能力的场景暴露一个空的二级入口）。
  const surfaceViews: { id: "transcript" | "trajectory"; label: string; icon: LucideIcon }[] = surfaceView ? [
    { id: "transcript", label: t("trajectory.view.transcript"), icon: MessageSquareText },
    { id: "trajectory", label: t("trajectory.view.trajectory"), icon: GitBranch },
  ] : [];

  return (
    <div ref={menuRef} className="topicbar__more-menu" role="menu" aria-label={t("topicBar.more")} onKeyDown={onMainMenuKeyDown}>
      <button tabIndex={-1} className={`topicbar__menu-item${tasksOpen ? " topicbar__menu-item--active" : ""}`} type="button" role="menuitem" aria-pressed={tasksOpen} onClick={() => closeAndRun(openSessionSummary)}>
        <ClipboardList size={14} />
        <span>{t("summary.session")}</span>
      </button>
      {/* tl 1120：复制和导出 ▸——原一级「复制会话」「导出会话 ▸」「复制 ▸」三项合并为一项，
          二级覆盖：复制会话 / 导出 MD·JSON·PDF·图片 / 会话路径 / 日志路径 / 会话ID。 */}
      <div className={`topicbar__more-submenu-host${submenu === "copyExport" ? " topicbar__more-submenu-host--open" : ""}`}>
        <SubmenuTrigger
          triggerRef={copyExportTriggerRef}
          label={t("topicBar.copyAndExport")}
          icon={Copy}
          open={submenu === "copyExport"}
          onHoverOpen={() => scheduleHoverOpen("copyExport")}
          onHoverCancel={cancelHoverOpen}
          onKeyboardOpen={() => openSubmenu("copyExport")}
          onToggle={() => (submenu === "copyExport" ? closeSubmenu("copyExport") : openSubmenu("copyExport"))}
        />
        {submenu === "copyExport" && (
          <SubmenuShell label={t("topicBar.copyAndExport")} submenuRef={copyExportMenuRef} onCloseSubmenu={() => closeSubmenu("copyExport")}>
            <button
              tabIndex={-1}
              className="topicbar__more-submenu-item"
              type="button"
              role="menuitem"
              disabled={!sessionHasContent}
              onClick={() => closeAndRun(() => { void copySession(); })}
            >
              {copied ? <Check size={13} /> : <Copy size={13} />}
              <span>{copied ? t("msg.copied") : t("topicBar.copyAll")}</span>
            </button>
            {([
              ["markdown", FileText, t("topicBar.exportMarkdown")],
              ["json", FileJson, t("topicBar.exportJson")],
              ["pdf", FileDown, t("topicBar.exportPdf")],
              ["image", FileImage, t("topicBar.exportImage")],
            ] as const).map(([format, Icon, label]) => (
              <button
                key={format}
                tabIndex={-1}
                className="topicbar__more-submenu-item"
                type="button"
                role="menuitem"
                disabled={!sessionHasContent}
                onClick={() => closeAndRun(() => exportSession(format))}
              >
                <Icon size={13} />
                <span>{label}</span>
              </button>
            ))}
            <button
              tabIndex={-1}
              className="topicbar__more-submenu-item"
              type="button"
              role="menuitem"
              disabled={!activeTab?.sessionPath}
              onClick={() => closeAndRun(() => { void copyInfoValue(activeTab?.sessionPath ?? ""); })}
            >
              <Folder size={13} />
              <span>{t("topicBar.copySessionPath")}</span>
            </button>
            <button
              tabIndex={-1}
              className="topicbar__more-submenu-item"
              type="button"
              role="menuitem"
              disabled={!logDirectory}
              onClick={() => closeAndRun(() => { void copyInfoValue(logDirectory ?? ""); })}
            >
              <FileText size={13} />
              <span>{t("topicBar.copyLogPath")}</span>
            </button>
            <button
              tabIndex={-1}
              className="topicbar__more-submenu-item"
              type="button"
              role="menuitem"
              disabled={!activeTab}
              onClick={() => closeAndRun(() => { void copyInfoValue(activeTab?.topicId || activeTab?.id || ""); })}
            >
              <FileJson size={13} />
              <span>{t("topicBar.copySessionId")}</span>
            </button>
          </SubmenuShell>
        )}
      </div>
      {/* tl 1120：会话视图 ▸——原一级「转录视图」「轨迹视图」两项并入此子菜单，
          带「当前」标记与单选语义（radio）。 */}
      {surfaceViews.length > 0 && (
        <div className={`topicbar__more-submenu-host${submenu === "views" ? " topicbar__more-submenu-host--open" : ""}`}>
          <SubmenuTrigger
            triggerRef={viewsTriggerRef}
            label={t("topicBar.sessionView")}
            icon={MessageSquareText}
            open={submenu === "views"}
            onHoverOpen={() => scheduleHoverOpen("views")}
            onHoverCancel={cancelHoverOpen}
            onKeyboardOpen={() => openSubmenu("views")}
            onToggle={() => (submenu === "views" ? closeSubmenu("views") : openSubmenu("views"))}
          />
          {submenu === "views" && (
            <SubmenuShell label={t("topicBar.sessionView")} submenuRef={viewsMenuRef} onCloseSubmenu={() => closeSubmenu("views")}>
              {surfaceViews.map((view) => {
                const active = surfaceView?.value === view.id;
                const Icon = view.icon;
                return (
                  <button
                    key={view.id}
                    tabIndex={-1}
                    className={`topicbar__more-submenu-item${active ? " topicbar__more-submenu-item--checked" : ""}`}
                    type="button"
                    role="menuitemradio"
                    aria-checked={active}
                    onClick={() => closeAndRun(() => surfaceView?.setView(view.id))}
                  >
                    <Icon size={13} />
                    <span>{view.label}</span>
                    {active && <Check className="topicbar__more-submenu-check" size={13} aria-hidden="true" />}
                  </button>
                );
              })}
            </SubmenuShell>
          )}
        </div>
      )}
      <button tabIndex={-1} className="topicbar__menu-item" type="button" role="menuitem" onClick={() => closeAndRun(openChangedDock)}>
        <GitCompare size={14} />
        <span>{t("workspace.changedTab")}</span>
      </button>
      <button
        tabIndex={-1}
        className="topicbar__menu-item"
        type="button"
        role="menuitem"
        disabled={!terminalEnabled}
        onPointerEnter={terminalEnabled ? prefetchTerminal : undefined}
        onFocus={terminalEnabled ? prefetchTerminal : undefined}
        onClick={() => closeAndRun(toggleTerminal)}
      >
        <TerminalSquare size={14} />
        <span>{t("rightDock.terminal")}</span>
      </button>
      <div className={`topicbar__more-submenu-host${submenu === "openers" ? " topicbar__more-submenu-host--open" : ""}`}>
        <SubmenuTrigger
          triggerRef={openersTriggerRef}
          label={t("topicBar.openIn")}
          icon={FolderOpen}
          open={submenu === "openers"}
          disabled={!openers?.workspaceOpenable || (openers?.views.length ?? 0) === 0}
          onHoverOpen={() => scheduleHoverOpen("openers")}
          onHoverCancel={cancelHoverOpen}
          onKeyboardOpen={() => openSubmenu("openers")}
          onToggle={() => (submenu === "openers" ? closeSubmenu("openers") : openSubmenu("openers"))}
        />
        {submenu === "openers" && openers && (
          <SubmenuShell label={t("topicBar.openIn")} submenuRef={openersMenuRef} onCloseSubmenu={() => closeSubmenu("openers")}>
            {openers.views.map((opener) => (
              <button
                key={opener.id}
                tabIndex={-1}
                className={`topicbar__more-submenu-item${opener.id === openers.preferred ? " topicbar__more-submenu-item--checked" : ""}`}
                type="button"
                role="menuitemradio"
                aria-checked={opener.id === openers.preferred}
                onClick={() => openWithOpener(opener, opener.id !== openers.preferred)}
              >
                <OpenerIcon opener={opener} />
                <span>{opener.name}</span>
                {opener.id === openers.preferred && <Check className="topicbar__more-submenu-check" size={13} aria-hidden="true" />}
              </button>
            ))}
          </SubmenuShell>
        )}
      </div>
    </div>
  );
}
