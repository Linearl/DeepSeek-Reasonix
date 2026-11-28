// gate-metadata-audit 自测（node --test scripts/gate-metadata-audit.test.mjs；tl1756）。
// 覆盖（只测正向等于没测，红路径实测）：
//   ① 纯函数层：出处抽取 / blame porcelain 解析（含空行与短头组）/ 行映射数量对齐 CHECKS
//      / tsc 判级四态（needed/stale/dangling/unverified）/ fork 判级三态（ok/missing-*）
//      / 钉龄分桶 / 抽样排序
//   ② CLI 真实仓机械层跑通：exit 0 + 两源都出报告 + 「只列不删」声明在场
//   ③ 防复发断言：盘点端不得接进 CI/Makefile/package.json（政策 §6 non-goal，接线候拍板）
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { CHECKS } from "./check-fork-integrity.mjs";
import {
  ageBuckets, countLines, extractRefs, judgeForkChecks, judgeReachabilityEntries, judgeTscEntries,
  mapChecksToLines, parseBlamePorcelain, pickSample, refsSummary,
} from "./gate-metadata-audit.mjs";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const scriptPath = join(repoRoot, "scripts", "gate-metadata-audit.mjs");

test("① extractRefs：五类出处桶 + 无出处归未知钉龄桶", () => {
  const refs = extractRefs("任务350 层级化 tl1485 回归 #9222 分组 包110 抬线");
  assert.deepEqual(refs.tasks, ["350"]);
  assert.deepEqual(refs.tls, ["1485"]);
  assert.deepEqual(refs.prs, ["9222"]);
  assert.deepEqual(refs.packages, ["110"]);
  assert.equal(refsSummary("纯描述没有编号"), "无出处（未知钉龄桶）");
  assert.equal(refsSummary("tl1280 起 in-log 分叉退役").startsWith("tl1280"), true);
});

test("① parseBlamePorcelain：整块/短头组/空内容行三形态", () => {
  const t0 = Math.floor(Date.parse("2026-01-15T00:00:00Z") / 1000);
  const t1 = Math.floor(Date.parse("2026-06-02T00:00:00Z") / 1000);
  const sha0 = "a".repeat(40);
  const sha1 = "b".repeat(40);
  const porcelain = [
    `${sha0} 1 1 3`, `author someone`, `author-time ${t0}`, `author-tz +0000`,
    `summary first`, `\tline one`, ``,
    `${sha0} 2 2 1`, `\t`, // 空内容行（源码空行）：必须结算行 2，不得吞掉
    `${sha1} 3 3 1`, `author other`, `author-time ${t1}`, `author-tz +0000`,
    `summary second`, `\tline three`,
  ].join("\n");
  const dates = parseBlamePorcelain(porcelain);
  assert.deepEqual([...dates.keys()].sort((x, y) => x - y), [1, 2, 3]);
  assert.equal(dates.get(1), "2026-01-15");
  assert.equal(dates.get(2), "2026-01-15");
  assert.equal(dates.get(3), "2026-06-02");
});

test("① mapChecksToLines：与 CHECKS 数量对齐（登记行格式漂移即红）", () => {
  const sourceText = readFileSync(join(repoRoot, "scripts", "check-fork-integrity.mjs"), "utf8");
  const lines = mapChecksToLines(sourceText);
  assert.equal(lines.length, CHECKS.length, "源码 { feature: 行数必须等于 CHECKS 条数");
  const sourceLines = sourceText.split("\n");
  for (const lineNo of lines) assert.ok(sourceLines[lineNo - 1].includes("{ feature:"));
});

