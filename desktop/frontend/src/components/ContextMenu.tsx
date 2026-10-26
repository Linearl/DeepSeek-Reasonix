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
  // Task 720: a second-level section rendered as its own floating panel anchored
  // to its row (tl 1168: hover opens it, click/tap covers touch, →/← walk in and
  // out, Escape closes the whole chain). Before tl 1168 this was an in-place
  // accordion — the leaves lengthened the parent menu, which read as "more rows
  // in the level-1 menu". One level is all the sidebar needs ("move to group");
  // deeper nesting still renders disabled. Selecting an expanded leaf closes
  // the whole menu, so callers building items away from the menu state
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
// tl 1168 hover timing: the row must be *dwelled on* before the submenu opens
// (a pointer just passing through must not flash a panel), while an already
// open panel switches instantly as the pointer slides across sibling rows.
export const CONTEXT_MENU_HOVER_OPEN_MS = 180;
const HOVER_CLOSE_MS = 300;

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
  // tl 1168: the expanded submenu renders as a sibling fixed panel (outside the
  // root panel's scroll box — absolute positioning inside would be clipped by
  // its overflow), so every anchor/inside check needs its own ref.
  const flyoutRef = useRef<HTMLDivElement>(null);
  const submenuTriggerRef = useRef<HTMLButtonElement | null>(null);
  const keyboardFlyoutRef = useRef(false);
  const hoverOpenTimerRef = useRef<number | null>(null);
  const hoverCloseTimerRef = useRef<number | null>(null);
  const [position, setPosition] = useState<ContextMenuPoint | null>(point);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [flyoutPos, setFlyoutPos] = useState<{ left: number; top: number } | null>(null);

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
      if (target.isConnected && (menu.contains(document.activeElement) || flyoutRef.current?.contains(document.activeElement) || document.activeElement === document.body)) {
        target.focus({ preventScroll: true });
      }
    };
  }, [open, point]);

  const clearHoverTimers = () => {
    if (hoverOpenTimerRef.current !== null) {
      window.clearTimeout(hoverOpenTimerRef.current);
      hoverOpenTimerRef.current = null;
    }
    if (hoverCloseTimerRef.current !== null) {
      window.clearTimeout(hoverCloseTimerRef.current);
      hoverCloseTimerRef.current = null;
    }
  };

  const scheduleFlyoutOpen = (key: string, trigger: HTMLButtonElement) => {
    submenuTriggerRef.current = trigger;
    clearHoverTimers();
    if (expanded === key) return;
    // First open waits for the dwell; sliding from another open submenu
    // switches immediately (the delay exists to disambiguate, not to lag).
    const delay = expanded === null ? CONTEXT_MENU_HOVER_OPEN_MS : 0;
    hoverOpenTimerRef.current = window.setTimeout(() => setExpanded(key), delay);
  };

  const scheduleFlyoutClose = () => {
    clearHoverTimers();
    hoverCloseTimerRef.current = window.setTimeout(() => setExpanded(null), HOVER_CLOSE_MS);
  };

  useEffect(() => clearHoverTimers, []);

  // Anchor the flyout to its trigger row: right edge by default, flipped to the
  // left when the right side has no room; vertically clamped into the viewport.
  // Runs after mount, so the first paint is already in place (no jump).
  useLayoutEffect(() => {
    if (!expanded) {
      setFlyoutPos((current) => current === null ? current : null);
      return;
    }
    const trigger = submenuTriggerRef.current;
    const flyout = flyoutRef.current;
    if (!trigger || !flyout) return;
    const tr = trigger.getBoundingClientRect();
    const fr = flyout.getBoundingClientRect();
    const rightFlows = tr.right + fr.width + EDGE_GAP <= window.innerWidth;
    const leftFlows = tr.left - fr.width - EDGE_GAP >= 0;
    const left = rightFlows || !leftFlows ? tr.right : tr.left - fr.width;
    const top = Math.max(EDGE_GAP, Math.min(tr.top, window.innerHeight - fr.height - EDGE_GAP));
    setFlyoutPos((current) => current && current.left === left && current.top === top ? current : { left, top });
    if (keyboardFlyoutRef.current) {
      // Only keyboard opens move focus into the panel; hover must not steal it.
      keyboardFlyoutRef.current = false;
      flyout.querySelector<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)')?.focus({ preventScroll: true });
    }
  }, [expanded]);

  const onMenuKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>, isFlyout: boolean) => {
    const buttons = Array.from(event.currentTarget.querySelectorAll<HTMLButtonElement>('button[role="menuitem"]:not(:disabled)'));
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    let next: number;
    switch (event.key) {
      case "ArrowDown": next = (index + 1) % buttons.length; break;
      case "ArrowUp": next = index < 0 ? buttons.length - 1 : (index - 1 + buttons.length) % buttons.length; break;
      case "Home": next = 0; break;
      case "End": next = buttons.length - 1; break;
      case "ArrowLeft":
        // ← walks back out of the submenu; inside the root panel it is a no-op.
        if (!isFlyout) return;
        event.preventDefault();
        event.stopPropagation();
        clearHoverTimers();
        setExpanded(null);
        submenuTriggerRef.current?.focus({ preventScroll: true });
        return;
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
      clearHoverTimers();
      submenuTriggerRef.current = null;
      setExpanded(null);
      return;
    }
    const closeOnOutsidePointerDown = (event: PointerEvent) => {
      const target = event.target;
      if (target instanceof Node && (menuRef.current?.contains(target) || flyoutRef.current?.contains(target))) return;
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
      return <div key={item.key} className="context-menu__separator" role="separator" onMouseEnter={nested ? undefined : scheduleFlyoutClose} />;
    }
    if (item.type === "submenu") {
      const active = expanded === item.key;
      // Inside the flyout a deeper submenu stays disabled — one level
      // is the deal.
      const unusable = item.disabled || item.items.length === 0 || nested;
      return (
        <Fragment key={item.key}>
          <button
            type="button"
            role="menuitem"
            disabled={unusable}
            className="context-menu__item context-menu__item--submenu"
            aria-haspopup="menu"
            aria-expanded={active || undefined}
            onMouseEnter={(event) => {
              if (nested || unusable) return;
              scheduleFlyoutOpen(item.key, event.currentTarget);
            }}
            onClick={(event) => {
              event.stopPropagation();
              if (unusable) return;
              clearHoverTimers();
              submenuTriggerRef.current = event.currentTarget;
              setExpanded((current) => current === item.key ? null : item.key);
            }}
            onKeyDown={(event) => {
              // → enters the flyout (keyboard parity with hover and tap).
              if (nested || unusable || event.key !== "ArrowRight") return;
              event.preventDefault();
              event.stopPropagation();
              clearHoverTimers();
              submenuTriggerRef.current = event.currentTarget;
              keyboardFlyoutRef.current = true;
              setExpanded(item.key);
            }}
          >
            {item.icon}
            <span>{item.label}</span>
            <span className="context-menu__submenu-arrow" aria-hidden="true">▸</span>
          </button>
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
        onMouseEnter={nested ? undefined : scheduleFlyoutClose}
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

  const expandedItem = expanded
    ? items.find((item): item is Extract<ContextMenuItem, { type: "submenu" }> => item.type === "submenu" && item.key === expanded)
    : undefined;

  return createPortal(
    <>
      <div
        ref={menuRef}
        className={`context-menu${className ? ` ${className}` : ""}`}
        role="menu"
        aria-label={ariaLabel}
        style={{ left: (position ?? point).left, top: (position ?? point).top, minWidth }}
        onKeyDown={(event) => onMenuKeyDown(event, false)}
        // Scrolling the root panel moves the trigger out from under the
        // anchored flyout — folding the panel beats letting it drift.
        onScroll={scheduleFlyoutClose}
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
      </div>
      {expandedItem && (
        <div
          ref={flyoutRef}
          className="context-menu context-menu--flyout"
          role="menu"
          aria-label={typeof expandedItem.label === "string" ? expandedItem.label : ariaLabel}
          style={{ left: flyoutPos?.left ?? 0, top: flyoutPos?.top ?? 0, minWidth }}
          onKeyDown={(event) => onMenuKeyDown(event, true)}
          onMouseEnter={clearHoverTimers}
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
          {expandedItem.items.map((leaf) => renderItem(leaf, true))}
        </div>
      )}
    </>,
    document.body,
  );
}
