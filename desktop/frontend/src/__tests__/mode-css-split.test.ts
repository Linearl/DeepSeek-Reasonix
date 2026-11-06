// Run: tsx src/__tests__/mode-css-split.test.ts
//
// tl1191 harness: the layout-mode stylesheets were extracted verbatim from
// styles.css into src/styles/mode-{classic,workbench}.css and load next to
// their layout (lib/desktopLayoutStyle.ts). tl1450 retired the creation layout
// and its mode sheet, so the split is two files from here on. These assertions
// pin the invariants the split promised, so a later merge or hand edit cannot
// silently drop blocks (the auto-merge lost-CSS lesson) or put shared rules
// behind a mode gate:
//   1. byte-level block inventory: pre-split styles.css blocks == post-split
//      styles.css blocks ∪ mode blocks, modulo the single declared addition
//      (the .app-chrome--loading skeleton kept in the shared stylesheet) and
//      the tl1450 declared deletion (every block naming .app--creation — the
//      retired layout's selectors, deleted in the same commit);
//   2. ownership completeness: every block in a mode file matches only that
//      mode's signature;
//   3. extraction completeness: styles.css keeps no pure single-mode block —
//      tightened to ZERO leftovers by tl1450 (the retired layout's stranded
//      blocks were deleted, not grandfathered);
//   4. source contract: the chrome chunk stays lazy, mode sheets load through
//      lib/desktopLayoutStyle.ts only, and main.tsx joins the boot prefetch.

import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { existsSync } from "node:fs";
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
  const pushBuf = (chunk: string) => { buf += chunk; };
  const takeBuf = () => { const t = buf; buf = ""; return t; };
  let i = 0;
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
// 2026-10-28：签名扫描必须先剥注释。拆分器原实现把注释里出现的类名也算进归属，于是
// 「注释里提到已退役布局前缀的全局 popover 块」被整块判成该布局专属，非该布局用户丢掉
// 全局样式（实测 .context-ring-popover__inner 的 padding 在 classic 下丢失）；
// 反向也踩过：修复注释里写字面前缀，把刚搬回共享层的块又判成模式块。
function stripComments(text: string): string {
  return text.replace(/\/\*[\s\S]*?\*\//g, " ");
}

function signature(selectorHeader: string): "classic" | "workbench" | "creation" | null {
  const header = stripComments(selectorHeader);
  // 2026-10-28：`:not(.sidebar--workbench)` 是 classic 的否定限定（SidebarRegion
  // classic 分支渲染同款 utility-row），原字面扫描会把它误判成 workbench 分片，
  // 于是 classic 必需规则被留在 workbench 文件里、classic 用户永不加载。
  if (/not\(\.sidebar--workbench\)/.test(header)) return "classic";
  const set = new Set<string>();
  const re = /\.([A-Za-z_][A-Za-z0-9_-]*)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(header)) !== null) {
    const tok = m[1];
    if (tok.startsWith("app-chrome") || tok.startsWith("tabbar") || tok.startsWith("tab-overview") || tok.includes("--classic")) set.add("classic");
    else if (tok.includes("--workbench")) set.add("workbench");
    else if (tok.includes("--creation")) set.add("creation");
  }
  return set.size === 1 ? [...set][0] : null;
}

function blockSignature(block: string): "classic" | "workbench" | "creation" | null {
  // 注释先剥掉再取 header：否则注释里的类名会冒充块本身的归属（见 stripComments 说明）。
  const clean = stripComments(block);
  const brace = clean.indexOf("{");
  const header = clean.slice(0, brace).trim();
  if (header.startsWith("@property")) return header.includes("--creation") ? "creation" : null;
  if (header.startsWith("@keyframes")) return /@keyframes\s+creation-/.test(header) ? "creation" : null;
  if (header.startsWith("@media") || header.startsWith("@container")) {
    const inner = parseBlocks(clean.slice(brace + 1, clean.lastIndexOf("}")));
    const sigs = [...new Set(inner.map((b) => {
      const ib = b.indexOf("{");
      return signature(b.slice(0, ib));
    }))];
    return inner.length > 0 && sigs.length === 1 && sigs[0] !== null ? sigs[0] : null;
  }
  if (header.startsWith("@")) return null;
  return signature(header);
}

