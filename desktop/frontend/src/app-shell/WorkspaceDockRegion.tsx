import { lazy, Suspense, useEffect, useState, type ComponentProps, type KeyboardEvent, type PointerEvent, type ReactNode } from "react";
import { Activity, FileText, GitBranch, ListTodo, Server } from "lucide-react";
import type { Translator } from "../lib/i18n";
import type { RightDockMode } from "../store/layout";
import { loadHiddenDockTabs, onHiddenDockTabsChange, type DockTabId } from "../lib/dockTabs";

const ContextPanel = lazy(() => import("../components/ContextPanel").then((module) => ({ default: module.ContextPanel })));
const RemotePanel = lazy(() => import("../components/RemotePanel").then((module) => ({ default: module.RemotePanel })));
const TodoDockPanel = lazy(() => import("../components/TodoPanel").then((module) => ({ default: module.TodoPanel })));
const WorkspacePanel = lazy(async () => {
  const [module] = await Promise.all([
    import("../components/WorkspacePanel"),
    import("../components/WorkspacePanelStability.css"),
  ]);
  return { default: module.WorkspacePanel };
});

type TodoProps = ComponentProps<(typeof import("../components/TodoPanel"))["TodoPanel"]>;

export type WorkspaceDockRegionProps = {
  visible: boolean;
  overlay: boolean;
  mode: RightDockMode;
  creation: boolean;
  remoteAvailable: boolean;
  showContext: boolean;
  /** Task 259: the experimental todo-sidebar switch, snapshotted at boot. */
  todoSidebar: boolean;
  /** Task 259: the same payload the composer footer would render; undefined shows the empty state. */
  todo?: { identity: string; props: TodoProps };
  t: Translator;
  onMode: (mode: RightDockMode) => void;
  onRemote: () => void;
  remote: ComponentProps<typeof RemotePanel>;
  context: ComponentProps<typeof ContextPanel>;
  workspace: ComponentProps<typeof WorkspacePanel>;
  workspaceKey: string;
  resizer?: {
    min: number;
    max: number;
    value: number;
    onPointerDown: (event: PointerEvent<HTMLButtonElement>) => void;
    onKeyDown: (event: KeyboardEvent<HTMLButtonElement>) => void;
    onReset: () => void;
  };
};

/** Shared workbench/creation dock; layout variants change data, not component identity. */
export function WorkspaceDockRegion(props: WorkspaceDockRegionProps) {
  const { visible, overlay, mode, creation, remoteAvailable, showContext, todoSidebar, t, onMode, onRemote } = props;
  // Task 259: tab visibility applies live while the switch is on; with the
  // switch off the tab row is the original literal list, byte for byte.
  const [hiddenTabs, setHiddenTabs] = useState<readonly DockTabId[]>(loadHiddenDockTabs);
  useEffect(() => onHiddenDockTabsChange(setHiddenTabs), []);
  const tabVisible = (id: DockTabId) => !hiddenTabs.includes(id);
  return (
    <>
      {props.resizer && (
        <button
          className="workspace-panel-resizer" type="button" role="separator" aria-orientation="vertical"
          aria-label={t("rightDock.resize")} aria-valuemin={props.resizer.min}
          aria-valuemax={props.resizer.max} aria-valuenow={props.resizer.value}
          onPointerDown={props.resizer.onPointerDown} onKeyDown={props.resizer.onKeyDown}
          onDoubleClick={props.resizer.onReset}
        />
      )}
      {visible && (
        <aside className={["workbench-dock", `workbench-dock--${mode}`, overlay ? "workbench-dock--overlay" : ""].join(" ")} aria-label={t("rightDock.workbench")}>
          <div className={["workbench-dock__tools", todoSidebar ? "workbench-dock__tools--wrap" : ""].filter(Boolean).join(" ")}>
            <div className={["workbench-dock__tabs", todoSidebar ? "workbench-dock__tabs--wrap" : ""].filter(Boolean).join(" ")} role="tablist" aria-label={t("rightDock.views")}>
              {todoSidebar ? (
                <>
                  {showContext && !creation && tabVisible("context") && <DockTab active={mode === "context"} onClick={() => onMode("context")} icon={<Activity size={13} />} label={t("rightDock.overview")} />}
                  {tabVisible("files") && <DockTab active={mode === "files"} onClick={() => onMode("files")} icon={<FileText size={13} />} label={t("workspace.filesTab")} />}
                  {tabVisible("changed") && <DockTab active={mode === "changed"} onClick={() => onMode("changed")} icon={<GitBranch size={13} />} label={t("workspace.changedTab")} />}
                  {remoteAvailable && tabVisible("remote") && <DockTab active={mode === "remote"} onClick={onRemote} icon={<Server size={13} />} label={t("rightDock.remote")} />}
                  {tabVisible("todos") && <DockTab active={mode === "todos"} onClick={() => onMode("todos")} icon={<ListTodo size={13} />} label={t("workspace.todosTab")} />}
                </>
              ) : (
                <>
                  {showContext && !creation && <DockTab active={mode === "context"} onClick={() => onMode("context")} icon={<Activity size={13} />} label={t("rightDock.overview")} />}
                  <DockTab active={mode === "files"} onClick={() => onMode("files")} icon={<FileText size={13} />} label={t("workspace.filesTab")} />
                  <DockTab active={mode === "changed"} onClick={() => onMode("changed")} icon={<GitBranch size={13} />} label={t("workspace.changedTab")} />
                  {remoteAvailable && <DockTab active={mode === "remote"} onClick={onRemote} icon={<Server size={13} />} label={t("rightDock.remote")} />}
                </>
              )}
            </div>
          </div>
          <div className="workbench-dock__body">
            {mode === "todos" && todoSidebar ? (
              props.todo ? (
                <div className="workbench-dock__todo">
                  <Suspense fallback={null}><TodoDockPanel key={props.todo.identity} {...props.todo.props} defaultOpen /></Suspense>
                </div>
              ) : (
                <div className="workbench-dock__todo workbench-dock__todo--empty">{t("rightDock.todoEmpty")}</div>
              )
            ) : mode === "remote" ? (
              <Suspense fallback={null}><RemotePanel {...props.remote} /></Suspense>
            ) : mode === "context" && !creation ? (
              <Suspense fallback={null}><ContextPanel {...props.context} /></Suspense>
            ) : (
              <Suspense fallback={null}><WorkspacePanel key={props.workspaceKey} {...props.workspace} /></Suspense>
            )}
          </div>
        </aside>
      )}
    </>
  );
}

function DockTab({ active, onClick, icon, label }: { active: boolean; onClick: () => void; icon: ReactNode; label: string }) {
  return (
    <button type="button" role="tab" aria-selected={active} className={`workbench-dock__tab${active ? " workbench-dock__tab--active" : ""}`} onClick={onClick}>
      {icon}<span className="workbench-dock__tab-label">{label}</span>
    </button>
  );
}
