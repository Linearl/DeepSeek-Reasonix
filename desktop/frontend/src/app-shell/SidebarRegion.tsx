import { lazy, Suspense, type ComponentProps, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { AlarmClock, MessageSquare, MessagesSquare, Settings, SquarePen, Trash2 } from "lucide-react";
// NOTE: the monitor/feedback entry points were removed from this rail — the
// settings pane is the single entry point now (task 121/123). The panels
// themselves are mounted at the App root (App.tsx), not here.
// 452L→856：两图标合一后本行只需要协作中心一个接缝（信号在 Hub 内转发给
// 子面板；与 SettingsPanel 副入口同模式），本卸载树无需新增 props 接线。
import { setCollabHubOpen } from "../components/CollabHubPanel";
import { useHydrateSessionExperimentFlags } from "../app-runtime/useHydrateSessionExperimentFlags";
import { Tooltip } from "../components/Tooltip";
import type { Translator } from "../lib/i18n";
import type { SettingsTab } from "../lib/types";

const ProjectTree = lazy(() => import("../components/ProjectTree").then((module) => ({ default: module.ProjectTree })));

export type SidebarRegionProps = {
  className: string;
  workbench: boolean;
  automation?: boolean;
  collapsed: boolean;
  navTooltipDisabled: boolean;
  resize: {
    min: number;
    max: number;
    value: number;
    onPointerDown: (event: PointerEvent<HTMLButtonElement>) => void;
    onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
    onReset: () => void;
  };
  projectTree: ComponentProps<typeof ProjectTree>;
  t: Translator;
  onNewSession: () => void;
  onOpenTrash: () => void;
  onOpenAutomation: () => void;
  onOpenSettings: (tab: SettingsTab) => void;
};

/** Sidebar presentation shared by classic and workbench layouts. */
export function SidebarRegion(props: SidebarRegionProps) {
  const { t } = props;
  // Task 140: hydrate experiment flags from the host on mount so the buttons
  // appear after restart without a Settings round-trip.
  useHydrateSessionExperimentFlags();
  return (
    <>
      <aside className={props.className} aria-label={t("sidebar.navigation")}>
        {props.workbench ? (
          <>
            <div className="sidebar__head" aria-hidden={props.collapsed}>
              <div className="sidebar__brand sidebar__brand--workbench">
                <span role="img" aria-label="Reasonix" className="sidebar__brand-logo sidebar__brand-logo--workbench" />
              </div>
            </div>
            <div className="sidebar__quick-actions">
              <button className="sidebar__quick-action" type="button" onClick={props.onNewSession}>
                <MessageSquare size={18} aria-hidden="true" /><span>{t("topbar.newSession")}</span>
              </button>
            </div>
          </>
        ) : (
          <>
            <div className="sidebar__brand" aria-hidden={props.collapsed}>
              <span role="img" aria-label="Reasonix" className="sidebar__brand-logo" />
            </div>
            <button className="sidebar__new" onClick={props.onNewSession}>
              <SquarePen size={18} /><span>{t("topbar.newSession")}</span>
            </button>
          </>
        )}
        <section className="sidebar__section sidebar__section--projects">
          <Suspense fallback={null}><ProjectTree {...props.projectTree} /></Suspense>
        </section>
        {/* Shared classic/workbench footer utility row (the task-412 rework
            aligned the two; tl1450 removed the last differing creation branch).
            452L→856：收件站/群聊两键融合为协作中心一键（App.tsx 四位行同步）：
            回收站 → 协作中心 → 自动化 → 设置。未读徽标属挂载侧动态状态
            （useCollabInboxUnreadCount），本卸载树不接；复挂时如需徽标再补
            count 接线。 */}
        <nav className="sidebar__nav sidebar__nav--footer">
          <div className="sidebar__utility-row" aria-label={t("sidebar.utilityActions")}>
            <UtilityButton label={t("sidebar.trash")} icon={<Trash2 size={16} />} onClick={props.onOpenTrash} />
            <UtilityButton label={t("sidebar.collabHub")} icon={<MessagesSquare size={16} />} onClick={() => setCollabHubOpen(true)} />
            <UtilityButton active={props.automation} label={t("heartbeat.scheduler")} icon={<AlarmClock size={16} />} onClick={props.onOpenAutomation} />
            <UtilityButton label={t("topbar.settings")} icon={<Settings size={16} />} onClick={() => props.onOpenSettings("general")} />
          </div>
        </nav>
      </aside>
      <button className="sidebar-resizer" type="button" role="separator" aria-orientation="vertical" aria-label={t("sidebar.resize")}
        aria-valuemin={props.resize.min} aria-valuemax={props.resize.max} aria-valuenow={props.resize.value}
        onPointerDown={props.resize.onPointerDown} onKeyDown={props.resize.onKeyDown} onDoubleClick={props.resize.onReset} />
    </>
  );
}

function UtilityButton({ icon, label, onClick, active }: { icon: ReactNode; label: string; onClick: () => void; active?: boolean }) {
  return <Tooltip label={label} fill side="top"><button className={`sidebar__utility-button${active ? " sidebar__utility-button--active" : ""}`} aria-current={active ? "page" : undefined} type="button" onClick={onClick}>{icon}<span className="sr-only">{label}</span></button></Tooltip>;
}


