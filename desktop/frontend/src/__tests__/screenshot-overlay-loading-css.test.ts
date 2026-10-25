// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/screenshot-overlay-loading-css.test.ts
//
// tl 1130 capture-race guard, static contract: while the screenshot overlay
// waits for the Go capture it mounts with data-capture-ready="loading" and a
// `--shadow-color: #000` full-bleed background. If that frame reaches the
// screen before the backend capture runs, the capture photographs the
// overlay's own black frame as the desktop (the user-reported all-black
// overlay). The fix hides the overlay until the capture is ready, so the
// contract below pins both halves:
//   1. ScreenshotOverlay really emits the loading attribute value;
//   2. styles.css really hides [data-capture-ready="loading"] with
//      display:none (visibility/opacity would still composite the black
//      frame; only display:none removes it from the rendered output).

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

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

const srcRoot = resolve(import.meta.dirname, "..");
const overlaySource = readFileSync(resolve(srcRoot, "components", "ScreenshotOverlay.tsx"), "utf8");
const stylesSource = readFileSync(resolve(srcRoot, "styles.css"), "utf8");

console.log("\ntl 1130 overlay loading-state source contract");
ok(
  overlaySource.includes('captureError ? "error" : "loading"'),
  'ScreenshotOverlay emits data-capture-ready="loading" while the capture is in flight',
);

console.log("\ntl 1130 overlay loading-state stylesheet contract");
const rule = /\.screenshot-overlay\[data-capture-ready="loading"\]\s*\{[^}]*\}/.exec(stylesSource);
ok(rule !== null, "styles.css carries a [data-capture-ready=loading] rule for .screenshot-overlay");
ok(
  rule !== null && /display:\s*none/.test(rule[0]),
  "the loading rule hides the overlay with display:none (not visibility/opacity, which would still composite the black frame)",
);

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
