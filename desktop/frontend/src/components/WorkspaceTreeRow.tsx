import { useEffect, useRef } from "react";
import type { DragEvent, KeyboardEvent, MouseEvent } from "react";
import { ChevronRight, Folder } from "lucide-react";
import type { DirEntry } from "../lib/types";
import { workspaceBasename as basename, workspaceParentPath as parentPath } from "../lib/workspacePanelFormat";
import { WorkspaceFileIcon } from "./WorkspaceFileIcon";

export interface WorkspaceTreeRowData {
  key: string;
  path: string;
  depth: number;
  entry: DirEntry;
  active: boolean;
  isOpen?: boolean;
  isSearch?: boolean;
  compactPaths?: string[];
  displayName?: string;
}

export interface WorkspaceTreeRenameState {
  value: string;
  label: string;
  onInput: (value: string) => void;
  onCommit: () => void;
  onCancel: () => void;
}

// While a row is being renamed inline, it renders as a plain div: an <input>
// nested in a <button> is invalid HTML and swallows focus/click handling.
function WorkspaceTreeRenameRow({
  className,
  style,
  rename,
}: {
  className: string;
  style?: { paddingLeft: number };
  rename: WorkspaceTreeRenameState;
}) {
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    const input = inputRef.current;
    if (!input) return;
    input.focus();
    const dot = input.value.lastIndexOf(".");
    input.setSelectionRange(0, dot > 0 ? dot : input.value.length);
  }, []);

  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    event.stopPropagation();
    if (event.key === "Enter") {
      event.preventDefault();
      rename.onCommit();
    } else if (event.key === "Escape") {
      event.preventDefault();
      rename.onCancel();
    }
  };

  return (
    <div className={className} data-workspace-renaming="true" style={style}>
      <input
        ref={inputRef}
        className="workspace-tree__rename-input"
        value={rename.value}
        aria-label={rename.label}
        onChange={(event) => rename.onInput(event.target.value)}
        onKeyDown={onKeyDown}
        onBlur={rename.onCommit}
        size={1}
      />
    </div>
  );
}

export function WorkspaceTreeRow({
  row,
  onActivate,
  onDragStart,
  onContextMenu,
  renaming,
}: {
  row: WorkspaceTreeRowData;
  onActivate: (row: WorkspaceTreeRowData, event: MouseEvent<HTMLElement>) => void;
  onDragStart: (event: DragEvent<HTMLElement>, path: string, isDir: boolean) => void;
  onContextMenu: (event: MouseEvent<HTMLElement>, path: string, isDir: boolean) => void;
  renaming?: WorkspaceTreeRenameState;
}) {
  const { path, depth, entry, isOpen, active, displayName = entry.name } = row;
  const rowClass = `workspace-tree__row${active ? " workspace-tree__row--active" : ""}`;

  if (renaming) {
    return (
      <WorkspaceTreeRenameRow
        className={rowClass}
        style={row.isSearch ? undefined : { paddingLeft: 8 + depth * 14 }}
        rename={renaming}
      />
    );
  }

  if (row.isSearch) {
    const dir = parentPath(path);
    return (
      <button
        className={`${rowClass} workspace-tree__row--search`}
        data-workspace-path={path}
        draggable
        onDragStart={(event) => onDragStart(event, path, entry.isDir)}
        onClick={(event) => onActivate(row, event)}
        onContextMenu={(event) => onContextMenu(event, path, entry.isDir)}
      >
        {entry.isDir ? (
          <Folder size={14} className="workspace-tree__icon workspace-tree__icon--dir" />
        ) : (
          <WorkspaceFileIcon fileName={entry.name} />
        )}
        <span className="workspace-tree__result">
          <span className="workspace-tree__result-name">{basename(path)}</span>
          {dir && <span className="workspace-tree__result-dir">{dir}</span>}
        </span>
      </button>
    );
  }

  return (
    <button
      className={rowClass}
      data-workspace-path={path}
      draggable
      onDragStart={(event) => onDragStart(event, path, entry.isDir)}
      onClick={(event) => onActivate(row, event)}
      onContextMenu={(event) => onContextMenu(event, path, entry.isDir)}
      style={{ paddingLeft: 8 + depth * 14 }}
    >
      {depth > 0 && (
        <span className="workspace-tree__guides" aria-hidden="true">
          {Array.from({ length: depth }, (_, index) => (
            <span
              className="workspace-tree__guide"
              key={index}
              style={{ left: 14 + index * 14 }}
            />
          ))}
        </span>
      )}
      {entry.isDir ? (
        <ChevronRight
          size={13}
          className={`workspace-tree__chev ${isOpen ? "workspace-tree__chev--open" : ""}`}
          style={{
            transition: "transform 0.15s ease",
            transform: isOpen ? "rotate(90deg)" : "rotate(0deg)",
          }}
        />
      ) : (
        <span className="workspace-tree__chev" />
      )}
      {entry.isDir ? (
        <Folder size={14} className="workspace-tree__icon workspace-tree__icon--dir" />
      ) : (
        <WorkspaceFileIcon fileName={entry.name} />
      )}
      <span className="workspace-tree__name">{displayName}</span>
    </button>
  );
}
