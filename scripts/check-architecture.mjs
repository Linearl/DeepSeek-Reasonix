#!/usr/bin/env node
// 结构治理门禁（任务 392）：消费根目录 architecture-policy.json 的模块注册表，
// 检查依赖方向 / testutil 隔离 / internal 禁环 / 禁深导入。存量违规登记在
// architecture-baseline.json（指纹 sha256），本脚本只报基线外的新增违规；
// 基线没有自动刷新通道，更新只能人工核对后编辑该文件。
//
// 分工（互不重叠）：单文件行数/复杂度/Go 粗分层归 tools/repolint；fork 合并
// 完整性归 scripts/check-fork-integrity.mjs；前端层内 AST 契约归
// desktop/frontend/scripts/check-app-layers.mjs；打包体积预算归
// desktop/frontend/scripts/check-bundle-budget.mjs。本脚本只管模块级结构。
//
// 用法：
//   node scripts/check-architecture.mjs                  全量扫（基线过滤；CI/make lint 同位）
//   node scripts/check-architecture.mjs --changed        增量：改动文件 + 直接反向依赖（一层，不递归）
//   node scripts/check-architecture.mjs --base <ref>     增量对照基线改为 git ref（默认 HEAD）
//   node scripts/check-architecture.mjs --context <id>   打印模块受控上下文（改码前先拉）
//   node scripts/check-architecture.mjs --strict         全量审计（忽略基线，不进门禁）
//   node scripts/check-architecture.mjs --self-test      规则引擎自检（node --test 单测）

import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { execFileSync, spawnSync } from "node:child_process";

const SCRIPT_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const DEFAULT_POLICY = "architecture-policy.json";
const DEFAULT_BASELINE = "architecture-baseline.json";
const SKIP_DIRS = new Set([".git", "node_modules", "vendor", "testdata", "dist", "bin", "wailsjs", "third_party", ".pnpm-store", "coverage"]);
const TS_SKIP_SEGMENTS = new Set(["__tests__", "test-support", "__fixtures__"]);
const TS_EXT_CANDIDATES = ["", ".ts", ".tsx", "/index.ts", "/index.tsx"];

// ── 注册表与基线 ────────────────────────────────────────────────

export function loadPolicy(root = SCRIPT_ROOT) {
  return JSON.parse(readFileSync(join(root, DEFAULT_POLICY), "utf8"));
}

export function loadBaseline(root = SCRIPT_ROOT) {
  const path = join(root, DEFAULT_BASELINE);
  if (!existsSync(path)) return { violations: {} };
  return JSON.parse(readFileSync(path, "utf8"));
}

// 指纹 = sha256(rule \0 file \0 target)。同类违规换个落点就是新指纹，躲不过基线。
export function fingerprint(finding) {
  return createHash("sha256").update(`${finding.rule}\u0000${finding.file}\u0000${finding.target}`).digest("hex");
}

function patternToRegExp(pattern) {
  const escaped = pattern.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*\*/g, "\u0000").replace(/\*/g, "[^/]*").replace(/\u0000/g, ".*");
  return new RegExp(`^${escaped}(/|$)`);
}

// 路径 → 模块。roots 取最长前缀（文件级 root 也支持），patterns 逐个匹配。
export function moduleForPath(rel, policy) {
  let best = null;
  for (const mod of policy.modules) {
    for (const root of mod.roots ?? []) {
      if ((rel === root || rel.startsWith(root + "/")) && (!best || root.length > best.root.length)) {
        best = { mod, root };
      }
    }
    for (const pattern of mod.patterns ?? []) {
      if (patternToRegExp(pattern).test(rel)) return mod;
    }
  }
  return best?.mod ?? null;
}

function moduleById(policy, id) {
  return policy.modules.find((mod) => mod.id === id) ?? null;
}

// ── 源码收集与依赖图 ────────────────────────────────────────────

// 只收集本脚本解析得了的源码扩展名（.go/.ts/.tsx/.js/.jsx）。仓库里的构建
// 产物（如 desktop/build/ 下数 GB 的 .exe/.wav）一旦被整体读进堆，4GB/8GB
// 堆都会被击穿（613：--changed 生产路径 OOM / AppHang 元凶）；其余扩展名
// 本来就没有任何下游消费，在收集期直接排除，扫描内存天然有界。
const SOURCE_FILE_RE = /\.(?:go|[jt]sx?)$/;

