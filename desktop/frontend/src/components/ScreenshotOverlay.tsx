// Screenshot overlay for the desktop screenshot button (tl 1087).
//
// Flow: the Composer's screenshot button asks the backend to grab the full
// screen of the monitor under the mouse cursor (CaptureScreenshotAtCursor),
// then this overlay paints that PNG fullscreen so the user can drag a
// selection, annotate (rect / arrow / text / mosaic), undo/redo, save to a
// file (Ctrl+S), and confirm with a double-click — which hands the final PNG
// back to the Composer as an attachment. Esc cancels with no side effects.
//
// The overlay is portaled into document.body (fullscreen, above the app chrome)
// and owns all of its own pointer/keyboard handling; the Composer only owns
// the button and the confirm callback.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Check, Copy, PanelsTopLeft, Redo2, Save, Type, Undo2, X } from "lucide-react";
import { useT, type Translator } from "../lib/i18n";
import { useToast } from "../lib/toast";
import { app } from "../lib/bridge";
import {
  DEFAULT_ANNOTATION_COLOR,
  NUMBER_BADGE_RADIUS,
  SCREENSHOT_TOOLS,
  arrowHeadPoints,
  buildAnnotation,
  buildNumberAnnotation,
  buildPenAnnotation,
  canRedo,
  canUndo,
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
  type NumberCounter,
  type ScreenshotTool,
  type SelectionHandle,
} from "../lib/screenshotAnnotation";

export interface ScreenshotOverlayProps {
  open: boolean;
  // onClose fires on Esc / cancel / confirm-complete — the Composer tears the
  // overlay down and returns focus to the composer input.
  onClose: () => void;
  // onConfirm receives the final PNG data URL (annotations baked in) plus the
  // selection's pixel size, after the backend has stored it as an attachment.
  onConfirm: (dataUrl: string, width: number, height: number) => void;
}

interface Capture {
  path: string;
  width: number;
  height: number;
  x: number;
  y: number;
  dataUrl: string;
}

// Pointer state during a drag: which gesture is armed (new selection, resize
// an existing one, draw an annotation, drag the text caret) and its origin.
type DragMode = "select" | "resize" | "annotate" | null;

