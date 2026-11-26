#!/usr/bin/env node
// tl1822：文档机械扫查门禁——两类检查，机械零误报口径：
//   ① md 相对链接存在性（[text](target) 与 <img src>，核目标文件存在；md 目标顺带核锚点）
//   ② scripts|cmd|internal|tools|workers 五前缀路径引用存在性（文档引用的仓内文件路径必须真实存在）
//
// 升级自 20261126 文档时效审计的一次性脚本 tmp/doc_audit_scan.py（仓库 tmp/，未跟踪），
// 保留其已实测的解析规则（围栏跳过/外链过滤/锚点 slug/`..` 排除），见
// workspace docs/reasonix/report/仓库环境审计-Top10问题与改善行动-20261126.md P2：
// 实测口径 = 168 份 md、坏链 10、失效路径引用 6/157。
//
// 两种模式：
//   node scripts/check-docs-links.mjs          门禁模式（默认）：零误报规则 + 存量基线豁免，exit 1 = 有新增违规
//   node scripts/check-docs-links.mjs --strict 审计回放口径：还原 doc_audit_scan.py 原样行为
//       （范围仅 docs/、不跳行内代码、不排除任何目录、不豁免基线、路径引用按仓根截断匹配），
//       仅追加扩展名白名单（该白名单机械复现审计「12 原始 MISSING → 6 真失效」的人工甄别）。
//       用于复现审计读数：pre-fix(5efd3ebd91) 应 = 10 坏链 + 6 失效引用。
//
// 对审计扫查器的一处缺陷修复（升级点，实证于本次全量跑）：
//   doc_audit_scan.py 的路径引用按「五前缀起截断」取串、按仓根判存在——文档写全路径
//   `desktop/frontend/scripts/check-app-layers.mjs` 时，截断串 scripts/check-app-layers.mjs
//   在仓根不存在 → 假红。审计 6 条失效引用按全路径核实：4 条实存于 desktop/frontend/scripts/
//   （check-app-layers / check-bundle-budget / check-restore-landing-sites / tsc-test-baseline.json），
//   真失效仅 2 条（check-desktop-host-boundary.mjs 未落地、internal/autoresearch/readiness.go 已删）。
//   门禁模式向左扩展完整路径后按 [仓根, desktop/] 两候选判存在；--strict 不做此修复以保持回放口径。
//
// 排除清单（逐类理由，防误报淹没——每类都对应审计或本次全量跑实证）：
//   A. 外部 URL（http/https/mailto/data/ftp/#锚点）——离线机械扫查不做网络断言，外链健康属
//      周扫甄别职责。双模式生效。
//   B. 围栏代码块（```/~~~）——块内是示例不是文档正文（SPEC.md 的 toml 示例含已退役键名）。
//      双模式生效。
//   C. 行内代码段（`...`）内的链接/img（仅门禁模式）——审计 10 坏链中 2 条实为语法示例：
//      `@[显示名](<路径>)`（ATTACHMENT_REF_FORMAT.md §1 附件引用语法）与
//      `@[name](path)`（tl1347 根因报告描述剥离规则）。语法文档不是真链接。
//   D. docs/_archive/（仅门禁模式）——归档冻结区：README.zh-CN 已改指 _archive 为历史入口，
//      修历史文档链接=篡改存档。审计 10 坏链中 6 条在此区（MIGRATING 双语）。
//   E. release-notes/（门禁模式排除）——出包台账 + Go embed 数据源（Top10 报告 P8），逐包
//      notes 记录「当时」的文件与提交，时点语义；改写=篡改台账。
//   F. docs-site/、site/（门禁模式排除）——独立构建部署的站点栈（P7），有自有链接生态。
//   G. tmp/、handoff/（门禁模式排除）——tmp/ 临时区语义（P4：33 个 tracked 文件属清理对象）；
//      仓库根 handoff/ 为历史交接快照（P4：双处收口候拍板），引用的是交接时点的仓内状态
//      （本次全量跑实证：handoff-903/905/917 引 12 个已重构消失的文件），时点语义同 E。
//   H. internal/skill/builtincontent/（门禁模式排除）——内置技能包内容（工作区 skill_pkg 的
//      仓内副本）：模板占位链接（REQ-XXXX.md、screenshots/issue-001-*.png）与工作区侧脚本
//      引用（scripts/tl.py、scripts/req-sync.js 等），均非本仓文档主张。本次全量跑实证 52 红全出自此区。
//   I. 路径引用仅认「末段带已知扩展名」的文件形引用——MCP JSON-RPC 方法名（tools/call、
//      tools/list、tools/prompt）与 Go 符号（internal/x.Store.CreateTask、internal/boot.TestXxx）
//      不是文件路径。审计 12 条 MISSING 原始值中 6 条属此类，与「157 引用中 6 条真失效」互证。
//      双模式生效（严格模式据此机械复现审计的人工甄别结果）。
//   J. 占位示例路径（仅门禁模式）——教程/纪律条文里的假想路径：REASONIX.md 的
//      `git add internal/x/y.go`（单字符段占位）、CONTRIBUTING.md 上游教程的 mytool.go。
//   K. 存量基线（仅门禁模式）——2 条全路径核实后仍真失效的引用登记豁免，修复归 P9 归档候选
//      （候拍板）；沿用 architecture-baseline.json「存量登记、只挡新增」既有模式，新增失效即红。
//
// 漏报边界（诚实声明）：① 锚点校验用 GitHub slug 近似（CJK 保留、重复标题 -2 后缀），极端
// 写法可能漏核锚点，不会误报；② 路径引用只查五前缀 + 已知扩展名，裸目录引用与其他前缀
// （docs/、desktop/ 起头）的引用不在本门职责内；③ 大小写按文件系统语义（Windows 不区分），
// 跨平台大小写漂移不属本门；④ [仓根, desktop/] 两候选判存在，desktop/ 深层文档以模块根口吻
// 书写的路径（desktop/AGENTS.md 的 frontend/scripts/...）可命中，其余相对书写不在候选集。

