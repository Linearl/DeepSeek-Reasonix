// Run: tsx src/__tests__/sidebar-new-button-theme.test.ts
// Task 358: the sidebar "new chat" button and the Reasonix wordmark must follow
// the theme accent, not a fixed brand blue. The wordmark side landed with task
// 401 (d0b27d136, pinned by sidebar-brand-logo-theme.test.ts); the button side
// was already variable-driven, so this contract exists to keep it that way —
// a merge that reintroduces a hardcoded colour turns this red.
//
// Theme-contrast matrix asserted below (acceptance: >= 2 themes, light+dark):
//   :root (dark default)            --accent #d97757 (orange)
//   :root[data-theme="light"]       --accent #2f5fa8 (blue)
//   [data-theme-style="graphite"]   --accent #ff6a3d + explicit --grad (orange)
// The button paints background: var(--grad) / color: var(--accent-text), and
// --grad derives from var(--accent) at :root, so every theme redefinition of
// --accent (or --grad) flows into the button automatically.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const srcRoot = resolve(testDir, "..");
const styles = readFileSync(resolve(srcRoot, "styles.css"), "utf8");
const appTsx = readFileSync(resolve(srcRoot, "App.tsx"), "utf8");
const sidebarRegion = readFileSync(resolve(srcRoot, "app-shell/SidebarRegion.tsx"), "utf8");

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

/** Extract the full brace block whose rule starts at the first occurrence of `start`. */
function blockStartingWith(start: string): string {
  const at = styles.indexOf(start);
  if (at < 0) return "";
  const open = styles.indexOf("{", at);
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

function stripComments(block: string): string {
  return block.replace(/\/\*[\s\S]*?\*\//g, "");
}

function hardcodedHex(block: string): string[] {
  return stripComments(block).match(/#[0-9a-fA-F]{3,8}\b/g) ?? [];
}

console.log("\nTask 358 sidebar new-chat button follows theme accent");

// 1. Render sites: both sidebar variants emit the new-chat button.
ok(appTsx.includes('<button\n                className="sidebar__new"') || /<button[^>]*className="sidebar__new"/.test(appTsx), "App.tsx renders sidebar__new button");
ok(/<button className="sidebar__new"/.test(sidebarRegion), "SidebarRegion.tsx renders sidebar__new button");

// 2. Base button block: theme variables only, zero hardcoded colours.
const baseBlock = stripComments(blockStartingWith("\n.sidebar__new {"));
ok(baseBlock.length > 0, "styles.css: base .sidebar__new block exists");
ok(/background:\s*var\(--grad\)/.test(baseBlock), "base button paints var(--grad)");
ok(/color:\s*var\(--accent-text\)/.test(baseBlock), "base button text uses var(--accent-text)");
ok(/var\(--accent\)/.test(baseBlock), "base button box-shadow glows with var(--accent)");
ok(hardcodedHex(baseBlock).length === 0, `base button block has no hardcoded colour (found ${hardcodedHex(baseBlock).join(", ") || "none"})`);

// 3. Hover block keeps the same variable-driven paint.
const hoverBlock = stripComments(blockStartingWith(".sidebar__new:hover {"));
ok(/background:\s*var\(--grad\)/.test(hoverBlock), "hover keeps var(--grad) background");
ok(/color:\s*var\(--accent-text\)/.test(hoverBlock), "hover keeps var(--accent-text) text");

// 4. Theme-style variant block also variable-driven.
const styleBlock = stripComments(blockStartingWith(":root[data-theme-style] .sidebar__new {"));
ok(styleBlock.length > 0, "styles.css: [data-theme-style] .sidebar__new block exists");
ok(/background:\s*var\(--grad\)/.test(styleBlock) && /color:\s*var\(--accent-text\)/.test(styleBlock), "theme-style variant paints var(--grad)/var(--accent-text)");
ok(hardcodedHex(styleBlock).length === 0, "theme-style variant block has no hardcoded colour");

// 5. Workbench quick variant is deliberately neutral (fg-dim on hover blend),
//    not the old brand blue — pinned so it stays a conscious choice.
const workbenchBlock = stripComments(blockStartingWith(".sidebar--workbench .sidebar__new {"));
ok(workbenchBlock.length > 0, "styles.css: workbench .sidebar__new block exists");
ok(/color:\s*var\(--fg-dim\)/.test(workbenchBlock), "workbench variant uses neutral var(--fg-dim)");
ok(/color-mix\(in srgb, var\(--sidebar-hover\)/.test(workbenchBlock), "workbench variant background derives from theme surfaces");
ok(hardcodedHex(workbenchBlock).length === 0, "workbench variant block has no hardcoded colour");

// 6. Creation variant derives from var(--accent) via color-mix.
const creationBlock = stripComments(blockStartingWith(".app--creation .sidebar__new,"));
ok(creationBlock.length > 0, "styles.css: creation .sidebar__new block exists");
ok(/color-mix\(in srgb, var\(--accent\)/.test(creationBlock), "creation variant derives from var(--accent) via color-mix");
ok(hardcodedHex(creationBlock).length === 0, "creation variant block has no hardcoded colour");

// 7. No block of the button carries the retired fixed brand blue (#0153e5,
//    the pre-401 wordmark fill users saw in the task 358 screenshot).
const allButtonBlocks = [baseBlock, hoverBlock, styleBlock, workbenchBlock, creationBlock].join("\n");
ok(!allButtonBlocks.includes("#0153e5"), "no sidebar__new block carries the retired fixed blue #0153e5");

// 8. Theme-contrast matrix: at least two accents across light/dark, and the
//    --grad derivation that carries them into the button.
const rootBlock = stripComments(blockStartingWith(":root {"));
ok(/--grad:\s*linear-gradient\(120deg,\s*var\(--accent\),\s*var\(--accent-strong\)\)/.test(rootBlock), ":root derives --grad from var(--accent) (theme flow-through)");
const lightBlock = stripComments(blockStartingWith(':root[data-theme="light"] {'));
ok(/--accent:\s*#2f5fa8/.test(lightBlock), "light theme accent #2f5fa8 (blue) defined");
const darkDefault = rootBlock.match(/--accent:\s*#[0-9a-fA-F]{3,8}/)?.[0] ?? "";
ok(/#d97757/.test(darkDefault), "dark default accent #d97757 (orange) defined at :root");
const graphiteDark = stripComments(blockStartingWith(':root[data-theme-style="graphite"] {'));
ok(/--accent:\s*#ff6a3d/.test(graphiteDark) && /--grad:\s*linear-gradient\(120deg,\s*#ff6a3d/.test(graphiteDark), "graphite theme redefines accent #ff6a3d + --grad (orange)");
const graphiteLight = stripComments(blockStartingWith(':root[data-theme="light"][data-theme-style="graphite"] {'));
ok(/--accent:\s*#ff5a2c/.test(graphiteLight) && /--grad:\s*linear-gradient\(120deg,\s*#ff5a2c/.test(graphiteLight), "graphite light override redefines accent #ff5a2c + --grad (light-tuned orange)");

// 9. Companion surface (task 358 scope covers both): the wordmark still paints
//    with var(--accent) — full contract lives in sidebar-brand-logo-theme.test.ts.
const logoBlock = stripComments(blockStartingWith(".sidebar__brand-logo {"));
ok(/background-color:\s*var\(--accent\)/.test(logoBlock), "sidebar brand logo still paints var(--accent) (task 401 anchor)");

process.stdout.write(`\n${passed}/${passed + failed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
