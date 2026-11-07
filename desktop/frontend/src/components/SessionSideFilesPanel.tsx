import { useEffect, useMemo, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { ExternalLink, FilePlus2, FileText, FolderOpen, ListPlus, MessageSquarePlus, PenLine, TerminalSquare, Trash2 } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useToast } from "../lib/toast";
import { collectSessionSideFiles, formatReferenceListForPrompt, type SessionSideFile, type SessionSideItem } from "../lib/sessionSideFiles";
import { looksAbsolutePath, sideFileDisplayPath, sideFileWorkspaceRelativePath } from "../lib/sideFilePathDisplay";
import { formatWorkspaceReference } from "../lib/workspaceDrag";
import { workspacePathCopyMenuItems } from "../lib/workspacePathCopyMenuItems";
import { useConfirmDialog } from "./ConfirmDialog";
import { FloatingMenu, FloatingMenuItems, type FloatingMenuItem } from "./FloatingMenu";

export type SideFilesDockVariant = "artifacts" | "references";

// tl1468: this pacing is now the COMBINED panel's only (the dock lists render
// every row — see DockSideFileList).
const INITIAL = 12;

// tl1421 同款就地提取：打开/显示失败的 toast 附原因。
function menuErrorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

/** tl1468b: the row's filename for the inline rename seed. Dock rows may be
 *  backslash-shaped canonical paths, so split on both separators — one local
 *  helper instead of a second basename to maintain. */
function sideFileBasename(path: string): string {
  return path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() ?? "";
}

/** tl1468b: inline rename editor for a dock row — the tree's
 *  WorkspaceTreeRenameRow shape (an <input> cannot nest in the row <button>):
 *  Enter commits, Escape cancels, blur commits. It commits through the SAME
 *  RenameWorkspaceEntryForTab bridge the tree uses (see DockSideFileList),
 *  scoped by the menu's tab-id snapshot. */
function DockRowRenameInput({ value, label, onInput, onCommit, onCancel }: {
  value: string;
  label: string;
  onInput: (value: string) => void;
  onCommit: () => void;
  onCancel: () => void;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    const dot = input.value.lastIndexOf(".");
    input.setSelectionRange(0, dot > 0 ? dot : input.value.length);
  }, []);
  const onKeyDown = (event: ReactKeyboardEvent<HTMLInputElement>) => {
    event.stopPropagation();
    if (event.key === "Enter") {
      event.preventDefault();
      onCommit();
    } else if (event.key === "Escape") {
      event.preventDefault();
      onCancel();
    }
  };
  return (
    <div className="side-files__row side-files__row--renaming" data-side-file-renaming="true">
      <FileText size={12} aria-hidden="true" />
      <input
        ref={inputRef}
        className="workspace-tree__rename-input"
        value={value}
        aria-label={label}
        onChange={(event) => onInput(event.target.value)}
        onKeyDown={onKeyDown}
        onBlur={onCommit}
        size={1}
      />
    </div>
  );
}

