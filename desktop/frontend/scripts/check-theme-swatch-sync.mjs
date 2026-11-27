#!/usr/bin/env node
// tl1814x legacy item 1 — theme card swatch ↔ definition block sync gate.
//
// Why: the six theme-card swatch families in settings (styles.css
// `.theme-card__swatches[data-theme-style-card=…]`) are deliberate literal
// mirrors of the theme definition blocks (`:root[data-theme-style=…]`, the
// SSOT). tl1814 registered all 70 swatch sites as bare-color exempt on exactly
// that premise — but nothing verified the premise. A human editing a
// definition block (or a batch from the tl1814 plan touching --grad) can
// silently desynchronize the preview cards from what the theme actually
// renders. This gate pins the mirror: every swatch value must equal its
// definition-block token value (resolved through var() aliases).
//
// Division of labor with the neighbouring gates (no double coverage):
//   - check-theme-token-contract.mjs validates selector presence, required
//     root tokens, and var() reference legality — it never compares swatch
//     VALUES to definitions.
//   - check-css-color-ratchet.mjs pins the total bare-color stock; swatch
//     literals live inside that stock but carry no consistency semantics.
//   - check-class-contract.mjs assert3 whitelists each swatch site by
//     "selector { declaration }" (value included) — a swatch value edit must
//     update that whitelist in the same commit.
//   - THIS gate asserts swatch value == resolved definition value.
//
// Also enforced here (tl1814x task book): every custom property defined
// inside the 18 theme definition blocks must belong to a registered semantic
// group (GROUPS below). An unregistered token fails the gate — register it
// with its group first (tl1814 naming spec: register before use).
//
// Registered exceptions (EXCEPTIONS below) are the single explanation source
// for known swatch sites that are NOT mirrors. Keep the table minimal; every
// entry needs a reason and an origin.
//
// Usage (from desktop/frontend):
//   node scripts/check-theme-swatch-sync.mjs             # gate: exit 1 on drift
//   node scripts/check-theme-swatch-sync.mjs --report    # per-checkpoint readings
//   node scripts/check-theme-swatch-sync.mjs --self-test # fixture negatives: must fail

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(scriptDir, "..");
const stylesCss = path.join(frontendRoot, "src", "styles.css");

const STYLES = ["graphite", "aurora", "slate", "carbon", "nocturne", "amber"];

// swatch field -> definition-block token it must mirror.
// Established by the tl1814 audit: aurora card surface #1d1b34 == aurora
// dark --surface-2 #1d1b34 (NOT --surface #17162a); accent mirrors --grad.
const FIELD_MAP = {
  bg: "--bg",
  surface: "--surface-2",
  accent: "--grad",
};

// Definition-block resolution chains (later blocks win, CSS cascade order):
//   dark  = base :root → :root[data-theme-style="X"]
//   light = base :root → :root[data-theme="light"] → light style block
//   auto  = base :root → dark style block → auto-light block
//         (the auto-light block overrides dark inside prefers-color-scheme: light)
const CHAINS = {
  dark: (style) => [":root", `:root[data-theme-style="${style}"]`],
  light: (style) => [":root", ':root[data-theme="light"]', `:root[data-theme="light"][data-theme-style="${style}"]`],
  auto: (style) => [":root", `:root[data-theme-style="${style}"]`, `:root[data-theme-style="${style}"]:not([data-theme])`],
};

