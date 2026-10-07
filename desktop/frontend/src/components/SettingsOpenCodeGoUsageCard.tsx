import { useCallback, useEffect, useState } from "react";
import { Activity, RefreshCw } from "lucide-react";
import { app } from "../lib/bridge";
import { useI18n, type DictKey } from "../lib/i18n";
import {
  OPENCODE_GO_OFFICIAL_BASE,
  OPENCODE_GO_WINDOW_KEYS,
  resetCountdown,
  usageNoteText,
  type OpenCodeGoUsageResult,
} from "../lib/opencodeGoUsage";
import { SettingsField } from "./SettingsForm";
import { SettingsOptions } from "./SettingsOptions";
import { TierBadge } from "./TierBadge";
import { EXPERIMENT_FEATURE_TIERS } from "../lib/experimentTiers";

// Task 163 — the OpenCode Go usage detail card (experimental, default off).
//
// The whole card (switch + query + three windows) lives in this component so
// SettingsPanel's conditional render never has to host hooks. While the switch
// is off this component only draws the on/off segment — it issues no
// GetOpenCodeGoUsage call at all (zero regression, the frontend gate).

const WINDOW_LABEL_KEY: Record<string, string> = {
  rolling: "settings.opencodeGoUsage.window.rolling",
  weekly: "settings.opencodeGoUsage.window.weekly",
  monthly: "settings.opencodeGoUsage.window.monthly",
};

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
  const [usage, setUsage] = useState<OpenCodeGoUsageResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [tick, setTick] = useState(0);

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      const result = await app.GetOpenCodeGoUsage(OPENCODE_GO_OFFICIAL_BASE);
      setUsage(result);
    } catch {
      setUsage({ tiers: [], note: "network" });
    } finally {
      setLoading(false);
    }
  }, []);

  // Query once when the switch turns on; refresh keeps the countdown line
  // honest (it re-renders every 30s off the same fetched resetsAt values).
  useEffect(() => {
    if (!enabled) {
      setUsage(null);
      return;
    }
    void refresh();
  }, [enabled, refresh]);
  useEffect(() => {
    const wireTiers = Array.isArray(usage?.tiers) ? usage.tiers : [];
    if (!enabled || !wireTiers.some((tier) => tier.resetsAt)) return;
    const timer = window.setInterval(() => setTick((value) => value + 1), 30_000);
    return () => window.clearInterval(timer);
  }, [enabled, usage]);

  // tiers is typed non-null, but the wire can still hand the card a null —
  // or a non-array shape entirely (a Go nil slice used to marshal as
  // "tiers": null and crash this lookup with TypeError: null.find —
  // opencodefix). Normalize any malformed payload so the card falls to its
  // note/empty branch instead of throwing in render; anything beyond these
  // known shapes stays behind the caller's ErrorBoundary, which isolates the
  // crash to this card and recovers when the pane is re-entered.
  const tiers = Array.isArray(usage?.tiers) ? usage.tiers : [];
  const windows = OPENCODE_GO_WINDOW_KEYS
    .map((key) => tiers.find((tier) => tier.window === key))
    .filter((tier): tier is NonNullable<typeof tier> => Boolean(tier));
  const noteText = usage ? usageNoteText(usage.note, t) : "";

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
      className="settings-field--opencode-usage"
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
      {enabled && (
        <div className="opencode-go-usage" data-testid="opencode-go-usage-card">
          {noteText ? (
            <p className="opencode-go-usage__note">{noteText}</p>
          ) : windows.length > 0 ? (
            <>
              {windows.map((tier) => {
                // Three known windows carry literal keys; an unknown wire key
                // (the endpoint has changed shape before) falls back to a
                // literal so the Translator's key type stays satisfied.
                const labelKey = (WINDOW_LABEL_KEY[tier.window] ?? "settings.opencodeGoUsage.window.monthly") as DictKey;
                return (
                <div key={tier.window} className="opencode-go-usage__row" data-window={tier.window}>
                  <span className="opencode-go-usage__window">{t(labelKey)}</span>
                  <span className="opencode-go-usage__percent">{Math.round(tier.percent ?? 0)}%</span>
                  <span className="opencode-go-usage__reset">
                    {/* tick participates so the interval re-render refreshes this line */}
                    {tier.resetsAt ? `${t("settings.opencodeGoUsage.resetsIn")} ${resetCountdown(tier.resetsAt) || "—"}` : ""}
                  </span>
                </div>
                );
              })}
              <button
                type="button"
                className="opencode-go-usage__refresh"
                disabled={busy || loading}
                onClick={() => void refresh()}
              >
                <RefreshCw size={13} data-tick={tick} />
                <span>{t("settings.opencodeGoUsage.refresh")}</span>
              </button>
            </>
          ) : (
            <p className="opencode-go-usage__note">{t("settings.opencodeGoUsage.note.empty")}</p>
          )}
        </div>
      )}
    </SettingsField>
  );
}
