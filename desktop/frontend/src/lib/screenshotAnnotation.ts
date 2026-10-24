// Screenshot annotation model for the desktop screenshot overlay (tl 1087
// phase one, extended by tl 1088 phase two).
//
// Pure functions only — no DOM, no React — so the selection/undo/redo rules
// the overlay depends on are unit-testable without a canvas. The overlay
// component owns pointer events and rendering; this module owns the facts.

// One annotation the user drew on top of the screenshot. tl 1087 shipped
// four tool kinds; tl 1088 adds ellipse / pen / number without touching the
// undo-stack shape (they are drag-shaped or click-placed like the originals).
export type AnnotationKind = "rect" | "arrow" | "text" | "mosaic" | "ellipse" | "pen" | "number";

export interface AnnotationPoint {
  x: number;
  y: number;
}

export interface Annotation {
  kind: AnnotationKind;
  // Start/end are drag extremes in screenshot-pixel space; text uses
  // start as its anchor and end as the caret's opposite corner; number uses
  // start as its center and ignores end.
  start: AnnotationPoint;
  end: AnnotationPoint;
  // text carries the typed content for the text tool; empty for the rest.
  text?: string;
  // color is the stroke/fill color the user picked (or the default).
  color: string;
  // points carries the freehand pen's polyline (tl 1088); empty for the
  // drag-shaped tools, which are fully described by start/end.
  points?: AnnotationPoint[];
  // number carries the tl 1088 auto-increment badge index (1, 2, 3 …).
  number?: number;
}

// A normalized rectangle: x/y are the top-left, width/height are positive.
export interface SelectionRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export type ScreenshotTool = AnnotationKind;

// The toolbar's tool order: tl 1087's four first, then tl 1088's three.
export const SCREENSHOT_TOOLS: ScreenshotTool[] = [
  "rect",
  "arrow",
  "text",
  "mosaic",
  "ellipse",
  "pen",
  "number",
];

export const DEFAULT_ANNOTATION_COLOR = "#ff3b30";

