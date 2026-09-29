import type { MouseEvent as ReactMouseEvent } from "react";
import { FolderInput, FolderPlus, Server } from "lucide-react";

import { ContextMenu, type ContextMenuItem, type ContextMenuPoint } from "./ContextMenu";
import { Tooltip } from "./Tooltip";

export function projectTreeHeaderAddItems({
  blankLabel,
  localLabel,
  remoteLabel,
  groupLabel,
  disabled,
  onBlank,
  onLocal,
  onRemote,
  onGroup,
}: {
  blankLabel?: string;
  localLabel: string;
  remoteLabel: string;
  /** Task 383 #2: project groups (a header feature distinct from the row
   * context-menu's session groups) — workbench/creation parity with classic. */
  groupLabel?: string;
  disabled: boolean;
  onBlank?: () => void;
  onLocal: () => void;
  onRemote: () => void;
  onGroup?: () => void;
}): ContextMenuItem[] {
  const items: ContextMenuItem[] = [];
  if (onBlank && blankLabel) {
    items.push({ key: "blank-project", icon: <FolderPlus size={13} />, label: blankLabel, disabled, onSelect: onBlank });
  }
  if (onGroup && groupLabel) {
    items.push({ key: "new-project-group", icon: <FolderInput size={13} />, label: groupLabel, disabled, onSelect: onGroup });
  }
  items.push(
    { key: "open-local-folder", icon: <FolderPlus size={13} />, label: localLabel, disabled, onSelect: onLocal },
    { key: "remote-connection", icon: <Server size={13} />, label: remoteLabel, onSelect: onRemote },
  );
  return items;
}

export function ProjectTreeHeaderAddControl({
  open,
  point,
  items,
  label,
  disabled,
  onOpen,
  onClose,
}: {
  open: boolean;
  point: ContextMenuPoint | null;
  items: ContextMenuItem[];
  label: string;
  disabled: boolean;
  onOpen: (event: ReactMouseEvent<HTMLButtonElement>) => void;
  onClose: () => void;
}) {
  return (
    <span className="project-tree__header-menu-wrap">
      <Tooltip label={label} className="project-tree__action-slot project-tree__header-action-slot project-tree__action-slot--add">
        <button
          type="button"
          className={`project-tree__add-project${open ? " project-tree__header-icon-btn--active" : ""}`}
          aria-label={label}
          aria-haspopup="menu"
          aria-expanded={open}
          disabled={disabled}
          onClick={onOpen}
        >
          <FolderPlus size={14} />
        </button>
      </Tooltip>
      <ContextMenu open={open} point={point} items={items} minWidth={206} ariaLabel={label} onClose={onClose} />
    </span>
  );
}

export function ProjectTreeRemoteAction({ label, disabled, onClick }: { label: string; disabled: boolean; onClick: () => void }) {
  return (
    <button type="button" className="project-tree__empty-secondary" onClick={onClick} disabled={disabled}>
      <Server size={14} />
      <span>{label}</span>
    </button>
  );
}
