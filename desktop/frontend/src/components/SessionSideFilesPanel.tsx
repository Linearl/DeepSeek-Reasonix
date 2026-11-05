import { useEffect, useMemo, useRef, useState } from "react";
import { FilePlus2, FileText, ListPlus } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { collectSessionSideFiles, formatReferenceListForPrompt, type SessionSideFile, type SessionSideItem } from "../lib/sessionSideFiles";
import { workspacePathCopyMenuItems } from "../lib/workspacePathCopyMenuItems";
import { FloatingMenu, FloatingMenuItems, type FloatingMenuItem } from "./FloatingMenu";

export type SideFilesDockVariant = "artifacts" | "references";

const INITIAL = 12;

/** tl1437: front-end path-shape recognition — same forms the open-probe
 *  contract describes (drive letter / UNC / leading slash). An absolute-shaped
 *  artifact path copies as-is; only workspace-relative shapes resolve through
 *  ResolveWorkspacePathForTab (the file tree's copy-absolute bridge). */
function looksAbsolutePath(path: string): boolean {
  return /^[A-Za-z]:[\\/]/.test(path) || path.startsWith("/") || path.startsWith("\\\\");
}

/** tl1437: interactive row list for the artifacts dock variant — the gap list
 *  against the file tree (WorkspaceTreeMenu family) asked for row selection
 *  and a context menu. A click highlights the row (single selection, same
 *  semantics as the tree's preview selection); a right-click selects the
 *  right-clicked row (Explorer behavior) and opens the row menu — the same
 *  FloatingMenu component the tree menu uses, with the tree's copy-path pair
 *  (relative copies the listed path, absolute resolves through
 *  ResolveWorkspacePathForTab with the same scope guard). Without a tabId the
 *  list renders byte-for-byte the static rows — the accordion's shared
 *  FileList above is untouched, so the default files-mode view keeps its
 *  exact pre-tl1437 behavior. */
