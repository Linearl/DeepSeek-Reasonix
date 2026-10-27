// Run: tsx src/__tests__/mode-css-split.test.ts
//
// tl1191 harness: the three layout-mode stylesheets were extracted verbatim
// from styles.css into src/styles/mode-{classic,workbench,creation}.css and
// load next to their layout (lib/desktopLayoutStyle.ts). These assertions pin
// the invariants the split promised, so a later merge or hand edit cannot
// silently drop blocks (the auto-merge lost-CSS lesson) or put shared rules
// behind a mode gate:
//   1. byte-level block inventory: pre-split styles.css blocks == post-split
//      styles.css blocks ∪ mode blocks, modulo the single declared addition
//      (the .app-chrome--loading skeleton kept in the shared stylesheet);
//   2. ownership completeness: every block in a mode file matches only that
//      mode's signature;
//   3. extraction completeness: styles.css keeps no pure single-mode block;
//   4. source contract: the chrome chunk stays lazy, mode sheets load through
//      lib/desktopLayoutStyle.ts only, and main.tsx joins the boot prefetch.

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "..", "..");

// --- shared lexer with the one-shot splitter: top-level blocks, comments and
// leading whitespace ride with their rule, byte order preserved.
function parseBlocks(input: string): string[] {
  const blocks: string[] = [];
  let buf = "";
  let i = 0;
  const pushBuf = (chunk: string) => { buf += chunk; };
  const takeBuf = () => { const t = buf; buf = ""; return t; };
  while (i < input.length) {
    const ch = input[i];
    if (ch === "/" && input[i + 1] === "*") {
      const end = input.indexOf("*/", i + 2);
      const stop = end === -1 ? input.length : end + 2;
      pushBuf(input.slice(i, stop));
      i = stop;
      continue;
    }
    if (ch === "{") {
      const blockStart = i - buf.length;
      takeBuf(); // header; not needed for inventory
      let depth = 1;
      let j = i + 1;
      while (j < input.length && depth > 0) {
        const c = input[j];
        if (c === "/" && input[j + 1] === "*") {
          const e = input.indexOf("*/", j + 2);
          j = e === -1 ? input.length : e + 2;
          continue;
        }
        if (c === '"' || c === "'") {
          const q = c;
          j++;
          while (j < input.length && input[j] !== q) {
            if (input[j] === "\\") j++;
            j++;
          }
          j++;
          continue;
        }
        if (c === "{") depth++;
        else if (c === "}") depth--;
        j++;
      }
      blocks.push(input.slice(blockStart, j));
      i = j;
      continue;
    }
    pushBuf(ch);
    i++;
  }
  return blocks;
}

// --- mode signature (mirrors the splitter's ownership rule)
function signature(selectorHeader: string): "classic" | "workbench" | "creation" | null {
  const set = new Set<string>();
  const re = /\.([A-Za-z_][A-Za-z0-9_-]*)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(selectorHeader)) !== null) {
    const tok = m[1];
    if (tok.startsWith("app-chrome") || tok.startsWith("tabbar") || tok.startsWith("tab-overview") || tok.includes("--classic")) set.add("classic");
    else if (tok.includes("--workbench")) set.add("workbench");
    else if (tok.includes("--creation")) set.add("creation");
  }
  return set.size === 1 ? [...set][0] : null;
}

function blockSignature(block: string): "classic" | "workbench" | "creation" | null {
  const brace = block.indexOf("{");
  const header = block.slice(0, brace).trim();
  if (header.startsWith("@property")) return header.includes("--creation") ? "creation" : null;
  if (header.startsWith("@keyframes")) return /@keyframes\s+creation-/.test(header) ? "creation" : null;
  if (header.startsWith("@media") || header.startsWith("@container")) {
    const inner = parseBlocks(block.slice(brace + 1, block.lastIndexOf("}")));
    const sigs = [...new Set(inner.map((b) => {
      const ib = b.indexOf("{");
      return signature(b.slice(0, ib));
    }))];
    return inner.length > 0 && sigs.length === 1 && sigs[0] !== null ? sigs[0] : null;
  }
  if (header.startsWith("@")) return null;
  return signature(header);
}