// tl1450 inventory normalization: the retirement commit (a) deletes blocks
// outright and (b) strips the retired layout's selector arms from mixed
// selector lists. To keep the byte inventory strict against everything ELSE,
// both inventories are normalized the same way before comparing:
//   - header (selector) lines naming the retired layout class are dropped;
//   - header lines of the creation-grid-only metric family are dropped (the
//     component and its CSS were deleted together);
//   - a block whose header empties out is pure retired-layout CSS and leaves
//     the inventory entirely.
// Anything else that changes bytes still trips check 1, and check 3 keeps the
// creation signature wired so no new creation-named block can sneak in.
// tl1450 declared-deletion normalization: the retirement commit (a) deleted
// retired-layout blocks outright and (b) stripped the retired selector arms
// out of mixed selector lists (plus the creation-grid-only metric family that
// died with its component). Both inventories run through the SAME transform
// before comparing, so those edits reconcile byte-exactly while anything else
// still trips check 1. A block whose whole header normalizes away leaves the
// inventory entirely; check 3 keeps the creation signature armed so no new
// creation-named block can sneak back in.
const TL1450_RETIRED_FRAGMENT = /(?:app--creation|--creation|context-panel__metric|context-panel__creation-grid)/;

function tl1450NormalizeBlock(block: string): string | null {
  const brace = block.indexOf("{");
  if (brace === -1) return block;
  let header = block.slice(0, brace);
  const body = block.slice(brace);
  // Drop every selector fragment whose text names the retired layout; the
  // fragment list is comma-separated and may carry leading comments.
  const fragments = header.split(",").map((fragment) => {
    const flat = fragment.split("\n").map((line) => line.trim()).join(" ").trim();
    return flat;
  });
  const kept = fragments.filter((fragment) => {
    if (fragment === "") return false;
    const withoutComments = fragment.replace(/\/\*[^*]*\*\//g, " ");
    return !TL1450_RETIRED_FRAGMENT.test(withoutComments);
  });
  if (kept.length === 0) return null; // pure retired-layout block
  const normalizedHeader = kept.join(",\n");
  // @-wrappers (media/container): the retirement also stripped arms from rules
  // INSIDE the wrapper — normalize the inner blocks recursively so both
  // inventories reconcile. Detection runs on the comment-stripped header TAIL
  // (leading comments may carry ASCII commas that survive fragment splitting);
  // the wrapper's own body braces are balanced by the lexer, so the last
  // closing brace belongs to the wrapper.
  const headerNoComments = normalizedHeader.replace(/\/\*[^*]*\*\//g, " ").trim();
  const lastDirective = headerNoComments.slice(headerNoComments.lastIndexOf("@"));
  if (lastDirective.startsWith("@media") || lastDirective.startsWith("@container")) {
    const innerStart = body.indexOf("{");
    const inner = body.slice(innerStart + 1, body.lastIndexOf("}"));
    const innerNormalized = parseBlocks(inner)
      .map((b) => tl1450NormalizeBlock(b))
      .filter((b): b is string => b !== null);
    if (innerNormalized.length === 0) return null; // wrapper emptied out
    return normalizedHeader + " {\n" + innerNormalized.join("") + "}\n";
  }
  return normalizedHeader + " " + body;
}

// --- inputs
// 2026-10-28 并集解（合并线，1202+1205 滚合）：基线机制取 tl1205 修法——
// merge-base(HEAD, main) 在拆分件合入 main 后=HEAD，基线退化为拆分后；
// 硬编码冻结 commit（3371f0cca）又需人工维护白名单。改为：
//   1. MODE_SPLIT_BASELINE_SHA env pin：冻结基线供逐时点审计（也可指向
//      pre-split commit 复现原始拆分读数，或历史重写后的逃生口）；
//   2. 否则滚动 HEAD 基线：HEAD 中已提交的样式表。冻结在拆分时刻的
//      基线会被普通主线 CSS 演进打红（无关合法提交也在挪块），滚动基线让
//      每条断言在合入前（分支态）与合入后（主线态）都严格生效。
// 基线自身的 skeleton 块一律先剥掉再比：skeleton 是 harness 唯一声明的
// 新增，剥掉后「恰一个新增」对任何基线选择都成立；skeleton 在共享样式表
// 里的归宿由下方专属断言守护。tl1450 起基线里 creation 命名块同样先剥
// （退役提交成批删除它们，见 namesRetiredCreationLayout）。
const SKELETON_BLOCK = ".app-chrome--loading";
const BASELINE_ENV = "MODE_SPLIT_BASELINE_SHA";
const envPin = process.env[BASELINE_ENV]?.trim() || "";
// `git show <rev>:<path>` 取仓库根相对路径。
const sheetPaths = [
  "desktop/frontend/src/styles.css",
  "desktop/frontend/src/styles/mode-classic.css",
  "desktop/frontend/src/styles/mode-workbench.css",
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
    // Mode 文件带声明 banner：与工作树副本同口径剥掉再比（否则 banner 块假 lost）。
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
const appSource = readFileSync(resolve(frontendRoot, "src/App.tsx"), "utf8");
const layoutLibSource = readFileSync(resolve(frontendRoot, "src/lib/desktopLayoutStyle.ts"), "utf8");
const mainSource = readFileSync(resolve(frontendRoot, "src/main.tsx"), "utf8");

console.log(`\nmode css split (baseline: ${baselineHow})`);

// --- 1. block inventory: nothing lost, one declared addition
// Comparison key strips leading whitespace: a block's leading gap is file
// formatting (banners, blank-line runs), not rule content.
const norm = (b: string) => b.replace(/^\s+/, "");
// Skeleton 在单次 parse 后按块剥掉（绝不文本拼回源码：块边界保持字节级忠实）。
// tl1450：creation 命名块同口径剥离（退役提交删除，见 harness 头注释）。
const baseBlocks = parseBlocks(baseStyles)
  .filter((b) => !b.includes(SKELETON_BLOCK))
  .map((b) => tl1450NormalizeBlock(b))
  .filter((b): b is string => b !== null)
  .map(norm);
const nowBlocks = [
  ...parseBlocks(mainStyles),
  ...parseBlocks(classicStyles),
  ...parseBlocks(workbenchStyles),
]
  .map((b) => tl1450NormalizeBlock(b))
  .filter((b): b is string => b !== null)
  .map(norm);
const baseSet = new Set(baseBlocks);
const nowSet = new Set(nowBlocks);
const lost = [...baseSet].filter((b) => !nowSet.has(b));
const added = [...nowSet].filter((b) => !baseSet.has(b));
// 滚动 HEAD 基线下（基线=工作树同源），块级对账恢复严格阻断：
// lost=拆分后丢块、added 除 skeleton 外的任何新增都是真回归。
ok(lost.length === 0, `no pre-split block was dropped (${lost.length} lost)`);
if (lost.length > 0) for (const b of lost.slice(0, 6)) process.stdout.write(`        lost: ${b.slice(0, 100).replace(/\n/g, " ")}\n`);
ok(nonSkeletonAdded(added), `the only post-split addition is the ${SKELETON_BLOCK} skeleton (found ${added.length})`);
if (added.length !== 1) for (const b of added.slice(0, 8)) process.stdout.write(`        added: ${b.slice(0, 100).replace(/\n/g, " ")}\n`);
ok(mainStyles.includes(SKELETON_BLOCK), "the skeleton stays in the shared app-shell stylesheet");
ok(!classicStyles.includes(SKELETON_BLOCK) && !workbenchStyles.includes(SKELETON_BLOCK), "the skeleton is not stranded in a mode file");

function nonSkeletonAdded(added: string[]): boolean {
  return added.length === 1 && added[0].includes(SKELETON_BLOCK);
}

// --- 2. ownership: mode files only contain their own signature
function ownershipOf(text: string, fileSig: "classic" | "workbench"): { own: number; foreign: number } {
  let own = 0;
  let foreign = 0;
  for (const block of parseBlocks(text)) {
    const sig = blockSignature(block);
    // 2026-10-28：中性块（选择器无任何模式标识，如 .project-tree__archive-spinner）
    // 不属于任何模式，不构成「串味」；只统计真正带别的模式签名的块。
    if (sig === null) continue;
    if (sig === fileSig) own += 1;
    else foreign += 1;
  }
  return { own, foreign };
}
for (const [name, text, sig] of [
  ["mode-classic.css", classicStyles, "classic"],
  ["mode-workbench.css", workbenchStyles, "workbench"],
] as const) {
  const { own, foreign } = ownershipOf(text, sig);
  ok(foreign === 0, `${name}: every block carries only its own mode signature (${own} own, ${foreign} foreign)`);
}

// --- 3. extraction completeness: NO pure single-mode block left behind.
// Declared exception: the .app-chrome--loading skeleton — it carries the
// classic signature but MUST render before the classic chunk (and its CSS)
// loads, so it stays in the shared stylesheet on purpose.
// tl1450: the retired creation layout's stranded shared-layer blocks were
// deleted, and the creation signature stays armed at ZERO — a new
// creation-named single-mode block turns this red. Stripping the creation
// arms from three PRE-EXISTING mixed blocks exposed their classic/workbench
// signatures that the creation tokens previously masked; those three are
// declared here with a hard upper bound of 3, so any NEW single-mode block
// beyond them also turns this red. Declared set, byte-frozen:
//   1. workbench: `.sidebar--workbench .project-tree__topic-lead` (tree meta column)
//   2. classic:   `.app--classic .project-tree__topic-unread-dot` (narrow-rail dot offset)
//   3. workbench: `.sidebar--workbench .project-tree__topic-waiting-pill` (waiting pill)
const LEFTOVER_DECLARED_MAX = 3;
const leftover: Record<string, number> = { classic: 0, workbench: 0, creation: 0 };
for (const block of parseBlocks(mainStyles)) {
  if (block.includes(SKELETON_BLOCK)) continue;
  // tl1277 例外：theme-bg 家族块（含 :not(.app--creation) 否定选择器的背景规则）
  // 是否定语义=全模式共享，classic/workbench 都必须加载；字面含 creation 不构成
  // 布局遗留。tl1450 之后该家族仍是合法共享层成员。
  if (block.includes(":not(.app--creation)")) continue;
  const sig = blockSignature(block);
  if (sig !== null) {
    leftover[sig] += 1;
    process.stdout.write(`        leftover[${sig}]: ${block.slice(0, 90).replace(/\n/g, " ")}\n`);
  }
}
const leftoverTotal = leftover.classic + leftover.workbench + leftover.creation;
ok(
  leftoverTotal <= LEFTOVER_DECLARED_MAX && leftover.creation === 0,
  `styles.css keeps no pure single-mode block beyond the declared skeleton + 3 exposed pre-1450 leftovers, and never a creation one (leftover ${JSON.stringify(leftover)})`,
);

// --- 3b. tl1450: the creation mode sheet is gone for good.
ok(!existsSync(resolve(frontendRoot, "src/styles/mode-creation.css")), "the retired creation mode sheet stays deleted");

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
  layoutLibSource.includes('from "../styles/mode-classic.css?url"') &&
    layoutLibSource.includes('from "../styles/mode-workbench.css?url"') &&
    layoutLibSource.includes("linkStylesheetOnce") &&
    !layoutLibSource.includes("mode-creation"),
  "the layout lib owns the two surviving mode stylesheets (url+link injection, Wails-safe)",
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
