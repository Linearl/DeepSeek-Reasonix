// verify-release-ancestry 单测（node --test scripts/verify-release-ancestry.test.mjs）。
// 纯逻辑层：参数解析 / classifyClaim 判定矩阵 / claimToken / findRollMerge 的
// merge( 前缀纪律。git 层走 replay fixture（verify-release-ancestry.replay.test.mjs）。
import { test } from "node:test";
import assert from "node:assert/strict";

import { parseArgs, classifyClaim, claimToken, findRollMerge } from "./verify-release-ancestry.mjs";

test("parseArgs：双基准必填 + branches 切分 + branches-file", () => {
  const args = parseArgs(["--build-point", "tip1", "--baseline", "base1", "--branches", " a , b ,,c"]);
  assert.deepEqual(args.branches, ["a", "b", "c"]);
  assert.throws(() => parseArgs(["--branches", "a"]), /--build-point/);
  assert.throws(() => parseArgs(["--build-point", "t"]), /--baseline/);
  assert.throws(() => parseArgs(["--build-point", "t", "--baseline", "b"]), /--branches/);
  assert.throws(() => parseArgs(["--bogus"]), /unknown argument/);
});

const FULL = {
  kind: "branch",
  windowCount: 3,
  notInTipCount: 0,
  cherryPlus: 0,
  forkInBaseHistory: true,
  forkOnFirstParentChain: true,
  rollMerge: null,
};

test("classifyClaim：W==0 → EMPTY（零 commit/停老 tip 判例三兜底）", () => {
  const v = classifyClaim({ ...FULL, windowCount: 0, notInTipCount: 0 });
  assert.equal(v.status, "EMPTY");
  assert.match(v.advisory, /is-ancestor/);
});

test("classifyClaim：N==0 → OK（merge 滚合停在滚合 tip 常态）", () => {
  assert.equal(classifyClaim({ ...FULL }).status, "OK");
});

test("classifyClaim：P==0 且 N>0 → OK-PATCHID（squash/cherry-pick 滚合）", () => {
  const v = classifyClaim({ ...FULL, notInTipCount: 2, cherryPlus: 0 });
  assert.equal(v.status, "OK-PATCHID");
});

test("classifyClaim：裸 commit P>0 → MISSING（包98 误标形态）", () => {
  const v = classifyClaim({ ...FULL, kind: "commit", notInTipCount: 1, cherryPlus: 1 });
  assert.equal(v.status, "MISSING");
  assert.match(v.advisory, /包98/);
});

test("classifyClaim：分支 + 滚合 merge 证据 → DRIFTED（tl1741 改写形态）", () => {
  const v = classifyClaim({
    ...FULL,
    notInTipCount: 4,
    cherryPlus: 4,
    rollMerge: { merge: "32642fb086", rolledTip: "b5d4669f7e", subject: "merge(tl1741): ..." },
  });
  assert.equal(v.status, "DRIFTED");
  assert.match(v.advisory, /32642fb086/);
});

test("classifyClaim：分支 + F 侧别检验命中 → DRIFTED（tl1746 后缀前进形态）", () => {
  const v = classifyClaim({
    ...FULL,
    notInTipCount: 1,
    cherryPlus: 1,
    forkInBaseHistory: false,
    forkOnFirstParentChain: false,
  });
  assert.equal(v.status, "DRIFTED");
  assert.match(v.advisory, /suffix/);
});

test("classifyClaim：分支零命中 → MISSING（包80/tl1724 形态；F 侧别检验防窗口内 fork 主线噪音误判）", () => {
  // 形态 A：fork 在基线历史内（基线前 fork、从未滚合）
  const a = classifyClaim({ ...FULL, notInTipCount: 2, cherryPlus: 2 });
  assert.equal(a.status, "MISSING");
  // 形态 B：窗口内主线 fork、从未滚合——F 在一亲链上，不得误判 DRIFTED
  const b = classifyClaim({ ...FULL, notInTipCount: 4, cherryPlus: 4, forkInBaseHistory: false, forkOnFirstParentChain: true });
  assert.equal(b.status, "MISSING");
  assert.match(b.advisory, /包80|tl1724/);
});

test("classifyClaim：git 读数缺失 → ERROR（视同未验证）", () => {
  const v = classifyClaim({ ...FULL, windowCount: null });
  assert.equal(v.status, "ERROR");
});

test("claimToken：feat-tlNNNN / 裸 tl号 / 无 token", () => {
  assert.equal(claimToken("feat-tl1741"), "tl1741");
  assert.equal(claimToken("feat-tl1746r2"), "tl1746");
  assert.equal(claimToken("tl1724"), "tl1724");
  assert.equal(claimToken("aabbccddeeff"), null);
});

test("findRollMerge：只认 merge(<token> 前缀，消息中段提及不算", () => {
  const { git } = (() => {
    const calls = [];
    return {
      calls,
      git: (args) => {
        calls.push(args);
        if (args[0] === "log") {
          return {
            code: 0,
            stdout: [
              "1111111111\u0000merge(tl1741): 实验室面板瘦身8项stable卡撤除",
              "2222222222\u0000fix(561): 冲突并集解(1741撤8卡x1746加tasklistPanel)",
            ].join("\n"),
            stderr: "",
          };
        }
        if (args[1] === "--verify") return { code: 0, stdout: "b5d4669f7e\n", stderr: "" };
        return { code: 128, stdout: "", stderr: "unexpected" };
      },
    };
  })();
  const hit = findRollMerge("base", "tip", "tl1741", git);
  assert.ok(hit);
  assert.equal(hit.merge, "1111111111");
  assert.equal(hit.rolledTip, "b5d4669f7e");
  // token 撞词：1746 只出现在消息中段，不得命中
  const miss = findRollMerge("base", "tip", "tl1746", git);
  assert.equal(miss, null);
});
