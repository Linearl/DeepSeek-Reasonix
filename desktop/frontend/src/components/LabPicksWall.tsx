// 任务 562 — 实验室精选图墙（lab picks wall）: the picks view of the lab
// features inside the fork-features intro dialog. 16 cards (xlsx 表B W1:
// 推荐 12 + 可选 4), each badge driven by lib/experimentTiers — the SAME map
// the settings lab tab reads, so the two views can never disagree.
// 任务 563 will grow these cards (一句话效果 / 当前状态 / 详情弹窗 / 建议开启
// 角标); the pick LIST itself stays human-curated (new lab items never join
// the wall automatically).

import type { Translator } from "../lib/i18n";
import { EXPERIMENT_FEATURE_TIERS, LAB_WALL_PICKS, type LabWallPickId } from "../lib/experimentTiers";
import { TierBadge } from "./TierBadge";

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
  return (
    <section className="lab-picks-wall" aria-label={t("settings.labPicks.title")}>
      <div className="lab-picks-wall__title">{t("settings.labPicks.title")}</div>
      <div className="lab-picks-wall__grid">
        {LAB_WALL_PICKS.map((id) => (
          <article key={id} className="lab-picks-wall__card" data-lab-pick={id}>
            <div className="lab-picks-wall__card-head">
              <h4 className="lab-picks-wall__card-title">{t(PICK_TITLE_KEYS[id])}</h4>
              <span className="lab-picks-wall__card-badge">
                <TierBadge tier={EXPERIMENT_FEATURE_TIERS[id]} translator={t} />
              </span>
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}
