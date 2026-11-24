#!/usr/bin/env node
// gate-metadata-audit.mjs — 门禁元数据退役盘点端（tl1756，政策文档 docs/gate-metadata-retirement.md）。
//
// 盘对象（只读）：
//   ① tsc 豁免  desktop/frontend/scripts/tsc-test-baseline.json（163 文件 226 对 file+错误码）
//      深验（--with-tsc）：复用 tsc-test-gate.mjs 导出的 parseDiagnostics/classify 跑全量 tsc，
//      pair 诊断消失 = 可退役候选（强）；未跑 tsc 时只做文件存在性（悬空检测）+ 待深验标记。
//   ② fork-integrity 判例  scripts/check-fork-integrity.mjs CHECKS 数组（847 条）
//      与门禁同构的存在性+锚点核对（只读复算，不改门禁）、git blame 行级钉龄、
//      feature 标签任务号（任务/task/tl/#PR）追溯抽取。
//
// 本脚本【不是门禁】：不接 CI/Makefile（self-test 有防复发断言），exit 0 = 盘点跑通。
// 候选清单是产出物，不阻断任何流程；退役动作（改 baseline / 删 CHECKS 行）候拍板，本脚本从不写回两份元数据。
//
// 用法：
//   node scripts/gate-metadata-audit.mjs                       # 机械层全量（快：存在性+钉龄+任务号）
//   node scripts/gate-metadata-audit.mjs --with-tsc            # 附加全量 tsc 深验（慢，约 1-2 分钟）
//   node scripts/gate-metadata-audit.mjs --source=tsc|fork-integrity|all
//   node scripts/gate-metadata-audit.mjs --json=<path>         # 结构化报告落盘
//   node scripts/gate-metadata-audit.mjs --sample=15           # 人工判定抽样（fork 取最旧 N 条；tsc 取候选优先 N 条）
//   node scripts/gate-metadata-audit.mjs --repo=<path>         # 默认仓库根（本脚本上级目录）

import { spawnSync } from "node:child_process";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const DEFAULT_REPO = resolve(SCRIPT_DIR, "..");
const EXIT_OK = 0;
const EXIT_USAGE = 2;
const EXIT_INTERNAL = 3;

// ── 纯函数层（self-test 覆盖面）──────────────────────────────────

