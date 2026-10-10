// 任务 745：测试面 typecheck 门禁。tsconfig.test.json 已纳入全部 *.ts/*.tsx，
// 存量类型欠账登记在 scripts/tsc-test-baseline.json（file+错误码 为一对，
// 对齐根目录 architecture-baseline.json 的房风）。本脚本只报基线外的新增诊断；
// 无自动刷新通道，基线条目只减不增——还清后人工核对删除对应行。
// 已知盲区（刻意取舍）：同文件同错误码的新增诊断会被基线吸收，换取行号/文案
// 变化不产生假红。要收紧盲区，清偿对应文件后整行删除即可。
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const SCRIPT_DIR = dirname(fileURLToPath(import.meta.url));
const FRONTEND_ROOT = dirname(SCRIPT_DIR);
const DIAGNOSTIC = /^([^(]+)\((\d+),(\d+)\): error (TS\d+): (.*)$/;

export function parseDiagnostics(output) {
  const diagnostics = [];
  for (const line of output.split("\n")) {
    const match = DIAGNOSTIC.exec(line.trim());
    if (!match) continue;
    diagnostics.push({
      file: match[1].replaceAll("\\", "/"),
      line: Number(match[2]),
      column: Number(match[3]),
      code: match[4],
      message: match[5],
    });
  }
  return diagnostics;
}

// "file" -> ["TS2322", ...]（错误码升序，便于人工编辑与 diff 稳定）
export function buildBaselineEntries(diagnostics) {
  const entries = new Map();
  for (const diagnostic of diagnostics) {
    if (!entries.has(diagnostic.file)) entries.set(diagnostic.file, new Set());
    entries.get(diagnostic.file).add(diagnostic.code);
  }
  return Object.fromEntries([...entries.entries()]
    .map(([file, codes]) => [file, [...codes].sort()])
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)));
}

export function classify(diagnostics, entries) {
  const seen = new Set(diagnostics.map(diagnostic => `${diagnostic.file}::${diagnostic.code}`));
  const fresh = [];
  for (const diagnostic of diagnostics) {
    const codes = entries[diagnostic.file];
    if (!Array.isArray(codes) || !codes.includes(diagnostic.code)) fresh.push(diagnostic);
  }
  const stale = [];
  let absorbed = 0;
  for (const [file, codes] of Object.entries(entries)) {
    for (const code of codes) {
      if (seen.has(`${file}::${code}`)) absorbed += 1;
      else stale.push({ file, code });
    }
  }
  return { fresh, stale, absorbed };
}

export function loadBaseline(baselinePath = join(SCRIPT_DIR, "tsc-test-baseline.json")) {
  const parsed = JSON.parse(readFileSync(baselinePath, "utf8"));
  if (typeof parsed.entries !== "object" || parsed.entries === null) throw new Error(`invalid baseline: ${baselinePath}`);
  return parsed;
}

function runTsc(tsconfigArg) {
  const require = createRequire(import.meta.url);
  const tscPath = require.resolve("typescript/bin/tsc");
  // 全量检查，禁用 pretty 与增量：诊断文本稳定可解析，且不允许增量缓存假绿。
  return spawnSync(process.execPath, [tscPath, "--noEmit", "--pretty", "false", "-p", tsconfigArg], {
    cwd: FRONTEND_ROOT, encoding: "utf8",
  });
}

async function main(argv) {
  if (argv.includes("--self-test")) {
    const { spawnSync: spawn } = await import("node:child_process");
    const result = spawn(process.execPath, ["--test", join(SCRIPT_DIR, "tsc-test-gate.test.mjs")], { stdio: "inherit" });
    process.exitCode = result.status ?? 1;
    return;
  }
  const tsconfigArg = argv.find(arg => arg.includes("tsconfig")) ?? "tsconfig.test.json";
  const run = runTsc(tsconfigArg);
  const output = `${run.stdout ?? ""}${run.stderr ?? ""}`;
  const diagnostics = parseDiagnostics(output);
  if (run.status !== 0 && diagnostics.length === 0) {
    console.error(`tsc-test-gate: tsc 退出 ${run.status} 但无可解析诊断（基础设施故障），原始输出尾部：`);
    console.error(output.slice(-4000));
    process.exitCode = 1;
    return;
  }
  const { fresh, stale, absorbed } = classify(diagnostics, loadBaseline().entries);
  for (const diagnostic of fresh) console.error(`${diagnostic.file}(${diagnostic.line},${diagnostic.column}): error ${diagnostic.code}: ${diagnostic.message}`);
  if (fresh.length) {
    console.error(`\ntsc-test-gate: ${fresh.length} 条基线外新增类型诊断。修复，或经人工 review 后登记` +
      ` scripts/tsc-test-baseline.json（本脚本无自动刷新通道）。`);
    process.exitCode = 1;
    return;
  }
  console.log(`tsc-test-gate: clean（${diagnostics.length} 条存量被基线吸收）`);
  if (stale.length) {
    console.error(`tsc-test-gate: 基线中 ${stale.length} 对 file+错误码 已无对应诊断（欠账还清了）。` +
      `请人工核对后从 scripts/tsc-test-baseline.json 删除这些行（只减不增）。`);
    for (const pair of stale) console.error(`  ${pair.file} :: ${pair.code}`);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) await main(process.argv.slice(2));
