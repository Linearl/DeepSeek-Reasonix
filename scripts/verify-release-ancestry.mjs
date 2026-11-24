#!/usr/bin/env node
// verify-release-ancestry.mjs — 出包链祖先性/虚报校验（tl1760 换代版，2026-11-22）。
//
// 站规（出包台账纪律 / 三判例）：
//   判例一（包 80 虚报 4 支，58a1acfedf）：notes 按已交付清单写、漏构建点祖先性核对，
//     四支未进构建点 820f534dd（git cherry 全+）——声称与构建内容脱钩。
//   判例二（包 98 误标形态 / 包 91 勘误先例）：merge message 误标包号，实际 commit 晚于
//     该包构建点——平铺 log 拓扑交错被人肉误读成「已进」。
//   判例三（tl1724 历史漂移 / 零 commit 支）：ancestor 判定对漂移支不可靠——支 tip 在
//     构建点历史内（含 tip=基点的零 commit 支）时 is-ancestor 恒读成「已合」，漏网。
//
// 因此本版判定 **cherry（patch-id）为主、ancestor 为辅（仅作诊断信息）、rev-list
// 基线窗口非空兜底**。两个基准 ref 缺一不可：
//   --build-point  被构建的 commit（如包 108 的 7fa4d346c3）——「已进」判定目标。
//   --baseline     上一包构建点（如包 108 的 a7dd9ee9b6）——「本包新增」窗口基线。
// 对每支声称（feat-* 分支名或 tl 号 commit）读四量：
//   W = rev-list --count --no-merges <基线>..<支>    声称窗口：支相对基线的交付量
//   N = rev-list --count --no-merges <构建点>..<支>  未进量：窗口内不在构建点者
//   P = git cherry <构建点> <支> 的 "+" 行数          patch-id 维度未进量（merge 行剔除）
//   F = merge-base(<构建点>, <支>)                    支侧与构建点的最深公共点
// 判定矩阵（先命中先停）：
//   基线/构建点不可解析、基线非构建点祖先       → ERROR（全门失败）
//   声称不可解析                                 → ERROR（视同未验证，拒）
//   W == 0   → EMPTY    判例三兜底：相对基线零交付（零 commit 支 tip=基点、陈旧支停在
//                       老 tip）——ancestor 恒读「已合」不可信，拒。
//   N == 0   → OK       交付量全部逐字在构建点（merge 滚合常态：支停在滚合 tip）。
//   P == 0   → OK-PATCHID  N>0 但 patch-id 全命中（squash/cherry-pick 滚合，sha 已改写）
//                       ——「已滚合件假 NOT-merged」的正解（判例二教训）。
//   裸 commit 声称
//   且 P > 0 → MISSING  tl 号 commit 交付内容不在构建点（包 98 误标形态）。
//   分支声称：
//     滚合 merge 发现：构建区间存在 subject 前缀 merge(<tl号>) 的 merge 且第二父可解析
//                → DRIFTED 滚合原件在构建点、支侧此后改写/前进（tl1741 实测形态）——
//                  过但带 advisory。
//     否则 F 侧别检验：F 不在基线历史 且 F 不在构建点一亲链（= F 是滚进构建点的支侧
//                commit）→ DRIFTED 滚合后支 tip 单纯前进（tl1746 r1 进包/r2 在后实测形态）
//                ——过但带 advisory。
//     其余       → MISSING 声称内容零命中（包 80/tl1724 形态；窗口内 fork 的主线噪音
//                被 F 侧别检验正确排除，不再误判「部分命中」）。
//   另查重复：清单内同 tip 多次 → DUPLICATE 警告（双滚嫌疑，不阻断）。
//
// 用法：
//   node scripts/verify-release-ancestry.mjs --build-point <被构建commit> \
//     --baseline <上一包构建点> --branches feat-a,feat-b [--repo /path/to/repo]
//   node scripts/verify-release-ancestry.mjs --build-point <sha> --baseline <sha> \
//     --branches-file branches.txt
//
// 语义边界（有意不做 / non-goals）：不改 release notes 模板与版本号规则；不做 notes
// 自动解析（声称清单由出包方显式给出）；「部分滚合/交错缺失」只拒不追滚。

import { readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const EXIT_OK = 0;
export const EXIT_FAIL = 1;
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
  const out = { repo: ".", buildPoint: null, baseline: null, branches: [], branchesFile: null, notesFile: null };
  for (let i = 0; i < argv.length; i += 1) {
    switch (argv[i]) {
      case "--repo": out.repo = argv[++i] ?? null; break;
      case "--build-point": out.buildPoint = argv[++i] ?? null; break;
      case "--baseline": out.baseline = argv[++i] ?? null; break;
      case "--branches": out.branches = (argv[++i] ?? "").split(",").map((s) => s.trim()).filter(Boolean); break;
      case "--branches-file": out.branchesFile = argv[++i] ?? null; break;
      case "--notes": out.notesFile = argv[++i] ?? null; break;
      default: throw new Error(`unknown argument: ${argv[i]}`);
    }
  }
  if (!out.buildPoint) throw new Error("--build-point <built commit> is required");
  if (!out.baseline) throw new Error("--baseline <previous package build point> is required");
  if (out.branchesFile) {
    out.branches = out.branches.concat(
      readFileSync(out.branchesFile, "utf8").split(/\r?\n/).map((s) => s.trim()).filter((s) => s && !s.startsWith("#")),
    );
  }
  if (out.notesFile) {
    // notes 内机器可读声称行是唯一声称源（SSOT）：`<!-- claims: feat-a,feat-b -->`。
    // 只列本包新增支（上一包构建点之后交付者）——夜批/更早已入库支列进来会因
    // W==0 被 EMPTY 拒。缺该行=无法验证=拒（视同未验证，20261124 包112 虚报判例）。
    const text = readFileSync(out.notesFile, "utf8");
    const m = /<!--\s*claims:\s*([^\r\n]*?)\s*-->/.exec(text);
    if (!m || !m[1].trim()) {
      throw new Error(`--notes ${out.notesFile}: no "<!-- claims: feat-a,feat-b -->" marker line found (claims are required)`);
    }
    out.branches = out.branches.concat(m[1].split(",").map((s) => s.trim()).filter(Boolean));
  }
  if (!out.branches.length) throw new Error("--branches <a,b,...> or --branches-file or --notes is required");
  return out;
}

/**
 * 从声称名提取 tl 号 token（feat-tl1741 → "tl1741"；无 tl 号返回 null）。
 */
export function claimToken(claim) {
  const m = /tl\s*(\d{3,4})/i.exec(claim);
  return m ? `tl${m[1]}` : null;
}

/**
 * 滚合 merge 发现（git 由 runner 注入）：在 <基线>..<构建点> 找 subject 以
 * merge(<token> 开头的 merge，返回 {merge, rolledTip, subject} 或 null。
 * 只认 merge( 前缀（仓内滚合纪律），消息中段提及不算——防 token 撞词。
 */
export function findRollMerge(baseline, buildPoint, token, git) {
  const log = git(["log", "--merges", "--format=%H%x00%s", `${baseline}..${buildPoint}`]);
  if (log.code !== 0) return null;
  const esc = token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const re = new RegExp(`^merge\\(${esc}[^)]*\\)`, "i");
  for (const line of log.stdout.split(/\r?\n/)) {
    if (!line.trim()) continue;
    const sep = line.indexOf("\0");
    if (sep < 0) continue;
    const sha = line.slice(0, sep);
    const subject = line.slice(sep + 1);
    if (!re.test(subject)) continue;
    const parent = git(["rev-parse", "--verify", `${sha}^2^{commit}`]);
    if (parent.code !== 0) continue;
    return { merge: sha, rolledTip: parent.stdout.trim(), subject };
  }
  return null;
}

/**
 * 核心分类（纯逻辑，git 读数由参数注入；滚合 merge 证据由 rollMerge 传入，null=未发现）。
 * kind: branch|commit；windowCount=W；notInTipCount=N；cherryPlus=P；
 * forkInBaseHistory=F 在基线历史内；forkOnFirstParentChain=F 在构建点一亲链上。
 * 返回 {status, advisory}；status ∈ OK|OK-PATCHID|DRIFTED|MISSING|EMPTY|ERROR。
 */
