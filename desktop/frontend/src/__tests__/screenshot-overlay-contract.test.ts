// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/screenshot-overlay-contract.test.ts
//
// tl 1087: source-contract coverage for the parts of the screenshot overlay
// that need a real 2d context and pixel geometry — which jsdom cannot host,
// so composer-screenshot.test.tsx covers only the wiring. What is pinned here
// is the flow the user asked for, as it appears in the component source: the
// four tools, the undo/redo stack hookup, double-click confirm, Ctrl+S save,
// Esc cancel, the selection-size readout, and the crop-to-selection export.

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

const here = dirname(fileURLToPath(import.meta.url));
const overlaySource = readFileSync(resolve(here, "../components/ScreenshotOverlay.tsx"), "utf8");
const modelSource = readFileSync(resolve(here, "../lib/screenshotAnnotation.ts"), "utf8");
const composerSource = readFileSync(resolve(here, "../components/Composer.tsx"), "utf8");
const backendSource = readFileSync(resolve(here, "../../../screenshot_app.go"), "utf8");

console.log("\ntl 1087 screenshot overlay contract");

ok(
  /SCREENSHOT_TOOLS/.test(overlaySource) && /"rect" \| "arrow" \| "text" \| "mosaic"/.test(modelSource),
  "the four tl 1087 tools are the shipped tool list (rect/arrow/text/mosaic)",
);

ok(
  /onDoubleClick=\{\(\) => \{[\s\S]*?if \(selectionRef\.current\) void confirm\(\)/.test(overlaySource),
  "double-clicking the selection confirms (attaches the image)",
);

ok(
  /event\.key\.toLowerCase\(\) === "s"/.test(overlaySource) && /saveToFile\(\)/.test(overlaySource),
  "Ctrl+S opens the save-to-file flow before confirming",
);

ok(
  /event\.key === "Escape"[\s\S]*?onClose\(\)/.test(overlaySource),
  "Escape cancels the overlay",
);

ok(
  /doUndo\(\)|undoAnnotation/.test(overlaySource) && /doRedo\(\)|redoAnnotation/.test(overlaySource),
  "the toolbar wires undo and redo to the history stack",
);

ok(
  /hitTestSelectionHandle/.test(overlaySource) && /resizeSelectionByHandle/.test(overlaySource),
  "selection handles hit-test and resize through the pure model",
);

ok(
  /Math\.round\(selection\.width\).*×.*Math\.round\(selection\.height\)/s.test(overlaySource),
  "the selection shows a live pixel size readout",
);

ok(
  /drawImage\(region, 0, 0\)/.test(overlaySource) && /screenshotRegionToCanvas/.test(overlaySource),
  "the export crops to the selection instead of shipping the whole screen",
);

ok(
  /SaveScreenshotAttachment\(dataUrl\)/.test(overlaySource),
  "confirm saves through the workspace attachment link (no clipboard)",
);

ok(
  /ReleaseScreenshotCapture/.test(overlaySource),
  "the backend capture temp file is released when the overlay closes",
);

ok(
  /CaptureScreenshotAtCursor\(\)/.test(overlaySource),
  "the capture is grabbed from the backend at the cursor's monitor (tl 1087 plan A)",
);

console.log("\ntl 1087 composer screenshot wiring");

ok(
  /composer-screenshot-trigger/.test(composerSource) && /setScreenshotOverlayOpen\(true\)/.test(composerSource),
  "the composer toolbar button opens the overlay",
);

ok(
  /\{attachmentInputEnabled && !heroMode && \(\s*<div className="composer-meta__control composer-meta__control--screenshot">/.test(composerSource),
  "the screenshot button is gated by attachmentInputEnabled and hidden in hero mode",
);

ok(
  /e\.key === "Escape" && screenshotOverlayOpen/.test(composerSource),
  "Escape is routed to the overlay, never to the composer's stop shortcut",
);

console.log("\ntl 1087 backend screenshot contract");

ok(
  /func \(a \*App\) CaptureScreenshotAtCursor\(\)/.test(backendSource) &&
    /func \(a \*App\) ReleaseScreenshotCapture\(/.test(backendSource) &&
    /func \(a \*App\) SaveScreenshotAttachment\(/.test(backendSource) &&
    /func \(a \*App\) SaveScreenshotToPath\(/.test(backendSource) &&
    /func \(a \*App\) PickScreenshotExportFile\(/.test(backendSource),
  "backend exposes capture/release/attachment-save/file-save bindings",
);

ok(
  /reasonix-screenshot-/.test(backendSource),
  "the temp-dir guard prefix matches what CaptureScreenshotAtCursor creates",
);

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
