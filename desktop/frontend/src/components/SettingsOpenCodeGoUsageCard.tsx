import { Activity } from "lucide-react";
import { useI18n } from "../lib/i18n";
import { SettingsField } from "./SettingsForm";
import { SettingsOptions } from "./SettingsOptions";
import { TierBadge } from "./TierBadge";
import { EXPERIMENT_FEATURE_TIERS } from "../lib/experimentTiers";

// 任务 163 — the OpenCode Go usage row (experimental, default off).
//
// 任务 800 — the in-card usage detail (three window rows + refresh + notes)
// is retired: since 783 the right-dock sidebar card (OpenCodeGoUsageSidebarCard)
// is the authoritative display, and since 799 it additionally gates on the
// active plan family. This row is now a plain member switch — label/hint on
// the left, the on/off segment on the right, the same layout as every other
// member row of a merged card (no single-column override, no query, no state).
// All network I/O and the 163 "switch off = zero requests" gate live in the
// sidebar card alone.

export function SettingsOpenCodeGoUsageCard({
  enabled,
  busy,
  onToggle,
}: {
  enabled: boolean;
  busy: boolean;
  onToggle: (on: boolean) => void;
}) {
  const { t } = useI18n();
  return (
    <SettingsField
      label={
        <>
          {t("settings.opencodeGoUsage")}
          {/* 任务 562: same tier source as the settings lab tab. */}
          <TierBadge tier={EXPERIMENT_FEATURE_TIERS.opencodeGoUsage} translator={t} />
        </>
      }
      hint={t("settings.opencodeGoUsageHint")}
      icon={<Activity size={18} />}
    >
      <SettingsOptions layout="field" className="set-seg">
        {[false, true].map((on) => (
          <button
            key={String(on)}
            className={`set-seg__btn${enabled === on ? " set-seg__btn--on" : ""}`}
            disabled={busy}
            onClick={() => onToggle(on)}
          >
            {t(on ? "settings.opencodeGoUsage.on" : "settings.opencodeGoUsage.off")}
          </button>
        ))}
      </SettingsOptions>
    </SettingsField>
  );
}
