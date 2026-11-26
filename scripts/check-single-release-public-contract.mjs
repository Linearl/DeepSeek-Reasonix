#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";

// tl1824：扫描面=活公共文档（README + docs 顶层 md）。归档区 docs/_archive/**
// （含归档 Astro 站与失效文档）不是公共契约面，显式排除；git pathspec 的
// * 会跨目录，故用 :(glob) 限定顶层 + :(exclude) 排除归档。原
// site/src/pages 两条目随 site/ 归档（tl1824）一并移除。
const files = execFileSync("git", ["ls-files", "README.md", ":(glob)docs/*.md", ":(exclude)docs/_archive/**"], {
  encoding: "utf8",
}).trim().split("\n").filter(Boolean);
// SIGNPATH_WINDOWS_ADMIN_SOP 条目随该文档归档（tl1824）移除；其余保持不变。
const migrationReferences = new Set([
  "docs/RELEASING.md",
  "docs/CLI.md",
  "docs/CLI.zh-CN.md",
]);
const rules = [
  [/release-channel-switch/, "public release channel selector"],
  [/reasonix upgrade (?:preview|stable)\b/i, "deprecated channel install command"],
  [/[?&]channel=(?:preview|canary)\b/i, "public channel deep link"],
  [/npm (?:i|install).*(?:@canary|@next)\b/i, "public prerelease npm install"],
  [/\bStable 1\.x\b|· stable\b|current 1\.x stable\b/i, "public Stable channel label"],
];
const failures = [];
for (const file of files) {
  if (migrationReferences.has(file) || file.includes("/changelog/")) continue;
  const source = readFileSync(file, "utf8");
  for (const [pattern, description] of rules) {
    if (pattern.test(source)) failures.push(`${file}: ${description}`);
  }
}
if (failures.length) {
  console.error(`single-release public contract failed:\n${failures.join("\n")}`);
  process.exit(1);
}
console.log(`single-release public contract OK (${files.length} files checked)`);