test("① judgeTscEntries：needed/stale/dangling 深验三态 + 未深验 unverified 态", () => {
  const entries = { "src/a.ts": ["TS6133", "TS2307"], "src/gone.ts": ["TS2322"], "src/b.tsx": ["TS18048"] };
  // 深验形态：诊断在=needed，消失=stale，文件没了=dangling；深验下不存在 unverified（全量 tsc 覆盖一切在盘文件）。
  const deep = judgeTscEntries(entries, [{ file: "src/a.ts", code: "TS6133" }], (file) => file !== "src/gone.ts");
  const deepByFile = Object.fromEntries(deep.map((r) => [r.file, r]));
  assert.equal(deepByFile["src/a.ts"].verdict, "stale-pairs");
  assert.deepEqual(deepByFile["src/a.ts"].pairs.map((p) => p.verdict), ["needed", "stale"]);
  assert.equal(deepByFile["src/gone.ts"].verdict, "dangling-file");
  assert.deepEqual(deepByFile["src/gone.ts"].pairs.map((p) => p.verdict), ["dangling"]);
  assert.equal(deepByFile["src/b.tsx"].verdict, "stale-pairs");
  assert.equal(deep.reduce((s, r) => s + r.candidates, 0), 3);
  // 未深验形态：只做存在性；在盘文件一律 unverified（待深验），悬空仍可判。
  const shallow = judgeTscEntries(entries, null, (file) => file !== "src/gone.ts");
  const shallowByFile = Object.fromEntries(shallow.map((r) => [r.file, r]));
  assert.equal(shallowByFile["src/a.ts"].verdict, "unverified");
  assert.equal(shallowByFile["src/b.tsx"].verdict, "unverified");
  assert.equal(shallowByFile["src/gone.ts"].verdict, "dangling-file");
  assert.equal(shallow.reduce((s, r) => s + r.candidates, 0), 1); // 只有悬空可计候选
});

