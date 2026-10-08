// 任务 562 — lab three-tier badge pill. Pure display: the tier text and color
// follow LabTier (推荐/可选/未稳定/已退役); data always comes from
// lib/experimentTiers so the settings lab tab and the picks wall can never
// disagree. No interactions, no state.
// 任务 621 — the pill carries a native tooltip (`title`) explaining what the
// tier means; the description keys live next to the label keys in the same
// register, so text and meaning can never drift apart.

import type { Translator } from "../lib/i18n";
import { LAB_TIER_DESC_KEYS, LAB_TIER_LABEL_KEYS, type LabTier } from "../lib/experimentTiers";

interface TierBadgeProps {
  tier: LabTier;
  translator: Translator;
}

export function TierBadge({ tier, translator: t }: TierBadgeProps) {
  return (
    <span
      className={`lab-tier-badge lab-tier-badge--${tier}`}
      data-lab-tier={tier}
      title={t(LAB_TIER_DESC_KEYS[tier])}
    >
      {t(LAB_TIER_LABEL_KEYS[tier])}
    </span>
  );
}