// Selection the user drags out before annotating. The overlay hands in raw
// pointer extremes; this turns them into a clamped, always-positive rect so
// the rest of the pipeline never sees a negative width/height.
export function normalizeSelection(
  start: AnnotationPoint,
  end: AnnotationPoint,
  bounds: { width: number; height: number },
): SelectionRect {
  const x = Math.min(start.x, end.x);
  const y = Math.min(start.y, end.y);
  const rawW = Math.max(start.x, end.x) - x;
  const rawH = Math.max(start.y, end.y) - y;
  const width = clamp(rawW, 0, Math.max(0, bounds.width - x));
  const height = clamp(rawH, 0, Math.max(0, bounds.height - y));
  return { x, y, width, height };
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

// Undo/redo stack: a fixed-capacity history of committed annotations plus a
// cursor. push() truncates any redo tail (the classic linear-history rule) so
// undo→redo→undo never resurrects a branch the user abandoned.
export interface AnnotationHistory {
  entries: Annotation[];
  cursor: number; // entries[0..cursor) are live; entries[cursor..] are the redo tail
}

export const ANNOTATION_HISTORY_LIMIT = 100;

export function emptyHistory(): AnnotationHistory {
  return { entries: [], cursor: 0 };
}

export function liveAnnotations(history: AnnotationHistory): Annotation[] {
  return history.entries.slice(0, history.cursor);
}

export function pushAnnotation(history: AnnotationHistory, annotation: Annotation): AnnotationHistory {
  const kept = history.entries.slice(0, history.cursor);
  const entries = [...kept, annotation];
  // Drop the oldest entries past the cap so a long scribble session cannot
  // grow the stack without bound.
  const overflow = Math.max(0, entries.length - ANNOTATION_HISTORY_LIMIT);
  return { entries: entries.slice(overflow), cursor: entries.length - overflow };
}

export function canUndo(history: AnnotationHistory): boolean {
  return history.cursor > 0;
}

export function canRedo(history: AnnotationHistory): boolean {
  return history.cursor < history.entries.length;
}

export function undoAnnotation(history: AnnotationHistory): AnnotationHistory {
  if (!canUndo(history)) return history;
  return { ...history, cursor: history.cursor - 1 };
}

export function redoAnnotation(history: AnnotationHistory): AnnotationHistory {
  if (!canRedo(history)) return history;
  return { ...history, cursor: history.cursor + 1 };
}

// An arrow is drawn from start to end; its head sits at `end`. This returns
// the two head-wing points so the renderer can stroke a chevron without
// recomputing trigonometry (kept pure for the test to pin the geometry).
// Convention: `left` is the wing counter-clockwise from the shaft (above the
// shaft for a rightward arrow), `right` the clockwise one.
export function arrowHeadPoints(
  start: AnnotationPoint,
  end: AnnotationPoint,
  headLength = 14,
  headAngle = Math.PI / 8,
): [left: AnnotationPoint, right: AnnotationPoint] {
  const angle = Math.atan2(end.y - start.y, end.x - start.x);
  const right: AnnotationPoint = {
    x: end.x - headLength * Math.cos(angle - headAngle),
    y: end.y - headLength * Math.sin(angle - headAngle),
  };
  const left: AnnotationPoint = {
    x: end.x - headLength * Math.cos(angle + headAngle),
    y: end.y - headLength * Math.sin(angle + headAngle),
  };
  return [left, right];
}

// Clamp a dragged selection handle so the rect never leaves the screenshot.
export function clampSelectionRect(rect: SelectionRect, bounds: { width: number; height: number }): SelectionRect {
  const x = clamp(rect.x, 0, Math.max(0, bounds.width - 1));
  const y = clamp(rect.y, 0, Math.max(0, bounds.height - 1));
  const width = clamp(rect.width, 1, bounds.width - x);
  const height = clamp(rect.height, 1, bounds.height - y);
  return { x, y, width, height };
}

// The eight drag handles a selection exposes (tl 1087: resize in place, no
// rotation). Names double as CSS class suffixes.
export type SelectionHandle = "nw" | "n" | "ne" | "e" | "se" | "s" | "sw" | "w";

export const SELECTION_HANDLES: SelectionHandle[] = ["nw", "n", "ne", "e", "se", "s", "sw", "w"];

// Apply a handle drag: the anchored corner stays put, the opposite corner
// follows the pointer, then the rect is clamped to the screenshot bounds.
export function resizeSelectionByHandle(
  rect: SelectionRect,
  handle: SelectionHandle,
  pointer: AnnotationPoint,
  bounds: { width: number; height: number },
): SelectionRect {
  const right = rect.x + rect.width;
  const bottom = rect.y + rect.height;
  let left = rect.x;
  let top = rect.y;
  let nextRight = right;
  let nextBottom = bottom;
  if (handle.includes("w")) left = pointer.x;
  if (handle.includes("e")) nextRight = pointer.x;
  if (handle.includes("n")) top = pointer.y;
  if (handle.includes("s")) nextBottom = pointer.y;
  return normalizeSelection({ x: left, y: top }, { x: nextRight, y: nextBottom }, bounds);
}

// Which handle (if any) sits under the pointer, within a grab radius. The
// overlay uses this to switch the cursor and arm a resize drag.
export function hitTestSelectionHandle(
  rect: SelectionRect,
  pointer: AnnotationPoint,
  radius = 8,
): SelectionHandle | null {
  const positions = handlePositions(rect);
  for (const handle of SELECTION_HANDLES) {
    const pos = positions[handle];
    if (Math.abs(pos.x - pointer.x) <= radius && Math.abs(pos.y - pointer.y) <= radius) {
      return handle;
    }
  }
  return null;
}

export function handlePositions(rect: SelectionRect): Record<SelectionHandle, AnnotationPoint> {
  const midX = rect.x + rect.width / 2;
  const midY = rect.y + rect.height / 2;
  return {
    nw: { x: rect.x, y: rect.y },
    n: { x: midX, y: rect.y },
    ne: { x: rect.x + rect.width, y: rect.y },
    e: { x: rect.x + rect.width, y: midY },
    se: { x: rect.x + rect.width, y: rect.y + rect.height },
    s: { x: midX, y: rect.y + rect.height },
    sw: { x: rect.x, y: rect.y + rect.height },
    w: { x: rect.x, y: midY },
  };
}

// Does the pointer sit inside the selection (annotation drawing area)? The
// overlay uses this to decide between "draw a new annotation" and "start a
// selection resize".
export function selectionContains(rect: SelectionRect, pointer: AnnotationPoint): boolean {
  return (
    pointer.x >= rect.x &&
    pointer.x <= rect.x + rect.width &&
    pointer.y >= rect.y &&
    pointer.y <= rect.y + rect.height
  );
}

// Build the annotation a drag produced, normalized so start is the drag
// origin and end the release point (matching how the renderer expects it).
export function buildAnnotation(
  kind: ScreenshotTool,
  start: AnnotationPoint,
  end: AnnotationPoint,
  color: string,
  text = "",
): Annotation {
  return { kind, start, end, color, text };
}

// tl 1088: a freehand pen stroke is a polyline, not a two-point shape. The
// overlay appends to `points` while the pointer is down; this helper trims a
// degenerate stroke (fewer than two distinct points) so a stray click never
// commits an invisible mark into the undo stack.
export function buildPenAnnotation(
  points: AnnotationPoint[],
  color: string,
): Annotation | null {
  const trimmed = dedupeNearbyPoints(points, 1.5);
  if (trimmed.length < 2) return null;
  return { kind: "pen", start: trimmed[0], end: trimmed[trimmed.length - 1], color, points: trimmed };
}

// Drop consecutive points closer than `minDistance` — a freehand drag emits
// dozens of nearly-identical samples per pixel of movement, and the raw list
// bloats the undo stack without adding visible fidelity.
export function dedupeNearbyPoints(points: AnnotationPoint[], minDistance: number): AnnotationPoint[] {
  const out: AnnotationPoint[] = [];
  for (const point of points) {
    const last = out[out.length - 1];
    if (!last || Math.hypot(point.x - last.x, point.y - last.y) >= minDistance) out.push(point);
  }
  return out;
}

// tl 1088: the number badge (WeChat-style auto-increment marker). Clicking
// stamps the next index; the overlay owns the counter, this helper centers
// the badge geometry and exposes the radius the renderer and hit-test share.
export const NUMBER_BADGE_RADIUS = 14;

export function buildNumberAnnotation(center: AnnotationPoint, number: number, color: string): Annotation {
  return { kind: "number", start: center, end: center, color, number };
}

// The auto-increment counter the number tool drives: a click stamps the next
// integer; the overlay resets it whenever a fresh selection starts.
export interface NumberCounter {
  next: number;
}

export function emptyNumberCounter(): NumberCounter {
  return { next: 1 };
}

export function stampNumber(counter: NumberCounter): { counter: NumberCounter; value: number } {
  return { counter: { next: counter.next + 1 }, value: counter.next };
}

// tl 1088: the ellipse's bounding box, shared by the renderer (ctx.ellipse)
// and any future hit-testing.
export function ellipseBounds(annotation: Annotation): SelectionRect {
  return normalizeSelection(annotation.start, annotation.end, { width: Number.MAX_SAFE_INTEGER, height: Number.MAX_SAFE_INTEGER });
}