// --- inputs
const SKELETON_BLOCK = ".app-chrome--loading";
// Baseline resolution, deliberately merge-state independent: the old probe
// (`git merge-base HEAD main-v2-stable`) silently degraded to "HEAD itself"
// once the split landed on the mainline, re-flagging every mode block as an
// addition. Order:
//   1. MODE_SPLIT_BASELINE_SHA env pin: a frozen stylesheet for point-in-time
//      audits (point it at a pre-split commit and run it against a tree of
//      that era to reproduce the original split read; also the escape hatch
//      for rewritten history);
//   2. otherwise a rolling HEAD baseline: the stylesheets as committed in
//      HEAD. A frozen split-era baseline would go red on ordinary mainline
//      CSS evolution (unrelated legit commits move blocks too), so the
//      default baseline rolls with HEAD and keeps every assertion live in
//      both pre-merge (branch-local) and post-merge (mainline) states.
// The baseline's own skeleton block is always stripped before comparison: the
// skeleton is the harness's single declared addition, so stripping keeps
// "exactly one addition" true for any baseline choice; the skeleton's home in
// the shared stylesheet is still guarded by its own assertions below.
const BASELINE_ENV = "MODE_SPLIT_BASELINE_SHA";
const envPin = process.env[BASELINE_ENV]?.trim() || "";
// `git show <rev>:<path>` takes repo-root-relative paths.
const sheetPaths = [
  "desktop/frontend/src/styles.css",
  "desktop/frontend/src/styles/mode-classic.css",
  "desktop/frontend/src/styles/mode-workbench.css",
  "desktop/frontend/src/styles/mode-creation.css",
];
function showOrNull(rev: string, path: string): string | null {
  try {
    return execFileSync("git", ["show", `${rev}:${path}`], { cwd: frontendRoot, encoding: "utf8", maxBuffer: 32 * 1024 * 1024, stdio: ["ignore", "pipe", "ignore"] });
  } catch {
    return null;
  }
}
let baselineHow = "";
const baseParts: string[] = [];
if (envPin) {
  const pinned = showOrNull(envPin, sheetPaths[0]);
  if (pinned === null) {
    process.stderr.write(`mode css split: ${BASELINE_ENV}=${envPin} has no ${sheetPaths[0]}.\n`);
    process.exit(1);
  }
  baselineHow = `${envPin.slice(0, 10)} ${BASELINE_ENV} pin (frozen)`;
  baseParts.push(pinned);
} else {
  baselineHow = `HEAD ${execFileSync("git", ["rev-parse", "--short=10", "HEAD"], { cwd: frontendRoot, encoding: "utf8" }).trim()} (rolling)`;
  for (const path of sheetPaths) {
    const text = showOrNull("HEAD", path);
    // Mode sheets carry a declaration banner; strip it exactly like the
    // working-tree copies below so both sides compare CSS blocks only.
    if (text !== null) baseParts.push(path.endsWith("styles.css") ? text : stripBanner(text));
  }
}
const baseStyles = baseParts.join("\n");
const mainStyles = readFileSync(resolve(frontendRoot, "src/styles.css"), "utf8");
// Strip each mode file's declaration banner so the inventory compares CSS
// blocks only (the banner would otherwise fuse with the first rule into a
// synthetic block that exists in neither inventory).
function stripBanner(text: string): string {
  const trimmed = text.trimStart();
  return trimmed.startsWith("/*") ? trimmed.slice(trimmed.indexOf("*/") + 2) : trimmed;
}
const classicStyles = stripBanner(readFileSync(resolve(frontendRoot, "src/styles/mode-classic.css"), "utf8"));
const workbenchStyles = stripBanner(readFileSync(resolve(frontendRoot, "src/styles/mode-workbench.css"), "utf8"));
const creationStyles = stripBanner(readFileSync(resolve(frontendRoot, "src/styles/mode-creation.css"), "utf8"));
const appSource = readFileSync(resolve(frontendRoot, "src/App.tsx"), "utf8");
const layoutLibSource = readFileSync(resolve(frontendRoot, "src/lib/desktopLayoutStyle.ts"), "utf8");
const mainSource = readFileSync(resolve(frontendRoot, "src/main.tsx"), "utf8");

console.log(`\nmode css split (baseline: ${baselineHow})`);