function DockArtifactList({ files, tabId }: { files: SessionSideFile[]; tabId?: string }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  // tl1437: the selected row path. Component-local like the tree's selection —
  // the artifacts list is a transcript projection, so nothing here persists.
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  // tl1437: the open row menu (viewport point + target path). The menu is
  // rendered through the same FloatingMenu portal the tree menu uses.
  const [menu, setMenu] = useState<{ x: number; y: number; path: string } | null>(null);
  // tl1437: tab id snapshots for the copy-absolute scope guard — the id the
  // menu was opened under vs the id live right now (a tab switch between menu
  // open and item click must not copy another session's resolved path; same
  // guard shape as the tree menu's isScopeCurrent).
  const [menuTabId, setMenuTabId] = useState("");
  const liveTabIdRef = useRef(tabId ?? "");
  liveTabIdRef.current = tabId ?? "";
  const listRef = useRef<HTMLUListElement>(null);
  const visible = expanded ? files : files.slice(0, INITIAL);
  const hidden = files.length - visible.length;
  const interactive = Boolean(tabId);
  // tl1437: dismiss on outside mousedown / scroll / resize / Escape — the
  // WorkspacePanel tree-menu effect verbatim (FloatingMenu stops propagation,
  // so pressing its items never counts as an outside press).
  useEffect(() => {
    if (!menu) return;
    const close = () => setMenu(null);
    const onKey = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    window.addEventListener("mousedown", close);
    listRef.current?.addEventListener("scroll", close, true);
    window.addEventListener("resize", close);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", close);
      listRef.current?.removeEventListener("scroll", close, true);
      window.removeEventListener("resize", close);
      window.removeEventListener("keydown", onKey);
    };
  }, [menu]);
  // tl1437: right-click = select the row, then open the menu on it (the tree's
  // openTreeMenu collapses the selection to the right-clicked row; this list
  // is single-selection so that is just the one row).
  const openRowMenu = (event: { preventDefault(): void; stopPropagation(): void; clientX: number; clientY: number }, path: string) => {
    if (!tabId) return;
    event.preventDefault();
    event.stopPropagation();
    setSelectedPath(path);
    setMenuTabId(tabId);
    setMenu({ x: event.clientX, y: event.clientY, path });
  };
  const menuItems = useMemo<FloatingMenuItem[]>(() => {
    if (!menu) return [];
    const path = menu.path;
    const close = () => setMenu(null);
    const isScopeCurrent = () => liveTabIdRef.current === menuTabId;
    return [
      ...workspacePathCopyMenuItems({
        path,
        resolveAbsolutePath: async () => {
          if (looksAbsolutePath(path)) return path;
          return app.ResolveWorkspacePathForTab(menuTabId, path);
        },
        isScopeCurrent,
        close,
        relativeLabel: t("workspace.copyRelativePath"),
        absoluteLabel: t("workspace.copyAbsolutePath"),
      }),
    ];
  }, [menu, menuTabId, t]);
  if (files.length === 0) return <p className="side-files__empty">{t("sideFiles.empty")}</p>;
  return (
    <>
      <ul ref={listRef} className="side-files__list" role={interactive ? "listbox" : undefined} aria-label={interactive ? t("sideFiles.artifacts") : undefined}>
        {visible.map((f) => (
          <li
            key={f.path}
            className={interactive ? "side-files__item side-files__item--interactive" : "side-files__item"}
            title={interactive ? undefined : `${f.path} · ${f.via}`}
          >
            {interactive ? (
              <button
                type="button"
                role="option"
                aria-selected={selectedPath === f.path}
                className={`side-files__row${selectedPath === f.path ? " side-files__row--selected" : ""}`}
                data-side-file-path={f.path}
                title={`${f.path} · ${f.via}`}
                onClick={() => setSelectedPath(f.path)}
                onContextMenu={(event) => openRowMenu(event, f.path)}
              >
                <FileText size={12} aria-hidden="true" />
                <span>{f.path}</span>
              </button>
            ) : (
              <>
                <FileText size={12} aria-hidden="true" />
                <span>{f.path}</span>
              </>
            )}
          </li>
        ))}
      </ul>
      {hidden > 0 && !expanded && (
        <div className="side-files__foot">
          <button type="button" className="btn btn--small" onClick={() => setExpanded(true)}>
            {t("sideFiles.showMore", { count: hidden })}
          </button>
        </div>
      )}
      {menu && (
        <FloatingMenu x={menu.x} y={menu.y} estimatedHeight={96} className="workspace-tree-menu">
          <FloatingMenuItems items={menuItems} />
        </FloatingMenu>
      )}
    </>
  );
}

function FileList({ files }: { files: SessionSideFile[] }) {
  const t = useT();
  const [expanded, setExpanded] = useState(false);
  const visible = expanded ? files : files.slice(0, INITIAL);
  const hidden = files.length - visible.length;
  if (files.length === 0) return <p className="side-files__empty">{t("sideFiles.empty")}</p>;
  return (
    <>
      <ul className="side-files__list">
        {visible.map((f) => (
          <li key={f.path} className="side-files__item" title={`${f.path} · ${f.via}`}>
            <FileText size={12} aria-hidden="true" />
            <span>{f.path}</span>
          </li>
        ))}
      </ul>
      {hidden > 0 && !expanded && (
        <div className="side-files__foot">
          <button type="button" className="btn btn--small" onClick={() => setExpanded(true)}>
            {t("sideFiles.showMore", { count: hidden })}
          </button>
        </div>
      )}
    </>
  );
}

export interface SessionSideFilesPanelProps {
  items: readonly SessionSideItem[];
  /** Appends a reference-list block to the composer draft. */
  onInjectReferences?: (text: string) => void;
}

/**
 * Task 114: right-panel grouping of session-produced artifacts vs files the
 * session read (references), with a one-click context injection for refs.
 */
