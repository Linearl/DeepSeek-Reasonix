// Run: tsx src/__tests__/tl1387-session-model-hover.test.ts
// tl1387 (user rulings 20261104 13:5x / 14:0x): sidebar session row model
// hover. Contract under test:
// - live value wins and equals the selector-source ref verbatim;
// - without a live runtime, the remembered last-known live value wins;
// - without any live history, the creation-time sidecar model is the floor,
//   reported under its own kind (locale annotates 「创建时」);
// - nothing known → undefined (never a guess);
// - the title composer appends the localized model line to the base title;
// - the row render block's inline provider tag is gone (source pin: the row
//   is title+time two segments — user ruling 13:0x).

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const { rememberLiveSessionModel, sessionModelHover, titleWithSessionModel } = await import("../lib/sessionModelHover");

let passed = 0;
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\ntl1387 session model hover");

// 1. Live value wins, verbatim from the selector source.
eq(sessionModelHover("s1", { modelRef: "mimo-token-plan/mimo-v2.6-flash", metaModel: "old/model" }), { kind: "live", model: "mimo-token-plan/mimo-v2.6-flash" }, "live: modelRef wins over metaModel");

// 2. Last-known live fallback (remembered from an earlier live render).
rememberLiveSessionModel("s2", "provider/a-model");
eq(sessionModelHover("s2", { metaModel: "old/model" }), { kind: "lastKnown", model: "provider/a-model" }, "lastKnown: remembered live value beats the creation-time model");

// 3. Creation-time floor carries its own kind (locale annotates).
eq(sessionModelHover("s3", { metaModel: "mimo-v2.6-flash" }), { kind: "createdAt", model: "mimo-v2.6-flash" }, "createdAt: sidecar model is the floor with its own kind");

// 4. Nothing known → undefined, never a guess.
eq(sessionModelHover("s4", {}), undefined, "empty: no value known yields no hover");
eq(sessionModelHover("s5", { modelRef: "  " }), undefined, "empty: whitespace-only ref is no value");

// 5. Title composer.
const format = (key: string, values: { model: string }) =>
  key === "projectTree.modelHover.createdAt" ? `${values.model}（创建时）` : `当前生效模型：${values.model}`;
eq(titleWithSessionModel("会话标题", { kind: "live", model: "p/m" }, format as never), "会话标题\n当前生效模型：p/m", "title: model line appended to the base title");
eq(titleWithSessionModel("会话标题", { kind: "createdAt", model: "p/m" }, format as never), "会话标题\np/m（创建时）", "title: createdAt kind uses the annotated key");
eq(titleWithSessionModel("会话标题", undefined, format as never), "会话标题", "title: no hover keeps the base title byte-identical");

// 6. Source pins (源码级断言).
const tree = readFileSync(new URL("../components/ProjectTree.tsx", import.meta.url), "utf8");
ok:
{
  const inline = /node\.modelRef \? \([\s\S]{0,200}?provider \|\| node\.modelRef/;
  eq(inline.test(tree), false, "source: the inline provider/modelRef render block is deleted (A)");
  eq(tree.includes("rememberLiveSessionModel(modelHoverKey, node.modelRef)"), true, "source: live modelRef feeds the last-known memory");
  eq(tree.includes("metaModel: node.metaModel"), true, "source: creation-time field feeds the hover chain");
}

let code = 0;
if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  code = 1;
} else {
  process.stdout.write(`\n${passed} passed, 0 failed\n`);
}
process.exit(code);
