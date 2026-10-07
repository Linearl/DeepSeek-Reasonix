// 任务 562 — lab three-tier badge pill. Pure display: the tier text and color
// follow LabTier (推荐/可选/未稳定/已退役); data always comes from
// lib/experimentTiers so the settings lab tab and the picks wall can never
// disagree. No interactions, no state.

import type { Translator } from "../lib/i18n";
import { LAB_TIER_LABEL_KEYS, type LabTier } from "../lib/experimentTiers";

interface TierBadgeProps {
  tier: LabTier;
  translator: Translator;
}

export function TierBadge({ tier, translator: t }: TierBadgeProps) {
  return (
    <span className={`lab-tier-badge lab-tier-badge--${tier}`} data-lab-tier={tier}>
      {t(LAB_TIER_LABEL_KEYS[tier])}
    </span>
  );
}
