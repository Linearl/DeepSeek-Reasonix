// Run: tsx src/__tests__/sidebar-brand-logo-theme.test.ts
// Task 401 (upstream #11168 -> #11188): the sidebar brand wordmark follows the
// theme accent instead of the fixed brand blue. It is now a masked <span>, so
// this contract pins the three things a merge could silently undo:
//   1. both render sites stop emitting a plain <img>,
//   2. the CSS block paints with var(--accent) through an SVG mask (no
//      hardcoded colour),
//   3. the dark-theme brightness/invert rule no longer claims the sidebar
//      logo (it would flatten the accent to white).

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const srcRoot = resolve(testDir, "..");
const styles = readFileSync(resolve(srcRoot, "styles.css"), "utf8");
const appTsx = readFileSync(resolve(srcRoot, "App.tsx"), "utf8");
const sidebarRegion = readFileSync(resolve(srcRoot, "app-shell/SidebarRegion.tsx"), "utf8");
const welcomeTsx = readFileSync(resolve(srcRoot, "components/Welcome.tsx"), "utf8");

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function ruleBlock(selector: string): string {
  const start = styles.indexOf(`${selector} {`);
  if (start < 0) return "";
  const open = styles.indexOf("{", start);
  let depth = 0;
  for (let index = open; index < styles.length; index += 1) {
    if (styles[index] === "{") depth += 1;
    else if (styles[index] === "}") {
      depth -= 1;
      if (depth === 0) return styles.slice(open + 1, index);
    }
  }
  return "";
}

console.log("\nTask 401 sidebar brand logo theme tint");

// 1. Render sites: masked span, not <img>.
for (const [name, source] of [["App.tsx", appTsx], ["SidebarRegion.tsx", sidebarRegion]] as const) {
  ok(!/<img[^>]*sidebar__brand-logo/.test(source), `${name}: no <img> carries sidebar__brand-logo`);
  const spans = source.match(/<span role="img" aria-label="Reasonix" className="sidebar__brand-logo[^"]*" \/>/g) ?? [];
  ok(spans.length === 2, `${name}: both brand variants render role=img span (got ${spans.length})`);
  ok(spans.some((entry) => entry.includes("sidebar__brand-logo--workbench")), `${name}: workbench variant present`);
  ok(!source.includes("logoWordmark"), `${name}: unused logoWordmark import removed`);
}

// 2. CSS: accent paint through a mask, zero hardcoded colours in the block.
// Comments carry upstream issue numbers (#11168) that look like hex colours;
// strip them so the no-hardcoded-colour check only reads declarations.
const logoBlock = ruleBlock(".sidebar__brand-logo").replace(/\/\*[\s\S]*?\*\//g, "");
ok(logoBlock.length > 0, "styles.css: .sidebar__brand-logo block exists");
ok(/background-color:\s*var\(--accent\)/.test(logoBlock), "logo paints with var(--accent)");
ok(/mask-image:\s*url\("\.\/assets\/logo-wordmark\.svg"\)/.test(logoBlock), "logo masked with the original SVG asset");
ok(/-webkit-mask-image:/.test(logoBlock) && /mask-size:\s*contain/.test(logoBlock), "webkit alias + contain sizing present");
const hexColors = logoBlock.match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
ok(hexColors.length === 0, `logo block has no hardcoded colour (found ${hexColors.join(", ") || "none"})`);

// 3. Dark-theme inversion must not claim the sidebar logo any more.
const invertedSelectors = styles.match(/[^{}]*brightness\(0\) invert\(1\)[^{}]*\{/g) ?? [];
ok(
  invertedSelectors.every((rule) => !rule.includes(".sidebar__brand-logo")),
  "brightness/invert dark rule excludes .sidebar__brand-logo",
);

// 4. Sibling surfaces untouched: welcome keeps its <img> path (unchanged task scope).
ok(/<img[^>]*welcome__brand-logo/.test(welcomeTsx), "Welcome logo remains an <img> (scope boundary)");
ok(styles.includes(":root[data-theme=\"dark\"] .welcome__brand-logo"), "welcome logo keeps its dark inversion rule");

process.stdout.write(`\n${passed}/${passed + failed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
