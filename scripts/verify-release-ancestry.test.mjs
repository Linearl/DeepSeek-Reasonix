// verify-release-ancestry 单测（node --test scripts/verify-release-ancestry.test.mjs）。
// git 层用注入 runner（不依赖真仓）：参数解析 / OK·MISSING·ERROR 三态 / 重复 tip /
// cherry 计数 / fail 分支清单的 exit 语义由 main 的分类函数保证（此处测纯逻辑层）。
import { test } from "node:test";
import assert from "node:assert/strict";

import { parseArgs, verifyAncestry, cherryMatches } from "./verify-release-ancestry.mjs";

test("parseArgs：branches 逗号切分 + branches-file 必填校验", () => {
  const args = parseArgs(["--build-point", "abc123", "--branches", " a , b ,,c"]);
  assert.deepEqual(args.branches, ["a", "b", "c"]);
  assert.throws(() => parseArgs(["--branches", "a"]), /--build-point/);
  assert.throws(() => parseArgs(["--build-point", "abc"]), /--branches/);
  assert.throws(() => parseArgs(["--bogus"]), /unknown argument/);
});

function fakeGit(script) {
  const calls = [];
  return { git: (args) => { calls.push(args); return script(args); }, calls };
}

test("verifyAncestry：OK / MISSING / 分支不存在 ERROR", () => {
  const { git } = fakeGit((args) => {
    if (args[0] === "rev-parse" && args[1] === "--verify" && args[2].startsWith("build^")) {
      return { code: 0, stdout: "buildsha\n", stderr: "" };
    }
    if (args[1] === "--verify") {
      const ref = args[2];
      if (ref.startsWith("ghost")) return { code: 128, stdout: "", stderr: "bad revision" };
      return { code: 0, stdout: `${ref.replace(/\W/g, "")}sha\n`, stderr: "" };
    }
    if (args[0] === "merge-base") {
      return args[2] === "good" ? { code: 0, stdout: "", stderr: "" } : { code: 1, stdout: "", stderr: "" };
    }
    throw new Error(`unexpected git call: ${args.join(" ")}`);
  });
  const { rows, duplicates } = verifyAncestry("build", ["good", "evil", "ghost"], git);
  assert.deepEqual(rows.map((r) => r.status), ["OK", "MISSING", "ERROR"]);
  assert.match(rows[1].detail, /overstate/);
  assert.deepEqual(duplicates, []);
});

test("verifyAncestry：构建点本身不可解析 → 全部 ERROR（视同未验证）", () => {
  const { git } = fakeGit(() => ({ code: 128, stdout: "", stderr: "bad revision" }));
  const { rows } = verifyAncestry("nope", ["a", "b"], git);
  assert.equal(rows.length, 2);
  for (const row of rows) assert.equal(row.status, "ERROR");
});

test("verifyAncestry：同 tip 多分支 → DUPLICATE 分组（双滚嫌疑）", () => {
  const { git } = fakeGit((args) => {
    if (args[1] === "--verify" && args[2].startsWith("build^")) return { code: 0, stdout: "buildsha\n", stderr: "" };
    if (args[1] === "--verify") {
      const tip = args[2].startsWith("feat-a") || args[2].startsWith("feat-b") ? "samesha" : "othersha";
      return { code: 0, stdout: `${tip}\n`, stderr: "" };
    }
    if (args[0] === "merge-base") return { code: 0, stdout: "", stderr: "" };
    throw new Error("unexpected");
  });
  const { duplicates } = verifyAncestry("build", ["feat-a", "feat-b", "feat-c"], git);
  assert.equal(duplicates.length, 1);
  assert.deepEqual(duplicates[0], ["feat-a", "feat-b"]);
});

test("cherryMatches：patch-id 维度统计 + 缺分支报 null", () => {
  const { git } = fakeGit((args) => {
    if (args[0] !== "cherry") throw new Error("unexpected");
    return args[1] === "bp" && args[2] === "has-new"
      ? { code: 0, stdout: "-aaa\n+bbb\n+ccc\n", stderr: "" }
      : { code: 128, stdout: "", stderr: "bad revision" };
  });
  const [hit, miss] = cherryMatches("bp", ["has-new", "ghost"], git);
  assert.equal(hit.missing, 2);
  assert.equal(miss.missing, null);
});