export function isSourceFile(name) {
  return SOURCE_FILE_RE.test(name);
}

function walkFiles(root) {
  const out = [];
  const visit = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.name.startsWith(".git") || SKIP_DIRS.has(entry.name)) continue;
      const path = join(dir, entry.name);
      if (entry.isDirectory()) visit(path);
      else if (isSourceFile(entry.name)) out.push(path);
    }
  };
  visit(root);
  return out;
}

function goImports(src) {
  const out = [];
  const block = src.match(/import\s*\(([^)]*)\)/s);
  if (block) for (const m of block[1].matchAll(/"([^"]+)"/g)) out.push(m[1]);
  for (const m of src.matchAll(/^import\s+(?:[.\w]+\s+)?"([^"]+)"/gm)) out.push(m[1]);
  return out;
}

const TS_SPEC_RES = [
  /(?:^|\n)[ \t]*(?:import|export)[ \t][^\n;]*?\bfrom[ \t]*["']([^"']+)["']/g,
  /(?:^|\n)[ \t]*import[ \t]*["']([^"']+)["']/g,
  /\bimport\([ \t]*["']([^"']+)["'][ \t]*\)/g,
  /\brequire\([ \t]*["']([^"']+)["'][ \t]*\)/g,
];

function tsSpecifiers(src) {
  const out = [];
  for (const re of TS_SPEC_RES) {
    for (const m of src.matchAll(re)) {
      const lineStart = src.lastIndexOf("\n", m.index) + 1;
      let lineEnd = src.indexOf("\n", m.index);
      if (lineEnd === -1) lineEnd = src.length;
      const line = src.slice(lineStart, lineEnd);
      if (/^[ \t]*(?:import|export)\s+type\b/.test(line)) continue; // 纯类型导入不构成运行时边
      out.push(m[1]);
    }
  }
  return out;
}

function isProductionTs(rel) {
  const segments = rel.split("/");
  if (segments.some((s) => TS_SKIP_SEGMENTS.has(s))) return false;
  const base = segments.at(-1);
  return !/\.(?:test|spec)\.[jt]sx?$/.test(base) && !base.endsWith(".d.ts");
}

// 解析相对导入到仓内 TS 文件。known = 全仓已收集的 .ts/.tsx 集合，
// 纯内存命中，避免 Windows 下逐候选 existsSync 的 I/O 风暴。
export function resolveTsTarget(root, fromRel, specifier, known) {
  if (!specifier.startsWith(".")) return null;
  if (/\.(?:css|svg|png|webp|woff2?|json)(?:\?.*)?$/.test(specifier)) return null;
  const base = relative(root, resolve(root, dirname(fromRel), specifier.split("?")[0])).replaceAll("\\", "/");
  for (const ext of TS_EXT_CANDIDATES) {
    if (ext === "" && !base.endsWith(".ts") && !base.endsWith(".tsx")) continue;
    if (known.has(base + ext)) return base + ext;
  }
  return null;
}

// 并发读源码：Windows 下逐文件同步读会被实时扫描的逐次延迟拖到分钟级，
// 64 路并发把墙钟压到秒级；读失败的文件（竞态删除）直接跳过。
async function readAllSources(entries) {
  const out = new Map();
  const queue = [...entries];
  const worker = async () => {
    while (queue.length) {
      const { abs, rel } = queue.pop();
      try {
        out.set(rel, await readFile(abs, "utf8"));
      } catch {
        // 文件在扫描间隙消失：跳过，下一轮自然收敛。
      }
    }
  };
  await Promise.all(Array.from({ length: 64 }, worker));
  return out;
}

