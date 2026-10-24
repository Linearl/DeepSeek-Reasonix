import { lazy, Suspense, useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from "react";
import {
  Check,
  ClipboardList,
  Copy,
  Download,
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

const loadExportMenu = () => import("./TopicbarExportMenu");
const TopicbarExportMenu = lazy(async () => ({ default: (await loadExportMenu()).TopicbarExportMenu }));

// tl 1100：topicbar 右侧从 5 个元素精简到只剩「…」——原分段控件（转录|轨迹）、
// 外部打开（黄色文件夹 ▾）的能力全部收进这份菜单，新增「复制 ▸」。一级固定
// 9 项：复制会话 / 导出会话 ▸ / 会话摘要 / 转录视图 / 轨迹视图 / 改动 / 终端 /
// 打开方式 ▸ / 复制 ▸。
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

type SubmenuId = "export" | "openers" | "copyInfo";

function enabledItems(menu: HTMLElement): HTMLButtonElement[] {
  return Array.from(menu.querySelectorAll<HTMLButtonElement>('[role="menuitem"]'))
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
  const exportTriggerRef = useRef<HTMLButtonElement>(null);
  const openersTriggerRef = useRef<HTMLButtonElement>(null);
  const copyInfoTriggerRef = useRef<HTMLButtonElement>(null);
  const exportMenuRef = useRef<HTMLDivElement>(null);
  const openersMenuRef = useRef<HTMLDivElement>(null);
  const copyInfoMenuRef = useRef<HTMLDivElement>(null);
  const [submenu, setSubmenu] = useState<SubmenuId | null>(null);

  const focusSubmenu = (id: SubmenuId) => {
    const ref = id === "export" ? exportMenuRef : id === "openers" ? openersMenuRef : copyInfoMenuRef;
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
    setSubmenu(id);
    focusSubmenu(id);
  };

  const closeSubmenu = useCallback((id: SubmenuId) => {
    setSubmenu(null);
    const trigger = id === "export" ? exportTriggerRef : id === "openers" ? openersTriggerRef : copyInfoTriggerRef;
    trigger.current?.focus();
  }, []);

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

  const surfaceViews: { id: "transcript" | "trajectory"; label: string; icon: LucideIcon }[] = surfaceView ? [
    { id: "transcript", label: t("trajectory.view.transcript"), icon: MessageSquareText },
    { id: "trajectory", label: t("trajectory.view.trajectory"), icon: GitBranch },
  ] : [];

  return (
    <div ref={menuRef} className="topicbar__more-menu" role="menu" aria-label={t("topicBar.more")} onKeyDown={onMainMenuKeyDown}>
      <button tabIndex={-1} className={`topicbar__menu-item${copied ? " topicbar__menu-item--active" : ""}`} type="button" role="menuitem" onClick={() => closeAndRun(() => { void copySession(); })}>
        {copied ? <Check size={14} /> : <Copy size={14} />}
        <span>{copied ? t("msg.copied") : t("topicBar.copyAll")}</span>
      </button>
      <div className={`topicbar__more-submenu-host${submenu === "export" ? " topicbar__more-submenu-host--open" : ""}`}>
        <button
          ref={exportTriggerRef}
          tabIndex={-1}
          className="topicbar__menu-item"
          type="button"
          role="menuitem"
          aria-haspopup="menu"
          aria-expanded={submenu === "export"}
          disabled={!sessionHasContent}
          onKeyDown={(event) => {
            if (event.key !== "ArrowRight") return;
            event.preventDefault();
            event.stopPropagation();
            openSubmenu("export");
          }}
          onClick={() => (submenu === "export" ? closeSubmenu("export") : openSubmenu("export"))}
        >
          <Download size={14} />
          <span>{t("topicBar.export")}</span>
        </button>
        {submenu === "export" && (
          <Suspense fallback={null}>
            <TopicbarExportMenu initialFocus="first" exportSession={exportSession} onClose={(restoreFocus) => { setSubmenu(null); if (restoreFocus) exportTriggerRef.current?.focus(); }} />
          </Suspense>
        )}
      </div>
      <button tabIndex={-1} className={`topicbar__menu-item${tasksOpen ? " topicbar__menu-item--active" : ""}`} type="button" role="menuitem" aria-pressed={tasksOpen} onClick={() => closeAndRun(openSessionSummary)}>
        <ClipboardList size={14} />
        <span>{t("summary.session")}</span>
      </button>
      {surfaceViews.map((view) => {
        const active = surfaceView?.value === view.id;
        const Icon = view.icon;
        return (
          <button
            key={view.id}
            tabIndex={-1}
            className={`topicbar__menu-item${active ? " topicbar__menu-item--active" : ""}`}
            type="button"
            role="menuitemradio"
            aria-checked={active}
            onClick={() => closeAndRun(() => surfaceView?.setView(view.id))}
          >
            <Icon size={14} />
            <span>{view.label}</span>
            {active && <span className="topicbar__menu-item-badge">{t("topicBar.menuCurrent")}</span>}
          </button>
        );
      })}
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
        <button
          ref={openersTriggerRef}
          tabIndex={-1}
          className="topicbar__menu-item"
          type="button"
          role="menuitem"
          aria-haspopup="menu"
          aria-expanded={submenu === "openers"}
          disabled={!openers?.workspaceOpenable || (openers?.views.length ?? 0) === 0}
          onKeyDown={(event) => {
            if (event.key !== "ArrowRight") return;
            event.preventDefault();
            event.stopPropagation();
            openSubmenu("openers");
          }}
          onClick={() => (submenu === "openers" ? closeSubmenu("openers") : openSubmenu("openers"))}
        >
          <FolderOpen size={14} />
          <span>{t("topicBar.openIn")}</span>
        </button>
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
      <div className={`topicbar__more-submenu-host${submenu === "copyInfo" ? " topicbar__more-submenu-host--open" : ""}`}>
        <button
          ref={copyInfoTriggerRef}
          tabIndex={-1}
          className="topicbar__menu-item"
          type="button"
          role="menuitem"
          aria-haspopup="menu"
          aria-expanded={submenu === "copyInfo"}
          onKeyDown={(event) => {
            if (event.key !== "ArrowRight") return;
            event.preventDefault();
            event.stopPropagation();
            openSubmenu("copyInfo");
          }}
          onClick={() => (submenu === "copyInfo" ? closeSubmenu("copyInfo") : openSubmenu("copyInfo"))}
        >
          <Copy size={14} />
          <span>{t("topicBar.copyInfo")}</span>
        </button>
        {submenu === "copyInfo" && (
          <SubmenuShell label={t("topicBar.copyInfo")} submenuRef={copyInfoMenuRef} onCloseSubmenu={() => closeSubmenu("copyInfo")}>
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
    </div>
  );
}
