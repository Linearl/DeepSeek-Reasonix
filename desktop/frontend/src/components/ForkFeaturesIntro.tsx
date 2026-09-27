// Task 282 (lab "Fork features" intro panel): pure display — no switches, no
// inputs, no state. Group count and entries come straight from the design
// table in `lib/forkFeaturesIntro`, so the panel can never drift from it.

import type { Translator } from "../lib/i18n";
import { FORK_FEATURE_INTRO_GROUPS } from "../lib/forkFeaturesIntro";

interface ForkFeaturesIntroProps {
  t: Translator;
}

export default function ForkFeaturesIntro({ t }: ForkFeaturesIntroProps) {
  return (
    <section className="experimental-intro" aria-label={t("settings.forkFeaturesIntro.title")}>
      <div className="experimental-intro__head">
        <div className="experimental-intro__title">{t("settings.forkFeaturesIntro.title")}</div>
        <p className="experimental-intro__lead">{t("settings.forkFeaturesIntro.lead")}</p>
      </div>
      {FORK_FEATURE_INTRO_GROUPS.map((group) => (
        <div key={group.key} className="experimental-intro__group">
          <div className="experimental-intro__group-title">{t(group.labelKey)}</div>
          <div className="experimental-intro__cards">
            {group.features.map((feature) => (
              <article key={feature.id} className="experimental-intro__card">
                <h4 className="experimental-intro__card-title">{t(feature.titleKey)}</h4>
                <p className="experimental-intro__card-desc">{t(feature.descKey)}</p>
                <p className="experimental-intro__card-how">{t(feature.howKey)}</p>
              </article>
            ))}
          </div>
        </div>
      ))}
    </section>
  );
}
