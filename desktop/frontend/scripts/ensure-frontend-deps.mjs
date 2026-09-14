#!/usr/bin/env node
// frontend:install for wails.json.
//
// The stock value is `pnpm install`, which wails runs on every build. On an unchanged
// dependency set that is tens of seconds of network validation for no effect, and it dominates
// the front half of a local build.
//
// This decides instead: if the installed tree is present and newer than both the manifest and
// the lockfile, there is nothing to install and we exit 0. Otherwise it runs the same command
// wails.json used before, with the same flag.
//
// Deliberately permissive about what counts as "installed": we check for the pnpm store marker
// and for mtimes, not for a full integrity proof. A stale tree that slips through produces a
// build error, which is noisy and obvious, whereas a false "needs install" only costs time.

import { execFileSync } from "node:child_process";
import { existsSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const frontendRoot = resolve(here, "..");
const manifest = join(frontendRoot, "package.json");
const lockfile = join(frontendRoot, "pnpm-lock.yaml");
// .pnpm is the content-addressed store layout pnpm always creates; .modules.yaml is written
// last, so its presence means the tree finished being laid down.
const installedMarker = join(frontendRoot, "node_modules", ".modules.yaml");

function mtime(path) {
  try {
    return statSync(path).mtimeMs;
  } catch {
    return 0;
  }
}

function needsInstall() {
  if (!existsSync(installedMarker)) return "node_modules is missing or incomplete";
  const installedAt = mtime(installedMarker);
  for (const [label, path] of [["package.json", manifest], ["pnpm-lock.yaml", lockfile]]) {
    const stamp = mtime(path);
    if (stamp === 0) continue; // no lockfile at all: nothing to compare against
    if (stamp > installedAt) return `${label} is newer than the installed tree`;
  }
  return null;
}

const reason = needsInstall();
if (reason === null) {
  process.stdout.write("frontend dependencies are current; skipping install\n");
  process.exit(0);
}

process.stdout.write(`installing frontend dependencies (${reason})\n`);
try {
  execFileSync("pnpm", ["install", "--config.confirmModulesPurge=false"], {
    cwd: frontendRoot,
    stdio: "inherit",
  });
} catch (err) {
  process.stderr.write(`pnpm install failed: ${err.message}\n`);
  process.exit(1);
}