// Swatch sites that are intentionally NOT definition mirrors. Single
// explanation source; every entry needs a reason + origin. Keys are
// `${style}/${mode}/${field}`.
const EXCEPTIONS = new Map([
  // Amber is an accent-only overlay style: its dark/light blocks define four
  // accent tokens on top of the base palette and never redefine --bg or
  // --surface-2, so the resolved chain yields the base surfaces — while its
  // card bg/surface swatches are hand-picked warmer preview values.
  // tl1814x measured 6 sites; registered 2026-11-27 (see
  // docs/样式红色六族族谱-20261127.md §5 and the tl1814x handoff).
  ["amber/dark/bg", "overlay-style preview value, no own --bg definition (base #090a0c)"],
  ["amber/dark/surface", "overlay-style preview value, no own --surface-2 definition (base #222631)"],
  ["amber/light/bg", "overlay-style preview value, diverges from inherited light --bg #f7f8fb"],
  ["amber/light/surface", "overlay-style preview value, diverges from inherited light --surface-2 #f2f5f9"],
  ["amber/auto/bg", "overlay-style preview value, diverges from inherited light --bg #f7f8fb"],
  ["amber/auto/surface", "overlay-style preview value, diverges from inherited light --surface-2 #f2f5f9"],
  // graphite dark card surface binds --surface (#15161a), while the other
  // five styles bind --surface-2. Upstream-native choice (#3752); the correct
  // mapping (--surface vs --surface-2) is undecided — listed, not fixed,
  // tl1814x 2026-11-27. If the user rules it should mirror --surface-2,
  // change the card to #191a1f and drop this entry.
  ["graphite/dark/surface", "upstream #3752 binds card surface to --surface; mapping question listed for decision"],
  // Same upstream mixing on the light side: aurora/nocturne light+auto cards
  // bind --surface (#fdfcff) while graphite/slate/carbon light cards bind
  // --surface-2 (#ffffff). Unification is a mapping decision, not a value
  // patch — listed for the same ruling as graphite/dark/surface.
  ["aurora/light/surface", "upstream #3752 binds card surface to --surface (#fdfcff); mapping question listed for decision"],
  ["aurora/auto/surface", "upstream #3752 binds card surface to --surface (#fdfcff); mapping question listed for decision"],
  ["nocturne/light/surface", "upstream #3752 binds card surface to --surface (#fdfcff); mapping question listed for decision"],
  ["nocturne/auto/surface", "upstream #3752 binds card surface to --surface (#fdfcff); mapping question listed for decision"],
]);

// Semantic groups for tokens defined inside the 18 theme definition blocks.
// A token defined in any block but absent here fails the gate — add it WITH
// its group, never bare (tl1814 naming spec: register before use).
const GROUPS = {
  "surfaces/backdrops": [
    "--stage", "--bg", "--bg-soft", "--bg-elev", "--bg-elev-2",
    "--surface", "--surface-2", "--surface-3", "--panel",
    "--chat-bg", "--code-bg", "--sidebar-bg",
    "--workspace-preview-bg", "--workspace-files-bg",
  ],
  borders: [
    "--border", "--border-2", "--border-soft",
    "--button-border", "--button-border-hover", "--pill-border",
  ],
  "text/foregrounds": [
    "--text", "--text-2", "--text-3", "--fg", "--fg-dim", "--fg-faint",
  ],
  "accent/controls": [
    "--accent", "--accent-fg", "--accent-soft", "--accent-strong", "--accent-text",
    "--grad", "--focus-ring",
    "--control-primary-bg", "--control-primary-fg",
    "--control-disabled-bg", "--control-disabled-fg",
  ],
  "status colors": [
    "--ok", "--warn", "--err", "--danger-fg",
    "--add-bg", "--add-fg", "--del-bg", "--del-fg",
  ],
  "syntax highlighting": [
    "--hl-keyword", "--hl-string", "--hl-number", "--hl-comment",
    "--hl-func", "--hl-type", "--hl-builtin", "--hl-meta",
  ],
  "charts": [
    "--chart-1", "--chart-2", "--chart-3", "--chart-4", "--chart-5", "--chart-other",
  ],
  "mode pills": [
    "--mode-auto-bg", "--mode-auto-border", "--mode-auto-fg",
    "--mode-autopilot-bg", "--mode-autopilot-border", "--mode-autopilot-fg",
    "--mode-goal-bg", "--mode-goal-border", "--mode-goal-fg",
    "--mode-plan-bg", "--mode-plan-border", "--mode-plan-fg",
    "--mode-yolo-bg", "--mode-yolo-border", "--mode-yolo-fg",
  ],
  "chat user bubble": [
    "--chat-user-bg", "--chat-user-border", "--chat-user-fg", "--chat-user-shadow",
  ],
  "interactive surfaces": [
    "--button-bg", "--button-bg-hover", "--pill-bg", "--pill-hover-bg",
    "--sidebar-hover", "--sidebar-active",
    "--list-row-hover-bg", "--list-row-current-bg", "--list-row-current-title", "--list-row-current-meta",
    "--list-row-title", "--list-row-meta",
    "--tree-row-hover-bg", "--tree-row-current-bg", "--tree-row-current-fg", "--tree-row-current-icon",
    "--tree-row-marker",
    "--workspace-files-hover",
    "--workspace-selection-bg", "--workspace-selection-fg", "--workspace-selection-bar",
  ],
  "shadow/geometry": [
    "--shadow-1", "--shadow-2", "--r-s", "--r", "--r-l", "--radius",
  ],
};

const GROUPED_TOKENS = new Set(Object.values(GROUPS).flat());

function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, (comment) => comment.replace(/[^\n]/g, " "));
}

