import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronLeft, ChevronRight, X } from "lucide-react";
import { useT } from "../lib/i18n";

/** One navigable entry of a multi-image viewer session (tl1289). */
export interface ImageViewerImage {
  url: string;
  name?: string;
}

export interface ImageViewerProps {
  open: boolean;
  /** data-URL of the image to display */
  imageUrl: string;
  /** optional filename shown at the bottom */
  imageName?: string;
  onClose: () => void;
  /**
   * tl1289: ordered image list of the host message. When it holds more than
   * one entry, the viewer grows prev/next arrow buttons (hover-shown),
   * ←/→ key bindings and an index counter (e.g. 3/8). With zero or one
   * entry — or when the prop is omitted — the viewer behaves exactly as
   * before (no switch UI), so single-image callers are unaffected.
   */
  images?: ImageViewerImage[];
  /** which entry of `images` the viewer should start on (clamped) */
  initialIndex?: number;
}

/**
 * ImageViewer renders a full-size image preview in a portal overlay.
 *
 * It follows the same portal pattern as MermaidDiagram fullscreen:
 * the overlay is portaled into .chat-pane (fallback document.body) so it
 * covers the chat area without being clipped by overflow containers.
 */
export function ImageViewer({ open, imageUrl, imageName, onClose, images, initialIndex }: ImageViewerProps) {
  const t = useT();
  const [portalTarget, setPortalTarget] = useState<Element | null>(null);
  const [visible, setVisible] = useState(false);
  // tl1289: position inside the multi-image list; -1 means "single-image mode".
  const [index, setIndex] = useState(0);

  const total = images?.length ?? 0;
  const multi = total > 1;
  const safeIndex = multi ? Math.min(Math.max(index, 0), total - 1) : 0;
  const current = multi ? images![safeIndex] : { url: imageUrl, name: imageName };

  // Re-arm the starting position each time the viewer opens for another entry.
  useEffect(() => {
    if (!open || !multi) return;
    const wanted = initialIndex ?? 0;
    setIndex(Math.min(Math.max(wanted, 0), total - 1));
  }, [open, multi, initialIndex, total]);

  const goPrev = useCallback(() => {
    setIndex((i) => Math.max(0, Math.min(i, total - 1) - 1));
  }, [total]);
  const goNext = useCallback(() => {
    setIndex((i) => Math.min(total - 1, Math.max(i, 0) + 1));
  }, [total]);

  // Resolve portal target when opening.
  useEffect(() => {
    if (!open) {
      setVisible(false);
      setPortalTarget(null);
      return;
    }
    const target = document.querySelector(".chat-pane") ?? document.body;
    setPortalTarget(target);
    // Trigger enter animation on the next frame.
    const raf = requestAnimationFrame(() => setVisible(true));
    return () => cancelAnimationFrame(raf);
  }, [open]);

  // Close on Escape; tl1289: switch on ArrowLeft/ArrowRight in multi-image mode.
  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
        return;
      }
      if (!multi) return;
      if (event.key === "ArrowLeft") goPrev();
      else if (event.key === "ArrowRight") goNext();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [open, onClose, multi, goPrev, goNext]);

  // Prevent body scroll while open.
  useEffect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [open]);

  const handleBackdropClick = useCallback(
    (e: React.MouseEvent) => {
      if (e.target === e.currentTarget) onClose();
    },
    [onClose],
  );

  if (!open || !portalTarget || !current.url) return null;

  const overlay = (
    <div
      className={`image-viewer-backdrop${visible ? " image-viewer--enter" : ""}`}
      onClick={handleBackdropClick}
      role="dialog"
      aria-modal="true"
      aria-label={current.name || t("imageViewer.title")}
    >
      <button
        className="image-viewer__close"
        type="button"
        onClick={onClose}
        aria-label={t("imageViewer.closePreview")}
      >
        <X size={22} />
      </button>
      {multi && (
        <>
          <div className="image-viewer__counter">
            {safeIndex + 1} / {total}
          </div>
          <button
            className="image-viewer__nav image-viewer__nav--prev"
            type="button"
            onClick={goPrev}
            disabled={safeIndex <= 0}
            aria-label={t("imageViewer.prevImage")}
          >
            <ChevronLeft size={22} />
          </button>
          <button
            className="image-viewer__nav image-viewer__nav--next"
            type="button"
            onClick={goNext}
            disabled={safeIndex >= total - 1}
            aria-label={t("imageViewer.nextImage")}
          >
            <ChevronRight size={22} />
          </button>
        </>
      )}
      <div className="image-viewer__content">
        <img
          className="image-viewer__image"
          src={current.url}
          alt={current.name || ""}
          draggable={false}
        />
        {current.name && <div className="image-viewer__name">{current.name}</div>}
      </div>
    </div>
  );

  return createPortal(overlay, portalTarget);
}
