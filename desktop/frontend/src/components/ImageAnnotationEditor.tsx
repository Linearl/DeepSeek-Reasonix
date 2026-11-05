// tl 1412: the image-viewer annotation editor. Opened from the viewer's
// toolbar "annotate" button; paints on an overlay canvas laid exactly over
// the displayed image (the original never changes) and, on save, composites
// a fresh PNG at the image's natural resolution and hands it back through
// lib/imageAnnotationAttach (Composer stores it and inserts the chip into
// the active draft — the same link the paste/screenshot chains use).
//
// Deliberately thin where the screenshot overlay is thick: no selection
// rectangle, no mosaic/number tools — the whole image is the canvas and the
// tool set is the requirement's pen / text / rect / arrow / ellipse. The
// annotation model (history, undo/redo, builders) and the painter are the
// screenshot flow's shared modules (screenshotAnnotation.ts +
// annotationRender.ts), so both hosts render identical semantics.

import { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowUpRight,
  Check,
  Circle,
  Pencil,
  Redo2,
  Square,
  Trash2,
  Type,
  Undo2,
  X,
} from "lucide-react";
import { useT } from "../lib/i18n";
import { requestAnnotatedImageAttachment } from "../lib/imageAnnotationAttach";
import { drawAnnotation } from "../lib/annotationRender";
import {
  DEFAULT_ANNOTATION_COLOR,
  buildAnnotation,
  buildPenAnnotation,
  canRedo,
  canUndo,
  emptyHistory,
  liveAnnotations,
  pushAnnotation,
  redoAnnotation,
  undoAnnotation,
  type Annotation,
  type AnnotationPoint,
} from "../lib/screenshotAnnotation";

export interface ImageAnnotationEditorProps {
  /** data-URL (or URL) of the image being annotated; never mutated. */
  imageUrl: string;
  imageName?: string;
  /** Exit annotation mode; the viewer returns to plain preview. */
  onCancel: () => void;
  /**
   * Fired after the composite PNG has been handed to the attach channel
   * (requestAnnotatedImageAttachment). The host usually closes the viewer.
   */
  onComplete: () => void;
}

// tl 1412 tool set: the requirement's three families (pen / text / shapes).
// The narrower union (not ScreenshotTool) keeps the `t()` template-literal
// keys inside the dictionary; the shared builders accept it as-is.
type AnnotationToolKind = "pen" | "text" | "rect" | "arrow" | "ellipse";
const ANNOTATION_TOOLS: AnnotationToolKind[] = ["pen", "text", "rect", "arrow", "ellipse"];

const ANNOTATION_COLORS = [
  "#ff3b30",
  "#ff9500",
  "#34c759",
  "#007aff",
  "#af52de",
  "#000000",
  "#ffffff",
];

const ANNOTATION_WIDTHS = [2, 4, 8];

// A drag shorter than this is a stray click, not a shape (same threshold as
// the screenshot overlay so the two hosts feel identical).
const MIN_SHAPE_DRAG = 3;


