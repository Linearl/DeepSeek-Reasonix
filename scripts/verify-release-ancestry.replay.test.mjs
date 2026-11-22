// verify-release-ancestry 三案例重放 + 正向形态 fixture 测试（真 git 仓，node --test）。
// 构造历史事故形态的拓扑重放：
//   案例1 包80 虚报形态：支从未滚合（基线前 fork，两 commit 全在构建点外）→ 必须 MISSING。
//   案例2 包98 误标形态：tl 号 commit 实为构建点之后的主线 commit → 必须 MISSING。
//   案例3 tl1724 漂移形态：窗口内主线 fork 未滚合支（一亲链护栏）→ MISSING；
//         零 commit 支 tip=基点 / 停老 tip → EMPTY（ancestor 误读「已合」的兜底）。
//   正向：verbatim 停滚合 tip=OK / squash 滚合=OK-PATCHID / 滚合后缀前进=DRIFTED /
//         滚合后被改写（merge(tlNNNN) 证据）=DRIFTED —— 全过 exit 0。
import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const SCRIPT = resolve(import.meta.dirname, "verify-release-ancestry.mjs");
let repo;

function git(args, cwd = repo) {
  const r = spawnSync("git", args, { cwd, encoding: "utf8" });
  assert.equal(r.status, 0, `git ${args.join(" ")} failed: ${r.stderr}`);
  return r.stdout.trim();
}

function commit(msg) {
  // 空 commit 的 patch-id 恒等会破坏 cherry 语义——每个 commit 写唯一文件制造真差异。
  const n = commit.seq = (commit.seq || 0) + 1;
  const f = join(repo, `f${String(n).padStart(3, "0")}.txt`);
  writeFileSync(f, `payload ${n} for ${msg}\n`, "utf8");
  git(["add", f]);
  git(["commit", "-m", msg]);
}

function verify(branches) {
  const r = spawnSync("node", [SCRIPT, "--build-point", "tip", "--baseline", "base", "--branches", branches.join(","), "--repo", repo], {
    encoding: "utf8",
  });
  const statuses = {};
  for (const line of r.stdout.split(/\r?\n/)) {
    const m = /^\s*(OK|OK-PATCHID|DRIFTED|MISSING|EMPTY|ERROR)\s+(\S+)\s/.exec(line);
    if (m) statuses[m[2]] = m[1];
  }
  const summary = /^\s*summary: (.*)$/m.exec(r.stdout)?.[1] ?? "";
  return { code: r.status, statuses, summary, stdout: r.stdout };
}

before(() => {
  repo = mkdtempSync(join(tmpdir(), "tl1760-replay-"));
  git(["init", "-q", "-b", "main"]);
  git(["config", "user.name", "tl1760-fixture"]);
  git(["config", "user.email", "tl1760@fixture.invalid"]);
  // 主线：c0 → c1 → base → 滚合三形态 → tip；tip 后 m3（包98 误标源）。
  commit("c0");
  commit("c1");
  git(["branch", "prebase"]); // 基线前主线点（包80 虚报/停老 tip 形态的 fork 源）
  commit("base");
  git(["branch", "base"]); // 记基线
  const head = () => git(["rev-parse", "HEAD"]);

  // 正向①verbatim：feat-tl2001 一 commit 后 merge，支停在滚合 tip。
  git(["checkout", "-q", "-b", "feat-tl2001"]);
  commit("w1: tl2001 deliverable");
  const w1 = head();
  git(["checkout", "-q", "main"]);
  git(["merge", "--no-ff", "-q", "-m", "merge(tl2001): verbatim roll", w1]);

  // 正向②squash：feat-tl2002 的 commit 被 cherry-pick 滚合（sha 改写 patch 同）。
  git(["checkout", "-q", "-b", "feat-tl2002"]);
  commit("w2: tl2002 deliverable");
  const w2 = head();
  git(["checkout", "-q", "main"]);
  git(["cherry-pick", "--no-edit", w2]);

  // 正向③后缀前进（tl1746 形态）：feat-tl2003 r1 滚合，r2 在 tip 之后加在支上。
  git(["checkout", "-q", "-b", "feat-tl2003"]);
  commit("r1: tl2003 deliverable");
  const r1 = head();
  git(["checkout", "-q", "main"]);
  git(["merge", "--no-ff", "-q", "-m", "merge(tl2003): suffix roll", r1]);

  // 正向④改写漂移（tl1741 形态）：x1 在构建区间内滚合，tip 之后支被重写到 tip 上换内容。
  git(["checkout", "-q", "-b", "feat-tl2041"]);
  commit("x1: tl2041 deliverable v1");
  const x1 = head();
  git(["checkout", "-q", "main"]);
  git(["merge", "--no-ff", "-q", "-m", "merge(tl2041): rewrite roll", x1]);

  commit("tip"); // 主线 tip=构建点
  git(["branch", "tip"]);
  git(["checkout", "-q", "feat-tl2003"]);
  commit("r2: tl2003 post-roll fix");
  git(["checkout", "-q", "feat-tl2041"]);
  git(["reset", "-q", "--hard", "tip"]);
  commit("x1: tl2041 deliverable v2 (rewritten, different patch)");
  git(["checkout", "-q", "main"]);

  // 案例1 包80 虚报：基线前 fork 两 commit，从未滚合。
  git(["checkout", "-q", "-b", "fake80", "prebase"]);
  commit("f80a: never rolled");
  commit("f80b: never rolled");
  git(["checkout", "-q", "main"]);

  // 案例3 tl1724 漂移：窗口内主线 fork（一亲链上）+ 1 commit 未滚合。
  git(["checkout", "-q", "-b", "drift1724", "tip^"]);
  commit("d1: forked in-window, never rolled");
  git(["checkout", "-q", "main"]);

  // 判例三兜底：零 commit 支 tip=基点；停老 tip 支。
  git(["branch", "zeroc", "base"]);
  git(["branch", "parked-old", "prebase"]);

  // 案例2 包98 误标：tip 之后的 commit 被误标进本包。
  commit("m3: landed after the build point (merge message mislabeled)");
  git(["checkout", "-q", "main"]);

  // 正向⑤merge 证据的裸 commit 声称（tl 号 commit 形态）：滚合 merge sha 本身。
  // x1 所在 merge = main 上 merge(tl2041) 的 sha：
  const m2041 = git(["log", "--merges", "--fixed-strings", "--format=%H", "--grep=merge(tl2041)", "base..tip"]);
  git(["tag", "claim-commit-2041", m2041.split("\n")[0]]);
});