export function ScreenshotOverlay({ open, onClose, onConfirm }: ScreenshotOverlayProps) {
  const t = useT();
  const { showToast } = useToast();
  const [capture, setCapture] = useState<Capture | null>(null);
  // tl 1167: the raw failure, not a bare flag — a preview that fails to load
  // must say so visibly instead of silently painting black. The error is kept
  // un-translated and rendered through describeCaptureFailure so the message
  // tracks the active locale at paint time (t's identity is not stable).
  const [captureError, setCaptureError] = useState<unknown>(null);
  const [selection, setSelection] = useState<{ x: number; y: number; width: number; height: number } | null>(null);
  const [tool, setTool] = useState<ScreenshotTool>("rect");
  const [history, setHistory] = useState(emptyHistory());
  const [draftAnnotation, setDraftAnnotation] = useState<Annotation | null>(null);
  const [dragMode, setDragMode] = useState<DragMode>(null);
  const [dragHandle, setDragHandle] = useState<SelectionHandle | null>(null);
  const [dragOrigin, setDragOrigin] = useState<AnnotationPoint | null>(null);
  const [textInput, setTextInput] = useState<{ x: number; y: number; value: string } | null>(null);
  const [saving, setSaving] = useState(false);
  // tl 1088: the number tool's auto-increment counter (a click stamps the
  // next badge); it resets whenever a fresh selection starts.
  const [numberCounter, setNumberCounter] = useState<NumberCounter>(emptyNumberCounter);

  const stageRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const selectionRef = useRef(selection);
  selectionRef.current = selection;
  const historyRef = useRef(history);
  historyRef.current = history;
  const toolRef = useRef(tool);
  toolRef.current = tool;
  // The pen's in-flight polyline lives in a ref, not state: it changes on
  // every pointermove (dozens per second) and only needs to reach the canvas
  // on the next repaint, not trigger a React render per sample.
  const penPointsRef = useRef<AnnotationPoint[]>([]);
  // tl 1088: cross-monitor capture toggle. Off = the monitor under the cursor
  // (tl 1087 plan A); on = the whole virtual desktop, so one selection can
  // span displays. Flipping it re-captures and clears any in-progress
  // selection/annotations (the pixels underneath changed).
  const [crossMonitor, setCrossMonitor] = useState(false);
  const crossMonitorRef = useRef(crossMonitor);
  crossMonitorRef.current = crossMonitor;

  // Kick off the capture the first time the overlay opens; release the temp
  // file when it closes (Esc / confirm) so no stray PNG outlives the gesture.
  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    setCapture(null);
    setCaptureError(null);
    setSelection(null);
    setHistory(emptyHistory());
    setDraftAnnotation(null);
    setTextInput(null);
    setNumberCounter(emptyNumberCounter());
    setCrossMonitor(false);
    (async () => {
      try {
        const shot = await app.CaptureScreenshotAtCursor();
        if (cancelled) {
          await app.ReleaseScreenshotCapture(shot.path).catch(() => {});
          return;
        }
        const dataUrl = await loadCapturePreview(shot.path);
        if (cancelled) {
          await app.ReleaseScreenshotCapture(shot.path).catch(() => {});
          return;
        }
        // Decode the screenshot once up front so the confirm path's crop is
        // synchronous (the cache is keyed by data URL).
        await primeScreenshotImageCache(dataUrl);
        if (cancelled) {
          await app.ReleaseScreenshotCapture(shot.path).catch(() => {});
          return;
        }
        setCapture({ path: shot.path, width: shot.width, height: shot.height, x: shot.x, y: shot.y, dataUrl });
      } catch (error) {
        console.warn("[screenshot] capture failed", error);
        if (!cancelled) setCaptureError(error);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open]);

  // Re-capture when the cross-monitor toggle flips (tl 1088). The previous
  // capture's temp file is released so a toggle storm cannot leak directories.
  const recapture = useCallback(async (virtual: boolean) => {
    const previous = capturePathRef.current;
    if (previous) {
      void app.ReleaseScreenshotCapture(previous).catch(() => {});
      capturePathRef.current = "";
    }
    setSelection(null);
    setHistory(emptyHistory());
    setDraftAnnotation(null);
    setTextInput(null);
    setNumberCounter(emptyNumberCounter());
    try {
      const shot = virtual ? await app.CaptureScreenshotVirtualDesktop() : await app.CaptureScreenshotAtCursor();
      capturePathRef.current = shot.path;
      const dataUrl = await loadCapturePreview(shot.path);
      await primeScreenshotImageCache(dataUrl);
      setCapture({ path: shot.path, width: shot.width, height: shot.height, x: shot.x, y: shot.y, dataUrl });
      setCaptureError(null);
    } catch (error) {
      console.warn("[screenshot] re-capture failed", error);
      setCaptureError(error);
    }
  }, []);

  // Release the backend temp file once the overlay unmounts or closes. The
  // capture path lives in a ref so the cleanup sees the final value even
  // though the effect above only runs on `open` transitions.
  const capturePathRef = useRef<string>("");
  capturePathRef.current = capture?.path ?? "";
  useEffect(() => {
    if (!open) return;
    return () => {
      const path = capturePathRef.current;
      if (path) void app.ReleaseScreenshotCapture(path).catch(() => {});
    };
  }, [open]);

  // Esc cancels with no side effects (tl 1087 contract). Ctrl+S saves the
  // current selection to a file before confirming.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        onClose();
        return;
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
        event.preventDefault();
        event.stopPropagation();
        void saveToFile();
        return;
      }
      // Undo/redo keyboard shortcuts mirror the composer's edit history.
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "z" && !event.shiftKey) {
        event.preventDefault();
        doUndo();
        return;
      }
      if ((event.ctrlKey || event.metaKey) && (event.key.toLowerCase() === "y" || (event.key.toLowerCase() === "z" && event.shiftKey))) {
        event.preventDefault();
        doRedo();
      }
    };
    document.addEventListener("keydown", onKeyDown, true);
    return () => document.removeEventListener("keydown", onKeyDown, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, capture, selection, history, textInput]);

  // Freeze body scroll while the overlay is up.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  // Repaint the annotation canvas whenever the history/selection/draft change.
  useEffect(() => {
    if (!capture) return;
    const canvas = canvasRef.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    // The canvas is sized to the selection in screenshot-pixel space; every
    // annotation is drawn in that same space so no scaling is involved.
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    for (const annotation of liveAnnotations(history)) {
      drawAnnotation(ctx, annotation);
    }
    if (draftAnnotation) drawAnnotation(ctx, draftAnnotation);
  }, [capture, history, draftAnnotation]);

  const stagePoint = useCallback((event: { clientX: number; clientY: number }): AnnotationPoint => {
    const stage = stageRef.current;
    if (!stage) return { x: 0, y: 0 };
    const rect = stage.getBoundingClientRect();
    return { x: event.clientX - rect.left, y: event.clientY - rect.top };
  }, []);

  const bounds = useMemo(
    () => ({ width: capture?.width ?? 0, height: capture?.height ?? 0 }),
    [capture],
  );

  // ---- pointer gestures -------------------------------------------------

  const onStagePointerDown = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!capture) return;
    if (event.button !== 0) return;
    const point = stagePoint(event);
    event.currentTarget.setPointerCapture?.(event.pointerId);

    // A committed selection: the next gesture is either a handle resize or a
    // new annotation, depending on where the pointer landed.
    const current = selectionRef.current;
    if (current) {
      const handle = hitTestSelectionHandle(current, point);
      if (handle) {
        setDragMode("resize");
        setDragHandle(handle);
        setDragOrigin(point);
        return;
      }
      if (selectionContains(current, point)) {
        // The text tool anchors a text box at the click point instead of
        // dragging a shape; the number tool (tl 1088) stamps an
        // auto-incrementing badge at the click point with no drag at all.
        if (toolRef.current === "text") {
          setTextInput({ x: point.x - current.x, y: point.y - current.y, value: "" });
          return;
        }
        if (toolRef.current === "number") {
          const stamped = stampNumber(numberCounter);
          const anchor = { x: point.x, y: point.y };
          setHistory((prev) => pushAnnotation(prev, buildNumberAnnotation(anchor, stamped.value, DEFAULT_ANNOTATION_COLOR)));
          setNumberCounter(stamped.counter);
          return;
        }
        // The pen (tl 1088) accumulates a polyline rather than a two-point
        // shape: seed the ref with the press point and start collecting.
        if (toolRef.current === "pen") {
          penPointsRef.current = [point];
          setDragMode("annotate");
          setDraftAnnotation(buildAnnotation("pen", point, point, DEFAULT_ANNOTATION_COLOR));
          return;
        }
        setDragMode("annotate");
        setDraftAnnotation(buildAnnotation(toolRef.current, point, point, DEFAULT_ANNOTATION_COLOR));
        return;
      }
    }

    // No selection yet (or the pointer fell outside it): start a fresh drag
    // selection from this point.
    setTextInput(null);
    setDragMode("select");
    setDragOrigin(point);
    setSelection(normalizeSelection(point, point, bounds));
  };

  const onStagePointerMove = (event: React.PointerEvent<HTMLDivElement>) => {
    if (!dragMode || !dragOrigin) return;
    const point = stagePoint(event);
    if (dragMode === "select") {
      setSelection(normalizeSelection(dragOrigin, point, bounds));
      return;
    }
    if (dragMode === "resize" && selectionRef.current && dragHandle) {
      setSelection(resizeSelectionByHandle(selectionRef.current, dragHandle, point, bounds));
      return;
    }
    if (dragMode === "annotate" && draftAnnotation) {
      // The pen accumulates its polyline in the ref (see pointerdown) so the
      // per-sample state updates stay off the React render path.
      if (draftAnnotation.kind === "pen") {
        penPointsRef.current = [...penPointsRef.current, point];
        setDraftAnnotation({ ...draftAnnotation, end: point, points: penPointsRef.current });
        return;
      }
      setDraftAnnotation({ ...draftAnnotation, end: point });
    }
  };

  const onStagePointerUp = () => {
    if (dragMode === "annotate" && draftAnnotation) {
      // A zero-length drag would commit a degenerate shape; treat it as a
      // cancelled gesture so a stray click never leaves a speck behind.
      if (draftAnnotation.kind === "pen") {
        const committed = buildPenAnnotation(penPointsRef.current, draftAnnotation.color);
        if (committed) setHistory((prev) => pushAnnotation(prev, committed));
      } else {
        const moved = Math.hypot(draftAnnotation.end.x - draftAnnotation.start.x, draftAnnotation.end.y - draftAnnotation.start.y);
        if (moved >= 3) {
          setHistory((prev) => pushAnnotation(prev, draftAnnotation));
        }
      }
      penPointsRef.current = [];
      setDraftAnnotation(null);
    }
    setDragMode(null);
    setDragHandle(null);
    setDragOrigin(null);
  };

  // ---- annotations ------------------------------------------------------

  const doUndo = () => setHistory((prev) => undoAnnotation(prev));
  const doRedo = () => setHistory((prev) => redoAnnotation(prev));

  const commitText = () => {
    if (!textInput) return;
    const value = textInput.value.trim();
    const current = selectionRef.current;
    if (value && current) {
      // Text anchors at the click point inside the selection; the "end" is
      // the same point (the renderer measures the string itself).
      const anchor = { x: current.x + textInput.x, y: current.y + textInput.y };
      setHistory((prev) => pushAnnotation(prev, buildAnnotation("text", anchor, anchor, DEFAULT_ANNOTATION_COLOR, value)));
    }
    setTextInput(null);
  };

  // ---- export -----------------------------------------------------------

  const renderFinalDataURL = useCallback((): string | null => {
    const canvas = canvasRef.current;
    if (!canvas || !capture || !selectionRef.current) return null;
    // Rebuild the final PNG at the selection's pixel size: the screenshot
    // region, then every committed annotation drawn on top. Going through a
    // fresh canvas keeps the export independent of the on-screen one.
    const out = document.createElement("canvas");
    out.width = canvas.width;
    out.height = canvas.height;
    const ctx = out.getContext("2d");
    if (!ctx) return null;
    const region = screenshotRegionToCanvas(capture.dataUrl, selectionRef.current);
    if (!region) return null;
    ctx.drawImage(region, 0, 0);
    for (const annotation of liveAnnotations(historyRef.current)) {
      drawAnnotation(ctx, annotation);
    }
    return out.toDataURL("image/png");
  }, [capture]);

  const saveToFile = async () => {
    if (saving) return;
    const dataUrl = renderFinalDataURL();
    if (!dataUrl) return;
    setSaving(true);
    try {
      const path = await app.PickScreenshotExportFile();
      if (!path) return; // cancelled dialog: stay in the overlay
      await app.SaveScreenshotToPath(path, dataUrl);
    } catch (error) {
      console.warn("[screenshot] save to file failed", error);
    } finally {
      setSaving(false);
    }
  };

  const confirm = async () => {
    const dataUrl = renderFinalDataURL();
    if (!dataUrl || !selectionRef.current) return;
    const { width, height } = selectionRef.current;
    try {
      // The attachment lands in the workspace's .reasonix/attachments through
      // the same link the paste/drop image chain uses — no clipboard involved.
      await app.SaveScreenshotAttachment(dataUrl);
    } catch (error) {
      console.warn("[screenshot] attachment save failed", error);
      onClose();
      return;
    }
    onConfirm(dataUrl, width, height);
  };

  // tl 1088: copy the final PNG to the OS clipboard so external apps can paste
  // it as a real image. The backend writes CF_PNG + CF_DIB through syscall;
  // if that path is unavailable (non-Windows, or the webview lacks the
  // binding) fall back to the browser Clipboard API.
  const copyToClipboard = async () => {
    const dataUrl = renderFinalDataURL();
    if (!dataUrl) return;
    try {
      await app.CopyScreenshotToClipboard(dataUrl);
      showToast(t("screenshot.copied"), "info");
    } catch (error) {
      console.warn("[screenshot] backend clipboard copy failed", error);
      try {
        const blob = await (await fetch(dataUrl)).blob();
        await navigator.clipboard.write([new ClipboardItem({ [blob.type]: blob })]);
        showToast(t("screenshot.copied"), "info");
      } catch (fallbackError) {
        console.warn("[screenshot] clipboard copy failed", fallbackError);
        showToast(t("screenshot.copyFailed"), "warn");
      }
    }
  };

  // ---- render -----------------------------------------------------------

  if (!open) return null;

  const liveCount = liveAnnotations(history).length;

  const body = (
    <div
      className="screenshot-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={t("screenshot.title")}
      data-capture-ready={capture ? "ready" : captureError ? "error" : "loading"}
    >
      {captureError ? (
        <div className="screenshot-overlay__error">
          <p>{describeCaptureFailure(t, captureError)}</p>
          <button type="button" onClick={onClose}>{t("screenshot.close")}</button>
        </div>
      ) : !capture ? (
        <div className="screenshot-overlay__loading">{t("screenshot.capturing")}</div>
      ) : (
        <>
          {/* The captured screenshot, painted fullscreen; the stage on top
              handles selection/annotation gestures. */}
          <img className="screenshot-overlay__image" src={capture.dataUrl} alt="" draggable={false} />
          <div
            ref={stageRef}
            className="screenshot-overlay__stage"
            onPointerDown={onStagePointerDown}
            onPointerMove={onStagePointerMove}
            onPointerUp={onStagePointerUp}
            onDoubleClick={() => {
              // Double-click confirms (tl 1087): only meaningful once a
              // selection exists; a double-click in empty space is a no-op.
              if (selectionRef.current) void confirm();
            }}
          >
            {/* Dim everything outside the selection. */}
            {selection && (
              <div
                className="screenshot-overlay__selection"
                style={{ left: selection.x, top: selection.y, width: selection.width, height: selection.height }}
              >
                {handlePositions(selection) && Object.entries(handlePositions(selection)).map(([handle, pos]) => (
                  <span
                    key={handle}
                    className={`screenshot-overlay__handle screenshot-overlay__handle--${handle}`}
                    style={{ left: pos.x - selection.x, top: pos.y - selection.y }}
                  />
                ))}
              </div>
            )}
            {/* The annotation canvas sits exactly over the selection. */}
            {selection && (
              <canvas
                ref={canvasRef}
                className="screenshot-overlay__canvas"
                width={Math.max(1, Math.round(selection.width))}
                height={Math.max(1, Math.round(selection.height))}
                style={{ left: selection.x, top: selection.y }}
              />
            )}
            {/* Inline text box while the text tool is typing. */}
            {textInput && selection && (
              <input
                className="screenshot-overlay__text-input"
                autoFocus
                style={{ left: selection.x + textInput.x, top: selection.y + textInput.y }}
                value={textInput.value}
                onChange={(event) => setTextInput({ ...textInput, value: event.target.value })}
                onBlur={commitText}
                onKeyDown={(event) => {
                  if (event.key === "Enter") {
                    event.preventDefault();
                    commitText();
                  } else if (event.key === "Escape") {
                    event.preventDefault();
                    setTextInput(null);
                  }
                }}
              />
            )}
            {/* Size readout while dragging a selection. */}
            {selection && selection.width > 0 && selection.height > 0 && (
              <div
                className="screenshot-overlay__size"
                style={{ left: selection.x, top: selection.y + selection.height + 6 }}
              >
                {Math.round(selection.width)} × {Math.round(selection.height)}
              </div>
            )}
          </div>
          {/* Floating toolbar: tools, undo/redo, save, confirm, cancel. */}
          <div className="screenshot-overlay__toolbar" role="toolbar" aria-label={t("screenshot.toolbarLabel")}>
            {SCREENSHOT_TOOLS.map((item) => (
              <button
                key={item}
                type="button"
                className={`screenshot-overlay__tool${tool === item ? " screenshot-overlay__tool--active" : ""}`}
                onClick={() => setTool(item)}
                aria-pressed={tool === item}
                aria-label={toolLabel(t, item)}
                title={toolLabel(t, item)}
              >
                {item === "text" ? <Type size={16} /> : <span className={`screenshot-overlay__tool-glyph screenshot-overlay__tool-glyph--${item}`} />}
              </button>
            ))}
            <span className="screenshot-overlay__divider" />
            <button type="button" className="screenshot-overlay__action" onClick={doUndo} disabled={!canUndo(history)} aria-label={t("screenshot.undo")} title={t("screenshot.undo")}>
              <Undo2 size={16} />
            </button>
            <button type="button" className="screenshot-overlay__action" onClick={doRedo} disabled={!canRedo(history)} aria-label={t("screenshot.redo")} title={t("screenshot.redo")}>
              <Redo2 size={16} />
            </button>
            <span className="screenshot-overlay__divider" />
            {/* tl 1088：跨屏开关——抓整个虚拟桌面，选区可横跨显示器。翻转会
                重新抓图并清掉当前选区/标注（底下的像素变了）。 */}
            <button
              type="button"
              className={`screenshot-overlay__action${crossMonitor ? " screenshot-overlay__action--on" : ""}`}
              onClick={() => {
                const next = !crossMonitor;
                setCrossMonitor(next);
                void recapture(next);
              }}
              aria-pressed={crossMonitor}
              aria-label={t("screenshot.crossMonitor")}
              title={t("screenshot.crossMonitorHint")}
            >
              <PanelsTopLeft size={16} />
            </button>
            <span className="screenshot-overlay__divider" />
            <button type="button" className="screenshot-overlay__action" onClick={() => void saveToFile()} disabled={saving || !selection} aria-label={t("screenshot.save")} title={t("screenshot.saveHint")}>
              <Save size={16} />
            </button>
            {/* tl 1088：复制到剪贴板——除「进输入框附件」外的第二个出口。走后端
                CF_PNG/CF_DIB syscall（外部程序可直接粘贴为图片），失败时退回
                前端 navigator.clipboard。 */}
            <button type="button" className="screenshot-overlay__action" onClick={() => void copyToClipboard()} disabled={!selection} aria-label={t("screenshot.copy")} title={t("screenshot.copyHint")}>
              <Copy size={16} />
            </button>
            <button
              type="button"
              className="screenshot-overlay__confirm"
              onClick={() => void confirm()}
              disabled={!selection}
              aria-label={t("screenshot.confirm")}
              title={t("screenshot.confirmHint")}
            >
              <Check size={16} />
            </button>
            <button type="button" className="screenshot-overlay__cancel" onClick={onClose} aria-label={t("screenshot.cancel")} title={t("screenshot.cancelHint")}>
              <X size={16} />
            </button>
            {liveCount > 0 && <span className="screenshot-overlay__count">{liveCount}</span>}
          </div>
        </>
      )}
    </div>
  );

  return createPortal(body, document.body);
}