export function ImageAnnotationEditor({ imageUrl, imageName, onCancel, onComplete }: ImageAnnotationEditorProps) {
  const t = useT();
  const imgRef = useRef<HTMLImageElement | null>(null);
  // The editor's own decoded Image (not the layout <img>): the export pass
  // needs natural dimensions and decoded pixels independent of CSS layout.
  const decodedImageRef = useRef<HTMLImageElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const penPointsRef = useRef<AnnotationPoint[]>([]);
  const textAnchorRef = useRef<AnnotationPoint | null>(null);

  const [imgLoaded, setImgLoaded] = useState(false);
  const [imgFailed, setImgFailed] = useState(false);
  const [tool, setTool] = useState<AnnotationToolKind>("pen");
  const [color, setColor] = useState(DEFAULT_ANNOTATION_COLOR);
  const [width, setWidth] = useState(4);
  const [history, setHistory] = useState(emptyHistory);
  const [draftAnnotation, setDraftAnnotation] = useState<Annotation | null>(null);
  const [dragging, setDragging] = useState(false);
  // The text tool: a click anchors an inline input; Enter/blur commits it.
  const [textInput, setTextInput] = useState<{ x: number; y: number; value: string } | null>(null);

  const annotations = liveAnnotations(history);
  const canSave = annotations.length > 0 && imgLoaded && !imgFailed;

  // Repaint the overlay canvas: committed annotations first, then the
  // in-flight draft. Runs after every render (the draw is cheap and always
  // correct, matching the screenshot overlay's repaint-on-change behavior).
  const paint = useCallback(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    for (const annotation of liveAnnotations(history)) drawAnnotation(ctx, annotation);
    if (draftAnnotation) drawAnnotation(ctx, draftAnnotation);
  }, [history, draftAnnotation]);

  // Load the image for measurement (natural size) and for the export pass.
  useEffect(() => {
    setImgLoaded(false);
    setImgFailed(false);
    let stale = false;
    const image = new Image();
    image.onload = () => {
      if (stale) return;
      decodedImageRef.current = image;
      setImgLoaded(true);
    };
    image.onerror = () => {
      if (stale) return;
      setImgFailed(true);
    };
    image.src = imageUrl;
    return () => {
      stale = true;
    };
  }, [imageUrl]);

  const canvasPoint = useCallback((event: { clientX: number; clientY: number }): AnnotationPoint => {
    const canvas = canvasRef.current;
    if (!canvas) return { x: 0, y: 0 };
    const rect = canvas.getBoundingClientRect();
    const x = Math.min(Math.max(event.clientX - rect.left, 0), rect.width || canvas.width);
    const y = Math.min(Math.max(event.clientY - rect.top, 0), rect.height || canvas.height);
    return { x, y };
  }, []);

  // Keep the overlay canvas pixel-matched to the displayed image.
  useEffect(() => {
    if (!imgLoaded) return;
    const measure = () => {
      const img = imgRef.current;
      const canvas = canvasRef.current;
      if (!img || !canvas) return;
      const w = Math.max(1, Math.round(img.clientWidth || img.naturalWidth || 1));
      const h = Math.max(1, Math.round(img.clientHeight || img.naturalHeight || 1));
      if (canvas.width !== w || canvas.height !== h) {
        canvas.width = w;
        canvas.height = h;
      }
      paint();
    };
    measure();
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [imgLoaded, imageUrl, paint]);

  // Repaint after every render (see paint above).
  useEffect(() => {
    paint();
  }, [paint]);

  // ---- pointer gestures (mirrors the screenshot overlay's contract) ----

  const onCanvasPointerDown = (event: React.PointerEvent<HTMLCanvasElement>) => {
    if (event.button !== 0 || imgFailed) return;
    const point = canvasPoint(event);
    event.currentTarget.setPointerCapture?.(event.pointerId);
    if (tool === "text") {
      textAnchorRef.current = point;
      setTextInput({ x: point.x, y: point.y, value: "" });
      return;
    }
    if (tool === "pen") {
      penPointsRef.current = [point];
      setDragging(true);
      setDraftAnnotation(buildAnnotation("pen", point, point, color));
      return;
    }
    setDragging(true);
    setDraftAnnotation(buildAnnotation(tool, point, point, color));
  };

  const onCanvasPointerMove = (event: React.PointerEvent<HTMLCanvasElement>) => {
    if (!dragging || !draftAnnotation) return;
    const point = canvasPoint(event);
    if (draftAnnotation.kind === "pen") {
      penPointsRef.current = [...penPointsRef.current, point];
      setDraftAnnotation({ ...draftAnnotation, end: point, points: penPointsRef.current });
      return;
    }
    setDraftAnnotation({ ...draftAnnotation, end: point });
  };

  const onCanvasPointerUp = () => {
    if (dragging && draftAnnotation) {
      if (draftAnnotation.kind === "pen") {
        const committed = buildPenAnnotation(penPointsRef.current, color, width);
        if (committed) setHistory((prev) => pushAnnotation(prev, committed));
      } else {
        const moved = Math.hypot(draftAnnotation.end.x - draftAnnotation.start.x, draftAnnotation.end.y - draftAnnotation.start.y);
        if (moved >= MIN_SHAPE_DRAG) {
          setHistory((prev) => pushAnnotation(prev, { ...draftAnnotation, width }));
        }
      }
      penPointsRef.current = [];
      setDraftAnnotation(null);
    }
    setDragging(false);
  };

  // ---- text tool ----

  const commitText = () => {
    const anchor = textAnchorRef.current;
    const value = textInput?.value.trim();
    if (anchor && value) {
      // Text has no stroke width — the renderer draws it at the shared
      // 20px display-space font, scaled into image space by the export pass.
      setHistory((prev) => pushAnnotation(prev, buildAnnotation("text", anchor, anchor, color, value)));
    }
    textAnchorRef.current = null;
    setTextInput(null);
  };

  // Esc inside the editor steps back one layer (text input → editor → viewer)
  // without ever reaching the viewer's close-on-Esc handler. Registered on
  // window in the CAPTURE phase on purpose: the viewer listens on document,
  // and for an Escape dispatched on document both listeners sit at target
  // phase where capture flags don't reorder — window capture runs first and
  // stopPropagation keeps the viewer's handler out of the way entirely.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.stopPropagation();
      if (textInput) {
        textAnchorRef.current = null;
        setTextInput(null);
        return;
      }
      onCancel();
    };
    window.addEventListener("keydown", onKeyDown, true);
    return () => window.removeEventListener("keydown", onKeyDown, true);
  }, [textInput, onCancel]);

  // ---- export ----

  const renderFinalDataURL = useCallback((): string | null => {
    const img = decodedImageRef.current ?? imgRef.current;
    const canvas = canvasRef.current;
    if (!img || !canvas) return null;
    const naturalWidth = Math.max(1, img.naturalWidth || canvas.width || 1);
    const naturalHeight = Math.max(1, img.naturalHeight || canvas.height || 1);
    // Fresh canvas at natural resolution: the displayed image first, then
    // every committed annotation scaled from display space into image space
    // (ctx.scale scales coordinates AND stroke widths together, so the PNG
    // looks exactly like the on-screen overlay).
    const out = document.createElement("canvas");
    out.width = naturalWidth;
    out.height = naturalHeight;
    const ctx = out.getContext("2d");
    if (!ctx) return null;
    ctx.drawImage(img, 0, 0, naturalWidth, naturalHeight);
    const scaleX = naturalWidth / (canvas.width || naturalWidth);
    const scaleY = naturalHeight / (canvas.height || naturalHeight);
    ctx.save();
    ctx.scale(scaleX, scaleY);
    for (const annotation of liveAnnotations(history)) drawAnnotation(ctx, annotation);
    ctx.restore();
    return out.toDataURL("image/png");
  }, [history]);

  const save = () => {
    if (!canSave) return;
    const dataUrl = renderFinalDataURL();
    if (!dataUrl) return;
    requestAnnotatedImageAttachment({ dataUrl, name: imageName });
    onComplete();
  };

  const toolIcon = (kind: AnnotationToolKind) => {
    switch (kind) {
      case "pen":
        return <Pencil size={16} />;
      case "text":
        return <Type size={16} />;
      case "rect":
        return <Square size={16} />;
      case "arrow":
        return <ArrowUpRight size={16} />;
      case "ellipse":
        return <Circle size={16} />;
      default:
        return null;
    }
  };

  return (
    <div className="image-annotation" role="region" aria-label={t("imageAnnotation.title")} data-testid="image-annotation-editor">
      <div className="image-annotation__toolbar">
        <div className="image-annotation__group" role="toolbar" aria-label={t("imageAnnotation.title")}>
          {ANNOTATION_TOOLS.map((kind) => (
            <button
              key={kind}
              type="button"
              className={`image-annotation__btn${tool === kind ? " image-annotation__btn--active" : ""}`}
              onClick={() => {
                setTool(kind);
                setTextInput(null);
              }}
              aria-pressed={tool === kind}
              aria-label={t(`imageAnnotation.tool.${kind}`)}
              title={t(`imageAnnotation.tool.${kind}`)}
            >
              {toolIcon(kind)}
            </button>
          ))}
        </div>
        <div className="image-annotation__group" aria-label={t("imageAnnotation.color")}>
          {ANNOTATION_COLORS.map((value) => (
            <button
              key={value}
              type="button"
              className={`image-annotation__swatch${color === value ? " image-annotation__swatch--active" : ""}`}
              style={{ backgroundColor: value }}
              onClick={() => setColor(value)}
              aria-pressed={color === value}
              aria-label={`${t("imageAnnotation.color")} ${value}`}
              title={`${t("imageAnnotation.color")} ${value}`}
            />
          ))}
        </div>
        <div className="image-annotation__group">
          {ANNOTATION_WIDTHS.map((value) => (
            <button
              key={value}
              type="button"
              className={`image-annotation__btn image-annotation__width${width === value ? " image-annotation__btn--active" : ""}`}
              onClick={() => setWidth(value)}
              aria-pressed={width === value}
              aria-label={value === 2 ? t("imageAnnotation.width.thin") : value === 8 ? t("imageAnnotation.width.bold") : t("imageAnnotation.width.medium")}
              title={value === 2 ? t("imageAnnotation.width.thin") : value === 8 ? t("imageAnnotation.width.bold") : t("imageAnnotation.width.medium")}
            >
              <span className="image-annotation__width-dot" data-width={value} />
            </button>
          ))}
        </div>
        <div className="image-annotation__group">
          <button
            type="button"
            className="image-annotation__btn"
            onClick={() => setHistory((prev) => undoAnnotation(prev))}
            disabled={!canUndo(history)}
            aria-label={t("imageAnnotation.undo")}
            title={t("imageAnnotation.undo")}
          >
            <Undo2 size={16} />
          </button>
          <button
            type="button"
            className="image-annotation__btn"
            onClick={() => setHistory((prev) => redoAnnotation(prev))}
            disabled={!canRedo(history)}
            aria-label={t("imageAnnotation.redo")}
            title={t("imageAnnotation.redo")}
          >
            <Redo2 size={16} />
          </button>
          <button
            type="button"
            className="image-annotation__btn"
            onClick={() => setHistory(emptyHistory())}
            disabled={annotations.length === 0}
            aria-label={t("imageAnnotation.clear")}
            title={t("imageAnnotation.clear")}
          >
            <Trash2 size={16} />
          </button>
        </div>
        <div className="image-annotation__spacer" />
        <div className="image-annotation__group">
          <button
            type="button"
            className="image-annotation__btn image-annotation__btn--text"
            onClick={() => {
              textAnchorRef.current = null;
              setTextInput(null);
              onCancel();
            }}
            aria-label={t("imageAnnotation.cancel")}
            title={t("imageAnnotation.cancel")}
          >
            <X size={16} />
            <span>{t("imageAnnotation.cancel")}</span>
          </button>
          <button
            type="button"
            className="image-annotation__btn image-annotation__btn--primary"
            onClick={save}
            disabled={!canSave}
            aria-label={t("imageAnnotation.save")}
            title={t("imageAnnotation.save")}
          >
            <Check size={16} />
            <span>{t("imageAnnotation.save")}</span>
          </button>
        </div>
      </div>

      <div className="image-annotation__stage">
        <img ref={imgRef} className="image-annotation__image" src={imageUrl} alt={imageName || ""} draggable={false} />
        <canvas
          ref={canvasRef}
          className={`image-annotation__canvas${tool === "text" ? " image-annotation__canvas--text" : ""}`}
          onPointerDown={onCanvasPointerDown}
          onPointerMove={onCanvasPointerMove}
          onPointerUp={onCanvasPointerUp}
          data-state={dragging ? "drawing" : "idle"}
        />
        {textInput && (
          <textarea
            className="image-annotation__text-input"
            style={{ left: textInput.x, top: textInput.y }}
            value={textInput.value}
            placeholder={t("imageAnnotation.textPlaceholder")}
            autoFocus
            rows={1}
            onChange={(event) => setTextInput({ ...textInput, value: event.target.value })}
            onKeyDown={(event) => {
              // Enter commits; Escape is owned by the document-level capture
              // listener (steps back one layer: text input → editor).
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                commitText();
              }
            }}
            onBlur={commitText}
          />
        )}
        {imgFailed && <div className="image-annotation__error">{t("imageAnnotation.loadFailed")}</div>}
      </div>
    </div>
  );
}
