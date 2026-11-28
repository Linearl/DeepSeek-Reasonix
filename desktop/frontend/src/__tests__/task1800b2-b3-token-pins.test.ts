// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1800b2-b3-token-pins.test.ts
// tl1800 batch 3 (tl1814 plan B3) — same-value replacement pins.
//
// Why: batch 3 converted 45 bare color uses to 22 new tokens under the
// "definition value = the replaced literal" rule (tl1814 naming spec §1.4,
// route C). The zero-visual-regression guarantee rests on the byte identity
// between each token's :root definition and the literal it replaced. This
// pin freezes those identities so a future edit cannot silently recolor a
// "same-value" token.
//
// Pin groups:
//   1. The 22 new B3 tokens: --ctx-* palette (12), --code-hl,
//      --backdrop-deep, --wash-neutral, --wash-ink, --canvas-dark,
//      --ok-strong(-hover), --danger-confirm(-hover), --surface-fixed-light.
//      :root definition must stay literally equal to the original bare value
//      recorded in tl1814-裸色值六组分批计划-20261126.tsv.
//   2. Declared deviation anchor: heartbeat.css's wash site was
//      rgba(128,128,128,0.08) and consolidates onto --wash-neutral
//      rgb(127,127,127) — the plan-declared Δ1 (imperceptible at 8% alpha).
//      The pin asserts the heartbeat site now references the token and the
//      bare 128 literal is gone, so the deviation stays a recorded decision.
//   3. TSV grouping correction anchor: the salmon hover rule's border/background
//      literals are byte-equal compositions of --danger-confirm's base
//      (#ff8b6d = rgb(255,139,109)), NOT --danger-confirm-hover (#ff9f86,
//      text color only). The pins assert that mapping survives.
//
// Pin semantics: equality pins — change the expected literal ONLY in the
// same commit that deliberately recolors the token AND its use sites (that
// is a design decision, not a refactor; tl1800 non-goals forbid it inside
// the收口 batches).

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const here = fileURLToPath(new URL(".", import.meta.url));
const stylesPath = `${here}../styles.css`;
const heartbeatPath = `${here}../custom/features/heartbeat/heartbeat.css`;

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

console.log("\ntl1800 batch 3 same-value token pins (definition = replaced literal)");

const styles = readFileSync(stylesPath, "utf8");
const heartbeat = readFileSync(heartbeatPath, "utf8");

// Base :root block only (first ":root {" in the file) — the theme-invariant
// constants live there, theme definition blocks must never override them.
const rootStart = styles.indexOf(":root {");
assert.ok(rootStart >= 0, "base :root block found");
const declStart = styles.indexOf("{", rootStart) + 1;
let depth = 1;
let declEnd = declStart;
for (let i = declStart; i < styles.length; i += 1) {
  if (styles[i] === "{") depth += 1;
  else if (styles[i] === "}") { depth -= 1; if (depth === 0) { declEnd = i; break; } }
}
const rootBlock = styles.slice(declStart, declEnd);

function rootValue(token: string): string | null {
  const m = rootBlock.match(new RegExp(`--${token}\\s*:\\s*([^;]+);`));
  return m ? m[1].trim() : null;
}

// Group 1 — the 22 new B3 tokens (tl1814 plan B3 registry values).
const newTokens: Array<[string, string]> = [
  // context-panel palette (main + muted tiers).
  ["ctx-prompt", "#13a7a5"],
  ["ctx-completion", "#2f6df6"],
  ["ctx-reasoning", "#f97316"],
  ["ctx-hit", "#16a34a"],
  ["ctx-amber", "#f59e0b"],
  ["ctx-slate", "#64748b"],
  ["ctx-violet", "#7c3aed"],
  ["ctx-rose", "#e11d48"],
  ["ctx-prompt-muted", "#6b8f8f"],
  ["ctx-completion-muted", "#7a8fb8"],
  ["ctx-reasoning-muted", "#b48a58"],
  ["ctx-other-muted", "#c7c7c7"],
  // editor highlight yellow base (rgba(255,193,7,α) alphas ride color-mix).
  ["code-hl", "#ffc107"],
  // style-scoped deep backdrop/shadow base (rgba(20,22,28,α)).
  ["backdrop-deep", "rgb(20, 22, 28)"],
  // neutral washes (rgb(127,127,127) / rgb(64,64,64) bases).
  ["wash-neutral", "rgb(127, 127, 127)"],
  ["wash-ink", "rgb(64, 64, 64)"],
  // dark-forced mermaid canvas (= baseline --bg-soft value, independent
  // token on purpose: it must not follow the theme in light mode).
  ["canvas-dark", "#111319"],
  // heartbeat greens (Material green 500/600 pair).
  ["ok-strong", "#4caf50"],
  ["ok-strong-hover", "#43a047"],
  // history clear-confirm salmon (base + non-linear hover tint).
  ["danger-confirm", "#ff8b6d"],
  ["danger-confirm-hover", "#ff9f86"],
  // !important fixed light surface (topicbar; must not read a scoped var).
  ["surface-fixed-light", "#f5f6f9"],
];
for (const [token, literal] of newTokens) {
  const actual = rootValue(token);
  ok(actual !== null && actual.toLowerCase() === literal.toLowerCase(),
    `--${token} stays ${literal} (found ${actual ?? "<missing>"})`);
}

// Group 2 — heartbeat Δ1 anchor: the 128-gray literal is gone and the site
// rides --wash-neutral (127 base) via color-mix.
ok(!heartbeat.includes("rgba(128, 128, 128"),
  "heartbeat bare rgba(128,128,128,*) literal removed");
ok(heartbeat.includes("color-mix(in srgb, var(--wash-neutral) 8%, transparent)"),
  "heartbeat project-menu active row rides --wash-neutral (declared Δ1)");

// Group 3 — salmon mapping anchors: hover rule text uses --danger-confirm-hover
// while its border/background keep --danger-confirm's base (byte-equality).
ok(/\.history-clear--confirm:hover \{[^}]*color: var\(--danger-confirm-hover\);/.test(styles),
  "history-clear hover text stays --danger-confirm-hover");
ok(/\.history-clear--confirm:hover \{[^}]*border-color: color-mix\(in srgb, var\(--danger-confirm\) 62%, transparent\);/.test(styles),
  "history-clear hover border rides --danger-confirm base (62%)");
ok(/\.history-clear--confirm:hover \{[^}]*background: color-mix\(in srgb, var\(--danger-confirm\) 16%, transparent\);/.test(styles),
  "history-clear hover background rides --danger-confirm base (16%)");

// Group 3b — miss segment shares --ctx-reasoning (same value, plan note).
ok(/\.context-panel__source-bar-segment--miss \{ background: var\(--ctx-reasoning\); \}/.test(styles),
  "source-bar miss segment rides --ctx-reasoning (same-value share)");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
