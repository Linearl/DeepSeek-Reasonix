import { useEffect, useState } from "react";
import { ExternalLink, FileText, FolderOpen, MessageSquarePlus, PenLine, Pin, PinOff, TerminalSquare, Trash2 } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import type { PinnedFileInfo } from "../lib/pinnedContextBridge";
import { useToast } from "../lib/toast";
import {
  WORKSPACE_CONTEXT_MENU_FILE_HEIGHT,
  WORKSPACE_CONTEXT_MENU_REF_HEIGHT,
  workspacePathCopyMenuItems,
} from "../lib/workspacePathCopyMenuItems";
import { FloatingMenu, FloatingMenuItems } from "./FloatingMenu";

export interface WorkspaceTreeMenuTarget {
  x: number;
  y: number;
  path: string;
  isDir: boolean;
  /** Task 855: the selection snapshot the menu acts on (right-click keeps an
   * existing multi-selection; otherwise exactly the right-clicked row). */
  paths: string[];
}

export function WorkspaceTreeMenu({
  target,
  workspaceTabId,
  isScopeCurrent,
  onClose,
  onOpenInTerminal,
  onAddReference,
  onAddFile,
  onRename,
  onDelete,
  onDeletePermanent,
}: {
  target: WorkspaceTreeMenuTarget;
  workspaceTabId: string;
  isScopeCurrent: () => boolean;
  onClose: () => void;
  onOpenInTerminal?: (path: string) => void;
  onAddReference: () => void;
  onAddFile: () => void;
  onRename: (path: string) => void;
  onDelete: (paths: string[]) => void;
  onDeletePermanent: (paths: string[]) => void;
}) {
  const t = useT();
  const { showToast } = useToast();
  const [isPinned, setIsPinned] = useState(false);
  const multi = target.paths.length > 1;

  useEffect(() => {
    if (multi || target.isDir || !workspaceTabId) return;
    let active = true;
    app.GetPinnedFilesForTab(workspaceTabId)
      .then((files: PinnedFileInfo[]) => {
        if (!active || !Array.isArray(files)) return;
        const normalizedTarget = target.path.replace(/^[/\\]+/, "").replace(/\\/g, "/");
        const found = files.some(
          (f) => f.path.replace(/^[/\\]+/, "").replace(/\\/g, "/") === normalizedTarget
        );
        setIsPinned(found);
      })
      .catch(() => {});
    return () => {
      active = false;
    };
  }, [workspaceTabId, target.path, target.isDir, multi]);

  const closeThen = (action: () => void) => {
    onClose();
    action();
  };

  const fileOps = [
    { separator: true as const, key: "sep-file-ops" },
    {
      key: "rename",
      icon: <PenLine size={14} />,
      label: t("workspace.renameEntry"),
      disabled: multi,
      onSelect: () => {
        onClose();
        onRename(target.path);
      },
    },
    {
      key: "trash",
      icon: <Trash2 size={14} />,
      label: multi
        ? t("workspace.trashEntriesCount", { count: target.paths.length })
        : t("workspace.trashEntry"),
      onSelect: () => {
        onClose();
        onDelete(target.paths);
      },
    },
    {
      key: "delete-permanent",
      icon: <Trash2 size={14} />,
      label: multi
        ? t("workspace.deletePermanentEntriesCount", { count: target.paths.length })
        : t("workspace.deletePermanentEntry"),
      onSelect: () => {
        onClose();
        onDeletePermanent(target.paths);
      },
    },
  ];

  // Multi-selection keeps the menu on the destructive block only: the
  // single-file actions below (open, copy, pin, chat references) have no
  // well-defined meaning across a mixed batch.
  if (multi) {
    return (
      <FloatingMenu
        x={target.x}
        y={target.y}
        estimatedHeight={WORKSPACE_CONTEXT_MENU_FILE_HEIGHT + 40}
        className="workspace-tree-menu"
      >
        <FloatingMenuItems items={fileOps.slice(1)} />
      </FloatingMenu>
    );
  }

  return (
    <FloatingMenu
      x={target.x}
      y={target.y}
      estimatedHeight={target.isDir ? WORKSPACE_CONTEXT_MENU_REF_HEIGHT : WORKSPACE_CONTEXT_MENU_FILE_HEIGHT + 32}
      className="workspace-tree-menu"
    >
      <FloatingMenuItems
        items={[
          ...(target.isDir
            ? []
            : [{
                icon: <ExternalLink size={14} />,
                label: t("workspace.openWithDefaultApp"),
                onSelect: () => closeThen(() => void app.OpenWorkspacePathForTab(workspaceTabId, target.path).catch(() => {})),
              }]),
          {
            icon: <FolderOpen size={14} />,
            label: t("workspace.revealInFileManager"),
            onSelect: () => closeThen(() => void app.RevealWorkspacePathForTab(workspaceTabId, target.path).catch(() => {})),
          },
          ...(onOpenInTerminal
            ? [{
                icon: <TerminalSquare size={14} />,
                label: t("workspace.openInTerminal"),
                onSelect: () => closeThen(() => onOpenInTerminal(target.path)),
              }]
            : []),
          ...workspacePathCopyMenuItems({
            path: target.path,
            resolveAbsolutePath: () => app.ResolveWorkspacePathForTab(workspaceTabId, target.path),
            isScopeCurrent,
            close: onClose,
            relativeLabel: t("workspace.copyRelativePath"),
            absoluteLabel: t("workspace.copyAbsolutePath"),
          }),
          ...fileOps,
          { separator: true },
          {
            icon: <MessageSquarePlus size={14} />,
            label: target.isDir ? t("workspace.addFolderReferenceToChat") : t("workspace.addFileReferenceToChat"),
            onSelect: onAddReference,
          },
          ...(target.isDir
            ? []
            : [
                {
                  icon: <FileText size={14} />,
                  label: t("workspace.addFileContentToChat"),
                  onSelect: onAddFile,
                },
                {
                  icon: isPinned ? <PinOff size={14} /> : <Pin size={14} />,
                  label: isPinned ? t("workspace.unpinFileFromContext") : t("workspace.pinFileToContext"),
                  onSelect: () =>
                    closeThen(() => {
                      if (isPinned) {
                        void app.UnpinFileForTab(workspaceTabId, target.path).catch((err: unknown) => {
                          showToast(String((err as Error)?.message || err), "error");
                        });
                      } else {
                        void app.PinFileForTab(workspaceTabId, target.path).catch((err: unknown) => {
                          showToast(String((err as Error)?.message || err), "error");
                        });
                      }
                    }),
                },
              ]),
        ]}
      />
    </FloatingMenu>
  );
}
