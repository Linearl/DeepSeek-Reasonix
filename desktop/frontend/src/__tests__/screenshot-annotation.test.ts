// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/screenshot-annotation.test.ts
//
// tl 1087: pure annotation-model coverage for the screenshot overlay —
// selection normalization/clamping, the eight-handle resize algebra, the
// undo/redo stack's linear-history rule, and the arrow head geometry. The
// DOM/canvas side of the overlay lives in composer-screenshot.test.tsx.
//
// tl 1088 phase one adds coverage for the ellipse / pen / number tools: the
// pen's polyline trimming, the number tool's auto-increment counter, the
// ellipse's bounding box, and the fact that the tool list grew from four to
// seven without disturbing the four originals.

import {
  ANNOTATION_HISTORY_LIMIT,
  NUMBER_BADGE_RADIUS,
  SCREENSHOT_TOOLS,
  arrowHeadPoints,
  buildAnnotation,
  buildNumberAnnotation,
  buildPenAnnotation,
  canRedo,
  canUndo,
  clampSelectionRect,
  dedupeNearbyPoints,
  ellipseBounds,
  emptyHistory,
  emptyNumberCounter,
  handlePositions,
  hitTestSelectionHandle,
  liveAnnotations,
  normalizeSelection,
  pushAnnotation,
  redoAnnotation,
  resizeSelectionByHandle,
  selectionContains,
  stampNumber,
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

console.log("\ntl 1088 tool list");

{
  eq(SCREENSHOT_TOOLS.length, 7, "the toolbar ships seven tools after tl 1088");
  ok(SCREENSHOT_TOOLS.includes("rect") && SCREENSHOT_TOOLS.includes("arrow") && SCREENSHOT_TOOLS.includes("text") && SCREENSHOT_TOOLS.includes("mosaic"), "the tl 1087 four tools survive");
  ok(SCREENSHOT_TOOLS.includes("ellipse") && SCREENSHOT_TOOLS.includes("pen") && SCREENSHOT_TOOLS.includes("number"), "the tl 1088 three tools are added");
  eq(SCREENSHOT_TOOLS[0], "rect", "rect stays first in the toolbar order");
}

console.log("\ntl 1088 pen polyline");

{
  // A real drag produces a polyline; nearby samples collapse to one point.
  const raw: AnnotationPoint[] = [
    { x: 0, y: 0 },
    { x: 0.4, y: 0.2 }, // closer than 1.5 → dropped
    { x: 10, y: 5 },
    { x: 20, y: 9 },
  ];
  const trimmed = dedupeNearbyPoints(raw, 1.5);
  eq(trimmed.length, 3, "near-duplicate samples collapse to distinct points");
  eq(trimmed[0], { x: 0, y: 0 }, "the first sample is kept");
}

{
  // A stroke with fewer than two distinct points never commits.
  eq(buildPenAnnotation([{ x: 3, y: 3 }], "#fff"), null, "a single-point stroke is rejected");
  eq(buildPenAnnotation([{ x: 3, y: 3 }, { x: 3.2, y: 3.1 }], "#fff"), null, "a sub-threshold stroke is rejected");
  const stroke = buildPenAnnotation([{ x: 0, y: 0 }, { x: 12, y: 8 }], "#fff");
  ok(stroke !== null && stroke.kind === "pen", "a two-point stroke commits as a pen annotation");
  eq(stroke?.points?.length, 2, "the committed stroke carries both points");
  eq(stroke?.end, { x: 12, y: 8 }, "the stroke's end is its last sample");
}

console.log("\ntl 1088 number badge");

{
  let counter = emptyNumberCounter();
  const first = stampNumber(counter);
  counter = first.counter;
  const second = stampNumber(counter);
  counter = second.counter;
  const third = stampNumber(counter);
  counter = third.counter;
  eq([first.value, second.value, third.value], [1, 2, 3], "three clicks stamp 1/2/3");
  eq(counter.next, 4, "the counter keeps advancing after the third stamp");
  const annotation = buildNumberAnnotation({ x: 40, y: 60 }, 2, "#fff");
  eq(annotation.kind, "number", "the number tool commits a number annotation");
  eq(annotation.number, 2, "the badge carries its index");
  eq(annotation.start, { x: 40, y: 60 }, "the badge is anchored at its click point");
  ok(NUMBER_BADGE_RADIUS > 0, "the badge radius is a positive constant");
}

console.log("\ntl 1088 ellipse");

{
  const ellipse = buildAnnotation("ellipse", { x: 10, y: 20 }, { x: 110, y: 220 }, "#fff");
  const bounds = ellipseBounds(ellipse);
  eq(bounds, { x: 10, y: 20, width: 100, height: 200 }, "the ellipse's bounds are its drag extremes");
  const flipped = ellipseBounds(buildAnnotation("ellipse", { x: 110, y: 220 }, { x: 10, y: 20 }, "#fff"));
  eq(flipped, { x: 10, y: 20, width: 100, height: 200 }, "a reversed drag normalizes to the same bounds");
}

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