export function classifyClaim({
  kind,
  windowCount,
  notInTipCount,
  cherryPlus,
  forkInBaseHistory,
  forkOnFirstParentChain,
  rollMerge,
}) {
  if (
    windowCount === null || notInTipCount === null || cherryPlus === null ||
    forkInBaseHistory === null || forkOnFirstParentChain === null
  ) {
    return { status: "ERROR", advisory: null };
  }
  if (windowCount === 0) {
    return {
      status: "EMPTY",
      advisory: "claim adds nothing beyond the baseline (zero-delivery; is-ancestor would falsely read it as merged — tl1724/zero-commit 判例)",
    };
  }
  if (notInTipCount === 0) {
    return { status: "OK", advisory: null };
  }
  if (cherryPlus === 0) {
    return { status: "OK-PATCHID", advisory: null };
  }
  if (kind === "commit") {
    return { status: "MISSING", advisory: "claimed commit content absent from build point (包98 误标形态)" };
  }
  if (rollMerge) {
    return {
      status: "DRIFTED",
      advisory: `rolled verbatim via ${rollMerge.merge.slice(0, 10)} (${rollMerge.subject}); branch has ${notInTipCount} commit(s) since diverged/rewritten`,
    };
  }
  if (!forkInBaseHistory && !forkOnFirstParentChain) {
    return {
      status: "DRIFTED",
      advisory: `roll point is a branch-side commit inside this build; ${notInTipCount} newer commit(s) not in this build (post-roll suffix evolution)`,
    };
  }
  return { status: "MISSING", advisory: "nothing of this branch is in the build point (包80 虚报 / tl1724 漂移形态)" };
}

function countLines(stdout) {
  return stdout.split(/\r?\n/).filter((l) => l.trim()).length;
}

/**
 * 逐支校验（git 由 runner 注入）。
 * 返回 { rows, duplicates, gateError }；rows = [{claim, kind, tip, status, detail, advisory}]。
 * gateError 非空 = 基线/构建点自身不可用，全部行 ERROR。
 */