import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { join, dirname, resolve, relative } from "node:path";

const ROOT = process.cwd();
const STRICT = process.argv.includes("--strict");

// ── 扫描范围 ─────────────────────────────────────────────────────────────
// 门禁模式：全仓 md，排除生成物/站点栈/临时区/归档区/技能包副本（理由见头部 D–H）。
// --strict：仅 docs/（审计原样口径）。
const SKIP_DIR_NAMES = new Set([
  ".git", "node_modules", "tmp", "temp", "dist", "build", "coverage",
  "docs-site", "site", "release-notes", "_archive", ".reasonix", ".codegraph",
  ".pw-browsers", "playwright-report", "test-results",
  "handoff", // 排除规则 G：仓库根历史交接快照（时点语义）
  "builtincontent", // 排除规则 H：内置技能包内容（工作区副本）
]);
function walk(dir, out, skipNames) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return out;
  }
  for (const name of entries) {
    if (skipNames.has(name) || name.startsWith("_recycle-")) continue;
    const full = join(dir, name);
    let st;
    try {
      st = statSync(full);
    } catch {
      continue;
    }
    if (st.isDirectory()) walk(full, out, skipNames);
    else if (name.toLowerCase().endsWith(".md")) out.push(full);
  }
  return out;
}
// strict（审计原样）只跳 .git/node_modules；门禁模式用完整排除集（含 _archive 等）。
const SKIP_DIR_NAMES_STRICT = new Set([".git", "node_modules"]);
const mds = STRICT
  ? walk(join(ROOT, "docs"), [], SKIP_DIR_NAMES_STRICT)
  : walk(ROOT, [], SKIP_DIR_NAMES);

// ── 解析规则（移植自 doc_audit_scan.py，已实测） ───────────────────────────
const mdLinkRe = /\[([^\]]*)\]\(([^)\s]+(?:\s+"[^"]*")?)\)/g;
const imgHtmlRe = /<img[^>]+src="([^"]+)"/g;
// 路径引用：门禁模式向左扩展完整路径（升级点，修审计扫查器根相对截断缺陷）；
// strict 用审计原样正则（五前缀起截断）以保持回放口径一致。
const pathRefReGate = /(?:[A-Za-z0-9_.-]+\/)*(?:scripts|cmd|internal|tools|workers)\/[A-Za-z0-9_./-]+/g;
const pathRefReStrict = /(?:scripts|cmd|internal|tools|workers)\/[A-Za-z0-9_./-]+/g;
const pathRefRe = STRICT ? pathRefReStrict : pathRefReGate;