// ---- capture preview (tl 1167) -------------------------------------------

// A failed preview must be visible, never a silent black frame. The capture
// temp path is an os.MkdirTemp absolute path; the generic AttachmentDataURL
// bridge only serves workspace attachment relative paths and rejects it (a
// security invariant we must not loosen), which is exactly what used to paint
// the overlay black — the old code swallowed the rejection into an empty
// string. Previews now go through the screenshot-specific bridge, and every
// failure carries the backend reason into the overlay's error pane.
class PreviewLoadError extends Error {}

async function loadCapturePreview(path: string): Promise<string> {
  try {
    const url = await app.ScreenshotPreviewDataURL(path);
    if (!url) throw new Error("empty preview data URL");
    return url;
  } catch (error) {
    console.warn("[screenshot] preview load failed", error);
    const reason = error instanceof Error && error.message ? error.message : String(error);
    throw new PreviewLoadError(reason);
  }
}

function describeCaptureFailure(t: Translator, error: unknown): string {
  if (error instanceof PreviewLoadError) return t("screenshot.previewFailed", { reason: error.message });
  return t("screenshot.captureFailed");
}

// ---- canvas drawing ------------------------------------------------------

// Tool labels for the toolbar buttons; a small map instead of dynamic key
// lookups keeps the i18n key set statically checkable.
function toolLabel(t: Translator, tool: ScreenshotTool): string {
  switch (tool) {
    case "rect":
      return t("screenshot.tool.rect");
    case "arrow":
      return t("screenshot.tool.arrow");
    case "text":
      return t("screenshot.tool.text");
    case "mosaic":
      return t("screenshot.tool.mosaic");
    case "ellipse":
      return t("screenshot.tool.ellipse");
    case "pen":
      return t("screenshot.tool.pen");
    case "number":
      return t("screenshot.tool.number");
  }
}

