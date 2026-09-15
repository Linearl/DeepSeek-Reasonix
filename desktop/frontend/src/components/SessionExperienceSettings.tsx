import { SettingsOptions } from "./SettingsOptions";
import { useEffect, useState } from "react";
import { PanelBottom } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useCommittedCommand } from "../lib/useCommittedCommand";
import { applySessionExperience, getSessionExperience, type SessionExperience } from "../lib/sessionExperience";
import { hydrateReasoningDisplayMode } from "../lib/reasoningDisplayPreference";
import type { SettingsView } from "../lib/types";
import { SettingsField, SettingsSection } from "./SettingsForm";

// Cards read left to right as "less shown → more shown": 简洁, 标准, 深入.
const MODES = ["concise", "standard", "deep"] as const satisfies readonly SessionExperience[];

type Props = {
  snapshot: SettingsView;
  busy: boolean;
  apply: (write: () => Promise<unknown>) => Promise<boolean>;
};

function normalizeSnapshot(value: unknown): SessionExperience {
  if (value === "deep") return "deep";
  if (value === "concise") return "concise";
  return "standard";
}

export function SessionExperienceSettings({ snapshot, busy, apply }: Props) {
  const t = useT();
  const [mode, setMode] = useState<SessionExperience>(getSessionExperience);
  const present = useCommittedCommand((next: SessionExperience) => {
    setMode(next);
    applySessionExperience(next);
    hydrateReasoningDisplayMode(next === "deep" ? "expanded" : "auto", next === "deep");
  });
  // Snapshot identity matters: a failed write may reload the same backend value.
  useEffect(() => { present(normalizeSnapshot(snapshot.sessionExperience)); }, [snapshot, present]);
  const save = useCommittedCommand(async (next: SessionExperience) => {
    present(next);
    // The shared Settings apply/reload path owns both success and failure.
    await apply(() => app.SetSessionExperience(next));
  });
  const hintKey = mode === "deep"
    ? "settings.sessionExperience.deepHint"
    : mode === "concise"
      ? "settings.sessionExperience.conciseHint"
      : "settings.sessionExperience.standardHint";
  return <SettingsSection title={t("settings.general.sectionConversation")} description={t("settings.sessionExperienceHint")}>
    <SettingsField label={t("settings.sessionExperience")} hint={t(hintKey)} icon={<PanelBottom size={18} />}>
      <SettingsOptions layout="field" className="set-seg" role="radiogroup" aria-label={t("settings.sessionExperience")}>
        {MODES.map(value => <button key={value} type="button"
          className={`set-seg__btn${mode === value ? " set-seg__btn--on" : ""}`} role="radio"
          aria-checked={mode === value} disabled={busy} onClick={() => void save(value)}>
          {t(`settings.sessionExperience.${value}`)}
        </button>)}
      </SettingsOptions>
    </SettingsField>
  </SettingsSection>;
}