const externalRe = /^(https?:|mailto:|data:|ftp:|#)/i;

function cleanTarget(t) {
  t = t.trim();
  const m = t.match(/^(\S+)\s+"[^"]*"$/); // [text](path "title") 剥标题
  return m ? m[1] : t;
}

function stripInlineCode(line) {
  // 行内代码段内容替换为空格（保位宽），避免 `[a](b)` 语法示例被当真链接。
  return line.replace(/`[^`]*`/g, (m) => "`" + " ".repeat(Math.max(0, m.length - 2)) + "`");
}

// GitHub 风格锚点 slug 近似：小写、去标点（保留 CJK/词字符/空格/连字符）、空格→连字符、
// 重复标题追加 -2/-3。
function ghSlug(s) {
  s = s.trim().toLowerCase();
  s = s.replace(/[^\w\u4e00-\u9fff\- ]/g, "");
  s = s.replace(/ /g, "-");
  return s;
}
function collectAnchors(content) {
  const heads = new Set();
  const seen = new Set();
  for (const line of content.split(/\r?\n/)) {
    const m = line.match(/^(#{1,6})\s+(.*)$/);
    if (!m) continue;
    let h = ghSlug(m[2]);
    const base = h;
    let n = 2;
    while (seen.has(h)) h = `${base}-${n++}`;
    seen.add(h);
    heads.add(h);
  }
  return heads;
}

// 路径引用只认「末段带已知扩展名」的文件形引用（排除规则 I）。
const PATH_FILE_EXTS = new Set([
  ".go", ".mjs", ".cjs", ".js", ".ts", ".tsx", ".jsx", ".json", ".sh", ".py",
  ".cmd", ".bat", ".ps1", ".psm1", ".toml", ".yaml", ".yml", ".md", ".mdx",
  ".txt", ".csv", ".sql", ".css", ".scss", ".html", ".htm", ".svg", ".png",
  ".jpg", ".jpeg", ".gif", ".webp", ".ico", ".pdf", ".zip", ".wasm",
]);
function looksLikeFileRef(p) {
  const lastSeg = p.slice(p.lastIndexOf("/") + 1);
  const dot = lastSeg.lastIndexOf(".");
  if (dot <= 0) return false; // 无点或以点开头（隐藏文件形态）→ 不是文件形引用
  return PATH_FILE_EXTS.has(lastSeg.slice(dot).toLowerCase());
}

// 占位示例路径（排除规则 J）：假想路径不是文档主张。
function isPlaceholderRef(p) {
  if (/(^|\/)[A-Za-z]\//.test(p)) return true; // 单字符段：internal/x/y.go（REASONIX.md git add 示例）
  if (/\/mytool(_test)?\.go$/.test(p)) return true; // CONTRIBUTING.md 上游教程示例
  return false;
}

// 存在性候选：desktop/ 是独立 Go module，其文档按模块根口吻书写路径。
function refExists(p) {
  return existsSync(join(ROOT, p)) || existsSync(join(ROOT, "desktop", p));
}

// ── 存量基线（门禁模式豁免；登记理由，沿用 architecture-baseline 模式） ──────
// 来源：审计 20261126 报 6 条失效引用，经本门全路径核实 4 条实存于 desktop/frontend/scripts/
// （扫查器根相对截断假阳性，见头部「升级点」），真失效 2 条登记如下。
// 修复动作 = P9 归档候选（候拍板），登记后新增失效引用仍即红。
const BASELINE_KNOWN_MISSING = [
  {
    path: "internal/autoresearch/readiness.go",
    reason: "autoresearch 已被 Goal 统一运行时取代；引用方为历史实现计划 docs/superpowers/plans/2026-06-29（P9 归档候选，候拍板）",
  },
  {
    path: "desktop/frontend/scripts/check-desktop-host-boundary.mjs",
    reason: "ARCHITECTURE_CONTEXT.md 自研裁定段引用的宿主边界检查脚本未落地（审计判定存量，登记待落地或文档更新）",
  },
];
const BASELINE_BY_PATH = new Map(BASELINE_KNOWN_MISSING.map((e) => [e.path, e]));

// ── 主扫查 ────────────────────────────────────────────────────────────────
const broken = []; // { file, line, target, kind, reason }
const pathRefs = new Map(); // ref -> Set(referencing md rel)
const missingRefs = []; // { ref, reason }

for (const f of mds) {
  const rel = relative(ROOT, f).split("\\").join("/");
  let lines;
  try {
    lines = readFileSync(f, "utf8").split(/\r?\n/);
  } catch (e) {
    console.error(`READ FAIL ${rel}: ${e.message}`);
    continue;
  }
  let inFence = false;
  for (let i = 0; i < lines.length; i++) {
    const raw = lines[i];
    const s = raw.trim();
    if (s.startsWith("```") || s.startsWith("~~~")) {
      inFence = !inFence;
      continue;
    }
    if (inFence) continue;

    // ① md 相对链接 / img：门禁模式剥行内代码（排除规则 C）；strict 不剥（审计原样）。
    const linkLine = STRICT ? raw : stripInlineCode(raw);
    for (const m of linkLine.matchAll(mdLinkRe)) {
      const verdict = checkLink(f, m[2]);
      if (verdict) broken.push({ file: rel, line: i + 1, target: m[2], kind: "link", reason: verdict });
    }
    for (const m of linkLine.matchAll(imgHtmlRe)) {
      const verdict = checkLink(f, m[1]);
      if (verdict) broken.push({ file: rel, line: i + 1, target: m[1], kind: "img", reason: "HTML img " + verdict });
    }

    // ② 五前缀路径引用：围栏外一律收集（行内代码里的是真引用，如 `scripts/x.mjs`）。
    for (const m of raw.matchAll(pathRefRe)) {
      const p = m[0].replace(/[./-]+$/, "");
      if (!p || p.includes("..")) continue; // doc_audit_scan.py 同款排除
      if (!looksLikeFileRef(p)) continue; // 排除规则 I
      if (!STRICT && isPlaceholderRef(p)) continue; // 排除规则 J
      if (!pathRefs.has(p)) pathRefs.set(p, new Set());
      pathRefs.get(p).add(rel);
    }
  }
}

