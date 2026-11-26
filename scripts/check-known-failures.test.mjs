// check-known-failures 单测（node --test scripts/check-known-failures.test.mjs）。
// 覆盖：go test -json 宽容解析（混流行忽略）/ 三类判定（登记放行·新红阻断·已愈退役提示）/
// 包级失败匹配 / 子测试路径精确匹配 / 重复登记与坏 JSON fail-closed / exit 语义。
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { classify, loadRegistry, parseGoTestJSON } from "./check-known-failures.mjs";

const ev = (action, pkg, test) =>
  JSON.stringify({ Time: "2026-11-15T00:00:00Z", Action: action, Package: pkg, ...(test ? { Test: test } : {}) });

function registryOf(entries) {
  // 经由真实 JSON 路径加载，保证 fail-closed 语义被测到。
  const dir = mkdtempSync(join(tmpdir(), "knownfail-"));
  const path = join(dir, "kf.json");
  writeFileSync(path, JSON.stringify({ entries }), "utf8");
  return loadRegistry(path);
}

test("parseGoTestJSON 忽略非 JSON 混流行并统计", () => {
  const text = [ev("run", "p1", "TestA"), "build output noise", "", ev("pass", "p1", "TestA"), "{broken json"].join("\n");
  const { events, skippedLines } = parseGoTestJSON(text);
  assert.equal(events.length, 2);
  assert.equal(skippedLines, 2);
});

test("未登记失败 → unregistered（新红阻断）", () => {
  const reg = registryOf([]);
  const { events } = parseGoTestJSON([ev("run", "pkg/x", "TestNew"), ev("fail", "pkg/x", "TestNew")].join("\n"));
  const result = classify(events, reg);
  assert.equal(result.unregistered.length, 1);
  assert.equal(result.registered.length, 0);
  assert.deepEqual(result.unregistered[0], { package: "pkg/x", test: "TestNew" });
});

test("登记失败 → registered（放行）", () => {
  const reg = registryOf([{ package: "pkg/x", test: "TestOld", reason: "r", registered: "2026-11-15", source: "tl1" }]);
  const { events } = parseGoTestJSON([ev("run", "pkg/x", "TestOld"), ev("fail", "pkg/x", "TestOld")].join("\n"));
  const result = classify(events, reg);
  assert.equal(result.registered.length, 1);
  assert.equal(result.unregistered.length, 0);
});

// tl1819 修复用例：真实 go test -json 对失败包发双事件（测试级 + 包级聚合，
// scratch 必败模块实证 2026-11-26）。聚合事件不得把登记红重新拦下。
test("真实 go 形态：测试级登记红 + 包级聚合 → 放行（tl1819 修复）", () => {
  const reg = registryOf([{ package: "pkg/x", test: "TestOld", reason: "r", registered: "d", source: "s" }]);
  const events = parseGoTestJSON(
    [ev("run", "pkg/x", "TestOld"), ev("fail", "pkg/x", "TestOld"), ev("fail", "pkg/x")].join("\n"),
  ).events;
  const result = classify(events, reg);
  assert.equal(result.registered.length, 2); // 测试级 + 包级聚合都归放行
  assert.equal(result.unregistered.length, 0);
});

test("真实 go 形态：包级聚合不能替未登记的测试红遮掩", () => {
  const reg = registryOf([{ package: "pkg/x", test: "TestOld", reason: "r", registered: "d", source: "s" }]);
  const events = parseGoTestJSON(
    [
      ev("fail", "pkg/x", "TestOld"),
      ev("run", "pkg/x", "TestNewRed"),
      ev("fail", "pkg/x", "TestNewRed"),
      ev("fail", "pkg/x"),
    ].join("\n"),
  ).events;
  const result = classify(events, reg);
  assert.equal(result.registered.length, 2);
  assert.equal(result.unregistered.length, 1);
  assert.equal(result.unregistered[0].test, "TestNewRed");
});

test("子测试路径精确匹配，父名不吞子名", () => {
  const reg = registryOf([{ package: "pkg/x", test: "TestOld/Sub", reason: "r", registered: "d", source: "s" }]);
  const events = parseGoTestJSON(ev("fail", "pkg/x", "TestOld/Sub")).events;
  assert.equal(classify(events, reg).registered.length, 1);
  const other = parseGoTestJSON(ev("fail", "pkg/x", "TestOld/Other")).events;
  assert.equal(classify(other, reg).unregistered.length, 1);
});

test("包级失败（无 Test）匹配 test 省略的条目", () => {
  const reg = registryOf([{ package: "pkg/y", reason: "compile fail", registered: "d", source: "s" }]);
  const events = parseGoTestJSON(ev("fail", "pkg/y")).events;
  const result = classify(events, reg);
  assert.equal(result.registered.length, 1);
  assert.equal(result.unregistered.length, 0);
});

test("已愈提示：包实际运行过且未复失败 → recovered；没跑过 → 不提示", () => {
  const reg = registryOf([{ package: "pkg/z", test: "TestHealed", reason: "r", registered: "d", source: "s" }]);
  const ran = parseGoTestJSON([ev("run", "pkg/z", "TestHealed"), ev("pass", "pkg/z", "TestHealed")].join("\n")).events;
  assert.equal(classify(ran, reg).recovered.length, 1);
  const notRan = parseGoTestJSON(ev("run", "pkg/other", "TestX")).events;
  assert.equal(classify(notRan, reg).recovered.length, 0);
});

test("同一登记失败一次即 matched，重复 fail 事件不产生幽灵 recovered", () => {
  const reg = registryOf([{ package: "pkg/z", test: "TestStillRed", reason: "r", registered: "d", source: "s" }]);
  const events = parseGoTestJSON([ev("run", "pkg/z", "TestStillRed"), ev("fail", "pkg/z", "TestStillRed"), ev("fail", "pkg/z", "TestStillRed")].join("\n")).events;
  const result = classify(events, reg);
  assert.equal(result.registered.length, 2); // 两个 fail 事件都归类 registered
  assert.equal(result.recovered.length, 0);
});

test("registry fail-closed：坏 JSON / 重复条目 / 缺 package 均抛错", () => {
  const dir = mkdtempSync(join(tmpdir(), "knownfail-bad-"));
  const path = join(dir, "kf.json");
  writeFileSync(path, "{not json", "utf8");
  assert.throws(() => loadRegistry(path), /not valid JSON/);
  writeFileSync(path, JSON.stringify({ entries: [{ package: "a", test: "T" }, { package: "a", test: "T" }] }), "utf8");
  assert.throws(() => loadRegistry(path), /duplicate entry/);
  writeFileSync(path, JSON.stringify({ entries: [{ test: "T" }] }), "utf8");
  assert.throws(() => loadRegistry(path), /missing "package"/);
});