test("① judgeForkChecks：绿/锚失效/文件缺失 + 未知钉龄", () => {
  const root = mkdtempSync(join(tmpdir(), "gate-audit-fork-"));
  try {
    mkdirSync(join(root, "src"), { recursive: true });
    writeFileSync(join(root, "src", "ok.ts"), "const anchorA = 1;\nconst anchorB = 2;\n");
    writeFileSync(join(root, "src", "drift.ts"), "// anchor moved away\n");
    const checks = [
      { feature: "任务1 绿例", file: "src/ok.ts", patterns: ["anchorA", "anchorB"] },
      { feature: "任务2 锚失效", file: "src/drift.ts", patterns: ["anchorA"] },
      { feature: "#9222 文件缺失", file: "src/deleted.ts", patterns: ["anchorC"] },
      { feature: "无钉龄例", file: "src/ok.ts", patterns: ["anchorA"] },
    ];
    const blame = new Map([[1, "2025-11-01"], [2, "2026-10-01"], [3, "2026-05-20"]]); // 第 4 条无 blame → 未知钉龄
    const records = judgeForkChecks(checks, root, blame, [1, 2, 3, 4], "2026-11-23");
    assert.deepEqual(records.map((r) => r.verdict), ["ok", "missing-pattern", "missing-file", "ok"]);
    assert.equal(records[1].missingPatterns.length, 1);
    assert.equal(records[0].ageDays, 387); // 2025-11-01 → 2026-11-23
    assert.equal(records[3].ageDays, null);
    assert.equal(records[2].refs.startsWith("#"), true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("① ageBuckets 分桶边界", () => {
  const buckets = ageBuckets([
    { ageDays: 0 }, { ageDays: 29 }, { ageDays: 30 }, { ageDays: 89 },
    { ageDays: 90 }, { ageDays: 179 }, { ageDays: 180 }, { ageDays: null },
  ]);
  assert.deepEqual(buckets, { "0-29": 2, "30-89": 2, "90-179": 2, "180+": 1, unknown: 1 });
});

test("① pickSample：fork 按钉龄最旧优先、未知殿后；tsc 候选优先", () => {
  const fork = [
    { index: 0, feature: "新", ageDays: 5 },
    { index: 1, feature: "旧", ageDays: 900 },
    { index: 2, feature: "未知", ageDays: null },
  ];
  const tsc = [
    { file: "z.ts", verdict: "unverified", pairs: [], candidates: 0 },
    { file: "a.ts", verdict: "stale-pairs", pairs: [], candidates: 3 },
    { file: "gone.ts", verdict: "dangling-file", pairs: [], candidates: 2 },
  ];
  const sample = pickSample(fork, tsc, 2);
  assert.deepEqual(sample.fork.map((r) => r.index), [1, 0]); // 未知钉龄殿后
  assert.deepEqual(sample.tsc.map((r) => r.file), ["gone.ts", "a.ts"]); // 悬空最优先，其余稳序
});

test("② CLI 真实仓机械层跑通：exit 0、两源在场、只列不删声明在场", () => {
  const run = spawnSync(process.execPath, [scriptPath, "--sample=3"], { cwd: repoRoot, encoding: "utf8", timeout: 120000 });
  assert.equal(run.status, 0, `exit ${run.status}; stderr: ${run.stderr?.slice(0, 800)}`);
  assert.ok(run.stdout.includes("[tsc] 380 文件 / 572 对"), `tsc 段缺失: ${run.stdout.slice(0, 400)}`);
  assert.ok(run.stdout.includes("[fork-integrity]"), "fork-integrity 段缺失");
  assert.ok(run.stdout.includes("只列不删"), "退役候拍板声明缺失");
  assert.ok(run.stdout.includes("[抽样]"), "抽样段缺失");
  assert.ok(!run.stdout.includes("CHECKS 行映射错位"), "映射错位守卫不应触发");
});

test("② CLI --json 落盘结构可解析且两源 summary 在场", () => {
  const out = join(mkdtempSync(join(tmpdir(), "gate-audit-json-")), "audit.json");
  try {
    const run = spawnSync(process.execPath, [scriptPath, `--json=${out}`, "--source=tsc"], { cwd: repoRoot, encoding: "utf8", timeout: 120000 });
    assert.equal(run.status, 0);
    const report = JSON.parse(readFileSync(out, "utf8"));
    assert.equal(report.tool, "gate-metadata-audit");
    assert.equal(report.sources.length, 1);
    assert.equal(report.sources[0].pairs, 572);
    assert.ok(report.sources[0].summary);
  } finally {
    rmSync(dirname(out), { recursive: true, force: true });
  }
});

test("① judgeReachabilityEntries：五态判级 + countLines 同 Python 语义", () => {
  assert.equal(countLines(""), 0);
  assert.equal(countLines("a\nb"), 2);
  assert.equal(countLines("a\nb\n"), 2);
  const tmpRepo = mkdtempSync(join(tmpdir(), "gate-audit-reach-"));
  try {
    const src = join(tmpRepo, "desktop", "frontend", "src");
    mkdirSync(src, { recursive: true });
    // dangling：登记后被删——刻意不写 gone.ts 文件
    writeFileSync(join(src, "live.ts"), "export {};\n", "utf8"); // wired：重新接线可达
    writeFileSync(join(src, "grew.ts"), "a\nb\nc\n", "utf8");    // grew：3 > 2（门禁红面）
    writeFileSync(join(src, "shrank.ts"), "a\n", "utf8");        // shrank：1 < 2（C-3 进度面）
    writeFileSync(join(src, "frozen.ts"), "x\ny\n", "utf8");     // frozen：2 == 2
    const reachable = new Set([join(src, "live.ts")]);
    const records = judgeReachabilityEntries(
      { "gone.ts": 5, "live.ts": 1, "grew.ts": 2, "shrank.ts": 2, "frozen.ts": 2 },
      tmpRepo, reachable,
    );
    assert.deepEqual(records.map((r) => r.verdict), ["dangling-file", "wired", "grew", "shrank", "frozen"]);
    assert.deepEqual(records.filter((r) => r.candidate).map((r) => r.file), ["gone.ts", "live.ts"]);
  } finally {
    rmSync(tmpRepo, { recursive: true, force: true });
  }
});

test("② CLI --source=reachability：冻结基线盘点跑通且 104 条在场", () => {
  const run = spawnSync(process.execPath, [scriptPath, "--source=reachability"], { cwd: repoRoot, encoding: "utf8", timeout: 120000 });
  assert.equal(run.status, 0, `exit ${run.status}; stderr: ${run.stderr?.slice(0, 800)}`);
  assert.ok(run.stdout.includes("[reachability] 104 文件"), `reachability 段缺失: ${run.stdout.slice(0, 400)}`);
  assert.ok(run.stdout.includes("冻结中 104"), "冻结面计数缺失");
});

test("③ 防复发：盘点端未接线 CI/Makefile/package.json（政策 §6，接线候拍板）", () => {
  const surfaces = [
    join(repoRoot, "Makefile"),
    join(repoRoot, "desktop", "frontend", "package.json"),
  ];
  for (const surface of surfaces) {
    assert.ok(!readFileSync(surface, "utf8").includes("gate-metadata-audit"), `${surface} 不得引用盘点端`);
  }
  const workflows = join(repoRoot, ".github", "workflows");
  for (const name of readdirSync(workflows)) {
    if (!name.endsWith(".yml") && !name.endsWith(".yaml")) continue;
    const body = readFileSync(join(workflows, name), "utf8");
    assert.ok(!body.includes("gate-metadata-audit"), `workflow ${name} 不得引用盘点端`);
  }
});
