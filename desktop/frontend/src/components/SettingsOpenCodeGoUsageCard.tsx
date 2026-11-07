import { Activity } from "lucide-react";
import { useI18n } from "../lib/i18n";
import { SettingsField } from "./SettingsForm";
import { TierBadge } from "./TierBadge";
import { EXPERIMENT_FEATURE_TIERS } from "../lib/experimentTiers";

// 任务 163 — the OpenCode Go usage row (experimental, default off).
//
// 任务 800 — the in-card usage detail (three window rows + refresh + notes)
// is retired: since 783 the right-dock sidebar card (OpenCodeGoUsageSidebarCard)
// is the authoritative display, and since 799 it additionally gates on the
// active plan family.
//
// tl1487 四组 — the switch row itself retires with the「不提供开关、始终打开」
// ruling: the row is now a plain display row (glmPlanUsage 同款形态), the
// usage query is always on and lives entirely in the sidebar card. Badge stays
// on the row title (562 ⑤ exemption contract).
export function SettingsOpenCodeGoUsageCard() {
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
      icon={<Activity size={18} />}
      stacked
    >
      <p className="settings-field__hint">{t("settings.opencodeGoUsageHint")}</p>
    </SettingsField>
  );
}
