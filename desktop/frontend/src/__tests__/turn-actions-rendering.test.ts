// Run: tsx src/__tests__/turn-actions-rendering.test.ts

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
// tl1191: mode-scoped rules live in src/styles/mode-*.css now.
const styles = [
  readFileSync(resolve(testDir, "../styles.css"), "utf8"),
  readFileSync(resolve(testDir, "../styles/mode-classic.css"), "utf8"),
  readFileSync(resolve(testDir, "../styles/mode-workbench.css"), "utf8"),
].join("\n");
const transcriptSource = readFileSync(resolve(testDir, "../components/Transcript.tsx"), "utf8");
const transcriptRowsSource = readFileSync(resolve(testDir, "../lib/transcriptRows.ts"), "utf8");
const messageSource = readFileSync(resolve(testDir, "../components/Message.tsx"), "utf8");

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nturn actions rendering");

ok(
  transcriptRowsSource.includes("model.actionText.trim() || options.hasCheckpointForTurn?.(turn)") &&
    transcriptSource.includes("hasCheckpointForTurn"),
  "checkpointed turns keep rewind actions visible even when cancellation produced no assistant text",
);

ok(
  messageSource.includes('disabled={!hasCopyText}') &&
    messageSource.includes('disabledLabel={t("turnActions.copyEmpty")}') &&
    messageSource.includes("const hasCopyText = text.trim() !== \"\";"),
  "task 630: the copy entry stays mounted on empty turns, disabled with an explicit reason",
);

ok(
  !messageSource.includes("text.trim() && <CopyButton text={text} label={t(\"msg.copy\")} />"),
  "task 630: an interrupted turn no longer loses the copy button while fork/rewind stay visible",
);

function ruleBody(selector: string): string {
  const ruleStart = styles.indexOf(`${selector} {`);
  if (ruleStart < 0) return "";
  const bodyStart = styles.indexOf("{", ruleStart) + 1;
  const bodyEnd = styles.indexOf("\n}", bodyStart);
  return bodyEnd < 0 ? "" : styles.slice(bodyStart, bodyEnd);
}

const turnActionInlineLabelRule = ruleBody(".turn-actions__label-inline");
const turnActionInlineLabelCount = messageSource.match(/className="turn-actions__label-inline"/g)?.length ?? 0;

ok(
  turnActionInlineLabelCount === 3 &&
    /max-width:\s*0;/.test(turnActionInlineLabelRule) &&
    /overflow:\s*hidden;/.test(turnActionInlineLabelRule) &&
    /transition:\s*max-width 0\.12s;/.test(turnActionInlineLabelRule),
  "fork, compress, and rewind labels share the copy action's collapsed inline-label contract",
);

ok(
  styles.includes(".turn-actions__btn:hover .turn-actions__label-inline,") &&
    styles.includes(".turn-actions__btn:focus-visible .turn-actions__label-inline,") &&
    styles.includes(".turn-actions__group--open .turn-actions__label-inline,") &&
    styles.includes(".turn-actions__btn--confirm .turn-actions__label-inline {") &&
    styles.includes("max-width: 240px;"),
  "turn action labels expand on hover and keyboard focus and stay visible for open or confirming actions",
);

const windowsPrimaryTranscriptSelector =
  ':root[data-platform="windows"] .app:not(.app--creation) .chat-pane > .main > .transcript-shell > .transcript';
const windowsThemeTranscriptSelector =
  ':root[data-platform="windows"][data-theme-pack][data-theme-has-bg="true"] .app:not(.app--creation) .chat-pane > .main > .transcript-shell > .transcript';
const windowsTaskTranscriptSelector =
  ':root[data-platform="windows"][data-theme-pack][data-theme-has-bg="true"][data-theme-scene="task"] .app:not(.app--creation) .chat-pane > .main > .transcript-shell > .transcript';
const windowsTaskLeftTranscriptSelector =
  ':root[data-platform="windows"][data-theme-pack][data-theme-has-bg="true"][data-theme-scene="task"][data-theme-safe-area="left"] .app:not(.app--creation) .chat-pane > .main > .transcript-shell > .transcript';
const windowsTaskRightTranscriptSelector =
  ':root[data-platform="windows"][data-theme-pack][data-theme-has-bg="true"][data-theme-scene="task"][data-theme-safe-area="right"] .app:not(.app--creation) .chat-pane > .main > .transcript-shell > .transcript';
const windowsTranscriptRule = ruleBody(windowsPrimaryTranscriptSelector);
const windowsThemeTranscriptRule = ruleBody(windowsThemeTranscriptSelector);
const windowsThemeImageRule = ruleBody(`${windowsThemeTranscriptSelector}::before`);
const windowsThemeOverlayRule = ruleBody(`${windowsThemeTranscriptSelector}::after`);
const windowsThemeContentRule = ruleBody(`${windowsThemeTranscriptSelector} > *`);
const windowsTaskTranscriptRule = ruleBody(windowsTaskTranscriptSelector);
const windowsTaskLeftTranscriptRule = ruleBody(windowsTaskLeftTranscriptSelector);
const windowsTaskRightTranscriptRule = ruleBody(windowsTaskRightTranscriptSelector);
const lastTransparentTranscriptRule = styles.lastIndexOf(
  '.app:not(.app--creation) .transcript-shell {\n  background: transparent !important;',
);
const windowsTranscriptRuleIndex = styles.indexOf(`${windowsPrimaryTranscriptSelector} {`);