after(() => {
  try {
    rmSync(repo, { recursive: true, force: true });
  } catch {
    /* Windows 句柄延迟，留 tmp 自清 */
  }
});

test("正向形态四件全过（exit 0）：OK / OK-PATCHID / DRIFTED(suffix) / DRIFTED(rewrite)", () => {
  const v = verify(["feat-tl2001", "feat-tl2002", "feat-tl2003", "feat-tl2041"]);
  assert.equal(v.statuses["feat-tl2001"], "OK");
  assert.equal(v.statuses["feat-tl2002"], "OK-PATCHID");
  assert.equal(v.statuses["feat-tl2003"], "DRIFTED");
  assert.equal(v.statuses["feat-tl2041"], "DRIFTED");
  assert.equal(v.code, 0);
  assert.match(v.summary, /missing=0 empty=0 error=0/);
});

test("正向⑤：滚合 merge sha 作为 tl 号 commit 声称 → OK", () => {
  const v = verify(["claim-commit-2041"]);
  assert.equal(v.statuses["claim-commit-2041"], "OK");
  assert.equal(v.code, 0);
});

test("案例1 包80 虚报形态 → MISSING 拒", () => {
  const v = verify(["fake80"]);
  assert.equal(v.statuses["fake80"], "MISSING");
  assert.equal(v.code, 1);
});

test("案例2 包98 误标形态（构建点后的 commit 声称进包）→ MISSING 拒", () => {
  const m3 = git(["rev-parse", "main"]); // tip 之后的主线 commit
  const v = verify([m3]);
  assert.equal(v.statuses[m3], "MISSING");
  assert.equal(v.code, 1);
});

test("案例3 tl1724 漂移形态（窗口内 fork 未滚合）→ MISSING；零 commit/停老 tip → EMPTY", () => {
  const v = verify(["drift1724", "zeroc", "parked-old"]);
  assert.equal(v.statuses["drift1724"], "MISSING");
  assert.equal(v.statuses["zeroc"], "EMPTY");
  assert.equal(v.statuses["parked-old"], "EMPTY");
  assert.equal(v.code, 1);
});

test("混合清单一次判定：该拒的拒、该过的过，exit 1 汇总", () => {
  const v = verify(["feat-tl2001", "fake80", "zeroc"]);
  assert.equal(v.statuses["feat-tl2001"], "OK");
  assert.equal(v.statuses["fake80"], "MISSING");
  assert.equal(v.statuses["zeroc"], "EMPTY");
  assert.equal(v.code, 1);
});

test("门禁自身：基线不是构建点祖先 → GATE-ERROR exit 1", () => {
  const r = spawnSync(
    "node",
    [SCRIPT, "--build-point", "prebase", "--baseline", "tip", "--branches", "feat-tl2001", "--repo", repo],
    { encoding: "utf8" },
  );
  assert.match(r.stdout, /GATE-ERROR/);
  assert.match(r.stdout, /not an ancestor/);
  assert.equal(r.status, 1);
});

test("同 tip 双支 → DUPLICATE 警告不阻断", () => {
  git(["branch", "feat-dup-a", "feat-tl2001"]);
  git(["branch", "feat-dup-b", "feat-tl2001"]);
  const v = verify(["feat-dup-a", "feat-dup-b"]);
  assert.match(v.stdout, /DUPLICATE/);
  assert.equal(v.code, 0);
});
