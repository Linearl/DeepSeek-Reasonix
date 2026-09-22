#!/usr/bin/env node
// ui-verify.mjs — the executable half of the task 233 verification pipeline
// (batch 3). The agent drives UI verification itself: it reads the checklist,
// runs the tool sequence each item names (screenshot / ui_interact / log
// reads / readouts), writes one result row per item into the result file,
// and this script keeps the mechanics honest:
//
//   init      parse a checklist markdown and emit a result-file skeleton
//             with an evidence directory per item
//   log       filter a desktop.log by time window and message patterns
//             (the 190/196/232-family readouts: hydrate reasons, vetos,
//             switch timings) — the "log readout" leg of the tripod
//   summarize count the status enums in a result file and print the
//             conclusion line the batch report needs
//
// The agent remains the executor; this script never runs the UI itself.

import { readdirSync, readFileSync, statSync, writeFileSync, mkdirSync, existsSync } from "node:fs";
import { join, resolve } from "node:path";

const [, , command, ...rest] = process.argv;

function fail(message) {
  process.stderr.write(`ui-verify: ${message}\n`);
  process.exitCode = 1;
}

function parseArgs(pairs) {
  const out = {};
  for (let i = 0; i < pairs.length; i += 1) {
    const arg = pairs[i];
    if (!arg.startsWith("--")) continue;
    const key = arg.slice(2);
    const next = pairs[i + 1];
    if (next === undefined || next.startsWith("--")) {
      out[key] = true;
    } else {
      out[key] = next;
      i += 1;
    }
  }
  return out;
}

// ── init: checklist -> result skeleton ──────────────────────────────────────

function splitTableRow(line) {
  return line.trim().replace(/^\|/, "").replace(/\|$/, "").split("|").map((c) => c.trim());
}

function looksLikeItemRow(cells) {
  if (cells.length < 3) return false;
  // Item rows lead with a label like "5. xxx", "12. xxx", "A. xxx" or "D. xxx".
  return /^\d+[.、)]\s*\S/.test(cells[0]) || /^[A-Z][.、)]\s*\S/.test(cells[0]);
}

function cmdInit(listFile, resultFile, opts) {
  if (!existsSync(listFile)) return fail(`checklist not found: ${listFile}`);
  const lines = readFileSync(listFile, "utf8").split(/\r?\n/);
  const evidenceDir = resolve(opts["evidence-dir"] ?? "tasks/evidence-ui-verify");
  mkdirSync(evidenceDir, { recursive: true });

  const items = [];
  for (const line of lines) {
    // Checklist tables often live inside a blockquote ("> | ... |") — strip
    // the quote marker before the table-cell check.
    const bare = line.trim().replace(/^(>+)\s*/, "");
    if (!bare.startsWith("|")) continue;
    const cells = splitTableRow(bare);
    if (!looksLikeItemRow(cells)) continue;
    items.push({
      label: cells[0],
      title: cells[0].replace(/^\d+[.、)]\s*/, "").replace(/^[A-Z][.、)]\s*/, ""),
      status: "⏳",
      evidence: "",
    });
  }
  if (items.length === 0) return fail("no item rows found (expected table rows whose first column starts with 'N.' or 'X.')");

  const stamp = new Date().toISOString().replace(/[-:]/g, "").slice(0, 13);
  const out = [
    `# UI 验证结果（${stamp}，agent 流水线执行 · task 233 批三）`,
    "",
    `- 清单：${resolve(listFile)}`,
    `- 证据目录：${evidenceDir}`,
    `- 状态枚举：✅ agent 自验通过 / ⏳ 待复测 / 👤 用户测 / ❌ 失败（附现象）`,
    "",
    "| 项 | 状态 | 证据（截图/日志行/读数路径） |",
    "|---|---|---|",
    ...items.map((item) => `| ${item.label} | ${item.status} | ${item.evidence} |`),
    "",
    "<!-- 执行循环：逐项按清单的触发路径调用工具（screenshot/ui_interact/log 读数），",
    "     把证据文件放入证据目录并在证据列写文件名或日志行，最后跑 summarize 出结论行。 -->",
    "",
  ].join("\n");
  writeFileSync(resultFile, out, "utf8");
  process.stdout.write(`init: ${items.length} items -> ${resultFile} (evidence: ${evidenceDir})\n`);
}

// ── log: time-window + pattern filter over desktop.log ─────────────────────

function findLogFiles(logPath) {
  const files = [];
  if (statSync(logPath).isDirectory()) {
    for (const name of readdirSync(logPath)) {
      if (name.startsWith("desktop.log")) files.push(join(logPath, name));
    }
    files.sort();
  } else {
    files.push(logPath);
  }
  return files;
}

function cmdLog(logPath, opts) {
  const patterns = String(opts.patterns ?? "")
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean);
  if (patterns.length === 0) return fail("log needs --patterns (comma-separated substrings)");
  if (!existsSync(logPath)) return fail(`log not found: ${logPath}`);

  const since = opts.since ? String(opts.since) : "";
  const until = opts.until ? String(opts.until) : "";
  const files = findLogFiles(logPath);
  const counts = Object.fromEntries(patterns.map((p) => [p, 0]));
  let printed = 0;
  for (const file of files) {
    const lines = readFileSync(file, "utf8").split(/\r?\n/);
    for (const line of lines) {
      const timeMatch = line.match(/time=(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2})?)/);
      const ts = timeMatch ? timeMatch[1] : "";
      if (since && !ts.startsWith(since) && ts > "" && ts < since) continue;
      if (until && ts > until) continue;
      const hit = patterns.find((p) => line.includes(p));
      if (!hit) continue;
      counts[hit] += 1;
      if (printed < 200) {
        process.stdout.write(`${file.split(/[\\/]/).pop()}: ${line.trim()}\n`);
        printed += 1;
      }
    }
  }
  process.stdout.write(`\ncounts: ${JSON.stringify(counts)}\n`);
}

// ── summarize: status enums -> conclusion line ──────────────────────────────

function cmdSummarize(resultFile) {
  if (!existsSync(resultFile)) return fail(`result file not found: ${resultFile}`);
  const lines = readFileSync(resultFile, "utf8").split(/\r?\n/);
  const counts = { "✅": 0, "⏳": 0, "👤": 0, "❌": 0 };
  for (const line of lines) {
    if (!line.trim().startsWith("|")) continue;
    const cells = splitTableRow(line);
    const status = cells.find((c) => c in counts);
    if (status) counts[status] += 1;
  }
  const total = counts["✅"] + counts["⏳"] + counts["👤"] + counts["❌"];
  process.stdout.write(
    `结论：${total} 项中 ${counts["✅"]} 自验通过、${counts["⏳"]} 待复测、${counts["👤"]} 用户测、${counts["❌"]} 失败。\n`,
  );
  if (counts["❌"] > 0) process.exitCode = 1;
}

// ── dispatch ────────────────────────────────────────────────────────────────

function main() {
  const opts = parseArgs(rest);
  switch (command) {
    case "init":
      if (rest.length < 2) return fail("usage: init <checklist.md> <result.md> [--evidence-dir dir]");
      cmdInit(rest[0], rest[1], opts);
      break;
    case "log":
      if (rest.length < 1) return fail("usage: log <desktop.log-or-dir> --patterns a,b [--since ts] [--until ts]");
      cmdLog(rest[0], opts);
      break;
    case "summarize":
      if (rest.length < 1) return fail("usage: summarize <result.md>");
      cmdSummarize(rest[0]);
      break;
    default:
      fail("usage: ui-verify.mjs <init|log|summarize> ...");
  }
}

main();