// 全仓单遍扫描：Go 按包聚合生产导入，TS 按文件聚合解析后的相对边。
export async function scanGraph(root = SCRIPT_ROOT) {
  const go = new Map(); // pkg -> { files, imports: Set<"reasonix/..."> }
  const tsFiles = new Map(); // rel -> Set<resolved rel>
  const all = walkFiles(root).map((abs) => ({ abs, rel: relative(root, abs).replaceAll("\\", "/") }));
  const sources = await readAllSources(all);
  const knownTs = new Set(all.filter((f) => sources.has(f.rel) && /\.[jt]sx?$/.test(f.rel) && isProductionTs(f.rel)).map((f) => f.rel));
  for (const { rel } of all) {
    const src = sources.get(rel);
    if (src === undefined) continue;
    if (rel.endsWith(".go")) {
      if (/\.root_bak\.go$|\.bak\.go$/.test(rel) || rel.includes("_generated.")) continue;
      if (/^\/\/ Code generated .* DO NOT EDIT\.$/m.test(src.split("\n", 12).join("\n"))) continue;
      const pkg = dirname(rel).replaceAll("\\", "/");
      const entry = go.get(pkg) ?? { files: [], imports: new Set() };
      entry.files.push(rel);
      if (!rel.endsWith("_test.go")) for (const path of goImports(src)) entry.imports.add(path);
      go.set(pkg, entry);
    } else if (/\.[jt]sx?$/.test(rel) && isProductionTs(rel)) {
      const imports = new Set();
      for (const spec of tsSpecifiers(src)) {
        const target = resolveTsTarget(root, rel, spec, knownTs);
        if (target) imports.add(target);
      }
      tsFiles.set(rel, imports);
    }
  }
  const goImporters = new Map(); // pkg -> Set<importer pkg>（生产边，供禁环与反向闭包）
  for (const [pkg, entry] of go) {
    for (const path of entry.imports) {
      if (!path.startsWith("reasonix/")) continue;
      const dep = path.slice("reasonix/".length);
      if (!go.has(dep)) continue;
      if (!goImporters.has(dep)) goImporters.set(dep, new Set());
      goImporters.get(dep).add(pkg);
    }
  }
  const tsImporters = new Map();
  for (const [rel, imports] of tsFiles) {
    for (const target of imports) {
      if (!tsImporters.has(target)) tsImporters.set(target, new Set());
      tsImporters.get(target).add(rel);
    }
  }
  return { go, tsFiles, goImporters, tsImporters };
}

// ── 规则 ────────────────────────────────────────────────────────

function underRoots(rel, roots) {
  return roots.some((root) => rel === root || rel.startsWith(root + "/"));
}

function matchesAnyPattern(rel, patterns) {
  return (patterns ?? []).some((p) => patternToRegExp(p).test(rel));
}

function tsModuleOf(rel, policy) {
  const mod = moduleForPath(rel, policy);
  return mod?.kind === "ts" ? mod : null;
}

// 宿主模块只能被装配：其他生产 Go 包禁止 import reasonix/<host root>*。
function ruleHostImport(graph, policy, files) {
  const hosts = policy.modules.filter((mod) => mod.managed && mod.host).flatMap((mod) => mod.roots ?? []);
  if (!hosts.length) return [];
  const out = [];
  for (const [pkg, entry] of graph.go) {
    if (underRoots(pkg, hosts)) continue;
    for (const path of entry.imports) {
      if (!hosts.some((root) => path === `reasonix/${root}` || path.startsWith(`reasonix/${root}/`))) continue;
      for (const rel of entry.files) {
        if (files && !files.has(rel)) continue;
        out.push({ rule: "go-host-import", file: rel, target: path });
      }
    }
  }
  return out;
}

// testOnly 模块（patterns 声明）只许被 _test.go 导入；imports 已只含生产文件。
function ruleTestOnly(graph, policy, files) {
  const testMods = policy.modules.filter((mod) => mod.managed && mod.testOnly);
  if (!testMods.length) return [];
  const out = [];
  for (const mod of testMods) {
    for (const [pkg, entry] of graph.go) {
      if (matchesAnyPattern(pkg, mod.patterns)) continue; // testutil 互相引用不算
      for (const path of entry.imports) {
        if (!path.startsWith("reasonix/")) continue;
        const dep = path.slice("reasonix/".length);
        if (!matchesAnyPattern(dep, mod.patterns)) continue;
        for (const rel of entry.files) {
          if (files && !files.has(rel)) continue;
          out.push({ rule: "go-testonly", file: rel, target: path });
        }
      }
    }
  }
  return out;
}

