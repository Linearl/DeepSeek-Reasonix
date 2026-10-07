// Task 379 (+ 379 rework, user 0929 additions) — fork feature intro copy
// comes from a YAML file at runtime so copy edits never touch code:
//
//   layer 1  fetch("/fork-features.yaml")  — public asset, edit + reload
//   layer 2  the task-282 locale keys      — per-field fallback
//
// The parser is schema-scoped (top-level `columns:`/`lead:`, a `groups:` list
// with optional `label`, nested `features:` lists) and driven by content shape
// rather than hardcoded indents, so an odd indent can never silently drop a
// feature. The renderer is yaml-DRIVEN since the rework: whatever groups and
// ids this file carries get wall tiles (the six task-282 ids still prefer
// their locale keys; yaml-only ids — like the rework's project grouping /
// long-running dev / sandbox switch entries — render straight from yaml).
// A malformed file fails the parse and the whole panel falls back to locale
// (no white screen). No secrets live in this file; it mirrors public locale
// copy only.

import type { ForkFeatureIntroGroup } from "./forkFeaturesIntro";

export interface ForkFeaturesYamlGroup {
  key: string;
  label: string;
  ids: string[];
}

export interface ForkFeaturesCopy {
  lead: string;
  columns: number;
  groups: ForkFeaturesYamlGroup[];
  byId: Record<string, { title: string; desc: string; how: string; icon: string; recommended: boolean }>;
  /** 任务 563 — lab picks wall copy (xlsx 表B cols 6/7): one-line effect for
   * the card, detail paragraph for the pick dialog. WHICH picks exist stays in
   * code (lib/experimentTiers LAB_WALL_PICKS, human-curated) — this carries
   * words only, so a yaml edit can never grow the wall. */
  labPicks: LabPickCopy[];
}

/** 表B copy for one wall pick (effect = col 6, detail = col 7). */
export interface LabPickCopy {
  id: string;
  effect: string;
  detail: string;
}

export const FORK_FEATURES_DEFAULT_COLUMNS = 3;

const YAML_URL = "fork-features.yaml";

function parseScalar(raw: string): string {
  const t = raw.trim();
  if ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'"))) return t.slice(1, -1);
  return t;
}

/** parseForkFeaturesYaml accepts the file's fixed shape; throws on structural
 * trouble so the caller falls back to locale wholesale. */
export function parseForkFeaturesYaml(text: string): ForkFeaturesCopy {
  const out: ForkFeaturesCopy = { lead: "", columns: FORK_FEATURES_DEFAULT_COLUMNS, groups: [], byId: {}, labPicks: [] };
  let group: ForkFeaturesYamlGroup | null = null;
  let feature: Record<string, string> | null = null;
  const flushFeature = () => {
    if (feature?.id) {
      out.byId[feature.id] = {
        title: feature.title ?? "",
        desc: feature.desc ?? "",
        how: feature.how ?? "",
        icon: feature.icon ?? "",
        recommended: feature.recommended === "true",
      };
      if (group && !group.ids.includes(feature.id)) group.ids.push(feature.id);
    }
    feature = null;
  };
  // 任务 563: the labPicks section runs its own two-level state machine
  // (items at one indent, id/effect/detail fields under them). Mutually
  // exclusive with the intro-wall machine — `groups:` ends the section, and
  // any other top-level key leaves it — so a hand edit in one half can never
  // corrupt the other.
  let inLabPicks = false;
  let labPick: Record<string, string> | null = null;
  const flushLabPick = () => {
    if (labPick?.id) {
      out.labPicks.push({ id: labPick.id, effect: labPick.effect ?? "", detail: labPick.detail ?? "" });
    }
    labPick = null;
  };
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.replace(/#.*$/, "");
    if (!line.trim()) continue;
    const indent = line.match(/^\s*/)?.[0].length ?? 0;
    const body = line.trim();
    if (indent === 0) {
      flushFeature();
      flushLabPick();
      if (/^labPicks:\s*$/.test(body)) { inLabPicks = true; continue; }
      inLabPicks = false;
      const lead = body.match(/^lead:\s*(.*)$/);
      if (lead) { out.lead = parseScalar(lead[1]); continue; }
      const cols = body.match(/^columns:\s*(\d+)\s*$/);
      if (cols) {
        const n = Number(cols[1]);
        // Guard against nonsense values from a hand edit: 1..6 only, else keep
        // the default — a bad number must never destroy the wall layout.
        out.columns = n >= 1 && n <= 6 ? n : FORK_FEATURES_DEFAULT_COLUMNS;
        continue;
      }
      continue;
    }
    // Indent tiers vary between editors (groups 2 / features 4 / item 6 /
    // fields 8 in the shipped file), so drive the state machine off content
    // shape instead of hardcoded columns — a blank/odd indent can never drop
    // a feature (that would silently blank the wall).
    // 任务 563: labPicks items (`- id:` + effect/detail fields) never reach
    // the intro-wall machine below — the section owns every line until a
    // top-level key ends it.
    if (inLabPicks) {
      if (body.startsWith("- ")) {
        flushLabPick();
        labPick = {};
        const m = body.slice(2).match(/^([A-Za-z]+):\s*(.*)$/);
        if (m) labPick[m[1]] = parseScalar(m[2]);
        continue;
      }
      if (labPick) {
        const m = body.match(/^([A-Za-z]+):\s*(.*)$/);
        if (m) labPick[m[1]] = parseScalar(m[2]);
        continue;
      }
    }
    if (body === "groups:" || body === "features:") continue;
    if (body.startsWith("- key:")) {
      flushFeature();
      const m = body.slice(2).match(/^key:\s*(.*)$/);
      if (m) {
        group = { key: parseScalar(m[1]), label: "", ids: [] };
        out.groups.push(group);
      }
      continue;
    }
    if (body.startsWith("- ")) {
      flushFeature();
      feature = {};
      const m = body.slice(2).match(/^([A-Za-z]+):\s*(.*)$/);
      if (m) feature[m[1]] = parseScalar(m[2]);
      continue;
    }
    if (group && !feature) {
      const lbl = body.match(/^label:\s*(.*)$/);
      if (lbl) { group.label = parseScalar(lbl[1]); continue; }
    }
    if (feature) {
      const m = body.match(/^([A-Za-z]+):\s*(.*)$/);
      if (m) feature[m[1]] = parseScalar(m[2]);
      continue;
    }
  }
  flushFeature();
  flushLabPick();
  if (Object.keys(out.byId).length === 0 && !out.lead) throw new Error("fork-features.yaml: no content parsed");
  return out;
}

