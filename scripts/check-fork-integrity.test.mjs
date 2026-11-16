// check-fork-integrity 冒烟测（node --test scripts/check-fork-integrity.test.mjs；tl1626 档D）。
// 覆盖三用例（只测正向等于没测，红路径实测）：
//   ① 正常输入 → 绿（真实仓库根：CLI 子进程 + runIntegrityChecks 双形态）
//   ② manifest 登记的文件缺失 → 必须红（MISSING FILE 红路径）
//   ③ 人为删一个锚点 pattern → 必须红（MISSING 红路径）
// 另含 tl1626 验收 6 的防复发断言（一条，不新建机制）：compare-manifests 族
// 已由档A合并为「一主脚本 + 两入口 wrapper」，本族命名空间再出现第三份副本即红。
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { CHECKS, runIntegrityChecks } from "./check-fork-integrity.mjs";

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const scriptPath = join(repoRoot, "scripts", "check-fork-integrity.mjs");

// 已知锚点样例：取单 pattern、文件小的 fork 特有条目，便于在临时目录复刻红/绿环境。
const SAMPLE = CHECKS.find(
  (check) => check.file === "internal/config/desktop_preferences.go" && check.patterns.length === 1,
);
assert.ok(SAMPLE, "sample entry (任务377 config 开关) must exist in the CHECKS manifest");

function tmpTreeWithSampleFile() {
  const root = mkdtempSync(join(tmpdir(), "fork-integrity-smoke-"));
  const target = join(root, SAMPLE.file);
  mkdirSync(dirname(target), { recursive: true });
  writeFileSync(target, readFileSync(join(repoRoot, SAMPLE.file), "utf8"), "utf8");
  return root;
}

test("① 正常输入 → 绿：真实仓库根跑 CLI，exit 0 且全部通过", () => {
  const run = spawnSync(process.execPath, [scriptPath], { cwd: repoRoot, encoding: "utf8" });
  assert.equal(run.status, 0, `CLI should pass on a clean tree; stderr: ${run.stderr}`);
  assert.ok(
    run.stdout.trimEnd().endsWith(`${CHECKS.length}/${CHECKS.length} checks passed.`),
    `unexpected CLI tail: ${JSON.stringify(run.stdout.slice(-80))}`,
  );
  assert.ok(!run.stdout.includes("MISSING"), "green run must not report MISSING lines");
});

test("①b 正常输入 → 绿：runIntegrityChecks 在真实仓库根零失败", () => {
  const { failed, results, total } = runIntegrityChecks(repoRoot);
  assert.equal(total, CHECKS.length);
  assert.equal(failed, 0);
  assert.equal(results.length, total);
  assert.ok(results.every((line) => line.startsWith("OK")));
});

test("② manifest 登记文件缺失 → 必须红（MISSING FILE 红路径实测）", () => {
  const root = tmpTreeWithSampleFile();
  try {
    // 先正向对照：同一环境下文件在 → 绿，随后仅删文件即翻红。
    const green = runIntegrityChecks(root, [SAMPLE]);
    assert.equal(green.failed, 0);
    rmSync(join(root, SAMPLE.file));
    const red = runIntegrityChecks(root, [SAMPLE]);
    assert.equal(red.failed, 1);
    assert.equal(red.total, 1);
    assert.match(red.results[0], /^MISSING FILE/);
    assert.ok(red.results[0].includes(SAMPLE.file));
    assert.ok(red.results[0].includes(SAMPLE.feature));
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("③ 人为删一个锚点 → 必须红（MISSING pattern 红路径实测）", () => {
  const root = mkdtempSync(join(tmpdir(), "fork-integrity-smoke-"));
  try {
    const target = join(root, SAMPLE.file);
    mkdirSync(dirname(target), { recursive: true });
    const anchor = SAMPLE.patterns[0];
    const original = readFileSync(join(repoRoot, SAMPLE.file), "utf8").replace(/\r\n/g, "\n");
    assert.ok(original.includes(anchor), "anchor must exist in the repo file for this smoke test");
    writeFileSync(target, original.replace(anchor, "/* tl1626 smoke: anchor deliberately removed */"), "utf8");
    const red = runIntegrityChecks(root, [SAMPLE]);
    assert.equal(red.failed, 1);
    assert.match(red.results[0], /^MISSING /);
    assert.ok(red.results[0].includes(JSON.stringify(anchor)), "missing anchor must be named in the report line");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

// tl1626 验收 6 · 防复发断言（唯一一条）：compare-manifests 族白名单。
// 档A 合并后的合法形态 = 共享主脚本 + CLI/Desktop 两个入口 wrapper；
// scripts/ 下再出现任何新的 compare-*-release-manifests.sh（第三份副本）即红。
test("防复发：compare-manifests 族文件白名单（出现第三份副本即红）", () => {
  const scriptsDir = join(repoRoot, "scripts");
  const family = readdirSync(scriptsDir)
    .filter((name) => /^compare-.*release-manifests\.sh$/.test(name))
    .sort();
  assert.deepEqual(family, [
    "compare-cli-release-manifests.sh",
    "compare-desktop-release-manifests.sh",
    "compare-release-manifests.sh",
  ]);
});
