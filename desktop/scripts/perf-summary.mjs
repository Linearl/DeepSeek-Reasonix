#!/usr/bin/env node
// One-line summary of a host performance series (task 184).
//
//   node desktop/scripts/perf-summary.mjs <logs/perf dir>            # every file
//   node desktop/scripts/perf-summary.mjs <logs/perf dir> <one.jsonl>
//
// Prints: sample count and span, the memory trend (first -> last, min/max, MB/h),
// the disk trend for the two session roots and the append-only event logs, the
// fastest-growing watched files, and how many samples were heartbeats.
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

const dir = process.argv[2];
if (!dir) {
  console.error("usage: node perf-summary.mjs <logs/perf dir> [file.jsonl]");
  process.exit(2);
}

function seriesFiles() {
  const explicit = process.argv[3];
  if (explicit) return [explicit.startsWith("/") || explicit.includes(":") ? explicit : join(dir, explicit)];
  return readdirSync(dir)
    .filter((name) => name.startsWith("perf-sample-") && name.endsWith(".jsonl"))
    .sort()
    .map((name) => join(dir, name));
}

function readSamples(paths) {
  const samples = [];
  for (const path of paths) {
    for (const line of readFileSync(path, "utf8").split("\n")) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      try {
        samples.push(JSON.parse(trimmed));
      } catch {
        // A torn last line after a crash: skip it, the series is still usable.
      }
    }
  }
  return samples;
}

const paths = seriesFiles();
if (paths.length === 0) {
  console.error(`perf-summary: no perf-sample-*.jsonl under ${dir}`);
  process.exit(1);
}
const samples = readSamples(paths);
if (samples.length === 0) {
  console.error("perf-summary: series is empty");
  process.exit(1);
}

const first = samples[0];
const last = samples[samples.length - 1];
const spanSeconds = Math.max(1, new Date(last.ts) - new Date(first.ts)) / 1000;
const perHour = (delta) => (delta / spanSeconds) * 3600;

function trend(label, key, digits = 0) {
  const values = samples.map((s) => Number(s[key] ?? 0));
  const min = Math.min(...values);
  const max = Math.max(...values);
  const from = values[0];
  const to = values[values.length - 1];
  const unit = digits === 0 ? "MB" : "";
  console.log(
    `${label.padEnd(14)} ${from.toFixed(digits)} -> ${to.toFixed(digits)}${unit}` +
      ` (min ${min.toFixed(digits)} max ${max.toFixed(digits)}, ${perHour(to - from) >= 0 ? "+" : ""}${perHour(to - from).toFixed(1)}${unit}/h)`,
  );
}

const heartbeats = samples.filter((s) => s.heartbeat).length;
console.log(`samples        ${samples.length} over ${(spanSeconds / 60).toFixed(1)} min (${heartbeats} heartbeats)`);
console.log(`window         ${first.ts} -> ${last.ts}`);
console.log(`paths          ${paths.length} file(s) under ${dir}`);

trend("workingSet", "workingSetMb", 1);
trend("private", "privateMb", 1);
trend("heapInuse", "heapInuseMb", 1);
trend("storeV4", "storeMb", 1);
trend("projects", "projectsMb", 1);
trend("eventsLogs", "eventsMb", 1);

const buckets = samples.filter((s) => s.v4Operations !== undefined).map((s) => s.v4Operations);
if (buckets.length > 0) {
  console.log(
    `v4Operations   ${buckets[0]} -> ${buckets[buckets.length - 1]} (max ${Math.max(...buckets)})`,
  );
}

// Files that actually grew: compare the first and last sample that carried a
// file table (heartbeats omit it).
const withFiles = samples.filter((s) => s.filesKb && Object.keys(s.filesKb).length > 0);
if (withFiles.length >= 2) {
  const head = withFiles[0].filesKb;
  const tail = withFiles[withFiles.length - 1].filesKb;
  const growth = [];
  for (const [path, size] of Object.entries(tail)) {
    const before = head[path] ?? size;
    const deltaMB = (size - before) / 1024;
    growth.push({ path, deltaMB, sizeMB: size / 1024 });
  }
  growth.sort((a, b) => b.deltaMB - a.deltaMB);
  console.log("top growth:");
  for (const row of growth.slice(0, 5)) {
    console.log(`  ${row.deltaMB >= 0 ? "+" : ""}${row.deltaMB.toFixed(1)} MB -> ${row.sizeMB.toFixed(1)} MB  ${row.path}`);
  }
}

// A series is only trustworthy if it is still being written: say how fresh it is.
const newest = paths.map((p) => statSync(p).mtimeMs).sort((a, b) => b - a)[0];
console.log(`last write     ${((Date.now() - newest) / 1000).toFixed(0)}s ago`);