function findBlock(source, selector) {
  const start = source.indexOf(`${selector} {`);
  if (start < 0) return null;
  const open = source.indexOf("{", start);
  let depth = 0;
  for (let index = open; index < source.length; index += 1) {
    if (source[index] === "{") depth += 1;
    else if (source[index] === "}") {
      depth -= 1;
      if (depth === 0) return source.slice(open + 1, index);
    }
  }
  return null;
}

function declarations(block) {
  const result = new Map();
  if (!block) return result;
  for (const match of block.matchAll(/(--[a-zA-Z0-9_-]+)\s*:\s*([^;]+);/g)) {
    result.set(match[1], match[2].trim());
  }
  return result;
}

function normalize(value) {
  // Whitespace carries no meaning in color/gradient values (`120deg, #a` ==
  // `120deg,#a`), so strip it entirely before comparing.
  return value.replace(/\s+/g, "").toLowerCase();
}

// Resolve var() references inside a declaration value against the ordered
// chain scopes (first scope defining the name wins, cycle-safe). Handles the
// common alias (`--surface-2: var(--bg-elev-2)`) and composition
// (`--grad: linear-gradient(120deg, var(--accent), var(--accent-strong))`).
function resolveValue(value, scopes, seen = new Set()) {
  let out = value;
  for (const match of out.matchAll(/var\(\s*(--[a-zA-Z0-9_-]+)\s*\)/g)) {
    const name = match[1];
    if (seen.has(name)) return null;
    let found;
    // CSS cascade: later (more specific) scopes override earlier ones.
    for (const scope of [...scopes].reverse()) {
      if (scope.has(name)) { found = scope.get(name); break; }
    }
    if (found === undefined) return null;
    const nextSeen = new Set(seen);
    nextSeen.add(name);
    const resolved = resolveValue(found, scopes, nextSeen);
    if (resolved === null) return null;
    out = out.split(match[0]).join(resolved);
  }
  return out;
}