// Tarjan SCC；roots 内生产包导入图上 size>1 的强连通分量即环。
export function findCycles(graph, roots = ["internal"]) {
  const index = new Map();
  const low = new Map();
  const onStack = new Set();
  const stack = [];
  const sccs = [];
  let counter = 0;
  const inRoots = (pkg) => roots.some((r) => pkg === r || pkg.startsWith(r + "/"));
  const strongconnect = (v) => {
    index.set(v, counter);
    low.set(v, counter);
    counter += 1;
    stack.push(v);
    onStack.add(v);
    for (const path of graph.go.get(v)?.imports ?? []) {
      if (!path.startsWith("reasonix/")) continue;
      const w = path.slice("reasonix/".length);
      if (!inRoots(w) || !graph.go.has(w)) continue;
      if (!index.has(w)) {
        strongconnect(w);
        low.set(v, Math.min(low.get(v), low.get(w)));
      } else if (onStack.has(w)) {
        low.set(v, Math.min(low.get(v), index.get(w)));
      }
    }
    if (low.get(v) === index.get(v)) {
      const scc = [];
      for (;;) {
        const w = stack.pop();
        onStack.delete(w);
        scc.push(w);
        if (w === v) break;
      }
      if (scc.length > 1) sccs.push(scc.sort());
    }
  };
  for (const pkg of graph.go.keys()) if (inRoots(pkg) && !index.has(pkg)) strongconnect(pkg);
  return sccs;
}

export function ruleCycles(graph, policy) {
  const roots = policy.cycleRule?.roots ?? ["internal"];
  return findCycles(graph, roots).map((scc) => ({
    rule: "go-cycles",
    file: scc.join(" "),
    target: scc.join(" "),
    detail: `环: ${scc.join(" -> ")} -> ${scc[0]}`,
  }));
}

// directionRules：from 里的模块不得 import forbid 里的模块（TS 侧按解析后文件归属）。
function ruleDirections(graph, policy, files) {
  const out = [];
  for (const rule of policy.directionRules ?? []) {
    const fromMods = new Set(rule.from);
    const forbidMods = new Set(rule.forbid);
    for (const [rel, imports] of graph.tsFiles) {
      if (files && !files.has(rel)) continue;
      const mod = tsModuleOf(rel, policy);
      if (!mod || !fromMods.has(mod.id)) continue;
      for (const target of imports) {
        const targetMod = tsModuleOf(target, policy);
        if (!targetMod || !forbidMods.has(targetMod.id)) continue;
        out.push({ rule: rule.id, file: rel, target });
      }
    }
  }
  return out;
}

// 禁深导入：模块声明 publicEntrypoints 后，外部只许经入口包进入，直达子包即违规。
function ruleDeepImports(graph, policy, files) {
  const out = [];
  for (const mod of policy.modules) {
    const entries = mod.publicEntrypoints;
    if (!mod.managed || mod.kind !== "go" || !entries?.length) continue;
    const inModule = (rel) => underRoots(rel, mod.roots ?? []);
    for (const [pkg, entry] of graph.go) {
      if (inModule(pkg)) continue;
      for (const path of entry.imports) {
        if (!path.startsWith("reasonix/")) continue;
        const dep = path.slice("reasonix/".length);
        if (!inModule(dep)) continue;
        if (entries.some((e) => dep === e)) continue; // 入口包本身；入口下的其他子包都是深导入
        for (const rel of entry.files) {
          if (files && !files.has(rel)) continue;
          out.push({ rule: `deep-import:${mod.id}`, file: rel, target: path });
        }
      }
    }
  }
  return out;
}

export function runEdgeRules(graph, policy, files = null) {
  return [
    ...ruleHostImport(graph, policy, files),
    ...ruleTestOnly(graph, policy, files),
    ...ruleDirections(graph, policy, files),
    ...ruleDeepImports(graph, policy, files),
  ];
}

// ── 基线过滤 ────────────────────────────────────────────────────

export function filterBaseline(findings, baseline, allFindings = findings) {
  const registered = baseline.violations ?? {};
  const seen = new Set(findings.map(fingerprint));
  const allSeen = new Set(allFindings.map(fingerprint));
  const fresh = findings.filter((f) => !registered[fingerprint(f)]);
  // 陈旧指纹只按全量结果判定：增量模式下闭包外的存量违规没被评估，不能算还清。
  const stale = Object.keys(registered).filter((fp) => !allSeen.has(fp));
  return { fresh, stale, suppressed: findings.length - fresh.length };
}