// --- 1. block inventory: nothing lost, one declared addition
// Comparison key strips leading whitespace: a block's leading gap is file
// formatting (banners, blank-line runs), not rule content.
const norm = (b: string) => b.replace(/^\s+/, "");
// Skeleton stripped per-block after the single parse (never text-spliced back
// into the source: block boundaries stay byte-faithful).
const baseBlocks = parseBlocks(baseStyles).filter((b) => !b.includes(SKELETON_BLOCK)).map(norm);
const nowBlocks = [
  ...parseBlocks(mainStyles),
  ...parseBlocks(classicStyles),
  ...parseBlocks(workbenchStyles),
  ...parseBlocks(creationStyles),
].map(norm);
const baseSet = new Set(baseBlocks);
const nowSet = new Set(nowBlocks);
const lost = [...baseSet].filter((b) => !nowSet.has(b));
const added = [...nowSet].filter((b) => !baseSet.has(b));
ok(lost.length === 0, `no pre-split block was dropped (${lost.length} lost)`);
ok(added.length === 1 && added[0].includes(SKELETON_BLOCK), `the only post-split addition is the ${SKELETON_BLOCK} skeleton (found ${added.length})`);
ok(mainStyles.includes(SKELETON_BLOCK), "the skeleton stays in the shared app-shell stylesheet");
ok(!classicStyles.includes(SKELETON_BLOCK) && !workbenchStyles.includes(SKELETON_BLOCK) && !creationStyles.includes(SKELETON_BLOCK), "the skeleton is not stranded in a mode file");

// --- 2. ownership: mode files only contain their own signature
function ownershipOf(text: string): { own: number; foreign: number } {
  let own = 0;
  let foreign = 0;
  for (const block of parseBlocks(text)) {
    const sig = blockSignature(block);
    const fileSig = text === classicStyles ? "classic" : text === workbenchStyles ? "workbench" : "creation";
    if (sig === fileSig) own += 1;
    else foreign += 1;
  }
  return { own, foreign };
}
for (const [name, text] of [["mode-classic.css", classicStyles], ["mode-workbench.css", workbenchStyles], ["mode-creation.css", creationStyles]] as const) {
  const { own, foreign } = ownershipOf(text);
  ok(foreign === 0, `${name}: every block carries only its own mode signature (${own} own, ${foreign} foreign)`);
}

// --- 3. extraction completeness: no pure single-mode block left behind.
// Declared exception: the .app-chrome--loading skeleton — it carries the
// classic signature but MUST render before the classic chunk (and its CSS)
// loads, so it stays in the shared stylesheet on purpose.
const leftover: Record<string, number> = { classic: 0, workbench: 0, creation: 0 };
for (const block of parseBlocks(mainStyles)) {
  if (block.includes(SKELETON_BLOCK)) continue;
  const sig = blockSignature(block);
  if (sig !== null) leftover[sig] += 1;
}
ok(leftover.classic === 0 && leftover.workbench === 0 && leftover.creation === 0, `styles.css keeps no pure single-mode block beyond the declared skeleton (leftover ${JSON.stringify(leftover)})`);

// --- 4. source contract: lazy chrome chunk + mode stylesheet loaders
ok(
  !/import\s+\{[^}]*AppChrome\s*\}\s+from\s+["']\.\/components\/AppChrome["']/.test(appSource) &&
    appSource.includes('lazy(() => import("./components/AppChrome")'),
  "App keeps the classic chrome chunk lazy (no eager runtime edge)",
);
ok(
  appSource.includes("loadModeStylesheet(desktopLayoutStyle)") &&
    appSource.includes('from "./lib/desktopLayoutStyle"'),
  "App loads mode stylesheets through the shared layout lib",
);
ok(
  layoutLibSource.includes('import("../styles/mode-classic.css")') &&
    layoutLibSource.includes('import("../styles/mode-workbench.css")') &&
    layoutLibSource.includes('import("../styles/mode-creation.css")'),
  "the layout lib owns the three dynamic mode-stylesheet imports",
);
ok(
  mainSource.includes("startupModeAssetPrefetch()"),
  "main joins the boot parallel-load set with the active mode's assets",
);
ok(
  /useEffect\(\(\) => \{\s*loadModeStylesheet\(desktopLayoutStyle\)/.test(appSource),
  "App re-loads the mode stylesheet when the authoritative layout changes",
);

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
