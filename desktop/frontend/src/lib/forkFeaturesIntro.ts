// Task 282 (lab "Fork features" intro panel, 2026-09-27): a pure-display
// catalogue for fork improvements that have no lab switch — each entry is
// title + one-line description + where-to-find. The intake rule (task 282):
// improvements only; fixes and pure UI tweaks stay out (they live in release
// notes).
//
// The template-literal key types below pin every key to its feature id, and
// the rendered keys must exist in the locale dictionaries (DictKey), so a typo
// or a missing translation fails `tsc --noEmit`.

export type ForkFeatureIntroId =
  | "classicLayout"
  | "groupFold"
  | "projectGroups"
  | "colorFilter"
  | "mergePanel"
  | "detachedTabs";

export type ForkFeatureIntroGroupKey = "layout" | "projects" | "sessions";

export interface ForkFeatureIntro {
  readonly id: ForkFeatureIntroId;
  readonly titleKey: `settings.forkFeaturesIntro.${ForkFeatureIntroId}.title`;
  readonly descKey: `settings.forkFeaturesIntro.${ForkFeatureIntroId}.desc`;
  readonly howKey: `settings.forkFeaturesIntro.${ForkFeatureIntroId}.how`;
}

export interface ForkFeatureIntroGroup {
  readonly key: ForkFeatureIntroGroupKey;
  readonly labelKey: `settings.forkFeaturesIntro.group.${ForkFeatureIntroGroupKey}`;
  readonly features: readonly ForkFeatureIntro[];
}

// Design table (task 282 acceptance): 3 groups — layout / projects / sessions.
// The panel renders this array directly, so the rendered group count can never
// drift from this table; the pinned assertion below makes a count change an
// explicit, reviewable edit.
export const FORK_FEATURE_INTRO_GROUPS = [
  {
    key: "layout",
    labelKey: "settings.forkFeaturesIntro.group.layout",
    features: [
      { id: "classicLayout", titleKey: "settings.forkFeaturesIntro.classicLayout.title", descKey: "settings.forkFeaturesIntro.classicLayout.desc", howKey: "settings.forkFeaturesIntro.classicLayout.how" },
      { id: "groupFold", titleKey: "settings.forkFeaturesIntro.groupFold.title", descKey: "settings.forkFeaturesIntro.groupFold.desc", howKey: "settings.forkFeaturesIntro.groupFold.how" },
    ],
  },
  {
    key: "projects",
    labelKey: "settings.forkFeaturesIntro.group.projects",
    features: [
      { id: "projectGroups", titleKey: "settings.forkFeaturesIntro.projectGroups.title", descKey: "settings.forkFeaturesIntro.projectGroups.desc", howKey: "settings.forkFeaturesIntro.projectGroups.how" },
      { id: "colorFilter", titleKey: "settings.forkFeaturesIntro.colorFilter.title", descKey: "settings.forkFeaturesIntro.colorFilter.desc", howKey: "settings.forkFeaturesIntro.colorFilter.how" },
    ],
  },
  {
    key: "sessions",
    labelKey: "settings.forkFeaturesIntro.group.sessions",
    features: [
      { id: "mergePanel", titleKey: "settings.forkFeaturesIntro.mergePanel.title", descKey: "settings.forkFeaturesIntro.mergePanel.desc", howKey: "settings.forkFeaturesIntro.mergePanel.how" },
      { id: "detachedTabs", titleKey: "settings.forkFeaturesIntro.detachedTabs.title", descKey: "settings.forkFeaturesIntro.detachedTabs.desc", howKey: "settings.forkFeaturesIntro.detachedTabs.how" },
    ],
  },
] as const satisfies readonly ForkFeatureIntroGroup[];

// tsc-pinned group count: changing the design table (adding/removing a group)
// must update this line too, so the acceptance criterion "panel group count
// matches the design table" is checked by the compiler, not by inspection.
const _groupCountPinnedToDesignTable: 3 = FORK_FEATURE_INTRO_GROUPS.length;
void _groupCountPinnedToDesignTable;

// Task 282 acceptance: at least 5 entries in the first batch.
export const FORK_FEATURE_INTRO_COUNT = FORK_FEATURE_INTRO_GROUPS.reduce(
  (n, group) => n + group.features.length,
  0,
);