// ── --changed：改动文件 + 反向依赖闭包 ──────────────────────────

export function gitChangedFiles(root, base = "HEAD") {
  const run = (args) => execFileSync("git", args, { cwd: root, encoding: "utf8" });
  const files = new Set();
  try {
    for (const f of run(["diff", "--name-only", base]).split("\n")) files.add(f);
    for (const f of run(["ls-files", "--others", "--exclude-standard"]).split("\n")) files.add(f);
  } catch (err) {
    throw new Error(`git 变更集读取失败（--base ${base} 是否存在？）: ${err.message}`);
  }
  files.delete("");
  return files;
}

// 改动集的直接反向依赖（一层展开，不递归；613 增量语义修正）：
// - Go 按包粒度——改动文件所在包的全部文件 + 直接导入这些包的包的全部文件；
// - TS 按文件粒度——直接导入改动文件的生产文件。
// 不再传递展开（旧实现把「导入方的导入方」也收进来，改动一个底层包就把
// 半个仓拉进增量集）：边规则违规一律由「含导入语句的文件」承担，隔层的包
// 不因本次改动产生新违规，一层即为语义完备的最小集，规模天然有界。
export function expandTouched(graph, changedFiles) {
  const touched = new Set(changedFiles);
  const pkgs = new Set();
  for (const rel of changedFiles) {
    if (!rel.endsWith(".go")) continue;
    const pkg = rel.slice(0, rel.lastIndexOf("/")) || ".";
    if (graph.go.has(pkg)) pkgs.add(pkg);
  }
  for (const pkg of pkgs) {
    for (const rel of graph.go.get(pkg)?.files ?? []) touched.add(rel);
    for (const importer of graph.goImporters.get(pkg) ?? []) {
      for (const rel of graph.go.get(importer)?.files ?? []) touched.add(rel);
    }
  }
  for (const rel of changedFiles) {
    if (!graph.tsFiles.has(rel)) continue;
    for (const importer of graph.tsImporters.get(rel) ?? []) touched.add(importer);
  }
  return touched;
}

// ── --context：改码前拉模块受控上下文 ───────────────────────────

const SPEC_SKELETON = `spec-first 五步（动手前写清，评审逐条核对）：
  1. 唯一所有者 —— 这件事落在哪个模块？现有 owner 是否同意边界移动？
  2. 单一路径   —— 同一能力是否已有一条实现路径？本次是复用还是新开（为何旧路径不够）？
  3. 显式边界   —— 模块公开面（入口/导出）是否变化？方向规则是否仍然成立？
  4. 显式时序   —— 跨模块调用谁先谁后？回调/事件回到哪个模块收口？
  5. 有界上下文 —— 改动文件集合是否圈定在模块内？闭包下游是否需要同步？
设计八问（答不上来就先别写码）：改哪个模块 / 为什么是它 / 公开面动不动 /
依赖方向反不反 / 谁消费 / 谁被消费 / 失败路径归谁 / 怎么验收（可测判据）。`;

export function formatContext(policy, id) {
  const mod = moduleById(policy, id);
  if (!mod) {
    const known = policy.modules.map((m) => m.id).join("\n  ");
    return `check-architecture: 未注册模块 ${id}\n已注册：\n  ${known}`;
  }
  const lines = [
    `# ${mod.id}`,
    `归属: ${mod.owner ?? "未标注"}   managed: ${String(mod.managed ?? false)}` +
      `${mod.host ? "   host: true（只许被装配，禁止反向导入）" : ""}` +
      `${mod.testOnly ? "   testOnly: true（只许被 _test.go 导入）" : ""}`,
    `roots: ${(mod.roots ?? []).join(", ") || "-"}${mod.patterns ? `\npatterns: ${mod.patterns.join(", ")}` : ""}`,
  ];
  if (mod.notes) lines.push(`备注: ${mod.notes}`);
  const touching = (policy.directionRules ?? []).filter((r) => r.from.includes(id) || r.forbid.includes(id));
  if (touching.length) {
    lines.push("方向红线:");
    for (const r of touching) {
      const outgoing = r.from.includes(id);
      lines.push(`  [${r.id}] ${outgoing ? "本模块不得 import" : "以下模块不得 import 本模块"}: ` +
        `${(outgoing ? r.forbid : r.from).join(", ")}${r.note ? ` — ${r.note}` : ""}`);
    }
  }
  if (mod.publicEntrypoints?.length) lines.push(`公开入口: ${mod.publicEntrypoints.join(", ")}`);
  lines.push("改完自跑: node scripts/check-architecture.mjs --changed");
  lines.push(SPEC_SKELETON);
  return lines.join("\n");
}