let cached: ForkFeaturesCopy | null = null;
let inFlight: Promise<ForkFeaturesCopy | null> | null = null;

/** loadForkFeaturesCopy resolves layer 1, falls back to layer 2 (null → the
 * renderer uses locale keys per field). Cached after the first success; a
 * fetch failure resolves null instead of throwing. */
export function loadForkFeaturesCopy(): Promise<ForkFeaturesCopy | null> {
  if (cached) return Promise.resolve(cached);
  if (inFlight) return inFlight;
  inFlight = fetch(YAML_URL, { cache: "no-cache" })
    .then((r) => (r.ok ? r.text() : Promise.reject(new Error(`http ${r.status}`))))
    .then((text) => {
      cached = parseForkFeaturesYaml(text);
      return cached;
    })
    .catch(() => null)
    .finally(() => { inFlight = null; });
  return inFlight;
}

export interface ForkFeatureWallGroup {
  id: string;
  yamlLabel: string;
  features: Array<{ id: string; title: string; desc: string; how: string; icon: string; recommended: boolean }>;
}

/** 任务 563 — the 表B copy for one wall pick, or null when the yaml section is
 * absent/malformed or misses the id: the card then renders without its copy
 * lines (same per-field degradation as the intro wall), never a blank wall. */
export function labPickCopyFor(yaml: ForkFeaturesCopy | null, id: string): LabPickCopy | null {
  return yaml?.labPicks.find((p) => p.id === id) ?? null;
}

/** forkFeatureCopyFor builds the WALL MODEL:
 *  - with yaml: groups come FROM THE YAML (yaml-only ids and groups render),
 *    fields prefer yaml then fall back per field to the task-282 locale keys;
 *  - without yaml: the design table drives the wall exactly as before (the
 *    original 379/282 behaviour), so the panel degrades to locale wholesale.
 * `columns` rides along (default 3) so the layout is yaml-configurable. */
export function forkFeatureCopyFor(
  yaml: ForkFeaturesCopy | null,
  groups: readonly ForkFeatureIntroGroup[],
  localeFor: (kind: "title" | "desc" | "how", id: string) => string,
): { lead: string; columns: number; groups: ForkFeatureWallGroup[] } {
  const pick = (id: string, kind: "title" | "desc" | "how", yamlFallback: string): string => {
    const fromYaml = yaml?.byId[id]?.[kind];
    if (fromYaml && fromYaml.trim()) return fromYaml;
    const fromLocale = localeFor(kind, id);
    if (fromLocale && fromLocale.trim()) return fromLocale;
    return yamlFallback;
  };
  const card = (id: string) => ({
    id,
    title: pick(id, "title", yaml?.byId[id]?.title ?? ""),
    desc: pick(id, "desc", yaml?.byId[id]?.desc ?? ""),
    how: pick(id, "how", yaml?.byId[id]?.how ?? ""),
    icon: yaml?.byId[id]?.icon ?? "",
    recommended: yaml?.byId[id]?.recommended === true,
  });
  if (yaml && yaml.groups.length > 0) {
    // Rework skeleton: yaml drives WHICH groups/ids exist (yaml-only entries
    // render), but every task-282 design-table entry stays mounted even when
    // a hand edit deletes its yaml block — deleting a yaml feature must fall
    // back to the locale card (no blank, no disappearance), which is the
    // original 379 fallback promise. Union per group + append missing groups.
    const wallGroups: ForkFeatureWallGroup[] = yaml.groups.map((g) => {
      const designed = groups.find((d) => d.key === g.key);
      const ids = designed
        ? [...designed.features.map((f) => f.id), ...g.ids.filter((id) => !designed.features.some((f) => f.id === id))]
        : [...g.ids];
      return { id: g.key, yamlLabel: g.label, features: ids.map(card) };
    });
    for (const d of groups) {
      if (!wallGroups.some((g) => g.id === d.key)) {
        wallGroups.push({ id: d.key, yamlLabel: "", features: d.features.map((f) => card(f.id)) });
      }
    }
    return {
      lead: yaml.lead.trim(),
      columns: yaml.columns,
      groups: wallGroups,
    };
  }
  // Layer 2: pure locale wall (original shape).
  return {
    lead: "",
    columns: FORK_FEATURES_DEFAULT_COLUMNS,
    groups: groups.map((g) => ({
      id: g.key,
      yamlLabel: "",
      features: g.features.map((f) => ({
        id: f.id,
        title: localeFor("title", f.id),
        desc: localeFor("desc", f.id),
        how: localeFor("how", f.id),
        icon: "",
        recommended: false,
      })),
    })),
  };
}