// Draw one annotation onto a 2d context. Kept here (not in the pure module)
// because it needs the CanvasRenderingContext2D API; the geometry it uses is
// already pinned by the pure-model tests.
function drawAnnotation(ctx: CanvasRenderingContext2D, annotation: Annotation) {
  const { start, end, color } = annotation;
  ctx.save();
  ctx.strokeStyle = color;
  ctx.fillStyle = color;
  ctx.lineWidth = 3;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  switch (annotation.kind) {
    case "rect": {
      ctx.strokeRect(start.x, start.y, end.x - start.x, end.y - start.y);
      break;
    }
    case "arrow": {
      ctx.beginPath();
      ctx.moveTo(start.x, start.y);
      ctx.lineTo(end.x, end.y);
      ctx.stroke();
      const [left, right] = arrowHeadPoints(start, end);
      ctx.beginPath();
      ctx.moveTo(end.x, end.y);
      ctx.lineTo(left.x, left.y);
      ctx.moveTo(end.x, end.y);
      ctx.lineTo(right.x, right.y);
      ctx.stroke();
      break;
    }
    case "text": {
      ctx.font = "600 20px system-ui, sans-serif";
      ctx.textBaseline = "top";
      ctx.fillText(annotation.text ?? "", start.x, start.y);
      break;
    }
    case "mosaic": {
      // Mosaic: downscale the region into a tiny scratch canvas, then blow it
      // back up with nearest-neighbour smoothing off — the classic pixelate.
      const x = Math.min(start.x, end.x);
      const y = Math.min(start.y, end.y);
      const w = Math.abs(end.x - start.x);
      const h = Math.abs(end.y - start.y);
      if (w < 1 || h < 1) break;
      const block = 12;
      const tiny = document.createElement("canvas");
      tiny.width = Math.max(1, Math.round(w / block));
      tiny.height = Math.max(1, Math.round(h / block));
      const tctx = tiny.getContext("2d");
      if (!tctx) break;
      tctx.imageSmoothingEnabled = true;
      const source = ctx.getImageData(x, y, w, h);
      tctx.putImageData(source, 0, 0);
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(tiny, x, y, w, h);
      ctx.imageSmoothingEnabled = true;
      break;
    }
    // tl 1088 additions — same stroke/fill conventions as the tl 1087 tools.
    case "ellipse": {
      const bounds = ellipseBounds(annotation);
      ctx.beginPath();
      ctx.ellipse(
        bounds.x + bounds.width / 2,
        bounds.y + bounds.height / 2,
        Math.abs(bounds.width / 2),
        Math.abs(bounds.height / 2),
        0,
        0,
        Math.PI * 2,
      );
      ctx.stroke();
      break;
    }
    case "pen": {
      // Freehand: stroke the polyline as one continuous path. A stroke with
      // fewer than two samples is a no-op (buildPenAnnotation filters these,
      // but the live draft can be a single point mid-drag).
      const points = annotation.points ?? [];
      if (points.length < 2) break;
      ctx.beginPath();
      ctx.moveTo(points[0].x, points[0].y);
      for (let i = 1; i < points.length; i += 1) ctx.lineTo(points[i].x, points[i].y);
      ctx.stroke();
      break;
    }
    case "number": {
      // WeChat-style numbered badge: a filled disc with the index in white.
      ctx.beginPath();
      ctx.arc(start.x, start.y, NUMBER_BADGE_RADIUS, 0, Math.PI * 2);
      ctx.fill();
      ctx.fillStyle = "#fff";
      ctx.font = "700 16px system-ui, sans-serif";
      ctx.textAlign = "center";
      ctx.textBaseline = "middle";
      ctx.fillText(String(annotation.number ?? ""), start.x, start.y);
      ctx.textAlign = "start";
      ctx.textBaseline = "alphabetic";
      break;
    }
  }
  ctx.restore();
}

