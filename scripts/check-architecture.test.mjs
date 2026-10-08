// check-architecture 规则引擎单测（node --test scripts/check-architecture.test.mjs）。
// 夹具树落在临时目录，逐条验证：方向规则 / testutil 隔离 / 禁环 / 禁深导入 /
// 基线只报新增与陈旧指纹 / 反向依赖闭包 / --context 输出。同时承担
// `check-architecture.mjs --self-test` 的实际执行体。
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import {
  expandClosure,
  filterBaseline,
  fingerprint,
  findCycles,
  formatContext,
  moduleForPath,
  resolveTsTarget,
  runEdgeRules,
  scanGraph,
} from "./check-architecture.mjs";

function makeFixture(policyOver = {}) {
  const root = mkdtempSync(join(tmpdir(), "archcheck-"));
  const w = (rel, body) => {
    mkdirSync(join(root, rel, ".."), { recursive: true });
    writeFileSync(join(root, rel), body);
  };
  // Go：干净边 a->b、反向闭包链 p<-q<-r、违规样例（host/testonly/环/深导入）
  w("internal/a/a.go", `package a\n\nimport (\n\t"reasonix/internal/b"\n)\n\nvar _ = b.X\n`);
  w("internal/b/b.go", `package b\n\nconst X = 1\n`);
  w("internal/p/p.go", `package p\nconst P = 1\n`);
  w("internal/q/q.go", `package q\n\nimport "reasonix/internal/p"\n\nvar _ = p.P\n`);
  w("internal/r/r.go", `package r\n\nimport "reasonix/internal/q"\n\nvar _ = q.P\n`);
  w("internal/y/y.go", `package y\n\nimport "reasonix/desktop/app"\n\nvar _ = app.X\n`);
  w("desktop/app.go", `package desktop\n\nconst X = 1\n`);
  w("internal/x/x.go", `package x\n\nimport "reasonix/internal/agent/testutil"\n\nvar _ = testutil.T\n`);
  w("internal/agent/testutil/t.go", `package testutil\n\nconst T = 1\n`);
  w("internal/m/m.go", `package m\n\nimport "reasonix/internal/n"\n\nvar _ = n.N\n`);
  w("internal/n/n.go", `package n\n\nimport "reasonix/internal/m"\n\nvar _ = m.M\n`);
  w("internal/z/z.go", `package z\n\nimport "reasonix/internal/tree/sub"\n\nvar _ = sub.S\n`);
  w("internal/tree/root.go", `package tree\n\nconst R = 1\n`);
  w("internal/tree/sub/s.go", `package sub\n\nconst S = 1\n`);
  // TS：内核->展示违规、纯类型导入豁免、透传导入链（闭包用）
  w("src/lib/k.ts", `import { renderP } from "../components/P";\nexport const k = renderP;\n`);
  w("src/components/P.tsx", `export const renderP = () => null;\n`);
  w("src/lib/ktype.ts", `import type { Shape } from "../components/P";\nexport const k2: Shape = null;\n`);
  w("src/store/s.ts", `import { k } from "../lib/k";\nexport const s = k;\n`);
  const policy = {
    version: 1,
    modules: [
      { id: "go/host", kind: "go", roots: ["desktop"], host: true, managed: true },
      { id: "go/testutil", kind: "go", patterns: ["internal/**/testutil"], testOnly: true, managed: true },
      { id: "go/tree", kind: "go", roots: ["internal/tree"], managed: true, publicEntrypoints: ["internal/tree"] },
      { id: "ts/lib", kind: "ts", roots: ["src/lib"], managed: true },
      { id: "ts/store", kind: "ts", roots: ["src/store"], managed: true },
      { id: "ts/components", kind: "ts", roots: ["src/components"], managed: true },
    ],
    directionRules: [{ id: "ts-kernel-no-presentation", from: ["ts/lib", "ts/store"], forbid: ["ts/components"], note: "内核不得反向依赖展示" }],
    cycleRule: { roots: ["internal"] },
    ...policyOver,
  };
  writeFileSync(join(root, "architecture-policy.json"), JSON.stringify(policy, null, 2));
  return { root, policy, cleanup: () => rmSync(root, { recursive: true, force: true }) };
}

const ids = (findings) => findings.map((f) => `${f.rule}:${f.file}`);