/** tl1437: interactive row list for the side-files dock variants — the gap
 *  list against the file tree (WorkspaceTreeMenu family) asked for row
 *  selection, a context menu, copy-path, add-reference and open-type actions.
 *  A click highlights the row (single selection, same semantics as the tree's
 *  preview selection); a right-click selects the right-clicked row (Explorer
 *  behavior) and opens the row menu — the same FloatingMenu component the
 *  tree menu uses, with: open/reveal behind the tl1421 openability probe
 *  (ResolveOpenablePathForTab → OpenLocalPath / RevealPath, no dead buttons —
 *  probing disables the pair, an empty probe removes it), the tree's
 *  copy-path pair (relative copies the listed path, absolute resolves through
 *  ResolveWorkspacePathForTab with the same scope guard; absolute-shaped
 *  paths copy as-is), and the tree's add-reference action reusing the dock's
 *  existing composer-injection channel (@path). Without a tabId the list
 *  renders byte-for-byte the static rows — the accordion's shared FileList
 *  above is untouched, so the default files-mode view keeps its exact
 *  pre-tl1437 behavior.
 *
 *  tl1445: the references dock variant shares this same list (the component
 *  was DockArtifactList before it grew the second caller) — both variants
 *  aggregate into the same SessionSideFile {path, via} shape, so the only
 *  per-variant input is the interactive listbox's aria label.
 *
 *  tl1468: rows render the DISPLAY path (sideFileDisplayPath — relative to
 *  the workspace root / session directory, tail-segment+ellipsis for files
 *  outside both, filename always visible) while every byte of the operation
 *  layer (data-side-file-path, row menu, copy/open/reveal/reference, the
 *  hover title) keeps the canonical stored path — the hover title still
 *  carries the full absolute path. The task-260 "show more" fold is gone
 *  here: the dock panel itself is the scroll container
 *  (.workbench-dock__body > .side-files--dock, height:100% + overflow:auto),
 *  so a fixed INITIAL slice folded rows behind a button even when the dock
 *  had free space (the tl1428/tl1468 screenshot complaint) and the old
 *  .side-files__list 240px cap forced an inner scrollbar alongside it. The
 *  combined task-114 panel above keeps its fold + cap (FileList).
 *
 *  tl1468b: the row menu catches up with the file-tree menu's remaining
 *  items (the tree menu's multi-select/dir/pin features have no dock
 *  counterpart; the ACTION layer is the same bridges, not a second copy):
 *  - open in integrated terminal — the tree's onOpenInTerminal channel
 *    (App.openTerminalForPath, the exact prop the tree menu gets). The
 *    terminal bridge only accepts workspace-relative shapes, so the item
 *    exists only when sideFileWorkspaceRelativePath resolves the row inside
 *    the workspace (relative shapes pass the canonical path through;
 *    absolute-inside shapes pass the root-relative slice) and the probe did
 *    not come back empty — a gone file would otherwise open an empty
 *    terminal panel with no session.
 *  - rename / trash / delete-permanent — the tree's same three bridges
 *    (RenameWorkspaceEntryForTab / TrashWorkspaceEntriesForTab /
 *    DeleteWorkspaceEntriesPermanentForTab), which fence the entry to the
 *    workspace root and reject session folder references, so they only
 *    render for workspace-managed rows (same gate as the terminal). Rename
 *    is the tree's inline input (DockRowRenameInput above); trash confirms
 *    first — in the dock context a right-click row reads like a list entry,
 *    but the operation deletes the real file on disk, so the confirm text
 *    says so (dock-specific keys; the tree stays confirm-less for trash).
 *    Delete-permanent gets the danger dialog with the heavier dock wording
 *    (real file, skips the Trash, cannot be undone). Outside-workspace rows
 *    keep the tl1437 read-only item set — no dead buttons. */