// tl1450 finding: the #7011/#6359 Windows repaint-backing family (plus the
// theme-bg transparency rules it must beat) was stranded inside
// mode-creation.css by the tl1191 splitter (mixed-block misjudgment — same
// failure class as tl1277). The :not(.app--creation) tokens made them LOOK
// creation-gated, but they target classic/workbench. Because per-mode loading
// never sends that sheet to classic/workbench, the primary backing has NOT
// applied in production since tl1191 — deleting the sheet changes nothing.
// The theme-pack variants (styles.css 44xxx) stayed live. Restoring the
// family to the shared layer ACTIVATES dormant rules = a visual change that
// needs its own user-ratified task; until then this pin records reality and
// turns red the moment the family re-enters the shared layer un-reviewed.
ok(
  !styles.includes("background-color: Canvas !important;") ||
    !/:root\[data-platform="windows"\] \.app:not\(\.app--creation\) \.chat-pane > \.main > \.transcript-shell > \.transcript \{/.test(styles),
  "the dormant #7011 Windows repaint-backing family stays out of the shared layer until its restoration task lands (tl1450 finding)",
);

ok(
  /background-color:\s*Canvas\s*!important;/.test(windowsThemeTranscriptRule) &&
    /background-image:\s*linear-gradient\(var\(--bg\),\s*var\(--bg\)\)\s*!important;/.test(windowsThemeTranscriptRule) &&
    /position:\s*fixed;/.test(windowsThemeImageRule) &&
    /inset:\s*-4%;/.test(windowsThemeImageRule) &&
    /z-index:\s*var\(--z-transcript-scene-image\);/.test(windowsThemeImageRule) &&
    /pointer-events:\s*none;/.test(windowsThemeImageRule) &&
    /background-image:\s*var\(--windows-transcript-scene-image\);/.test(windowsThemeImageRule) &&
    /background-size:\s*cover;/.test(windowsThemeImageRule) &&
    /background-position:\s*var\(--windows-transcript-scene-focus-x\)\s+var\(--windows-transcript-scene-focus-y\);/.test(windowsThemeImageRule) &&
    /opacity:\s*var\(--windows-transcript-image-opacity\);/.test(windowsThemeImageRule),
  "Windows theme image preserves the fixed -4% overscan above an opaque transcript base",
);

ok(
  /position:\s*fixed;/.test(windowsThemeOverlayRule) &&
    /inset:\s*0;/.test(windowsThemeOverlayRule) &&
    /z-index:\s*var\(--z-transcript-scene-overlay\);/.test(windowsThemeOverlayRule) &&
    /pointer-events:\s*none;/.test(windowsThemeOverlayRule) &&
    /var\(--windows-transcript-pane-alpha\)/.test(windowsThemeOverlayRule) &&
    /var\(--windows-transcript-scene-overlay\)/.test(windowsThemeOverlayRule) &&
    /background-size:\s*100%\s+100%,\s*100%\s+100%;/.test(windowsThemeOverlayRule),
  "Windows theme pane and safe-area wash preserve the viewport-sized overlay geometry",
);

const transcriptImageZ = Number(styles.match(/--z-transcript-scene-image:\s*(-?\d+);/)?.[1]);
const transcriptOverlayZ = Number(styles.match(/--z-transcript-scene-overlay:\s*(-?\d+);/)?.[1]);
const transcriptContentZ = Number(styles.match(/--z-transcript-content:\s*(-?\d+);/)?.[1]);

ok(
  transcriptImageZ >= 0 &&
    transcriptOverlayZ > transcriptImageZ &&
    transcriptContentZ > transcriptOverlayZ &&
    /position:\s*relative;/.test(windowsThemeContentRule) &&
    /z-index:\s*var\(--z-transcript-content\);/.test(windowsThemeContentRule),
  "Windows theme layers paint above the opaque backing and below transcript content",
);

ok(
  /--windows-transcript-scene-image:\s*var\(--theme-bg-home-image/.test(windowsThemeTranscriptRule) &&
    /--windows-transcript-image-opacity:\s*var\(--theme-bg-home-opacity/.test(windowsThemeTranscriptRule) &&
    /--windows-transcript-pane-alpha:\s*var\(--theme-pane-alpha/.test(windowsThemeTranscriptRule) &&
    /--windows-transcript-scene-image:\s*var\(--theme-bg-task-image/.test(windowsTaskTranscriptRule) &&
    /--windows-transcript-image-opacity:\s*var\(--theme-bg-task-opacity/.test(windowsTaskTranscriptRule) &&
    /--windows-transcript-pane-alpha:\s*var\(--theme-pane-task-alpha/.test(windowsTaskTranscriptRule),
  "Windows home and task scenes use their matching image, opacity, and pane tokens",
);

ok(
  /radial-gradient\(/.test(windowsTaskTranscriptRule) &&
    /linear-gradient\(\s*90deg,/.test(windowsTaskLeftTranscriptRule) &&
    /linear-gradient\(\s*270deg,/.test(windowsTaskRightTranscriptRule) &&
    /--windows-transcript-scene-overlay:/.test(windowsTaskTranscriptRule) &&
    /--windows-transcript-scene-overlay:/.test(windowsTaskLeftTranscriptRule) &&
    /--windows-transcript-scene-overlay:/.test(windowsTaskRightTranscriptRule),
  "Windows task scene preserves center, left, and right safe-area washes",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