/** feature 标签 → 任务号追溯桶。识别 任务350 / task 163 / tl1485 / #9222 / 包110 五类出处。 */
export function extractRefs(label) {
  const tasks = [];
  const tls = [];
  const prs = [];
  const packages = [];
  for (const match of label.matchAll(/(?:任务|task\s*)(\d{2,5})/gi)) tasks.push(match[1]);
  for (const match of label.matchAll(/\btl[- ]?(\d{3,5})\b/gi)) tls.push(match[1]);
  for (const match of label.matchAll(/#(\d{2,5})\b/g)) prs.push(match[1]);
  for (const match of label.matchAll(/包(\d{1,4})/g)) packages.push(match[1]);
  return { tasks, tls, prs, packages };
}

export function refsSummary(label) {
  const refs = extractRefs(label);
  const parts = [];
  if (refs.tasks.length) parts.push(`任务${refs.tasks.join("/")}`);
  if (refs.tls.length) parts.push(`tl${refs.tls.join("/")}`);
  if (refs.prs.length) parts.push(`#${refs.prs.join("/")}`);
  if (refs.packages.length) parts.push(`包${refs.packages.join("/")}`);
  return parts.length ? parts.join(" ") : "无出处（未知钉龄桶）";
}

/** git blame --line-porcelain 输出 → Map<最终行号, ISO 日期>。短头（同 commit 压缩行）沿用该组已见日期；
 * 空内容行（源码空行，"\t" 后无字符）也必须结算，否则后续行号整体错位。 */
export function parseBlamePorcelain(text) {
  const lineDates = new Map();
  let currentSha = null;
  let currentTime = null;
  let pendingFinalLine = null;
  for (const raw of text.split("\n")) {
    if (raw.startsWith("\t")) {
      if (pendingFinalLine !== null && currentTime !== null) lineDates.set(pendingFinalLine, currentTime);
      pendingFinalLine = null;
      continue;
    }
    const line = raw.trimEnd();
    if (!line) continue;
    const header = line.split(" ");
    if (header.length >= 3 && /^[0-9a-f]{40}$/.test(header[0]) && /^\d+$/.test(header[1]) && /^\d+$/.test(header[2])) {
      currentSha = header[0];
      pendingFinalLine = Number(header[2]);
      continue;
    }
    if (header[0] === "author-time" && header[1]) {
      const epoch = Number(header[1]);
      if (Number.isFinite(epoch) && currentSha) currentTime = new Date(epoch * 1000).toISOString().slice(0, 10);
    }
  }
  return lineDates;
}

/** CHECKS 源码文本 → `{ feature:` 出现行号（文件序），与 CHECKS 数组序一一对应（test 断言数量相等）。 */
export function mapChecksToLines(sourceText) {
  const lines = [];
  for (const [index, line] of sourceText.split("\n").entries()) {
    if (line.includes("{ feature:") && !line.trimStart().startsWith("//")) lines.push(index + 1);
  }
  return lines;
}

export function ageDays(sinceISO, todayISO) {
  const since = Date.parse(sinceISO);
  const today = Date.parse(todayISO);
  if (!Number.isFinite(since) || !Number.isFinite(today)) return null;
  return Math.max(0, Math.round((today - since) / 86400000));
}

/**
 * tsc 豁免逐条判级。entries: {file: [code...]}; diagnostics: tsc-test-gate.parseDiagnostics 产物（null=未深验）。
 * pair 判级：file 不存在=悬空（dangling，候选·强）；深验时 seen=needed / 不在=stale（候选·强）；否则 unverified。
 */
export function judgeTscEntries(entries, diagnostics, fileExists) {
  const seen = new Set((diagnostics ?? []).map((d) => `${d.file}::${d.code}`));
  const records = [];
  for (const [file, codes] of Object.entries(entries)) {
    const exists = fileExists(file);
    const pairs = codes.map((code) => {
      let verdict = "unverified";
      if (!exists) verdict = "dangling";
      else if (diagnostics) verdict = seen.has(`${file}::${code}`) ? "needed" : "stale";
      return { code, verdict };
    });
    records.push({
      file,
      fileExists: exists,
      verdict: !exists ? "dangling-file" : pairs.every((p) => p.verdict === "needed") ? "ok"
        : pairs.some((p) => p.verdict === "stale") ? "stale-pairs" : "unverified",
      pairs,
      candidates: pairs.filter((p) => p.verdict === "dangling" || p.verdict === "stale").length,
    });
  }
  return records;
}

/** fork-integrity 判例逐条判级（与 runIntegrityChecks 同构的只读复算 + 钉龄）。 */
export function judgeForkChecks(checks, root, blameDates, entryLines, todayISO) {
  return checks.map((check, index) => {
    const path = join(root, check.file);
    const fileExists = existsSync(path);
    let missingPatterns = [];
    if (fileExists) {
      const content = readFileSync(path, "utf8").replace(/\r\n/g, "\n");
      missingPatterns = check.patterns.filter((p) => !content.includes(p));
    }
    const since = blameDates.get(entryLines[index]) ?? null;
    return {
      index,
      line: entryLines[index] ?? null,
      feature: check.feature,
      file: check.file,
      fileExists,
      missingPatterns,
      verdict: !fileExists ? "missing-file" : missingPatterns.length ? "missing-pattern" : "ok",
      since,
      ageDays: since ? ageDays(since, todayISO) : null,
      refs: refsSummary(check.feature),
    };
  });
}

export function ageBuckets(records) {
  const buckets = { "0-29": 0, "30-89": 0, "90-179": 0, "180+": 0, unknown: 0 };
  for (const record of records) {
    const age = record.ageDays;
    if (age === null || age === undefined) buckets.unknown += 1;
    else if (age < 30) buckets["0-29"] += 1;
    else if (age < 90) buckets["30-89"] += 1;
    else if (age < 180) buckets["90-179"] += 1;
    else buckets["180+"] += 1;
  }
  return buckets;
}

/** 人工判定抽样：fork 取钉龄最旧 N 条（未知钉龄殿后）；tsc 取候选优先（stale/dangling 在前）再按文件名稳序取 N 条。 */
export function pickSample(forkRecords, tscRecords, n) {
  if (!n) return { fork: [], tsc: [] };
  const forkSorted = [...forkRecords].sort((a, b) => {
    const aAge = a.ageDays ?? -1;
    const bAge = b.ageDays ?? -1;
    return bAge - aAge;
  });
  const tscSorted = [...tscRecords].sort((a, b) => {
    const rank = (record) => (record.verdict === "dangling-file" ? 0 : record.verdict === "stale-pairs" ? 1 : 2);
    return rank(a) - rank(b) || (a.file < b.file ? -1 : a.file > b.file ? 1 : 0);
  });
  return { fork: forkSorted.slice(0, n), tsc: tscSorted.slice(0, n) };
}

// ── IO 层 ────────────────────────────────────────────────────────

async function loadTscGate(repoRoot) {
  const gatePath = join(repoRoot, "desktop", "frontend", "scripts", "tsc-test-gate.mjs");
  return import(pathToFileURL(gatePath).href);
}

function runBlame(repoRoot, targetRelPath) {
  const run = spawnSync("git", ["blame", "--line-porcelain", "--", targetRelPath], {
    cwd: repoRoot, encoding: "utf8", maxBuffer: 64 * 1024 * 1024,
  });
  if (run.status !== 0) throw new Error(`git blame failed: ${run.stderr?.slice(0, 500)}`);
  return parseBlamePorcelain(run.stdout);
}

function runFullTsc(repoRoot) {
  const frontendRoot = join(repoRoot, "desktop", "frontend");
  const requireCheck = spawnSync(process.execPath, ["-e", "console.log(require.resolve('typescript/bin/tsc'))"], {
    cwd: frontendRoot, encoding: "utf8",
  });
  if (requireCheck.status !== 0) throw new Error(`frontend node_modules 缺 typescript，无法深验：先装依赖再 --with-tsc`);
  const tscPath = requireCheck.stdout.trim();
  // 与门禁同参（--noEmit --pretty false 全量、禁增量），保证诊断文本可解析且口径一致。
  const run = spawnSync(process.execPath, [tscPath, "--noEmit", "--pretty", "false", "-p", "tsconfig.test.json"], {
    cwd: frontendRoot, encoding: "utf8", maxBuffer: 64 * 1024 * 1024,
  });
  return { output: `${run.stdout ?? ""}${run.stderr ?? ""}`, status: run.status };
}

async function auditTsc(repoRoot, { withTsc }) {
  const baselinePath = join(repoRoot, "desktop", "frontend", "scripts", "tsc-test-baseline.json");
  const parsed = JSON.parse(readFileSync(baselinePath, "utf8"));
  const entries = parsed.entries ?? {};
  const frontendRoot = join(repoRoot, "desktop", "frontend");
  let diagnostics = null;
  let tscStatus = null;
  if (withTsc) {
    const { parseDiagnostics } = await loadTscGate(repoRoot);
    const { output, status } = runFullTsc(repoRoot);
    diagnostics = parseDiagnostics(output);
    tscStatus = status;
  }
  const records = judgeTscEntries(entries, diagnostics, (file) => existsSync(join(frontendRoot, file)));
  const pairTotal = records.reduce((sum, r) => sum + r.pairs.length, 0);
  const candidates = records.filter((r) => r.candidates > 0);
  return {
    source: "tsc",
    baselinePath: "desktop/frontend/scripts/tsc-test-baseline.json",
    batchSince: parsed._meta?.generated ?? null,
    files: records.length,
    pairs: pairTotal,
    deepVerified: diagnostics !== null,
    tscExitStatus: tscStatus,
    summary: {
      okFiles: records.filter((r) => r.verdict === "ok").length,
      staleFiles: records.filter((r) => r.verdict === "stale-pairs").length,
      danglingFiles: records.filter((r) => r.verdict === "dangling-file").length,
      unverifiedFiles: records.filter((r) => r.verdict === "unverified").length,
      candidatePairs: records.reduce((sum, r) => sum + r.candidates, 0),
    },
    candidates,
    records,
  };
}

async function auditForkIntegrity(repoRoot, todayISO) {
  const gatePath = join(SCRIPT_DIR, "check-fork-integrity.mjs");
  const { CHECKS } = await import(pathToFileURL(gatePath).href);
  const sourceText = readFileSync(gatePath, "utf8");
  const entryLines = mapChecksToLines(sourceText);
  if (entryLines.length !== CHECKS.length) {
    throw new Error(`CHECKS 行映射错位：源码 { feature: 出现 ${entryLines.length} 次 vs CHECKS ${CHECKS.length} 条——登记行格式漂移，先修映射再盘点`);
  }
  const blameDates = runBlame(repoRoot, "scripts/check-fork-integrity.mjs");
  const records = judgeForkChecks(CHECKS, repoRoot, blameDates, entryLines, todayISO);
  const reds = records.filter((r) => r.verdict !== "ok");
  return {
    source: "fork-integrity",
    manifestPath: "scripts/check-fork-integrity.mjs",
    total: records.length,
    patterns: CHECKS.reduce((sum, c) => sum + c.patterns.length, 0),
    distinctFiles: new Set(CHECKS.map((c) => c.file)).size,
    summary: {
      ok: records.length - reds.length,
      missingPattern: records.filter((r) => r.verdict === "missing-pattern").length,
      missingFile: records.filter((r) => r.verdict === "missing-file").length,
      unknownAge: records.filter((r) => r.ageDays === null).length,
      over180: records.filter((r) => (r.ageDays ?? 0) >= 180).length,
    },
    ageBuckets: ageBuckets(records),
    reds,
    records,
  };
}

function renderHuman(report) {
  const lines = [];
  for (const source of report.sources) {
    if (source.source === "tsc") {
      lines.push(`[tsc] ${source.files} 文件 / ${source.pairs} 对豁免（批量登记 ${source.batchSince}，深验=${source.deepVerified ? "是" : "否"}）`);
      lines.push(`  判级：仍需要文件 ${source.summary.okFiles}｜含 stale 候选文件 ${source.summary.staleFiles}｜悬空文件 ${source.summary.danglingFiles}｜待深验 ${source.summary.unverifiedFiles}`);
      lines.push(`  可退役候选（pair 数）：${source.summary.candidatePairs}${source.summary.candidatePairs ? "（明细见 --json 或下方候选段）" : ""}`);
      for (const file of source.candidates.slice(0, 40)) {
        const codes = file.pairs.filter((p) => p.verdict !== "needed" && p.verdict !== "unverified")
          .map((p) => `${p.code}(${p.verdict})`).join(" ");
        lines.push(`    候选 ${file.file} :: ${codes}${file.verdict === "dangling-file" ? "  [文件已不存在]" : ""}`);
      }
      if (source.candidates.length > 40) lines.push(`    …另 ${source.candidates.length - 40} 文件见 JSON`);
    } else {
      lines.push(`[fork-integrity] ${source.total} 条判例 / ${source.patterns} 锚 / ${source.distinctFiles} 文件（钉龄=登记行 git blame 日期）`);
      lines.push(`  判级：绿 ${source.summary.ok}｜锚失效 ${source.summary.missingPattern}｜文件缺失 ${source.summary.missingFile}｜未知钉龄 ${source.summary.unknownAge}｜钉龄≥180 天 ${source.summary.over180}`);
      lines.push(`  钉龄分桶：${Object.entries(source.ageBuckets).map(([k, v]) => `${k}=${v}`).join("  ")}`);
      if (source.reds.length) {
        lines.push("  门禁红条目（人工分辨：有意删除→退役候选；合并事故→走恢复流程）：");
        for (const red of source.reds) lines.push(`    ${red.verdict}  ${red.file}  (${red.feature})`);
      }
    }
  }
  if (report.sample) {
    lines.push(`[抽样] 人工判定样本 ${report.sample.note}`);
    for (const record of report.sample.fork) {
      lines.push(`    fork #${record.index} 钉龄${record.ageDays ?? "?"}天 ${record.refs}  ${record.feature}`);
    }
    for (const record of report.sample.tsc) {
      lines.push(`    tsc ${record.file}  ${record.pairs.length} 对（候选 ${record.candidates}）`);
    }
  }
  lines.push(`[口径] 清零率 = 累计已退役 ÷（累计已退役 + 当前存量）＝ ${report.clearance.tsc}（tsc，累计退役 ${report.clearance.retiredTsc}）／ ${report.clearance.forkIntegrity}（fork-integrity，累计退役 ${report.clearance.retiredFork}）；退役数取自台账（--retired-tsc/--retired-fork 传入），本脚本只报存量与候选。`);
  lines.push("[声明] 本脚本只读盘点、不是门禁：候选清单只列不删，退役动作候拍板（docs/gate-metadata-retirement.md §2/§4）。");
  return lines.join("\n");
}

async function main(argv) {
  const getArg = (name, fallback = null) => {
    const hit = argv.find((a) => a === name || a.startsWith(`${name}=`));
    if (!hit) return fallback;
    const eq = hit.indexOf("=");
    return eq === -1 ? true : hit.slice(eq + 1);
  };
  const repoRoot = resolve(String(getArg("--repo", DEFAULT_REPO)));
  const source = String(getArg("--source", "all"));
  const withTsc = Boolean(getArg("--with-tsc", false));
  const jsonPath = getArg("--json", null);
  const sampleRaw = getArg("--sample", 0);
  const sampleN = sampleRaw === true ? 10 : Number(sampleRaw) || 0;
  const retiredTsc = Math.max(0, Number(getArg("--retired-tsc", 0)) || 0);
  const retiredFork = Math.max(0, Number(getArg("--retired-fork", 0)) || 0);
  const todayISO = String(getArg("--today", new Date().toISOString().slice(0, 10)));
  if (!["all", "tsc", "fork-integrity"].includes(source)) {
    console.error(`未知 --source=${source}（可选 all|tsc|fork-integrity）`);
    return EXIT_USAGE;
  }
  if (!existsSync(repoRoot)) {
    console.error(`仓库根不存在：${repoRoot}`);
    return EXIT_USAGE;
  }

  const report = {
    tool: "gate-metadata-audit",
    policyDoc: "docs/gate-metadata-retirement.md",
    repoRoot,
    auditedAt: new Date().toISOString(),
    todayISO,
    withTsc,
    sources: [],
    clearance: { retiredTsc: 0, retiredFork: 0, tsc: null, forkIntegrity: null },
  };
  try {
    if (source === "all" || source === "tsc") report.sources.push(await auditTsc(repoRoot, { withTsc }));
    if (source === "all" || source === "fork-integrity") report.sources.push(await auditForkIntegrity(repoRoot, todayISO));
    // 清零率（政策 §4.5）：退役数由台账人工累计后经参数传入；首轮起算退役=0 → 0%。
    const tscSource = report.sources.find((s) => s.source === "tsc");
    const forkSource = report.sources.find((s) => s.source === "fork-integrity");
    const rate = (retired, stock) => (retired + stock === 0 ? "n/a" : `${((retired * 100) / (retired + stock)).toFixed(2)}%`);
    report.clearance = {
      retiredTsc,
      retiredFork,
      tsc: tscSource ? rate(retiredTsc, tscSource.pairs) : null,
      forkIntegrity: forkSource ? rate(retiredFork, forkSource.total) : null,
    };
    if (sampleN > 0) {
      const fork = report.sources.find((s) => s.source === "fork-integrity");
      const tsc = report.sources.find((s) => s.source === "tsc");
      const picked = pickSample(fork?.records ?? [], tsc?.records ?? [], sampleN);
      report.sample = {
        ...picked,
        note: `fork 取钉龄最旧 ${picked.fork.length} 条（未知钉龄殿后）；tsc 取候选优先 ${picked.tsc.length} 文件。人工判定按政策 §2 逐条给证据。`,
      };
    }
  } catch (error) {
    console.error(`gate-metadata-audit: ${error.message}`);
    return EXIT_INTERNAL;
  }

  if (jsonPath && jsonPath !== true) writeFileSync(String(jsonPath), JSON.stringify(report, null, 2), "utf8");
  console.log(renderHuman(report));
  return EXIT_OK;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = await main(process.argv.slice(2));
}
