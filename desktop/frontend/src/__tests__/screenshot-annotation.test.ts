// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/screenshot-annotation.test.ts
//
// tl 1087: pure annotation-model coverage for the screenshot overlay —
// selection normalization/clamping, the eight-handle resize algebra, the
// undo/redo stack's linear-history rule, and the arrow head geometry. The
// DOM/canvas side of the overlay lives in composer-screenshot.test.tsx.

import {
  ANNOTATION_HISTORY_LIMIT,
  arrowHeadPoints,
  buildAnnotation,
  canRedo,
  canUndo,
  clampSelectionRect,
  emptyHistory,
  handlePositions,
  hitTestSelectionHandle,
  liveAnnotations,
  normalizeSelection,
  pushAnnotation,
  redoAnnotation,
  resizeSelectionByHandle,
  selectionContains,
  undoAnnotation,
  type Annotation,
  type AnnotationPoint,
} from "../lib/screenshotAnnotation";

let passed = 0;
let failed = 0;

function eq(actual: unknown, expected: unknown, label: string) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const BOUNDS = { width: 1920, height: 1080 };

console.log("\ntl 1087 selection normalization");

{
  // Drag up-left: x/y come from the smaller extremes, width/height stay positive.
  const rect = normalizeSelection({ x: 400, y: 300 }, { x: 100, y: 50 }, BOUNDS);
  eq(rect, { x: 100, y: 50, width: 300, height: 250 }, "drag up-left normalizes to positive width/height");
}

{
  // Drag down-right: the origin is the start point.
  const rect = normalizeSelection({ x: 100, y: 50 }, { x: 400, y: 250 }, BOUNDS);
  eq(rect, { x: 100, y: 50, width: 300, height: 200 }, "drag down-right keeps the start as origin");
}

{
  // Clamped to the screenshot: a selection reaching past the right edge stops
  // at the bound instead of overshooting into negative-coordinate space.
  const rect = normalizeSelection({ x: 1800, y: 1000 }, { x: 2100, y: 1300 }, BOUNDS);
  eq(rect, { x: 1800, y: 1000, width: 120, height: 80 }, "selection clamps to the screenshot bounds");
}

{
  // A degenerate drag (single point) stays a valid zero-area rect, not NaN.
  const rect = normalizeSelection({ x: 10, y: 10 }, { x: 10, y: 10 }, BOUNDS);
  eq(rect, { x: 10, y: 10, width: 0, height: 0 }, "degenerate drag yields a zero-area rect");
}

console.log("\ntl 1087 selection resize by handle");

{
  // Dragging the se handle moves only the bottom-right corner.
  const base = { x: 100, y: 100, width: 200, height: 150 };
  const next = resizeSelectionByHandle(base, "se", { x: 400, y: 350 }, BOUNDS);
  eq(next, { x: 100, y: 100, width: 300, height: 250 }, "se handle moves the bottom-right corner");
}

{
  // Dragging the nw handle moves only the top-left corner.
  const base = { x: 100, y: 100, width: 200, height: 150 };
  const next = resizeSelectionByHandle(base, "nw", { x: 50, y: 40 }, BOUNDS);
  eq(next, { x: 50, y: 40, width: 250, height: 210 }, "nw handle moves the top-left corner");
}

{
  // Edge handles keep the perpendicular axis fixed.
  const base = { x: 100, y: 100, width: 200, height: 150 };
  const next = resizeSelectionByHandle(base, "e", { x: 500, y: 9999 }, BOUNDS);
  eq(next, { x: 100, y: 100, width: 400, height: 150 }, "e handle changes width only");
}

{
  // A handle drag cannot push the rect out of bounds.
  const base = { x: 100, y: 100, width: 200, height: 150 };
  const next = resizeSelectionByHandle(base, "se", { x: 5000, y: 5000 }, BOUNDS);
  eq(next, { x: 100, y: 100, width: 1820, height: 980 }, "handle drag clamps to the screenshot bounds");
}

{
  const base = { x: 100, y: 100, width: 200, height: 150 };
  const next = clampSelectionRect({ x: -50, y: -50, width: 4000, height: 4000 }, BOUNDS);
  ok(next.x >= 0 && next.y >= 0, "clampSelectionRect moves a negative origin back inside");
  ok(next.x + next.width <= BOUNDS.width && next.y + next.height <= BOUNDS.height, "clampSelectionRect keeps the far corner inside");
}

console.log("\ntl 1087 handle hit-testing");

