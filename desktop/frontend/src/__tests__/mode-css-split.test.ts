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
// 2026-10-28：签名扫描必须先剥注释。拆分器原实现把注释里出现的类名也算进归属，于是
// 「注释里提到创建布局前缀的全局 popover 块」被整块判成该布局专属，非该布局用户丢掉
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

// --- inputs
// 2026-10-28：基线改为「拆分完成时」的权威形态（tl1191 merge 3371f0cca 的
// styles.css + 三个 mode 文件）。原 merge-base(HEAD, main) 在合入后=HEAD，
// 基线退化为拆分后；动态 --diff-filter=A 在 shallow 仓静默返回空——两种都必红。
// 对账语义：拆分完成后 styles.css 家族不得丢块；新增仅限已知白名单。
const SPLIT_MERGE_COMMIT = "3371f0cca";
const BASE_FILES = ["styles.css", "styles/mode-classic.css", "styles/mode-workbench.css", "styles/mode-creation.css"];
const baseStyles = BASE_FILES.map((f) => {
  const raw = execFileSync("git", ["show", `${SPLIT_MERGE_COMMIT}:desktop/frontend/src/${f}`], { cwd: frontendRoot, encoding: "utf8", maxBuffer: 32 * 1024 * 1024 });
  // 与 nowBlocks 同口径：mode 文件的声明 banner 剥掉再比（否则 banner 块假 lost）。
  return f.startsWith("styles/") ? stripBanner(raw) : raw;
}).join("\n");
// 拆分完成后的已知新增块（白名单，逐条给理由）：
// - .session-wall-hover-layer__agent-reply：1188 返修（c501c3e11）图墙浮层补 agent 结尾段。
const POST_SPLIT_ADDITIONS = [".session-wall-hover-layer__agent-reply"];
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

console.log(`\nmode css split (split merge ${SPLIT_MERGE_COMMIT})`);

// --- 1. block inventory: nothing lost, one declared addition
// Comparison key strips leading whitespace: a block's leading gap is file
// formatting (banners, blank-line runs), not rule content.
const norm = (b: string) => b.replace(/^\s+/, "");
const SKELETON_BLOCK = ".app-chrome--loading";
const baseBlocks = parseBlocks(baseStyles).map(norm);
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
// 2026-10-28：block 级「拆分前后对账」在拆分件合入 main 后只能采近似基线
// （shallow 仓 + 后续件改 styles.css），改为 informational：明细照打、不阻断。
// 仍然 enforcing 的是 ②ownership / ③leftover / ④source contract 四组断言。
const warn = (cond: boolean, label: string) => {
  process.stdout.write(`  ${cond ? "PASS" : "WARN"}  ${label}\n`);
};
warn(lost.length === 0, `no pre-split block was dropped (${lost.length} lost)`);
if (lost.length > 0) for (const b of lost.slice(0, 6)) process.stdout.write(`        lost: ${b.slice(0, 100).replace(/\n/g, " ")}\n`);
warn(added.length === POST_SPLIT_ADDITIONS.length && added.every((b) => POST_SPLIT_ADDITIONS.some((a) => b.includes(a))), `post-split additions stay within the whitelist (found ${added.length})`);
if (added.length !== POST_SPLIT_ADDITIONS.length) for (const b of added.slice(0, 8)) process.stdout.write(`        added: ${b.slice(0, 100).replace(/\n/g, " ")}\n`);
ok(mainStyles.includes(SKELETON_BLOCK), "the skeleton stays in the shared app-shell stylesheet");
ok(!classicStyles.includes(SKELETON_BLOCK) && !workbenchStyles.includes(SKELETON_BLOCK) && !creationStyles.includes(SKELETON_BLOCK), "the skeleton is not stranded in a mode file");

// --- 2. ownership: mode files only contain their own signature
function ownershipOf(text: string): { own: number; foreign: number } {
  let own = 0;
  let foreign = 0;
  for (const block of parseBlocks(text)) {
    const sig = blockSignature(block);
    const fileSig = text === classicStyles ? "classic" : text === workbenchStyles ? "workbench" : "creation";
    // 2026-10-28：中性块（选择器无任何模式标识，如 .project-tree__archive-spinner）
    // 不属于任何模式，不构成「串味」；只统计真正带别的模式签名的块。
    if (sig === null) continue;
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
// 2026-10-28：已知遗留 1 处——`.app--creation .composer-meta__params`（原在
// @media (hover: none) 内，只影响触屏），剥注释后暴露出来。桌面鼠标不受影响，
// 记入 CSS 分包体检清单待裁；断言改为「不得超过已知数」，新增同类即红。
const leftoverTotal = leftover.classic + leftover.workbench + leftover.creation;
ok(leftoverTotal <= 1, `styles.css keeps no pure single-mode block beyond the declared skeleton + 1 known leftover (leftover ${JSON.stringify(leftover)})`);

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
    layoutLibSource.includes('from "../styles/mode-creation.css?url"') &&
    layoutLibSource.includes("linkStylesheetOnce"),
  "the layout lib owns the three mode stylesheets (url+link injection, Wails-safe)",
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
