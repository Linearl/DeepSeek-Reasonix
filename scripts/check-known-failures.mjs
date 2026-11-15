#!/usr/bin/env node
// check-known-failures.mjs — 预存红登记制的机械化执行端（2026-11-15 体系补强件①）。
//
// 站规「预存红治理=回归挖矿」要求：红必须登记、新红必须拦、已愈红必须退役。
// 此前清单只存在于 workspace memory 与派单板（跨会话/跨线一致性靠人），
// 本脚本把它落成 in-repo 权威源 scripts/known-failures.json，并在读取
// `go test -json` 输出时做三类判定：
//
//   UNREGISTERED_FAIL  失败且不在清单 → exit 1（新红阻断，防「新红被当预存红放行」）
//   REGISTERED_FAIL    失败且在清单   → 放行，逐条打印（防「已知红被当新回归重复排查」）
//   RECOVERED          清单条目本轮包已实际运行且未见失败 → 提示退役（棘轮向下）
//
// 用法：
//   go test -json ./... | node scripts/check-known-failures.mjs
//   node scripts/check-known-failures.mjs --from-file gotest.json
//   node scripts/check-known-failures.mjs --registry path/to/known-failures.json
//
// 语义边界（有意不做）：不做通配匹配（精确 package+test，含 "/" 子测试路径）；
// 不改 go test 本身；skip 视同「未见失败」参与 RECOVERED 提示（提示语义，不阻断）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const DEFAULT_REGISTRY = resolve(here, "known-failures.json");

export const EXIT_OK = 0;
export const EXIT_UNREGISTERED = 1;
export const EXIT_USAGE = 2;

/** 宽容解析 go test -json 文本：逐行 JSON，非 JSON 行忽略（build 输出可能混流）。 */
export function parseGoTestJSON(text) {
  const events = [];
  let skippedLines = 0;
  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed.startsWith("{")) {
      if (trimmed) skippedLines += 1;
      continue;
    }
    try {
      const event = JSON.parse(trimmed);
      if (event && typeof event.Action === "string") events.push(event);
    } catch {
      skippedLines += 1;
    }
  }
  return { events, skippedLines };
}

function entryKey(entry) {
  return `${entry.package}\x00${entry.test ?? ""}`;
}

/** 加载登记清单：缺文件/坏 JSON fail-closed（宁可报错不可静默放行）。 */
export function loadRegistry(path = DEFAULT_REGISTRY) {
  let raw;
  try {
    raw = readFileSync(path, "utf8");
  } catch (err) {
    throw new Error(`known-failures registry unreadable: ${path} (${err.message})`);
  }
  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new Error(`known-failures registry is not valid JSON: ${path} (${err.message})`);
  }
  const entries = Array.isArray(parsed.entries) ? parsed.entries : [];
  const seen = new Map();
  for (const entry of entries) {
    if (!entry || typeof entry.package !== "string" || !entry.package) {
      throw new Error(`known-failures registry entry missing "package": ${JSON.stringify(entry)}`);
    }
    const key = entryKey(entry);
    if (seen.has(key)) throw new Error(`known-failures registry duplicate entry: ${key.replace("\x00", " :: ")}`);
    seen.set(key, entry);
  }
  return { entries, seen };
}

/**
 * 三类判定。matched 记录命中的清单 key（供 RECOVERED 对减）；
 * ranPackages 收集本轮出现过 run（或 fail）事件的包——只有真实运行过的包
 * 才能谈「已愈」，没跑过的包不进 RECOVERED（防「没跑=绿」假象）。
 */
export function classify(events, registry) {
  const ranPackages = new Set();
  const failed = []; // {package, test|null}
  for (const event of events) {
    if (typeof event.Package !== "string" || !event.Package) continue;
    switch (event.Action) {
      case "run":
      case "fail":
        ranPackages.add(event.Package);
        break;
      default:
        break;
    }
    if (event.Action === "fail") {
      failed.push({ package: event.Package, test: typeof event.Test === "string" ? event.Test : null });
    }
  }
  const matched = new Set();
  const registered = [];
  const unregistered = [];
  for (const failure of failed) {
    const testKey = `${failure.package}\x00${failure.test ?? ""}`;
    const pkgKey = failure.test === null ? `${failure.package}\x00` : null;
    if (registry.seen.has(testKey) || (pkgKey !== null && registry.seen.has(pkgKey))) {
      matched.add(registry.seen.has(testKey) ? testKey : pkgKey);
      registered.push(failure);
    } else {
      unregistered.push(failure);
    }
  }
  const recovered = registry.entries
    .map((entry) => ({ entry, key: entryKey(entry) }))
    .filter(({ entry, key }) => !matched.has(key) && ranPackages.has(entry.package))
    .map(({ entry }) => entry);
  return { registered, unregistered, recovered, ranPackages };
}

function formatFailure(failure) {
  return failure.test ? `${failure.package} :: ${failure.test}` : `${failure.package} :: (package-level)`;
}

function main(argv) {
  let fromFile = null;
  let registryPath = DEFAULT_REGISTRY;
  for (let i = 0; i < argv.length; i += 1) {
    if (argv[i] === "--from-file") fromFile = argv[++i] ?? null;
    else if (argv[i] === "--registry") registryPath = argv[++i] ?? null;
    else {
      process.stderr.write(`check-known-failures: unknown argument ${argv[i]}\n`);
      process.exitCode = EXIT_USAGE;
      return;
    }
  }
  if (fromFile === null) {
    process.stderr.write("check-known-failures: stdin intake not supported; pass --from-file <go-test-json>\n");
    process.exitCode = EXIT_USAGE;
    return;
  }
  let text;
  try {
    text = readFileSync(fromFile, "utf8");
  } catch (err) {
    process.stderr.write(`check-known-failures: cannot read input: ${err.message}\n`);
    process.exitCode = EXIT_USAGE;
    return;
  }
  let registry;
  try {
    registry = loadRegistry(registryPath);
  } catch (err) {
    process.stderr.write(`check-known-failures: ${err.message}\n`);
    process.exitCode = EXIT_USAGE;
    return;
  }
  const { events, skippedLines } = parseGoTestJSON(text);
  const { registered, unregistered, recovered } = classify(events, registry);

  for (const failure of registered) process.stdout.write(`  REGISTERED_FAIL  ${formatFailure(failure)}\n`);
  for (const entry of recovered) {
    const label = entry.test ? `${entry.package} :: ${entry.test}` : `${entry.package} :: (package-level)`;
    process.stdout.write(`  RECOVERED (candidate for retirement): ${label} registered=${entry.registered ?? "?"} source=${entry.source ?? "?"}\n`);
  }
  if (skippedLines) process.stdout.write(`  (ignored ${skippedLines} non-JSON line(s))\n`);
  process.stdout.write(
    `  summary: registered_fail=${registered.length} unregistered_fail=${unregistered.length} recovered=${recovered.length} registry=${registry.entries.length}\n`,
  );
  if (unregistered.length) {
    for (const failure of unregistered) process.stderr.write(`  UNREGISTERED_FAIL  ${formatFailure(failure)}\n`);
    process.stderr.write("  fail: unregistered failures present — register in scripts/known-failures.json or fix before gating\n");
    process.exitCode = EXIT_UNREGISTERED;
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2));
}