export function verifyAncestry(buildPoint, baseline, claims, git) {
  const bp = git(["rev-parse", "--verify", `${buildPoint}^{commit}`]);
  if (bp.code !== 0) {
    return {
      rows: claims.map((claim) => ({ claim, kind: "?", tip: null, status: "ERROR", detail: `build point unresolvable: ${bp.stderr.trim()}`, advisory: null })),
      duplicates: [],
      gateError: `build point unresolvable: ${bp.stderr.trim()}`,
    };
  }
  const base = git(["rev-parse", "--verify", `${baseline}^{commit}`]);
  if (base.code !== 0) {
    return {
      rows: claims.map((claim) => ({ claim, kind: "?", tip: null, status: "ERROR", detail: `baseline unresolvable: ${base.stderr.trim()}`, advisory: null })),
      duplicates: [],
      gateError: `baseline unresolvable: ${base.stderr.trim()}`,
    };
  }
  const anc = git(["merge-base", "--is-ancestor", baseline, buildPoint]);
  if (anc.code !== 0) {
    return {
      rows: claims.map((claim) => ({ claim, kind: "?", tip: null, status: "ERROR", detail: `baseline ${baseline} is not an ancestor of build point ${buildPoint}`, advisory: null })),
      duplicates: [],
      gateError: "baseline is not an ancestor of build point",
    };
  }
  const tip = bp.stdout.trim();
  const baseSha = base.stdout.trim();

  const tipCache = new Map();
  const resolveRef = (ref) => {
    if (!tipCache.has(ref)) {
      const r = git(["rev-parse", "--verify", `${ref}^{commit}`]);
      tipCache.set(ref, r.code === 0 ? r.stdout.trim() : null);
    }
    return tipCache.get(ref);
  };
  const kindOf = (ref) => {
    const r = git(["rev-parse", "--verify", "--symbolic-full-name", ref]);
    return r.code === 0 && r.stdout.trim().startsWith("refs/heads/") ? "branch" : "commit";
  };
  const revCount = (rangeArgs) => {
    const r = git(["rev-list", "--count", "--no-merges", ...rangeArgs]);
    if (r.code !== 0) return null;
    const n = Number.parseInt(r.stdout.trim(), 10);
    return Number.isFinite(n) ? n : null;
  };
  const firstParentChain = (() => {
    const r = git(["rev-list", "--first-parent", `${baseSha}..${tip}`]);
    return r.code === 0 ? new Set(r.stdout.split(/\r?\n/).filter((l) => l.trim())) : new Set();
  })();

  const rows = claims.map((claim) => {
    const refTip = resolveRef(claim);
    if (refTip === null) {
      return { claim, kind: "?", tip: null, status: "ERROR", detail: "claim unresolvable", advisory: null };
    }
    const kind = kindOf(claim);
    const w = revCount([`${baseline}..${claim}`]);
    const n = revCount([`${buildPoint}..${claim}`]);
    const fork = git(["merge-base", buildPoint, claim]);
    const forkSha = fork.code === 0 ? fork.stdout.trim() : null;
    const cherry = git(["cherry", buildPoint, claim]);
    let plus = null;
    if (cherry.code === 0) {
      // cherry 输出可能含 merge 行（无 patch-id），剔除后再数 "+"。
      const merges = git(["rev-list", "--merges", `${buildPoint}..${claim}`]);
      const mergeSet = new Set(merges.code === 0 ? merges.stdout.split(/\r?\n/).filter((l) => l.trim()) : []);
      plus = cherry.stdout.split(/\r?\n/).filter((l) => /^\+/.test(l) && !mergeSet.has(l.slice(1).trim())).length;
    }
    let forkInBaseHistory = null;
    let forkOnFirstParentChain = null;
    if (forkSha !== null) {
      const inBase = git(["merge-base", "--is-ancestor", forkSha, baseSha]);
      forkInBaseHistory = inBase.code === 0;
      forkOnFirstParentChain = firstParentChain.has(forkSha);
    }
    const rollMerge =
      kind === "branch" && plus !== null && plus > 0 && w !== null && w > 0 && n !== null && n > 0
        ? (() => {
            const token = claimToken(claim);
            return token ? findRollMerge(baseline, buildPoint, token, git) : null;
          })()
        : null;
    const verdict = classifyClaim({
      kind,
      windowCount: w,
      notInTipCount: n,
      cherryPlus: plus,
      forkInBaseHistory,
      forkOnFirstParentChain,
      rollMerge,
    });
    const ancTip = git(["merge-base", "--is-ancestor", claim, buildPoint]);
    const ancText = ancTip.code === 0 ? "yes" : ancTip.code === 1 ? "no" : "?";
    const detail =
      `W=${w} N=${n} cherryPlus=${plus} kind=${kind} ancestor=${ancText}` +
      (rollMerge ? ` rollMerge=${rollMerge.merge.slice(0, 10)}` : "") +
      (verdict.advisory ? ` — ${verdict.advisory}` : "");
    return { claim, kind, tip: refTip, status: verdict.status, detail, advisory: verdict.advisory };
  });

  const byTip = new Map();
  for (const row of rows) {
    if (row.tip === null) continue;
    if (!byTip.has(row.tip)) byTip.set(row.tip, []);
    byTip.get(row.tip).push(row.claim);
  }
  const duplicates = [...byTip.values()].filter((names) => names.length > 1);
  return { rows, duplicates, gateError: null };
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
  const { rows, duplicates, gateError } = verifyAncestry(args.buildPoint, args.baseline, args.branches, git);
  if (gateError) process.stdout.write(`  GATE-ERROR  ${gateError}\n`);
  for (const row of rows) {
    process.stdout.write(`  ${row.status.padEnd(11)}  ${row.claim}  tip=${row.tip ? row.tip.slice(0, 10) : "?"}  ${row.detail}\n`);
  }
  for (const group of duplicates) {
    process.stdout.write(`  DUPLICATE  same tip on: ${group.join(", ")}\n`);
  }
  const count = (s) => rows.filter((r) => r.status === s).length;
  const failed = count("MISSING") + count("EMPTY") + count("ERROR");
  process.stdout.write(
    `  summary: ok=${count("OK")} patchid=${count("OK-PATCHID")} drifted=${count("DRIFTED")} missing=${count("MISSING")} empty=${count("EMPTY")} error=${count("ERROR")} duplicates=${duplicates.length}\n`,
  );
  if (gateError || failed) {
    process.stderr.write("  fail: claims missing from build point / zero-delivery / unverifiable — fix notes or roll-up before release\n");
    process.exitCode = EXIT_FAIL;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