// ── CLI ─────────────────────────────────────────────────────────

function printFindings(findings) {
  for (const f of findings) {
    console.error(`${f.file}: [${f.rule}] -> ${f.target}${f.detail ? ` (${f.detail})` : ""}`);
  }
}

function report(findings, allFindings, baseline, meta) {
  const { fresh, stale, suppressed } = filterBaseline(findings, baseline, allFindings);
  printFindings(fresh);
  if (fresh.length) {
    console.error(`\ncheck-architecture: ${fresh.length} 条新增结构违规（基线外）。修复，或经人工 review 后登记` +
      ` architecture-baseline.json（本脚本无自动刷新通道）。`);
    return 1;
  }
  console.log(`check-architecture: clean（${meta}${suppressed ? `，${suppressed} 条存量被基线吸收` : ""}）`);
  if (stale.length) {
    console.error(`check-architecture: 基线中 ${stale.length} 条指纹已无对应违规（欠账还清了）。` +
      `请人工核对后从 architecture-baseline.json 删除这些条目（只减不增）。`);
  }
  return 0;
}

async function main(argv) {
  if (argv.includes("--self-test")) {
    const result = spawnSync(process.execPath, ["--test", join(SCRIPT_ROOT, "scripts", "check-architecture.test.mjs")], { stdio: "inherit" });
    process.exitCode = result.status ?? 1;
    return;
  }
  const root = SCRIPT_ROOT;
  const policy = loadPolicy(root);
  const contextIdx = argv.indexOf("--context");
  if (contextIdx !== -1) {
    const id = argv[contextIdx + 1];
    if (!id) {
      console.error("check-architecture: --context 需要模块 id，如 --context ts/lib");
      process.exitCode = 2;
      return;
    }
    console.log(formatContext(policy, id));
    return;
  }

  const strict = argv.includes("--strict");
  const incremental = argv.includes("--changed") || argv.includes("--base");
  const baseIdx = argv.indexOf("--base");
  const base = baseIdx !== -1 ? argv[baseIdx + 1] : "HEAD";

  const graph = await scanGraph(root);
  const allFindings = [...runEdgeRules(graph, policy), ...ruleCycles(graph, policy)];
  let findings = allFindings;
  let meta = `全量 ${graph.go.size} 个 Go 包 + ${graph.tsFiles.size} 个 TS 文件`;
  if (incremental) {
    const changed = gitChangedFiles(root, base);
    const touched = expandTouched(graph, changed);
    findings = allFindings.filter((f) => f.rule === "go-cycles" || touched.has(f.file));
    meta = `增量 ${touched.size} 个文件（改动 + 直接反向依赖，一层），环检测恒为全图`;
  }

  if (strict) {
    printFindings(findings);
    process.exitCode = findings.length ? 1 : 0;
    console.error(`\ncheck-architecture: --strict 共 ${findings.length} 条（忽略基线，审计用）`);
    return;
  }
  process.exitCode = report(findings, allFindings, loadBaseline(root), meta);
}

const invokedDirectly = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (invokedDirectly) {
  // Windows 实时扫描让同步文件读退化到分钟级；libuv 线程池默认 4 路并发
  // 扛不住逐文件扫描延迟，放大到 32 路（须在启动前设置，故只 re-exec 一次）。
  if (process.platform === "win32" && !process.env.UV_THREADPOOL_SIZE && !process.env.ARCHCHECK_REEXEC) {
    const relaunch = spawnSync(process.execPath, process.argv.slice(1), {
      stdio: "inherit",
      env: { ...process.env, UV_THREADPOOL_SIZE: "32", ARCHCHECK_REEXEC: "1" },
    });
    process.exitCode = relaunch.status ?? 1;
  } else {
    main(process.argv.slice(2)).catch((err) => {
      console.error("check-architecture:", err.message);
      process.exitCode = 2;
    });
  }
}
