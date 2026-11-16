#!/usr/bin/env node
// verify-release-ancestry.mjs — 出包祖先性审计的机械化执行端（2026-11-15 体系补强件②）。
//
// 站规（出包台账纪律 / package-notes-build-point-ancestry-audit 判例，包 80 虚报 4 支）：
// release notes 声称包含的每一支，其分支必须在构建点祖先链上。此前逐支
// `git cherry`/`merge-base --is-ancestor` 全靠人工执行，本脚本把它变成出包前
// 的固定校验：
//
//   OK         分支 tip 在构建点祖先链上（merge-base --is-ancestor 退出 0）
//   MISSING    分支存在但不在构建点祖先链上 → 虚报，exit 1
//   ERROR      分支不存在 / git 调用失败 → 视同未验证，exit 1
//
// 另查重复：分支清单内同 tip 出现多次 → DUPLICATE 警告（双滚嫌疑，不阻断）。
//
// 用法：
//   node scripts/verify-release-ancestry.mjs --build-point <sha> --branches feat-a,feat-b
//   node scripts/verify-release-ancestry.mjs --build-point <sha> --branches-file branches.txt --repo /path/to/repo
//
// 语义边界（有意不做）：不校验 commit 是否被 rebase 改写（cherry-pick 重复需
// git cherry 补充，见 --cherry 开关）；不做 CI 接线（workflow 改动候拍板）。

import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const EXIT_OK = 0;
export const EXIT_MISSING = 1;
export const EXIT_USAGE = 2;

/** git 调用注入层（测试替换用）。 */
export function makeGitRunner(repo) {
  return function git(args) {
    const result = spawnSync("git", args, { cwd: repo, encoding: "utf8" });
    if (result.error) return { code: 127, stdout: "", stderr: String(result.error) };
    return { code: result.status ?? 127, stdout: result.stdout ?? "", stderr: result.stderr ?? "" };
  };
}

export function parseArgs(argv) {
  const out = { repo: ".", buildPoint: null, branches: [], branchesFile: null, cherry: false };
  for (let i = 0; i < argv.length; i += 1) {
    switch (argv[i]) {
      case "--repo": out.repo = argv[++i] ?? null; break;
      case "--build-point": out.buildPoint = argv[++i] ?? null; break;
      case "--branches": out.branches = (argv[++i] ?? "").split(",").map((s) => s.trim()).filter(Boolean); break;
      case "--branches-file": out.branchesFile = argv[++i] ?? null; break;
      case "--cherry": out.cherry = true; break;
      default: throw new Error(`unknown argument: ${argv[i]}`);
    }
  }
  if (!out.buildPoint) throw new Error("--build-point <sha> is required");
  if (out.branchesFile) {
    out.branches = out.branches.concat(
      readFileSync(out.branchesFile, "utf8").split(/\r?\n/).map((s) => s.trim()).filter((s) => s && !s.startsWith("#")),
    );
  }
  if (!out.branches.length) throw new Error("--branches <a,b,...> or --branches-file is required");
  return out;
}

/**
 * 核心判定（纯逻辑，git 由 runner 注入）：
 * 返回 rows = [{branch, tip, status: OK|MISSING|ERROR, detail}]，duplicates = 同 tip 分支组。
 */
export function verifyAncestry(buildPoint, branches, git) {
  const base = git(["rev-parse", "--verify", `${buildPoint}^{commit}`]);
  if (base.code !== 0) {
    return {
      rows: branches.map((branch) => ({ branch, tip: null, status: "ERROR", detail: `build point unresolvable: ${base.stderr.trim()}` })),
      duplicates: [],
    };
  }
  const tipCache = new Map();
  const tipOf = (branch) => {
    if (!tipCache.has(branch)) {
      const result = git(["rev-parse", "--verify", `${branch}^{commit}`]);
      tipCache.set(branch, result.code === 0 ? result.stdout.trim() : null);
    }
    return tipCache.get(branch);
  };
  const rows = branches.map((branch) => {
    const tip = tipOf(branch);
    if (tip === null) return { branch, tip: null, status: "ERROR", detail: "branch unresolvable" };
    const check = git(["merge-base", "--is-ancestor", branch, buildPoint]);
    if (check.code === 0) return { branch, tip, status: "OK", detail: "contained in build point" };
    if (check.code === 1) return { branch, tip, status: "MISSING", detail: "NOT an ancestor of build point (release notes would overstate)" };
    return { branch, tip, status: "ERROR", detail: check.stderr.trim() || `git exit ${check.code}` };
  });
  const byTip = new Map();
  for (const row of rows) {
    if (row.tip === null) continue;
    if (!byTip.has(row.tip)) byTip.set(row.tip, []);
    byTip.get(row.tip).push(row.branch);
  }
  const duplicates = [...byTip.values()].filter((names) => names.length > 1);
  return { rows, duplicates };
}

export function cherryMatches(buildPoint, branches, git) {
  // git cherry <build-point> <branch>：列出分支有而构建点没有的等价补丁（"-"=已含，"+"=缺失）。
  return branches.map((branch) => {
    const result = git(["cherry", buildPoint, branch]);
    if (result.code !== 0) return { branch, missing: null, detail: result.stderr.trim() || `git exit ${result.code}` };
    const missing = result.stdout.split(/\r?\n/).filter((line) => line.startsWith("+")).length;
    return { branch, missing, detail: `${missing} unmerged patch(es) by patch-id` };
  });
}

function main(argv) {
  let args;
  try {
    args = parseArgs(argv);
  } catch (err) {
    process.stderr.write(`verify-release-ancestry: ${err.message}\n`);
    process.exitCode = EXIT_USAGE;
    return;
  }
  const git = makeGitRunner(resolve(args.repo));
  const { rows, duplicates } = verifyAncestry(args.buildPoint, args.branches, git);
  for (const row of rows) {
    process.stdout.write(`  ${row.status.padEnd(8)}  ${row.branch}  tip=${row.tip ?? "?"}  ${row.detail}\n`);
  }
  for (const group of duplicates) {
    process.stdout.write(`  DUPLICATE  same tip on: ${group.join(", ")}\n`);
  }
  if (args.cherry) {
    for (const item of cherryMatches(args.buildPoint, args.branches, git)) {
      process.stdout.write(`  CHERRY  ${item.branch}  ${item.missing === null ? item.detail : `missing=${item.missing}`}\n`);
    }
  }
  const bad = rows.filter((row) => row.status !== "OK").length;
  process.stdout.write(`  summary: ok=${rows.length - bad} missing=${rows.filter((r) => r.status === "MISSING").length} error=${rows.filter((r) => r.status === "ERROR").length} duplicates=${duplicates.length}\n`);
  if (bad) {
    process.stderr.write("  fail: branches missing from build point or unverifiable — fix notes/roll-up before release\n");
    process.exitCode = EXIT_MISSING;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
