#!/usr/bin/env node
// check-reachability-freeze.mjs — tl1826 C-2 冻结门禁（tl1861，A4-D 可达性门禁落地）。
//
// 冻结面 = desktop/frontend/src 生产 .ts/.tsx 中「不在生产入口可达闭包内」的文件
//（104 文件 / 12293 行，登记于同目录 reachability-baseline.json）。C-2 拍板后该树
// 停止上游同步，本门禁防两类新增（先红后清，先红后登记）：
//   ① 新增不可达文件：闭包外出现 baseline 外的新文件 → 红（删掉、接线，或经人工
//      review 后登记进 baseline——baseline 条目只减不增，登记是显式决策）。
//   ② 冻结面新增行：baseline 内文件仍在闭包外且行数超过登记值 → 红（冻结树不再
//      接收同步改动；减行绿，文件被删/接线后条目悬空走退役盘点）。
//
// 边提取复用 F1-1 修正版结论（docs/reasonix/report/slop审计-20261125/reachability-check.py，
// 两坑已内置）：跨行 import 必须锚定 `from "x"` / `import "x"` 引号本身而非单行正则；
// Vite 资源查询后缀（`?worker&inline` 等）必须剥离，否则活文件假不可达。
// 入口集合显式配置（见 ENTRY_SPECS），新增入口须同步改本脚本 + baseline `_meta.scope`。
//
// 用法：node scripts/check-reachability-freeze.mjs [--baseline=<path>]
//   exit 0 = 绿；exit 1 = 有违规；exit 2 = 基线/环境不可用。

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const SRC_DIR = resolve(SCRIPT_DIR, "..", "src");
// 入口显式（缺一不可；web/main.tsx 若不存在按实际缺失跳过并在汇总标注）。
const ENTRY_SPECS = ["main.tsx", "web/main.tsx"];
const DEFAULT_BASELINE = join(SCRIPT_DIR, "reachability-baseline.json");
const EXCLUDED_DIRS = new Set(["__tests__", "test-support", "node_modules", "wailsjs"]);
const EXTS = [".ts", ".tsx", ".js", ".jsx", ".mjs"];

// 锚定引号本身 ⇒ 天然跨行安全（单行 `[^\n]*?` 正则实测漏 23 处跨行 import）。
const RE_FROM = /\bfrom\s*["']([^"']+)["']/g;
const RE_BARE = /\bimport\s*["']([^"']+)["']/g;
const RE_DYN = /\bimport\s*\(\s*["']([^"']+)["']\s*\)/g;
const RE_REQ = /\brequire\s*\(\s*["']([^"']+)["']\s*\)/g;

const EXIT_OK = 0;
const EXIT_RED = 1;
const EXIT_INTERNAL = 2;

function collectProdFiles() {
  const out = [];
  const walk = (dir) => {
    for (const name of readdirSync(dir)) {
      const full = join(dir, name);
      if (statSync(full).isDirectory()) {
        if (!EXCLUDED_DIRS.has(name)) walk(full);
        continue;
      }
      if ((name.endsWith(".ts") || name.endsWith(".tsx")) && !name.endsWith(".d.ts")) out.push(full);
    }
  };
  walk(SRC_DIR);
  return out;
}

// 相对 spec → 生产集内真实文件（剥离 Vite `?query`；.js 写法按 TS 允许剥扩展名）。
function resolveSpec(baseFile, spec, fileSet) {
  const clean = spec.split("?", 1)[0];
  if (!clean.startsWith(".")) return null;
  const target = join(dirname(baseFile), clean);
  const candidates = [target];
  for (const ext of EXTS) candidates.push(target + ext);
  for (const ext of EXTS) candidates.push(join(target, "index" + ext));
  const noJs = target.replace(/\.js$/, "");
  candidates.push(noJs, noJs + ".ts", noJs + ".tsx");
  for (const candidate of candidates) {
    if (fileSet.has(candidate)) return candidate;
  }
  return null;
}

// Python `sum(1 for _ in open(f))` 同语义：空文件 0 行，结尾换行不虚增。
function countLines(text) {
  if (text === "") return 0;
  const lines = text.split("\n").length;
  return text.endsWith("\n") ? lines - 1 : lines;
}