// Crop the screenshot region under the selection into a fresh canvas so the
// final PNG is exactly the selected pixels (tl 1087: a 300×200 selection must
// produce a 300×200 image, not the whole screen).
function screenshotRegionToCanvas(dataUrl: string, selection: { x: number; y: number; width: number; height: number }): HTMLCanvasElement | null {
  const image = screenshotImageCache.get(dataUrl);
  if (!image) return null;
  const canvas = document.createElement("canvas");
  canvas.width = Math.max(1, Math.round(selection.width));
  canvas.height = Math.max(1, Math.round(selection.height));
  const ctx = canvas.getContext("2d");
  if (!ctx) return null;
  ctx.drawImage(image, selection.x, selection.y, selection.width, selection.height, 0, 0, canvas.width, canvas.height);
  return canvas;
}

// The captured screenshot decodes asynchronously; caching the Image per data
// URL keeps the confirm path synchronous once the overlay has painted.
const screenshotImageCache = new Map<string, HTMLImageElement>();

export function primeScreenshotImageCache(dataUrl: string): Promise<void> {
  if (screenshotImageCache.has(dataUrl)) return Promise.resolve();
  return new Promise((resolve) => {
    const image = new Image();
    image.onload = () => {
      screenshotImageCache.set(dataUrl, image);
      resolve();
    };
    image.onerror = () => resolve();
    image.src = dataUrl;
  });
}
