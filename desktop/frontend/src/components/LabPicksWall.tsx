// 任务 562 — 实验室精选图墙（lab picks wall）: the picks view of the lab
// features inside the fork-features intro dialog. 16 cards (xlsx 表B W1:
// 推荐 12 + 可选 4), each badge driven by lib/experimentTiers — the SAME map
// the settings lab tab reads, so the two views can never disagree.
//
// 任务 563 grows every card to the 表B three elements: tier badge (562) +
// one-line effect (表B col 6) + live on/off state, plus the 「建议开启」badge
// (recommended tier AND currently off — suggestEnable) and a click-through
// detail dialog (表B col 7, LabPickDetailDialog, portal-to-body). The copy
// comes from public/fork-features.yaml `labPicks:` (runtime fetch, same
// contract as the intro wall above); the pick LIST itself stays
// human-curated in code (LAB_WALL_PICKS — new lab items never join the wall
// automatically, and a yaml edit carries words only, never cards).

import { useEffect, useState } from "react";
import type { Translator } from "../lib/i18n";
import { EXPERIMENT_FEATURE_TIERS, LAB_WALL_PICKS, suggestEnable, type LabWallPickId } from "../lib/experimentTiers";
import { labPickCopyFor, loadForkFeaturesCopy, type ForkFeaturesCopy } from "../lib/forkFeaturesYaml";
import { labWallOnFor, useLabWallOn } from "../lib/labWallOn";
import { TierBadge } from "./TierBadge";
import LabPickDetailDialog from "./LabPickDetailDialog";

interface LabPicksWallProps {
  t: Translator;
}

/** Pick id → locale title key. Literal keys stay DictKey-checked: a typo here
 * (or a missing locale key) is a compile error, not a blank card. */
const PICK_TITLE_KEYS = {
  sessionWall: "settings.sessionWall",
  tabCompress: "settings.tabCompress",
  todoSidebar: "settings.todoSidebar",
  promptHistoryPicker: "settings.promptHistoryPicker",
  monitoring: "settings.monitoring",
  restartUpdate: "settings.restartUpdate",
  budgetControl: "settings.budgetControl",
  compressOpt: "settings.compressOpt",
  messageMerge: "settings.messageMerge",
  autopilot: "settings.autopilot",
  sessionCollab: "settings.sessionCollab",
  fullAccess: "settings.fullAccess",
  splitView: "settings.splitView",
  subagentPanel: "settings.subagentPanel",
  selectionActions: "settings.selectionActions",
  completionSummary: "settings.completionSummary",
} as const satisfies Readonly<Record<LabWallPickId, string>>;

export default function LabPicksWall({ t }: LabPicksWallProps) {
  // Live states from ExperimentalSection's features render table (context, so
  // the pinned <LabPicksWall t={t} /> mount line stays byte-identical).
  const wallOn = useLabWallOn();
  // 表B copy (effect/detail), runtime-fetched like the intro wall copy.
  const [yaml, setYaml] = useState<ForkFeaturesCopy | null>(null);
  const [openPick, setOpenPick] = useState<LabWallPickId | null>(null);
  useEffect(() => {
    let alive = true;
    void loadForkFeaturesCopy().then((copy) => { if (alive) setYaml(copy); });
    return () => { alive = false; };
  }, []);

  return (
    <section className="lab-picks-wall" aria-label={t("settings.labPicks.title")}>
      <div className="lab-picks-wall__title">{t("settings.labPicks.title")}</div>
      <div className="lab-picks-wall__grid">
        {LAB_WALL_PICKS.map((id) => {
          const on = labWallOnFor(wallOn, id);
          const copy = labPickCopyFor(yaml, id);
          const suggest = suggestEnable(EXPERIMENT_FEATURE_TIERS[id], on);
          return (
            <article key={id} className="lab-picks-wall__card" data-lab-pick={id} data-lab-on={on ? "on" : "off"}>
              <div className="lab-picks-wall__card-head">
                <h4 className="lab-picks-wall__card-title">{t(PICK_TITLE_KEYS[id])}</h4>
                <span className="lab-picks-wall__card-badge">
                  <TierBadge tier={EXPERIMENT_FEATURE_TIERS[id]} translator={t} />
                </span>
              </div>
              {copy?.effect ? <p className="lab-picks-wall__card-effect">{copy.effect}</p> : null}
              <div className="lab-picks-wall__card-foot">
                <span className="lab-picks-wall__card-state" data-on={on ? "true" : "false"}>
                  {t(on ? "settings.labPicks.statusOn" : "settings.labPicks.statusOff")}
                </span>
                {suggest ? <span className="lab-picks-wall__card-suggest">{t("settings.labPicks.suggest")}</span> : null}
              </div>
              {/* Stretched hit area: a real button overlaying the card, so the
                  whole card clicks and keyboards reach it, while the visible
                  content keeps its heading semantics. */}
              <button
                type="button"
                className="lab-picks-wall__card-open"
                aria-haspopup="dialog"
                aria-label={t(PICK_TITLE_KEYS[id])}
                onClick={() => setOpenPick(id)}
              />
            </article>
          );
        })}
      </div>
      {openPick ? (
        <LabPickDetailDialog
          t={t}
          title={t(PICK_TITLE_KEYS[openPick])}
          tier={EXPERIMENT_FEATURE_TIERS[openPick]}
          on={labWallOnFor(wallOn, openPick)}
          suggest={suggestEnable(EXPERIMENT_FEATURE_TIERS[openPick], labWallOnFor(wallOn, openPick))}
          copy={labPickCopyFor(yaml, openPick)}
          onClose={() => setOpenPick(null)}
        />
      ) : null}
    </section>
  );
}