export function SessionSideFilesPanel({ items, onInjectReferences }: SessionSideFilesPanelProps) {
  const t = useT();
  const { artifacts, references } = useMemo(() => collectSessionSideFiles(items), [items]);
  const empty = artifacts.length === 0 && references.length === 0;
  const inject = () => {
    const block = formatReferenceListForPrompt(references);
    if (block && onInjectReferences) onInjectReferences(block);
  };
  return (
    <div className="side-files" aria-label={t("sideFiles.title")}>
      {empty ? (
        <p className="side-files__empty">{t("sideFiles.empty")}</p>
      ) : (
        <>
          <details className="side-files__group" open>
            <summary title={t("sideFiles.artifactsHint")}>
              <span>
                <FilePlus2 size={13} aria-hidden="true" /> {t("sideFiles.artifacts")}
              </span>
              <span className="side-files__meta">{t("sideFiles.artifactsMeta", { count: artifacts.length })}</span>
            </summary>
            <FileList files={artifacts} />
          </details>
          <details className="side-files__group" open>
            <summary title={t("sideFiles.referencesHint")}>
              <span>
                <FileText size={13} aria-hidden="true" /> {t("sideFiles.references")}
              </span>
              <span className="side-files__meta">{t("sideFiles.referencesMeta", { count: references.length })}</span>
            </summary>
            <FileList files={references} />
            {references.length > 0 && onInjectReferences && (
              <div className="side-files__foot">
                <button type="button" className="btn btn--secondary btn--small" onClick={inject}>
                  <ListPlus size={13} aria-hidden="true" />
                  <span>{t("sideFiles.injectReferences")}</span>
                </button>
              </div>
            )}
          </details>
        </>
      )}
    </div>
  );
}

/**
 * Task 260: single-variant dock view for the two session side-files tabs.
 * "artifacts" renders the write-path list, "references" the read-path list —
 * same aggregation as the combined panel above, one group per tab, with the
 * same empty state and show-more pacing. Read-only: the only action is the
 * references "inject into composer" button, same payload as task 114.
 *
 * tl1437: the artifacts variant swaps the static list for the interactive
 * DockArtifactList when a tabId is provided (App always provides one) — row
 * selection and the row menu close the gap against the file tree. Without a
 * tabId the artifacts body stays byte-for-byte the task-260 shape.
 */
export function SideFilesDockPanel({ items, variant, onInjectReferences, tabId }: {
  items: readonly SessionSideItem[];
  variant: SideFilesDockVariant;
  onInjectReferences?: (text: string) => void;
  /** tl1437: the active tab id — scope for the row menu's ForTab bridges
   *  (open probe / absolute-path resolve). Absent keeps the list static. */
  tabId?: string;
}) {
  const t = useT();
  const { artifacts, references } = useMemo(() => collectSessionSideFiles(items), [items]);
  if (variant === "artifacts") {
    return (
      <div className="side-files side-files--dock" aria-label={t("sideFiles.artifacts")}>
        {artifacts.length === 0 ? (
          <p className="side-files__empty">{t("sideFiles.empty")}</p>
        ) : (
          <details className="side-files__group" open>
            <summary title={t("sideFiles.artifactsHint")}>
              <span>
                <FilePlus2 size={13} aria-hidden="true" /> {t("sideFiles.artifacts")}
              </span>
              <span className="side-files__meta">{t("sideFiles.artifactsMeta", { count: artifacts.length })}</span>
            </summary>
            <DockArtifactList files={artifacts} tabId={tabId} />
          </details>
        )}
      </div>
    );
  }
  return (
    <div className="side-files side-files--dock" aria-label={t("sideFiles.references")}>
      {references.length === 0 ? (
        <p className="side-files__empty">{t("sideFiles.empty")}</p>
      ) : (
        <details className="side-files__group" open>
          <summary title={t("sideFiles.referencesHint")}>
              <span>
                <FileText size={13} aria-hidden="true" /> {t("sideFiles.references")}
              </span>
              <span className="side-files__meta">{t("sideFiles.referencesMeta", { count: references.length })}</span>
            </summary>
          <FileList files={references} />
          {onInjectReferences && (
            <div className="side-files__foot">
              <button
                type="button" className="btn btn--secondary btn--small"
                onClick={() => {
                  const block = formatReferenceListForPrompt(references);
                  if (block) onInjectReferences(block);
                }}
              >
                <ListPlus size={13} aria-hidden="true" />
                <span>{t("sideFiles.injectReferences")}</span>
              </button>
            </div>
          )}
        </details>
      )}
    </div>
  );
}
