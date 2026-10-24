import { useCallback, useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from "react";
import {
  Check,
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
// 项右侧固定渲染一个 ChevronRight（▶）展开引导。
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
    setSubmenu(id);
    focusSubmenu(id);
  };

  const closeSubmenu = useCallback((id: SubmenuId) => {
    setSubmenu(null);
    const trigger = id === "copyExport" ? copyExportTriggerRef : id === "views" ? viewsTriggerRef : openersTriggerRef;
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
        <button
          ref={copyExportTriggerRef}
          tabIndex={-1}
          className="topicbar__menu-item"
          type="button"
          role="menuitem"
          aria-haspopup="menu"
          aria-expanded={submenu === "copyExport"}
          onKeyDown={(event) => {
            if (event.key !== "ArrowRight") return;
            event.preventDefault();
            event.stopPropagation();
            openSubmenu("copyExport");
          }}
          onClick={() => (submenu === "copyExport" ? closeSubmenu("copyExport") : openSubmenu("copyExport"))}
        >
          <Copy size={14} />
          <span>{t("topicBar.copyAndExport")}</span>
          {/* tl 1119：含二级子菜单的一级项固定渲染 ▶ 展开引导。 */}
          <ChevronRight className="topicbar__more-submenu-indicator" size={14} aria-hidden="true" />
        </button>
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
          <button
            ref={viewsTriggerRef}
            tabIndex={-1}
            className="topicbar__menu-item"
            type="button"
            role="menuitem"
            aria-haspopup="menu"
            aria-expanded={submenu === "views"}
            onKeyDown={(event) => {
              if (event.key !== "ArrowRight") return;
              event.preventDefault();
              event.stopPropagation();
              openSubmenu("views");
            }}
            onClick={() => (submenu === "views" ? closeSubmenu("views") : openSubmenu("views"))}
          >
            <MessageSquareText size={14} />
            <span>{t("topicBar.sessionView")}</span>
            {/* tl 1119：含二级子菜单的一级项固定渲染 ▶ 展开引导。 */}
            <ChevronRight className="topicbar__more-submenu-indicator" size={14} aria-hidden="true" />
          </button>
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
          {/* tl 1119：含二级子菜单的一级项固定渲染 ▶ 展开引导。 */}
          <ChevronRight className="topicbar__more-submenu-indicator" size={14} aria-hidden="true" />
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
    </div>
  );
}