{
  const base = { x: 100, y: 100, width: 200, height: 150 };
  const positions = handlePositions(base);
  eq(positions.se, { x: 300, y: 250 }, "se handle sits at the bottom-right corner");
  eq(positions.n, { x: 200, y: 100 }, "n handle sits at the top edge midpoint");
  ok(hitTestSelectionHandle(base, { x: 302, y: 248 }) === "se", "pointer near the se corner hits se");
  ok(hitTestSelectionHandle(base, { x: 200, y: 101 }) === "n", "pointer near the top midpoint hits n");
  ok(hitTestSelectionHandle(base, { x: 200, y: 175 }) === null, "pointer in the middle hits no handle");
  ok(selectionContains(base, { x: 200, y: 175 }), "pointer in the middle is inside the selection");
  ok(!selectionContains(base, { x: 50, y: 50 }), "pointer outside the rect is not inside");
}

console.log("\ntl 1087 undo/redo stack");

{
  let history = emptyHistory();
  eq(liveAnnotations(history).length, 0, "empty history has no live annotations");
  ok(!canUndo(history), "empty history cannot undo");
  ok(!canRedo(history), "empty history cannot redo");

  const a1 = buildAnnotation("rect", { x: 0, y: 0 }, { x: 10, y: 10 }, "#fff");
  const a2 = buildAnnotation("arrow", { x: 5, y: 5 }, { x: 50, y: 50 }, "#fff");
  const a3 = buildAnnotation("text", { x: 20, y: 20 }, { x: 80, y: 40 }, "#fff", "hi");

  history = pushAnnotation(history, a1);
  history = pushAnnotation(history, a2);
  history = pushAnnotation(history, a3);
  eq(liveAnnotations(history).map((a) => a.kind), ["rect", "arrow", "text"], "three pushes give three live annotations");

  history = undoAnnotation(history);
  eq(liveAnnotations(history).map((a) => a.kind), ["rect", "arrow"], "undo drops the last annotation");
  history = undoAnnotation(history);
  eq(liveAnnotations(history).map((a) => a.kind), ["rect"], "second undo keeps only the first");
  ok(canRedo(history), "redo becomes available after undo");

  history = redoAnnotation(history);
  eq(liveAnnotations(history).map((a) => a.kind), ["rect", "arrow"], "redo restores the second annotation");
  history = redoAnnotation(history);
  eq(liveAnnotations(history).map((a) => a.kind), ["rect", "arrow", "text"], "second redo restores the third");
  ok(!canRedo(history), "redo is exhausted at the head");
}

{
  // Undo then push truncates the redo tail (linear history rule).
  let history = emptyHistory();
  history = pushAnnotation(history, buildAnnotation("rect", { x: 0, y: 0 }, { x: 1, y: 1 }, "#fff"));
  history = pushAnnotation(history, buildAnnotation("arrow", { x: 0, y: 0 }, { x: 2, y: 2 }, "#fff"));
  history = undoAnnotation(history);
  history = pushAnnotation(history, buildAnnotation("mosaic", { x: 0, y: 0 }, { x: 3, y: 3 }, "#fff"));
  eq(liveAnnotations(history).map((a) => a.kind), ["rect", "mosaic"], "push after undo drops the abandoned redo branch");
  ok(!canRedo(history), "the abandoned branch is not redoable");
}

{
  // The history cap drops the oldest entries instead of growing forever.
  let history = emptyHistory();
  for (let i = 0; i < ANNOTATION_HISTORY_LIMIT + 25; i += 1) {
    history = pushAnnotation(history, buildAnnotation("rect", { x: i, y: 0 }, { x: i + 1, y: 1 }, "#fff"));
  }
  eq(history.entries.length, ANNOTATION_HISTORY_LIMIT, "history is capped at the limit");
  eq(liveAnnotations(history).length, ANNOTATION_HISTORY_LIMIT, "live annotations match the cap");
  const first = liveAnnotations(history)[0] as Annotation;
  eq(first.start.x, 25, "the oldest entries were dropped, not the newest");
}

{
  // Undo/redo on an empty stack are no-ops, not crashes.
  const history = emptyHistory();
  eq(undoAnnotation(history), history, "undo on empty history is a no-op");
  eq(redoAnnotation(history), history, "redo on empty history is a no-op");
}

console.log("\ntl 1087 arrow geometry");

{
  // A rightward arrow: the head wings sit above and below the tip, pulled
  // back from the tip by the head length.
  const start: AnnotationPoint = { x: 0, y: 0 };
  const end: AnnotationPoint = { x: 100, y: 0 };
  const [left, right] = arrowHeadPoints(start, end);
  ok(left.y < end.y && right.y > end.y, "rightward arrow head wings straddle the shaft");
  ok(left.x < end.x && right.x < end.x, "both wings sit behind the tip");
}

{
  // A downward arrow: the wings sit left and right of the shaft (the
  // counter-clockwise wing is on the right for a downward shaft).
  const start: AnnotationPoint = { x: 50, y: 0 };
  const end: AnnotationPoint = { x: 50, y: 100 };
  const [left, right] = arrowHeadPoints(start, end);
  ok(left.x > end.x && right.x < end.x, "downward arrow head wings straddle the shaft");
  ok(left.y < end.y && right.y < end.y, "both wings sit behind the tip");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