test("干净边不报、五类规则各自命中样例", async () => {
  const fx = makeFixture();
  try {
    const graph = await scanGraph(fx.root);
    const findings = runEdgeRules(graph, fx.policy);
    // a->b 是合法内核边；q->p、r->q 合法；type-only 不构成边。
    assert.ok(!ids(findings).some((id) => id.startsWith("go-host-import:internal/a/")));
    assert.ok(ids(findings).includes("go-host-import:internal/y/y.go"));
    assert.ok(ids(findings).includes("go-testonly:internal/x/x.go"));
    assert.ok(ids(findings).includes("ts-kernel-no-presentation:src/lib/k.ts"));
    assert.ok(!ids(findings).includes("ts-kernel-no-presentation:src/lib/ktype.ts"));
    assert.ok(ids(findings).includes("deep-import:go/tree:internal/z/z.go"));
    assert.equal(findings.filter((f) => f.rule.startsWith("deep-import")).length, 1, "经入口 internal/tree 的导入不违规");
  } finally {
    fx.cleanup();
  }
});

test("禁环：互导包成环，单包不成环", async () => {
  const fx = makeFixture();
  try {
    const graph = await scanGraph(fx.root);
    const cycles = findCycles(graph, ["internal"]);
    assert.equal(cycles.length, 1);
    assert.deepEqual(cycles[0], ["internal/m", "internal/n"]);
  } finally {
    fx.cleanup();
  }
});

test("基线：指纹精确吸收存量，新增仍报，陈旧指纹提示", async () => {
  const fx = makeFixture();
  try {
    const graph = await scanGraph(fx.root);
    const findings = runEdgeRules(graph, fx.policy);
    const target = findings.find((f) => f.rule === "go-host-import");
    const baseline = { violations: { [fingerprint(target)]: { rule: target.rule } } };
    const { fresh, stale, suppressed } = filterBaseline(findings, baseline);
    assert.ok(!fresh.some((f) => f.rule === "go-host-import"), "登记过的指纹不再报");
    assert.equal(suppressed, 1);
    assert.ok(stale.length === 0);
    const { stale: stale2 } = filterBaseline(findings.filter((f) => f.rule !== "go-host-import"), baseline);
    assert.equal(stale2.length, 1, "违规消失后基线条目转陈旧");
  } finally {
    fx.cleanup();
  }
});

test("反向依赖闭包：改动 p 收齐 q、r 及其 TS 导入方", async () => {
  const fx = makeFixture();
  try {
    const graph = await scanGraph(fx.root);
    const closure = expandClosure(graph, new Set(["internal/p/p.go", "src/components/P.tsx"]));
    assert.ok(closure.has("internal/q/q.go"), "直接导入方入闭包");
    assert.ok(closure.has("internal/r/r.go"), "传递导入方入闭包");
    assert.ok(closure.has("src/lib/k.ts"), "TS 导入方入闭包");
    assert.ok(closure.has("src/store/s.ts"), "TS 传递导入方入闭包");
    assert.ok(!closure.has("internal/a/a.go"), "无关包不入闭包");
  } finally {
    fx.cleanup();
  }
});

test("模块归属与 TS 目标解析", async () => {
  const fx = makeFixture();
  try {
    const graph = await scanGraph(fx.root);
    assert.equal(moduleForPath("internal/agent/testutil/t.go", fx.policy).id, "go/testutil");
    assert.equal(moduleForPath("internal/agent/agent.go", fx.policy), null);
    const known = new Set(graph.tsFiles.keys());
    assert.equal(resolveTsTarget(fx.root, "src/lib/k.ts", "../components/P", known), "src/components/P.tsx");
    assert.equal(resolveTsTarget(fx.root, "src/lib/k.ts", "../components/P.css", known), null);
    assert.equal(resolveTsTarget(fx.root, "src/lib/k.ts", "react", known), null);
  } finally {
    fx.cleanup();
  }
});

test("--context：已知模块含方向红线，未知模块列出注册表", async () => {
  const fx = makeFixture();
  try {
    const ctx = formatContext(fx.policy, "ts/lib");
    assert.match(ctx, /ts-kernel-no-presentation/);
    assert.match(ctx, /spec-first 五步/);
    const miss = formatContext(fx.policy, "ts/nope");
    assert.match(miss, /未注册模块/);
    assert.match(miss, /ts\/components/);
  } finally {
    fx.cleanup();
  }
});