function reachableClosure(files) {
  const fileSet = new Set(files);
  const edges = new Map(files.map((f) => [f, new Set()]));
  for (const file of files) {
    const text = readFileSync(file, "utf8");
    const specs = new Set();
    for (const rx of [RE_FROM, RE_BARE, RE_DYN, RE_REQ]) {
      rx.lastIndex = 0;
      for (const match of text.matchAll(rx)) specs.add(match[1]);
    }
    for (const spec of specs) {
      const target = resolveSpec(file, spec, fileSet);
      if (target) edges.get(file).add(target);
    }
  }
  const reachable = new Set();
  const stack = [];
  for (const spec of ENTRY_SPECS) {
    const entry = resolve(SRC_DIR, spec);
    if (fileSet.has(entry)) stack.push(entry);
  }
  while (stack.length) {
    const current = stack.pop();
    if (reachable.has(current)) continue;
    reachable.add(current);
    for (const next of edges.get(current) ?? []) stack.push(next);
  }
  return reachable;
}

function main(argv) {
  const getArg = (name, fallback) => {
    const hit = argv.find((a) => a === name || a.startsWith(`${name}=`));
    if (!hit) return fallback;
    const eq = hit.indexOf("=");
    return eq === -1 ? true : hit.slice(eq + 1);
  };
  const baselineArg = getArg("--baseline", DEFAULT_BASELINE);
  const baselinePath = isAbsolute(baselineArg) ? baselineArg : resolve(process.cwd(), baselineArg);
  if (!existsSync(SRC_DIR)) {
    console.error(`check-reachability-freeze: src 目录不存在 ${SRC_DIR}`);
    return EXIT_INTERNAL;
  }
  if (!existsSync(baselinePath)) {
    console.error(`check-reachability-freeze: 冻结基线缺失 ${baselinePath}——门禁无基线不运行（防静默假绿）`);
    return EXIT_INTERNAL;
  }
  const baseline = JSON.parse(readFileSync(baselinePath, "utf8"));
  const baselineEntries = baseline.entries ?? {};
  if (!baseline._meta || Object.keys(baselineEntries).length === 0) {
    console.error("check-reachability-freeze: baseline 结构非法（缺 _meta 或 entries 为空）");
    return EXIT_INTERNAL;
  }

  const files = collectProdFiles();
  const reachable = reachableClosure(files);
  const unreachable = files.filter((f) => !reachable.has(f));
  const unreachableSet = new Set(unreachable);
  const rel = (f) => f.slice(SRC_DIR.length + 1).replaceAll("\\", "/");

  const newUnreachable = unreachable.filter((f) => !(rel(f) in baselineEntries));
  const grew = [];
  const wired = [];
  const dangling = [];
  const shrank = [];
  for (const [file, frozenLines] of Object.entries(baselineEntries)) {
    const full = join(SRC_DIR, file);
    if (!existsSync(full)) {
      dangling.push(file);
      continue;
    }
    if (reachable.has(full)) {
      wired.push(file);
      continue;
    }
    const current = countLines(readFileSync(full, "utf8"));
    if (current > frozenLines) grew.push({ file, frozenLines, current });
    else if (current < frozenLines) shrank.push({ file, frozenLines, current });
  }

  console.log(`[reachability-freeze] 生产文件 ${files.length}｜可达 ${reachable.size}｜闭包外 ${unreachable.length}（baseline ${Object.keys(baselineEntries).length}）`);
  console.log(`[reachability-freeze] 入口=${ENTRY_SPECS.join("+")}｜基线=${baseline._meta.commit}（${baseline._meta.generated}，${baseline._meta.files} 文件/${baseline._meta.lines} 行）`);

  if (newUnreachable.length > 0) {
    for (const f of newUnreachable) console.error(`  红·新增不可达文件（不在冻结基线）：${rel(f)}——先删或接线；确属应冻结构事实则人工 review 后登记 baseline（条目只减不增）`);
  }
  if (grew.length > 0) {
    for (const g of grew) console.error(`  红·冻结面新增行：${g.file} ${g.frozenLines}→${g.current}（+${g.current - g.frozenLines}）——C-2 冻结树不再接收同步改动，先红后清`);
  }
  if (wired.length > 0) console.log(`  绿·已接线（条目可退役）：${wired.length} 文件——gate-metadata-audit --source=reachability 盘点后人工删行`);
  if (dangling.length > 0) console.log(`  绿·文件已删（条目可退役）：${dangling.length} 文件`);
  if (shrank.length > 0) console.log(`  绿·冻结面减行：${shrank.length} 文件（C-3 手术进度按此累积）`);

  if (newUnreachable.length > 0 || grew.length > 0) {
    console.error(`[reachability-freeze] ${newUnreachable.length + grew.length} 项违规（新增不可达 ${newUnreachable.length}+冻结面新增行 ${grew.length}）——红`);
    return EXIT_RED;
  }
  console.log("[reachability-freeze] 无新增不可达文件、冻结面零新增行——绿");
  return EXIT_OK;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv.slice(2));
}

export { countLines, reachableClosure, resolveSpec };
