import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import test from "node:test";
import { testPlan } from "./ci-test-plan.mjs";

const frontend = new URL("../", import.meta.url);
const scripts = JSON.parse(readFileSync(new URL("package.json", frontend), "utf8")).scripts;
const files = readdirSync(new URL("src/__tests__/", frontend)).filter(file => /\.test\.tsx?$/.test(file));

test("every discovered and dedicated test is scheduled exactly once", () => {
  const plan = testPlan(scripts, files);
  for (const file of files) {
    assert.equal(plan.filter(entry => entry.args.includes(`src/__tests__/${file}`)).length, 1, file);
  }
  assert.equal(plan.filter(entry => entry.args.includes("src/components/TaskMonitorPanel.test.tsx")).length, 1);
  assert.equal(plan.filter(entry => entry.args.includes("scripts/test-todo-visibility.mjs")).length, 1);
  assert.ok(plan.some(entry => entry.args.includes("--self-test")));
  assert.ok(plan.some(entry => entry.args.includes("tsconfig.test.json")));
  assert.ok(plan.some(entry => entry.args.includes("bench/app-memory-shards.test.mjs")));
});

test("asset loaders and the isolated performance benchmark retain their invocation", () => {
  const plan = testPlan(scripts, files);
  for (const [file, loader] of [["remote-connect-wizard", "css"], ["remote-server-panel", "svg"]]) {
    const entry = plan.find(entry => entry.key === `src/__tests__/${file}.test.tsx`);
    // tl1788 normalization: stubs are attached via the canonical absolute
    // face, so match on the register file name rather than its exact form.
    assert.ok(entry.args.some(arg => String(arg).endsWith(`${loader}-stub-register.mjs`)));
  }
  assert.deepEqual(plan.filter(entry => entry.serial).map(entry => entry.key), ["src/__tests__/history-performance-benchmark.tsx"]);
});

test("future tests are discovered without registration and unknown script grammars fail closed", () => {
  assert.ok(testPlan(scripts, [...files, "new-behavior.test.tsx"]).some(entry => entry.key.endsWith("new-behavior.test.tsx")));
  for (const body of ["pnpm test:motion", "echo passed", "node check.mjs || true", "node check.mjs; true"]) {
    assert.throws(() => testPlan({ ...scripts, "test:motion": body }, files));
  }
  assert.throws(() => testPlan({ ...scripts, "test:workspace": undefined }, files));
});

test("deduplication cannot silently discard different explicit loaders", () => {
  assert.throws(() => testPlan({ ...scripts,
    "test:mcp-app": "node --import ./scripts/svg-stub-register.mjs --import tsx src/__tests__/terminal-events.test.ts",
  }, files), /conflicting test invocation/);
});

test("every tsx entry runs the canonical governed face (tl1788)", () => {
  const plan = testPlan(scripts, files);
  for (const entry of plan) {
    if (entry.args[0] !== "tsx") continue;
    assert.ok(entry.args.some(arg => String(arg).endsWith("tsconfig.tsx-runner.json")), entry.key);
    assert.ok(entry.args.some(arg => String(arg).endsWith("css-stub-register.mjs")), entry.key);
    assert.ok(entry.args.some(arg => String(arg).endsWith("svg-stub-register.mjs")), entry.key);
    assert.ok(entry.args.some(arg => /\.(?:test\.tsx?|tsx)$/.test(String(arg))), entry.key);
  }
});
