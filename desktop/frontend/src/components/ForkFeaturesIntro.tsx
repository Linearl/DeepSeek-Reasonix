// Task 282 (lab "Fork features" intro panel): pure display — no switches, no
// inputs. Group count and entries come straight from the design table in
// `lib/forkFeaturesIntro`, so the panel can never drift from it.
//
// Task 379: copy now loads from public/fork-features.yaml at runtime (edit the
// file + reload → new wording; missing file or fields fall back per-field to
// the task-282 locale keys, so a bad file never blanks the panel). The panel
// renders as a card wall for the large dialog; `recommended` (yaml) opens a
// badge slot that defaults to zero badges until the user signs off the list.

import { useEffect, useState } from "react";
import type { Translator } from "../lib/i18n";
import { FORK_FEATURE_INTRO_GROUPS, type ForkFeatureIntro } from "../lib/forkFeaturesIntro";
import { forkFeatureCopyFor, loadForkFeaturesCopy, type ForkFeaturesCopy } from "../lib/forkFeaturesYaml";

interface ForkFeaturesIntroProps {
  t: Translator;
}

export default function ForkFeaturesIntro({ t }: ForkFeaturesIntroProps) {
  const [yaml, setYaml] = useState<ForkFeaturesCopy | null>(null);
  useEffect(() => {
    let alive = true;
    void loadForkFeaturesCopy().then((copy) => { if (alive) setYaml(copy); });
    return () => { alive = false; };
  }, []);

  // Widened to the shared element type: flatMap over the readonly design
  // table otherwise infers a tuple union that find() cannot narrow (TS2322).
  const allFeatures: ForkFeatureIntro[] = FORK_FEATURE_INTRO_GROUPS.flatMap((g) => [...g.features]);
  const localeFor = (kind: "title" | "desc" | "how", id: string): string => {
    const f = allFeatures.find((x) => x.id === id);
    if (!f) return "";
    return t(kind === "title" ? f.titleKey : kind === "desc" ? f.descKey : f.howKey);
  };
  const copy = forkFeatureCopyFor(yaml, FORK_FEATURE_INTRO_GROUPS, localeFor);
  const lead = copy.lead.trim() || t("settings.forkFeaturesIntro.lead");

  return (
    <section className="experimental-intro experimental-intro--wall" aria-label={t("settings.forkFeaturesIntro.title")}>
      <div className="experimental-intro__head">
        <div className="experimental-intro__title">{t("settings.forkFeaturesIntro.title")}</div>
        <p className="experimental-intro__lead">{lead}</p>
      </div>
      {copy.groups.map((group, index) => (
        <div key={group.id} className="experimental-intro__group">
          <div className="experimental-intro__group-title">{t(FORK_FEATURE_INTRO_GROUPS[index]?.labelKey ?? "settings.forkFeaturesIntro.title")}</div>
          <div className="experimental-intro__cards experimental-intro__cards--wall">
            {group.features.map((feature) => (
              <article key={feature.id} className="experimental-intro__card experimental-intro__card--wall">
                <div className="experimental-intro__card-head">
                  {feature.icon ? <span className="experimental-intro__card-icon" aria-hidden="true">{feature.icon}</span> : null}
                  <h4 className="experimental-intro__card-title">{feature.title}</h4>
                  {/* Task 379 badge slot: renders only when yaml says
                      `recommended: true` — the default file ships none, so the
                      badge count is zero until the user picks the list. */}
                  {feature.recommended ? (
                    <span className="experimental-intro__card-badge">{t("settings.forkFeaturesIntro.recommended")}</span>
                  ) : null}
                </div>
                <p className="experimental-intro__card-desc">{feature.desc}</p>
                <p className="experimental-intro__card-how">{feature.how}</p>
              </article>
            ))}
          </div>
        </div>
      ))}
    </section>
  );
}