function checkLink(srcFile, rawTarget) {
  const t = cleanTarget(rawTarget);
  if (!t || externalRe.test(t)) return null; // 排除规则 A
  if (!STRICT && /[<>]/.test(t)) return null; // <路径> 形占位符（防御性，实证均已在行内代码内）
  let pathPart = t;
  let anchor = "";
  if (t.includes("#")) {
    const idx = t.indexOf("#");
    pathPart = t.slice(0, idx);
    anchor = t.slice(idx + 1);
  }
  if (!pathPart) return null; // 同文件锚点
  let decoded = pathPart;
  try {
    decoded = decodeURIComponent(pathPart);
  } catch {
    /* 保留原样 */
  }
  // 相对链接按所在文档解析；"/"开头按仓根解析（仓库无此形态则恒不触发）。
  const resolved = decoded.startsWith("/")
    ? resolve(ROOT, "." + decoded)
    : resolve(dirname(srcFile), decoded);
  if (!existsSync(resolved)) {
    return `目标不存在: ${rawTarget} -> ${relative(ROOT, resolved).split("\\").join("/")}`;
  }
  if (anchor && resolved.toLowerCase().endsWith(".md")) {
    let content;
    try {
      content = readFileSync(resolved, "utf8");
    } catch {
      return `目标不可读: ${rawTarget}`;
    }
    const heads = collectAnchors(content);
    if (!heads.has(anchor.toLowerCase()) && !heads.has(anchor)) {
      return `锚点未命中: ${rawTarget} (目标文件存在)`;
    }
  }
  return null;
}

// 路径引用存在性：门禁模式按 [仓根, desktop/] 候选判存在（升级点）；strict 按仓根（审计原样）。
// 基线豁免仅门禁模式生效。
for (const [p, refs] of [...pathRefs.entries()].sort()) {
  const exists = STRICT ? existsSync(join(ROOT, p)) : refExists(p);
  if (exists) continue;
  const base = !STRICT ? BASELINE_BY_PATH.get(p) : undefined;
  if (base) {
    missingRefs.push({ ref: p, reason: `基线豁免: ${base.reason}` });
  } else {
    broken.push({
      file: [...refs].sort().join(", "),
      line: 0,
      target: p,
      kind: "path-ref",
      reason: `失效路径引用 (${refs.size} 处引用): 仓内不存在`,
    });
  }
}

// ── 输出与退出码 ─────────────────────────────────────────────────────────
const mode = STRICT
  ? "STRICT(审计回放口径: 仅docs/ 审计原样正则 不跳行内代码 不排除目录 不豁免基线)"
  : "GATE(门禁: 零误报规则+全路径核实+存量基线)";
console.log(`check-docs-links.mjs ${mode}`);
console.log(`md files: ${mds.length}; path refs: ${pathRefs.size}`);

if (broken.length) {
  console.log(`\n== RED (${broken.length}) ==`);
  for (const b of broken) {
    console.log(`${b.file}${b.line ? ":" + b.line : ""} [${b.kind}] ${b.target} | ${b.reason}`);
  }
}
if (missingRefs.length) {
  console.log(`\n== BASELINE/known (${missingRefs.length}) ==`);
  for (const m of missingRefs) console.log(`${m.ref} | ${m.reason}`);
}
const baselineHits = missingRefs.length;
const errors = broken.length;
console.log(`\nerrors: ${errors}; baseline-exempt: ${baselineHits}`);
if (!STRICT && errors > 0) {
  console.log("门禁红：以上为新增/未登记违规。存量失效引用须登记 BASELINE_KNOWN_MISSING（带理由），归档候选走 P9 候拍板。");
}
process.exit(errors > 0 ? 1 : 0);
