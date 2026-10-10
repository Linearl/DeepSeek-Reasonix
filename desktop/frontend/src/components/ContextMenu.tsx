import { Fragment, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent, ReactNode } from "react";
import { createPortal } from "react-dom";

export type ContextMenuPoint = {
  left: number;
  top: number;
  keyboardTarget?: HTMLElement;
};

export type ContextMenuItem =
  | {
      type?: "item";
      key: string;
      icon?: ReactNode;
      label: ReactNode;
      shortcut?: string;
      disabled?: boolean;
      danger?: boolean;
      variant?: "section";
      onSelect: () => void;
    }
  | {
      type: "separator";
      key: string;
    }
  // Task 720: a second-level section rendered inline under its row (click
  // expands or collapses it; the menu scrolls — no floating panel, so no
  // positioning or clamping code). One level is all the sidebar needs ("move
  // to group"); deeper nesting renders disabled. Selecting an expanded leaf
  // closes the whole menu, so callers building items away from the menu state
  // (organization.topicMenuItems) need no close callback.
  | {
      type: "submenu";
      key: string;
      icon?: ReactNode;
      label: ReactNode;
      disabled?: boolean;
      items: ContextMenuItem[];
    };

const EDGE_GAP = 8;

function clampMenuPoint(left: number, top: number, width: number, height: number): ContextMenuPoint {
  if (typeof window === "undefined") return { left, top };
  return {
    left: Math.min(Math.max(EDGE_GAP, left), Math.max(EDGE_GAP, window.innerWidth - width - EDGE_GAP)),
    top: Math.min(Math.max(EDGE_GAP, top), Math.max(EDGE_GAP, window.innerHeight - height - EDGE_GAP)),
  };
}

export function contextMenuPointFromEvent(
  event: ReactMouseEvent<HTMLElement> | ReactKeyboardEvent<HTMLElement>,
): ContextMenuPoint {
  if ("clientX" in event && event.clientX > 0 && event.clientY > 0) {
    return { left: event.clientX, top: event.clientY };
  }
  const rect = event.currentTarget.getBoundingClientRect();
  return {
    left: rect.left + 12,
    top: rect.bottom + 6,
    ...("key" in event ? { keyboardTarget: event.currentTarget } : {}),
  };
}

export function ContextMenu({
  open,
  point,
  items,
  onClose,
  className,
  minWidth = 180,
  ariaLabel = "Context menu",
}: {
  open: boolean;
  point: ContextMenuPoint | null;
  items: ContextMenuItem[];
  onClose: () => void;
  className?: string;
  minWidth?: number;
  ariaLabel?: string;
}) {
  const menuRef = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<ContextMenuPoint | null>(point);
  const [expanded, setExpanded] = useState<string | null>(null);

  useLayoutEffect(() => {
    if (!open || !point) return;
    const rect = menuRef.current?.getBoundingClientRect();
    if (!rect) {
      setPosition(point);
      return;
    }
    setPosition(clampMenuPoint(point.left, point.top, rect.width, rect.height));
  }, [open, point, items]);

  useLayoutEffect(() => {
    const target = point?.keyboardTarget;
    const menu = menuRef.current;
    if (!open || !target || !menu) return;
    menu.querySelector<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)')?.focus({ preventScroll: true });
    return () => {
      // Do not steal focus from an outside click or an action opening a dialog.
      if (target.isConnected && (menu.contains(document.activeElement) || document.activeElement === document.body)) {
        target.focus({ preventScroll: true });
      }
    };
  }, [open, point]);

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)'));
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    switch (event.key) {
      case "ArrowDown": next = (index + 1) % buttons.length; break;
      case "ArrowUp": next = index < 0 ? buttons.length - 1 : (index - 1 + buttons.length) % buttons.length; break;
      case "Home": next = 0; break;
      case "End": next = buttons.length - 1; break;
      case "Tab":
        // Let the browser continue from the invoking control in tab order.
        point?.keyboardTarget?.focus({ preventScroll: true });
        onClose();
        event.stopPropagation();
        return;
      case "Escape":
        event.preventDefault();
        event.stopPropagation();
        onClose();
        return;
      default: return;
    }
    event.preventDefault();
    event.stopPropagation();
    buttons[next]?.focus({ preventScroll: true });
  };

  useEffect(() => {
    if (!open) {
      setExpanded(null);
      return;
    }
    const closeOnOutsidePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && menuRef.current?.contains(target)) return;
      onClose();
    };
    const close = () => onClose();
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("pointerdown", closeOnOutsidePointerDown, true);
    window.addEventListener("resize", close);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", closeOnOutsidePointerDown, true);
      window.removeEventListener("resize", close);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open, onClose]);

  if (!open || !point) return null;

  const renderItem = (item: ContextMenuItem, nested: boolean) => {
    if (item.type === "separator") {
      return <div key={item.key} className="context-menu__separator" role="separator" />;
    }
    if (item.type === "submenu") {
      const active = expanded === item.key;
      // Inside an expanded section deeper nesting stays disabled — one level
      // is the deal.
      const unusable = item.disabled || item.items.length === 0 || nested;
      return (
        <Fragment key={item.key}>
          <button
            type="button"
            role="menuitem"
            disabled={unusable}
            className="context-menu__item context-menu__item--submenu"
            onClick={(event) => {
              event.stopPropagation();
              if (unusable) return;
              setExpanded((current) => current === item.key ? null : item.key);
            }}
          >
            {item.icon}
            <span>{item.label}</span>
            <span className="context-menu__submenu-arrow" aria-hidden="true">{active ? "▾" : "▸"}</span>
          </button>
          {active && item.items.map((leaf) => renderItem(leaf, true))}
        </Fragment>
      );
    }
    return (
      <button
        key={item.key}
        type="button"
        role="menuitem"
        disabled={item.disabled}
        className={`context-menu__item${nested ? " context-menu__item--nested" : ""}${item.danger ? " context-menu__item--danger" : ""}${item.variant ? ` context-menu__item--${item.variant}` : ""}`}
        onClick={(event) => {
          event.stopPropagation();
          if (item.disabled) return;
          item.onSelect();
          // An expanded leaf has no path back to the opener's menu state, so
          // the menu closes itself; top-level rows keep the caller-owned close.
          if (nested) {
            setExpanded(null);
            onClose();
          }
        }}
      >
        {item.icon}
        <span>{item.label}</span>
        {item.shortcut && <span className="context-menu__shortcut">{item.shortcut}</span>}
      </button>
    );
  };

  return createPortal(
    <div
      ref={menuRef}
      className={`context-menu${className ? ` ${className}` : ""}`}
      role="menu"
      aria-label={ariaLabel}
      style={{ left: (position ?? point).left, top: (position ?? point).top, minWidth }}
      onKeyDown={onMenuKeyDown}
      onMouseDown={(event) => {
        event.preventDefault();
        event.stopPropagation();
      }}
      onClick={(event) => event.stopPropagation()}
      onContextMenu={(event) => {
        event.preventDefault();
        event.stopPropagation();
      }}
    >
      {items.map((item) => renderItem(item, false))}
    </div>,
    document.body,
  );
}
