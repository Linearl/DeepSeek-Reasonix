import assert from "node:assert/strict";
import test from "node:test";
import { buildBaselineEntries, classify, loadBaseline, parseDiagnostics } from "./tsc-test-gate.mjs";

const sample = [
  "src/__tests__/a.test.tsx(12,7): error TS2322: Type 'number' is not assignable to type 'string'.",
  "src/__tests__/a.test.tsx(30,1): error TS6133: 'React' is declared but its value is never read.",
  'src\\__tests__\\b.test.ts(3,10): error TS18048: \'win.go\' is possibly \'undefined\'.',
  "ntvribble that must never parse as a diagnostic",
].join("\n");

test("diagnostic parsing is line-anchored and normalizes separators", () => {
  const parsed = parseDiagnostics(sample);
  assert.equal(parsed.length, 3);
  assert.deepEqual(parsed[0], {
    file: "src/__tests__/a.test.tsx", line: 12, column: 7, code: "TS2322",
    message: "Type 'number' is not assignable to type 'string'.",
  });
  assert.equal(parsed[2].file, "src/__tests__/b.test.ts", "windows separators normalize to forward slashes");
});

test("baseline entries sort codes and files for stable diffs", () => {
  const entries = buildBaselineEntries(parseDiagnostics(sample));
  assert.deepEqual(entries["src/__tests__/a.test.tsx"], ["TS2322", "TS6133"]);
  assert.deepEqual(Object.keys(entries), ["src/__tests__/a.test.tsx", "src/__tests__/b.test.ts"]);
});

test("classify fails closed outside the baseline and reports paid-off pairs", () => {
  const parsed = parseDiagnostics(sample);
  const entries = buildBaselineEntries(parsed);
  const clean = classify(parsed, entries);
  assert.deepEqual(clean.fresh, []);
  assert.deepEqual(clean.stale, []);
  assert.equal(clean.absorbed, 3, "three file+code pairs absorbed");
  // 同文件新增同码诊断会被吸收——刻意取舍的已知盲区。
  const absorbedGrowth = classify([...parsed, { ...parsed[0], line: 999 }], entries);
  assert.deepEqual(absorbedGrowth.fresh, []);
  // 新错误码、新文件必须即红。
  assert.equal(classify([...parsed, { ...parsed[0], code: "TS9999" }], entries).fresh.length, 1);
  assert.equal(classify([...parsed, { ...parsed[0], file: "src/__tests__/new.test.tsx" }], entries).fresh.length, 1);
  const paidOff = classify([], entries);
  assert.equal(paidOff.fresh.length, 0);
  assert.equal(paidOff.stale.length, 3, "paid-off pairs surface as stale for manual deletion");
});

test("registered baseline is structurally valid and self-consistent", () => {
  const baseline = loadBaseline();
  assert.equal(typeof baseline._meta.pairs, "number");
  const files = Object.keys(baseline.entries);
  let pairs = 0;
  for (const [file, codes] of Object.entries(baseline.entries)) {
    assert.ok(file.startsWith("src/__tests__/") || file.startsWith("bench/"), file);
    assert.ok(codes.length > 0, file);
    for (let i = 0; i < codes.length; i += 1) {
      assert.match(codes[i], /^TS\d+$/);
      if (i > 0) assert.ok(codes[i - 1] < codes[i], `codes sorted in ${file}`);
    }
    pairs += codes.length;
  }
  assert.equal(pairs, baseline._meta.pairs, "meta pair count matches entries");
  assert.ok(files.length > 0);
});

test("baseline meta records the registration commit this debt was seeded from", () => {
  const baseline = loadBaseline();
  assert.match(baseline._meta.commit, /^[0-9a-f]{9,40}$/);
  assert.equal(baseline._meta.generated, "2026-10-10");
  // 报告引用：登记时的诊断总数（472）与快照对账，防止登记与快照脱节。
  const recorded = Number(baseline._meta.diagnostics);
  assert.ok(recorded > 0 && recorded >= baseline._meta.pairs);
});
