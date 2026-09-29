// Task 379 — fork feature intro copy comes from a YAML file at runtime so
// copy edits never touch code:
//
//   layer 1  fetch("/fork-features.yaml")  — public asset, edit + reload
//   layer 2  the task-282 locale keys      — per-field fallback
//
// The parser below is intentionally schema-scoped (this exact file shape:
// one top-level `lead:`, a `groups:` list of maps with nested `features:`
// lists of maps). It is not a general YAML engine — a malformed file simply
// fails a field (or the whole parse) and the locale fallback keeps the panel
// alive, which is the acceptance rule ("missing yaml/fields → no white
// screen"). No secrets live in this file; it mirrors public locale copy.

import type { ForkFeatureIntroGroup } from "./forkFeaturesIntro";

export interface ForkFeaturesCopy {
  lead: string;
  byId: Record<string, { title: string; desc: string; how: string; icon: string; recommended: boolean }>;
}

const YAML_URL = "fork-features.yaml";

function parseScalar(raw: string): string {
  const t = raw.trim();
  if ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'"))) return t.slice(1, -1);
  return t;
}

/** parseForkFeaturesYaml accepts the file's fixed shape; throws on structural
 * trouble so the caller falls back to locale wholesale. */
export function parseForkFeaturesYaml(text: string): ForkFeaturesCopy {
  const out: ForkFeaturesCopy = { lead: "", byId: {} };
  let group: { key: string } | null = null;
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
    }
    feature = null;
  };
  for (const rawLine of text.split(/\r?\n/)) {
    const line = rawLine.replace(/#.*$/, "");
    if (!line.trim()) continue;
    const indent = line.match(/^\s*/)?.[0].length ?? 0;
    const body = line.trim();
    if (indent === 0) {
      flushFeature();
      const m = body.match(/^lead:\s*(.*)$/);
      if (m) out.lead = parseScalar(m[1]);
      continue;
    }
    // Indent tiers vary between editors (groups 2 / features 4 / item 6 /
    // fields 8 in the shipped file), so drive the state machine off content
    // shape instead of hardcoded columns — a blank/odd indent can never drop
    // a feature (that would silently blank the wall).
    if (body === "groups:" || body === "features:") continue;
    if (body.startsWith("- key:")) {
      flushFeature();
      const m = body.slice(2).match(/^key:\s*(.*)$/);
      if (m) group = { key: parseScalar(m[1]) };
      continue;
    }
    if (body.startsWith("- ")) {
      flushFeature();
      feature = {};
      const m = body.slice(2).match(/^([A-Za-z]+):\s*(.*)$/);
      if (m) feature[m[1]] = parseScalar(m[2]);
      continue;
    }
    if (feature) {
      const m = body.match(/^([A-Za-z]+):\s*(.*)$/);
      if (m) feature[m[1]] = parseScalar(m[2]);
      continue;
    }
  }
  flushFeature();
  void group;
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

/** forkFeatureCopyFor merges yaml copy over the locale table field-by-field:
 * a feature missing from yaml keeps its locale text, a blank yaml field keeps
 * the locale value — a partial file can never blank a card. */
export function forkFeatureCopyFor(
  yaml: ForkFeaturesCopy | null,
  groups: readonly ForkFeatureIntroGroup[],
  localeFor: (kind: "title" | "desc" | "how", id: string) => string,
): { lead: string; groups: Array<{ id: string; label: string; features: Array<{ id: string; title: string; desc: string; how: string; icon: string; recommended: boolean }> }> } {
  const pick = (id: string, kind: "title" | "desc" | "how", fallback: string): string => {
    const fromYaml = yaml?.byId[id]?.[kind];
    if (fromYaml && fromYaml.trim()) return fromYaml;
    if (fallback && fallback.trim()) return fallback;
    return localeFor(kind, id);
  };
  return {
    lead: yaml?.lead?.trim() || "",
    groups: groups.map((g) => ({
      id: g.key,
      label: g.key,
      features: g.features.map((f) => ({
        id: f.id,
        title: pick(f.id, "title", ""),
        desc: pick(f.id, "desc", ""),
        how: pick(f.id, "how", ""),
        icon: yaml?.byId[f.id]?.icon ?? "",
        recommended: yaml?.byId[f.id]?.recommended === true,
      })),
    })),
  };
}