function DockSideFileList({ files, tabId, workspaceRoot, sessionPath, onInjectReferences, onOpenInTerminal, ariaLabel }: { files: SessionSideFile[]; tabId?: string; workspaceRoot?: string; sessionPath?: string; onInjectReferences?: (text: string) => void; onOpenInTerminal?: (path: string) => void; ariaLabel: string }) {
  const t = useT();
  const { showToast } = useToast();
  // tl1468b: the dock's destructive/terminal confirm dialog — the same
  // useConfirmDialog component the tree panel drives for delete-permanent.
  const { confirm: confirmRowOps, dialog: rowOpsConfirmDialog } = useConfirmDialog();
  // tl1437: the selected row path. Component-local like the tree's selection —
  // the side-files lists are transcript projections, so nothing persists.
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  // tl1437: the open row menu (viewport point + target path). The menu is
  // rendered through the same FloatingMenu portal the tree menu uses.
  const [menu, setMenu] = useState<{ x: number; y: number; path: string } | null>(null);
  // tl1437: tab id snapshots for the copy-absolute scope guard — the id the
  // menu was opened under vs the id live right now (a tab switch between menu
  // open and item click must not copy another session's resolved path; same
  // guard shape as the tree menu's isScopeCurrent). tl1468b: the same snapshot
  // scopes the new ops (terminal/rename/trash/delete) via a ref so the async
  // confirm/rename flows re-check it after they await.
  const [menuTabId, setMenuTabId] = useState("");
  const menuTabIdRef = useRef("");
  const liveTabIdRef = useRef(tabId ?? "");
  liveTabIdRef.current = tabId ?? "";
  // tl1468b: the row being renamed inline (tree's treeRenaming shape: the
  // state drives the render, the ref is what the commit reads — blur and
  // keydown can fire with stale closures).
  const [rowRenaming, setRowRenaming] = useState<{ path: string; value: string } | null>(null);
  const rowRenamingRef = useRef<{ path: string; value: string } | null>(null);
  // tl1421 惯例：菜单打开即探针（ResolveOpenablePathForTab——相对路径按会话
  // 项目根解析、存在性 stat、opener 门禁），在途时打开/显示两项禁用（点了不
  // 落空），探针空串=不可开 → 两项从菜单移除（不发死按钮）；通过后记住解析
  // 出的绝对路径，打开/显示直接用它（OpenLocalPath / RevealPath，任务 1060/
  // 944 opener 家族，与回合文件 chip 完全同链路）。tl1445：引用是会话读过的
  // 文件，同链路语义不变——读过的文件同样可能被移走，探针兜底不变。
  const [menuProbe, setMenuProbe] = useState<"probing" | "ok" | "none">("probing");
  const [menuResolved, setMenuResolved] = useState("");
  const listRef = useRef<HTMLUListElement>(null);
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
  useEffect(() => {
    if (!menu || !menuTabId) return;
    let cancelled = false;
    setMenuProbe("probing");
    setMenuResolved("");
    app.ResolveOpenablePathForTab(menuTabId, menu.path)
      .then((absolute) => {
        if (cancelled) return;
        setMenuResolved(absolute || "");
        setMenuProbe(absolute ? "ok" : "none");
      })
      .catch(() => {
        if (cancelled) return;
        setMenuResolved("");
        setMenuProbe("none");
      });
    return () => {
      cancelled = true;
    };
  }, [menu, menuTabId]);
  // tl1437: right-click = select the row, then open the menu on it (the tree's
  // openTreeMenu collapses the selection to the right-clicked row; this list
  // is single-selection so that is just the one row).
  const openRowMenu = (event: { preventDefault(): void; stopPropagation(): void; clientX: number; clientY: number }, path: string) => {
    if (!tabId) return;
    event.preventDefault();
    event.stopPropagation();
    setSelectedPath(path);
    setMenuTabId(tabId);
    menuTabIdRef.current = tabId;
    setMenu({ x: event.clientX, y: event.clientY, path });
  };
  const menuItems = useMemo<FloatingMenuItem[]>(() => {
    if (!menu) return [];
    const path = menu.path;
    const close = () => setMenu(null);
    const isScopeCurrent = () => liveTabIdRef.current === menuTabId;
    // tl1468b: the file-tree destructive family's gate (sideFileWorkspaceRelativePath
    // — non-empty ⟺ the canonical path is a workspace-relative shape or an absolute
    // path inside the workspace root, exactly what the backend fences accept). The
    // op always acts on the CANONICAL bytes; only the terminal gets the relative
    // slice (its bridge refuses absolute shapes).
    const opsRelative = sideFileWorkspaceRelativePath(path, workspaceRoot ?? "");
    const opsManaged = opsRelative !== "";
    const openDefault = () => {
      close();
      const absolute = menuResolved;
      if (!absolute) return;
      app.OpenLocalPath(absolute).catch((error: unknown) => {
        showToast(t("localPath.openFailed", { path, error: menuErrorText(error) }), "error");
      });
    };
    const reveal = () => {
      close();
      const absolute = menuResolved;
      if (!absolute) return;
      app.RevealPath(absolute).catch((error: unknown) => {
        showToast(t("externalOpener.failed", { name: t("externalOpener.reveal"), error: menuErrorText(error) }), "error");
      });
    };
    // tl1468b: failure reporting mirrors the tree panel's reportFileOpsFailures —
    // one failure surfaces its detail, several surface the failed count (same
    // workspace.fileOpFailed / workspace.fileOpsFailedCount keys).
    const reportOpsFailures = (res: { failed: ReadonlyArray<{ path: string; err: string }> }) => {
      if (!res.failed?.length) return;
      const detail = res.failed[0]?.err ?? "";
      showToast(
        res.failed.length === 1
          ? t("workspace.fileOpFailed", { detail })
          : t("workspace.fileOpsFailedCount", { count: res.failed.length, detail }),
        "error",
      );
    };
    const runBridgeThenReport = async (run: (tabId: string) => Promise<{ failed: ReadonlyArray<{ path: string; err: string }> }>) => {
      const tabId = menuTabId;
      if (!tabId) return;
      try {
        reportOpsFailures(await run(tabId));
      } catch (error) {
        showToast(t("workspace.fileOpFailed", { detail: menuErrorText(error) }), "error");
      }
    };
    // tl1468b: trash confirms first — in the dock a right-clicked row reads
    // like a list entry, but the operation moves the REAL file on disk; the
    // dock-specific confirm says so (the tree stays confirm-less for trash).
    const trashRow = () => {
      close();
      const tabId = menuTabId;
      if (!tabId) return;
      void confirmRowOps({
        title: t("sideFiles.rowTrashConfirmTitle"),
        message: t("sideFiles.rowTrashConfirmMessage", { path }),
        confirmLabel: t("workspace.trashEntry"),
        cancelLabel: t("common.cancel"),
      }).then((confirmed) => {
        if (!confirmed || liveTabIdRef.current !== tabId) return;
        void runBridgeThenReport((id) => app.TrashWorkspaceEntriesForTab(id, [path]));
      });
    };
    // tl1468b: delete-permanent is non-restorable — the danger dialog carries
    // the heavier dock wording (real file, skips the Trash, cannot be undone),
    // the confirm/cancel labels are the tree's own keys.
    const deleteRowPermanent = () => {
      close();
      const tabId = menuTabId;
      if (!tabId) return;
      void confirmRowOps({
        title: t("workspace.deletePermanentConfirmTitle"),
        message: t("sideFiles.rowDeletePermanentConfirmMessage", { path }),
        confirmLabel: t("workspace.deletePermanentConfirmLabel"),
        cancelLabel: t("common.cancel"),
        tone: "danger",
      }).then((confirmed) => {
        if (!confirmed || liveTabIdRef.current !== tabId) return;
        void runBridgeThenReport((id) => app.DeleteWorkspaceEntriesPermanentForTab(id, [path]));
      });
    };
    const startRowRename = () => {
      close();
      const seed = { path, value: sideFileBasename(path) };
      rowRenamingRef.current = seed;
      setRowRenaming(seed);
    };
    return [
      ...(menuProbe !== "none"
        ? [
            {
              icon: <ExternalLink size={14} />,
              label: t("workspace.openWithDefaultApp"),
              disabled: menuProbe !== "ok",
              onSelect: openDefault,
            },
            {
              icon: <FolderOpen size={14} />,
              label: t("workspace.revealInFileManager"),
              disabled: menuProbe !== "ok",
              onSelect: reveal,
            },
            // tl1468b: the tree's terminal item through the tree's own channel
            // (onOpenInTerminal → App.openTerminalForPath). Probe-aware like
            // open/reveal: a gone file would otherwise just flash an empty
            // terminal panel (the bridge refuses silently).
            ...(opsManaged && onOpenInTerminal
              ? [{
                  icon: <TerminalSquare size={14} />,
                  label: t("workspace.openInTerminal"),
                  disabled: menuProbe !== "ok",
                  onSelect: () => {
                    close();
                    onOpenInTerminal(opsRelative);
                  },
                }]
              : []),
            { separator: true as const, key: "sep-open" },
          ]
        : []),
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
      // tl1468b: the tree's destructive trio, same bridges, workspace-managed
      // rows only (the backend fence would reject the rest — no dead items).
      ...(opsManaged
        ? [
            { separator: true as const, key: "sep-ops" },
            {
              icon: <PenLine size={14} />,
              label: t("workspace.renameEntry"),
              onSelect: startRowRename,
            },
            {
              icon: <Trash2 size={14} />,
              label: t("workspace.trashEntry"),
              onSelect: trashRow,
            },
            {
              icon: <Trash2 size={14} />,
              label: t("workspace.deletePermanentEntry"),
              onSelect: deleteRowPermanent,
            },
          ]
        : []),
      ...(onInjectReferences
        ? [
            { separator: true as const, key: "sep-ref" },
            {
              icon: <MessageSquarePlus size={14} />,
              label: t("workspace.addFileReferenceToChat"),
              onSelect: () => {
                close();
                onInjectReferences(formatWorkspaceReference(path));
              },
            },
          ]
        : []),
    ];
  }, [menu, menuTabId, menuProbe, menuResolved, onInjectReferences, onOpenInTerminal, workspaceRoot, confirmRowOps, showToast, t]);
  // tl1468b: the inline rename commit — the tree's commitTreeRename shape
  // (read via ref, trim, no-op when unchanged) through the tree's
  // RenameWorkspaceEntryForTab bridge with the menu's tab-id scope guard
  // (a tab switch between menu open and Enter must not rename into another
  // session's workspace). Failures reuse the tree's rename toast key.
  const commitRowRename = async () => {
    const renaming = rowRenamingRef.current;
    if (!renaming) return;
    rowRenamingRef.current = null;
    setRowRenaming(null);
    const nextName = renaming.value.trim();
    if (!nextName || nextName === sideFileBasename(renaming.path)) return;
    const tabId = menuTabIdRef.current;
    if (!tabId || liveTabIdRef.current !== tabId) return;
    try {
      await app.RenameWorkspaceEntryForTab(tabId, renaming.path, nextName);
    } catch (error) {
      showToast(t("workspace.renameFailed", { detail: menuErrorText(error) }), "error");
    }
  };
  const cancelRowRename = () => {
    rowRenamingRef.current = null;
    setRowRenaming(null);
  };
  if (files.length === 0) return <p className="side-files__empty">{t("sideFiles.empty")}</p>;
  return (
    <>
      <ul ref={listRef} className="side-files__list" role={interactive ? "listbox" : undefined} aria-label={interactive ? ariaLabel : undefined}>
        {files.map((f) => (
          <li
            key={f.path}
            className={interactive ? "side-files__item side-files__item--interactive" : "side-files__item"}
            title={interactive ? undefined : `${f.path} · ${f.via}`}
          >
            {interactive && rowRenaming?.path === f.path ? (
              <DockRowRenameInput
                value={rowRenaming.value}
                label={t("workspace.renameEntry")}
                onInput={(value) => {
                  const next = { path: f.path, value };
                  rowRenamingRef.current = next;
                  setRowRenaming(next);
                }}
                onCommit={() => void commitRowRename()}
                onCancel={cancelRowRename}
              />
            ) : interactive ? (
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
                <span>{sideFileDisplayPath(f.path, workspaceRoot ?? "", sessionPath ?? "")}</span>
              </button>
            ) : (
              <>
                <FileText size={12} aria-hidden="true" />
                <span>{sideFileDisplayPath(f.path, workspaceRoot ?? "", sessionPath ?? "")}</span>
              </>
            )}
          </li>
        ))}
      </ul>
      {menu && (
        <FloatingMenu x={menu.x} y={menu.y} estimatedHeight={Math.max(240, menuItems.length * 30 + 24)} className="workspace-tree-menu">
          <FloatingMenuItems items={menuItems} />
        </FloatingMenu>
      )}
      {rowOpsConfirmDialog}
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
 * DockSideFileList when a tabId is provided (App always provides one) — row
 * selection, the row menu (open/reveal/copy/reference) close the gap against
 * the file tree. Without a tabId the artifacts body stays byte-for-byte the
 * task-260 shape.
 *
 * tl1445: the references variant catches up with the same six interactions —
 * it aggregates into the same SessionSideFile {path, via} shape (the read
 * path of collectSessionSideFiles), so it mounts the same DockSideFileList
 * with the references aria label. The references-only "inject the reference
 * list" footer button stays: the row menu's add-reference injects one @path,
 * the footer injects the formatted whole-list block (task 114 payload) —
 * per-file vs list-level, complementary not duplicate. Without a tabId the
 * references body stays byte-for-byte the task-260 shape too.
 *
 * tl1468: workspaceRoot/sessionPath feed the row DISPLAY paths only (see
 * DockSideFileList) — the operation layer still consumes the canonical path.
 *
 * tl1468b: onOpenInTerminal is the tree menu's own channel (App passes the
 * same openTerminalForPath callback the WorkspacePanel gets) — the row menu's
 * terminal item routes through it unchanged.
 */
export function SideFilesDockPanel({ items, variant, onInjectReferences, tabId, workspaceRoot, sessionPath, onOpenInTerminal }: {
  items: readonly SessionSideItem[];
  variant: SideFilesDockVariant;
  onInjectReferences?: (text: string) => void;
  /** tl1437: the active tab id — scope for the dock rows' ForTab bridges
   *  (open probe / absolute-path resolve). Absent keeps the list static.
   *  tl1445: scopes the references variant the same way. */
  tabId?: string;
  /** tl1468: display-path bases (workspace root, then the session's own
   *  directory). Empty strings degrade gracefully to the tail-segment rule. */
  workspaceRoot?: string;
  sessionPath?: string;
  /** tl1468b: the tree menu's open-in-terminal channel (App.openTerminalForPath),
   *  shared, not re-implemented; absent drops the terminal item. */
  onOpenInTerminal?: (path: string) => void;
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
            <DockSideFileList files={artifacts} tabId={tabId} workspaceRoot={workspaceRoot} sessionPath={sessionPath} onInjectReferences={onInjectReferences} onOpenInTerminal={onOpenInTerminal} ariaLabel={t("sideFiles.artifacts")} />
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
          <DockSideFileList files={references} tabId={tabId} workspaceRoot={workspaceRoot} sessionPath={sessionPath} onInjectReferences={onInjectReferences} onOpenInTerminal={onOpenInTerminal} ariaLabel={t("sideFiles.references")} />
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