// Extract every swatch site: mode prefix (dark none / light / auto-light)
// + style + field + literal background value; plus the graphite-active
// override block which mirrors the graphite LIGHT palette (upstream #3752).
export function extractSwatches(source) {
  const swatches = [];
  const re = /(?:^|\n|\})\s*(:root\[data-theme="light"\]|:root:not\(\[data-theme\]\))?\s*\.theme-card__swatches\[data-theme-style-card="([a-z]+)"\]\s+\.theme-card__swatch--(bg|surface|accent)\s*\{\s*background:\s*([^;]+);/g;
  let m;
  while ((m = re.exec(source)) !== null) {
    const mode = m[1] === ':root[data-theme="light"]' ? "light" : m[1] === ":root:not([data-theme])" ? "auto" : "dark";
    swatches.push({ mode, style: m[2], field: m[3], value: m[4].trim(), index: m.index + m[0].indexOf(".theme-card") });
  }
  const overrideRe = /:root\[data-theme-style="graphite"\]\s+\.theme-card__swatches\[data-theme-style-card="graphite"\]\s+\.theme-card__swatch--(bg|surface|accent)\s*\{\s*background:\s*([^;]+);/g;
  while ((m = overrideRe.exec(source)) !== null) {
    swatches.push({ mode: "graphite-active", style: "graphite", field: m[1], value: m[2].trim(), index: m.index });
  }
  return swatches;
}

function lineOf(source, index) {
  return source.slice(0, index).split("\n").length;
}

// Core check over a stylesheet's text. Returns { errors, checkpoints }.
export function collectSwatchSync(cssText) {
  const source = stripComments(cssText);
  const errors = [];
  const checkpoints = [];

  // ---- definition block scopes per style/mode ----
  const merged = {};
  const definedTokens = new Set();
  for (const style of STYLES) {
    for (const mode of ["dark", "light", "auto"]) {
      const scopes = CHAINS[mode](style).map((sel) => declarations(findBlock(source, sel)));
      merged[`${style}/${mode}`] = scopes;
      for (let i = 1; i < scopes.length; i += 1) {
        for (const name of scopes[i].keys()) definedTokens.add(name);
      }
    }
  }

  // ---- gate A: every definition-block token belongs to a registered group ----
  for (const name of [...definedTokens].sort()) {
    if (!GROUPED_TOKENS.has(name)) {
      errors.push(`definition-block token ${name} is not registered in any semantic group (GROUPS) — register it with its group before use (tl1814 naming spec)`);
    }
  }

  // ---- gate B: swatch values mirror the resolved definition values ----
  const swatches = extractSwatches(source);
  const seenSites = new Set();
  for (const swatch of swatches) {
    const isOverride = swatch.mode === "graphite-active";
    const mode = isOverride ? "light" : swatch.mode;
    const key = `${swatch.style}/${swatch.mode}/${swatch.field}`;
    seenSites.add(key);
    const line = lineOf(source, swatch.index);
    const token = FIELD_MAP[swatch.field];
    const scopes = merged[`${swatch.style}/${mode}`];
    let rawValue = null;
    for (const scope of [...scopes].reverse()) {
      if (scope.has(token)) { rawValue = scope.get(token); break; }
    }
    if (rawValue === null) {
      errors.push(`styles.css:${line} swatch ${key} targets ${token} which is missing from the ${swatch.style}/${mode} definition chain`);
      checkpoints.push({ key, line, status: "missing-token" });
      continue;
    }
    const resolved = resolveValue(rawValue, scopes);
    if (resolved === null) {
      errors.push(`styles.css:${line} swatch ${key} targets ${token} whose var() chain cannot be resolved ("${rawValue}")`);
      checkpoints.push({ key, line, status: "unresolved" });
      continue;
    }
    const exempt = EXCEPTIONS.get(key);
    if (exempt) {
      checkpoints.push({ key, line, status: "exempt", reason: exempt });
      continue;
    }
    if (normalize(resolved) !== normalize(swatch.value)) {
      errors.push(`styles.css:${line} swatch ${key} drifted from ${token}: card has "${swatch.value}", definition resolves to "${resolved}"`);
      checkpoints.push({ key, line, status: "drift" });
    } else {
      checkpoints.push({ key, line, status: "mirror" });
    }
  }

  // ---- gate C: all 18 style/mode combinations must carry all three fields ----
  for (const style of STYLES) {
    for (const mode of ["dark", "light", "auto"]) {
      for (const field of Object.keys(FIELD_MAP)) {
        const key = `${style}/${mode}/${field}`;
        if (!seenSites.has(key)) {
          errors.push(`styles.css is missing the ${key} preview swatch (six styles x three modes x bg/surface/accent is the contract)`);
        }
      }
    }
  }

  return { errors, checkpoints };
}

function report(source) {
  const { errors, checkpoints } = collectSwatchSync(source);
  const byStatus = new Map();
  for (const cp of checkpoints) byStatus.set(cp.status, (byStatus.get(cp.status) ?? 0) + 1);
  process.stdout.write(`swatch sync checkpoints: ${checkpoints.length}\n`);
  for (const [status, count] of [...byStatus.entries()].sort()) {
    process.stdout.write(`  ${status.padEnd(14)} ${count}\n`);
  }
  process.stdout.write("\nper-checkpoint:\n");
  for (const cp of checkpoints) {
    process.stdout.write(`  styles.css:${String(cp.line).padEnd(6)} ${cp.key.padEnd(28)} ${cp.status}${cp.reason ? `  (${cp.reason})` : ""}\n`);
  }
  process.stdout.write("\nregistered exceptions:\n");
  for (const [key, reason] of EXCEPTIONS) process.stdout.write(`  ${key.padEnd(28)} ${reason}\n`);
  if (errors.length) {
    process.stdout.write(`\nerrors (${errors.length}):\n`);
    for (const e of errors) process.stdout.write(`- ${e}\n`);
  }
}

// Fixture: one style's definition blocks + nine swatch lines. Scoped to a
// single style so gate C noise stays out of the asserted surfaces.
function fixture({ bg, surface, accent, extraToken = "", swatchOverrides = {} }) {
  const swatch = (field, darkValue, lightValue) =>
    `.theme-card__swatches[data-theme-style-card="graphite"] .theme-card__swatch--${field} { background: ${darkValue}; }
:root[data-theme="light"] .theme-card__swatches[data-theme-style-card="graphite"] .theme-card__swatch--${field} { background: ${lightValue}; }
@media (prefers-color-scheme: light) {
:root:not([data-theme]) .theme-card__swatches[data-theme-style-card="graphite"] .theme-card__swatch--${field} { background: ${lightValue}; }
}`;
  const val = (mode, field, fallback) => swatchOverrides[`${mode}/${field}`] ?? fallback;
  return `
:root {
  --bg: #090a0c;
  --bg-elev-2: #222631;
  --surface-2: var(--bg-elev-2);
  --accent: #ff6a3d;
  --accent-strong: #ff9a52;
  --grad: linear-gradient(120deg, var(--accent), var(--accent-strong));
}
:root[data-theme-style="graphite"] {
  --bg: #0c0d10;
  --bg-elev-2: #191a1f;
  ${extraToken}
}
:root[data-theme="light"][data-theme-style="graphite"] {
  --bg: #f4f3ef;
  --bg-elev-2: #ffffff;
}
:root[data-theme-style="graphite"]:not([data-theme]) {
  --bg: #f4f3ef;
  --bg-elev-2: #ffffff;
}
${swatch("bg", val("dark", "bg", bg), val("light", "bg", "#f4f3ef"))}
${swatch("surface", val("dark", "surface", surface), val("light", "surface", "#ffffff"))}
${swatch("accent", val("dark", "accent", accent), val("light", "accent", accent))}
`;
}

async function selfTest() {
  const cases = [];
  const gateAB = (css) => collectSwatchSync(css).errors.filter((e) => !e.includes("preview swatch"));
  const has = (errors, needle) => errors.some((e) => e.includes(needle));

  const mirrored = fixture({
    bg: "#0c0d10",
    surface: "#191a1f",
    accent: "linear-gradient(120deg, #ff6a3d, #ff9a52)",
  });
  cases.push(["mirrored fixture: zero gate A/B errors", gateAB(mirrored).length === 0]);

  cases.push([
    "bg drift is caught and names the site",
    has(gateAB(fixture({ bg: "#0c0d10", surface: "#191a1f", accent: "linear-gradient(120deg, #ff6a3d, #ff9a52)", swatchOverrides: { "dark/bg": "#dead02" } })), "swatch graphite/dark/bg drifted from --bg"),
  ]);

  cases.push([
    "surface alias resolves through --surface-2 → --bg-elev-2",
    !has(gateAB(fixture({ bg: "#0c0d10", surface: "#191a1f", accent: "linear-gradient(120deg, #ff6a3d, #ff9a52)" })), "surface drifted"),
  ]);

  cases.push([
    "accent gradient comparison is whitespace-insensitive",
    !has(gateAB(fixture({ bg: "#0c0d10", surface: "#191a1f", accent: "linear-gradient(120deg,#ff6a3d,#ff9a52)" })), "accent drifted"),
  ]);

  cases.push([
    "unregistered definition token is caught (gate A)",
    has(gateAB(fixture({ bg: "#0c0d10", surface: "#191a1f", accent: "linear-gradient(120deg, #ff6a3d, #ff9a52)", extraToken: "--graphite-only: #123456;" })), "--graphite-only is not registered"),
  ]);

  cases.push([
    "missing swatch site is caught (gate C)",
    has(collectSwatchSync(fixture({ bg: "#0c0d10", surface: "#191a1f", accent: "linear-gradient(120deg, #ff6a3d, #ff9a52)" }).replace(/\n\.theme-card__swatches\[data-theme-style-card="graphite"\] \.theme-card__swatch--bg \{ background: #0c0d10; \}/, "")).errors, "missing the graphite/dark/bg preview swatch"),
  ]);

  cases.push([
    "light chain override: light card must follow the light --bg-elev-2",
    has(gateAB(fixture({ bg: "#0c0d10", surface: "#191a1f", accent: "linear-gradient(120deg, #ff6a3d, #ff9a52)" }).replace(/--bg-elev-2: #ffffff;/, "--bg-elev-2: #f4f3ef;")), "graphite/light/surface drifted"),
  ]);

  let ok = true;
  for (const [label, passed] of cases) {
    process.stdout.write(`  ${passed ? "PASS" : "FAIL"}  ${label}\n`);
    if (!passed) ok = false;
  }
  return ok;
}

const readSource = () => fs.readFileSync(stylesCss, "utf8");
const mode = process.argv[2];
if (mode === "--report") {
  report(readSource());
} else if (mode === "--self-test") {
  const ok = await selfTest();
  process.exit(ok ? 0 : 1);
} else if (mode === undefined) {
  const { errors } = collectSwatchSync(readSource());
  if (errors.length > 0) {
    process.stdout.write(`FAIL theme swatch sync: ${errors.length} error(s):\n`);
    for (const e of errors) process.stdout.write(`  ${e}\n`);
    process.exit(1);
  }
  process.stdout.write(`PASS theme swatch sync: 6 styles x 3 modes x bg/surface/accent all mirror their definition blocks (exceptions: ${EXCEPTIONS.size} registered).\n`);
} else {
  process.stderr.write(`unknown mode: ${mode}\n`);
  process.exit(2);
}
