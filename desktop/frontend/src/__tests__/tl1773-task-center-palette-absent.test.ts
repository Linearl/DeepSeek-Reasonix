// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1773-task-center-palette-absent.test.ts
// Source-contract test (tl1773): the command palette "Task Center" entry is
// hidden for a one-month observation window (expires 2026-12-23). The entry
// row itself is removed from App.tsx's live paletteItems; the underlying
// setTasksOpen / task panel implementation and the palette.cmd.taskCenter
// locale keys (zh / zh-TW / en) are intentionally kept for the window — a
// later cleanup step removes them together with this pin if no user feedback
// arrives. Re-adding the entry = revert the tl1773 deletion commit.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntl1773 task-center palette entry (observation window)");
const here = dirname(fileURLToPath(import.meta.url));
const appSource = readFileSync(resolve(here, "../App.tsx"), "utf8");
const locales = ["en", "zh", "zh-TW"].map((name) =>
  readFileSync(resolve(here, `../locales/${name}.ts`), "utf8"),
);

// Entry absent from the live palette: no cmd-task-center row anywhere in
// App.tsx (the palette is built inline in paletteItems; usePaletteCommands.tsx
// keeps its copy but that composition root is unmounted dead code — covered by
// the tl1773 observation window, not pinned here).
ok(!/cmd-task-center/.test(appSource), "palette entry cmd-task-center absent from App.tsx");

// Underlying implementation stays untouched: the task panel wiring must not
// be cleaned up during the observation window.
ok(/setTasksOpen\(/.test(appSource), "setTasksOpen implementation retained in App.tsx");

// Locale keys retained on purpose (tl1773 observation window): a cleanup step
// must delete these together with this pin, never silently.
for (const [index, source] of locales.entries()) {
  ok(/"palette\.cmd\.taskCenter"/.test(source), `palette.cmd.taskCenter key retained in locale #${index + 1}`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
