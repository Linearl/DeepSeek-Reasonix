// Shared canvas renderer for annotation shapes.
//
// tl 1087 built this for the screenshot overlay; tl 1412 lifts it out of the
// component so the image-viewer annotation editor paints with the exact same
// semantics (one renderer, two hosts — the pure geometry stays in
// screenshotAnnotation.ts, only the CanvasRenderingContext2D touching part
// lives here). Callers that need a different output resolution scale the
// context first (ctx.scale) — coordinates AND stroke widths scale together.
import {
  NUMBER_BADGE_RADIUS,
  arrowHeadPoints,
  ellipseBounds,
  type Annotation,
} from "./screenshotAnnotation";

// Draw one annotation onto a 2d context. The optional per-annotation
// `width` (tl 1412: adjustable pen/shape stroke) defaults to the tl 1087
// screenshot flow's fixed 3px so the overlay's output is pixel-identical.
export function drawAnnotation(ctx: CanvasRenderingContext2D, annotation: Annotation) {
  const { start, end, color } = annotation;
  ctx.save();
  ctx.strokeStyle = color;
  ctx.fillStyle = color;
  ctx.lineWidth = annotation.width ?? 3;
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
