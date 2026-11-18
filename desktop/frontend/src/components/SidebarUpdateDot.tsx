import { ArrowUpCircle } from "lucide-react";
import { useT } from "../lib/i18n";
import { useUpdater } from "../lib/useUpdater";
import { Tooltip } from "./Tooltip";

// tl1693: the sidebar footer's update light. Lights up (the shared
// `--active` accent face) while the updater context holds an available fork
// release, and clicking it opens the updates settings tab where the one-action
// update lives.
//
// Deliberately no network of its own: the check rides the existing faces —
// UpdateBanner's startup + hourly/focus refresh, and Settings' manual check —
// so a failed check stays silent here (availability first) and a disabled
// auto-check simply never lights the dot. Zero-distraction default: no
// available release means this component renders nothing at all.
export function SidebarUpdateDot({ onOpenUpdates }: { onOpenUpdates: () => void }) {
  const t = useT();
  const { status } = useUpdater();
  if (status.kind !== "available") return null;
  const label = t("updater.sidebarUpdateAvailable");
  return (
    <Tooltip label={label} fill side="top">
      <button
        className="sidebar__utility-button sidebar__utility-button--active"
        type="button"
        aria-label={label}
        onClick={onOpenUpdates}
      >
        <ArrowUpCircle size={16} aria-hidden="true" />
      </button>
    </Tooltip>
  );
}
