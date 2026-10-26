package control

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif" // register gif decoder
	"image/jpeg"
	"image/png"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register webp decoder
)

// MaxImageAttachmentBytes is the attachment image ceiling; the desktop
// screenshot preview bridge (tl 1167) stats against it before reading the
// capture temp file, so the constant has exactly one definition.
const MaxImageAttachmentBytes = maxImageAttachmentBytes

// maxVisionDim caps the longest image side sent to a model. OpenAI and Anthropic
// downscale to roughly this server-side anyway, so a larger upload only wastes
// request bytes and image tokens without adding fidelity.
const maxVisionDim = 1568

// maxDecodePixels guards against decompression-bomb attachments: a tiny file can
// declare enormous dimensions. Beyond this we skip decoding and send as-is (still
// bounded by the 64 MB file cap).
const maxDecodePixels = 50_000_000

// compressForVision downscales an oversized image to maxVisionDim and re-encodes
// it — PNG/GIF stay lossless (screenshots, text, transparency), JPEG/WebP go to
// JPEG. Best-effort: an undecodable format, a decode/encode failure, or an image
// already within budget returns the original bytes and mime unchanged.
func compressForVision(raw []byte, mime string) ([]byte, string) {
	switch mime {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return raw, mime // bmp/tiff/svg: no decoder wired, send original
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width*cfg.Height > maxDecodePixels {
		return raw, mime
	}
	if cfg.Width <= maxVisionDim && cfg.Height <= maxVisionDim {
		return raw, mime // within budget — no point re-encoding
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw, mime
	}
	w, h := scaledDims(cfg.Width, cfg.Height, maxVisionDim)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), xdraw.Over, nil)

	var buf bytes.Buffer
	if mime == "image/png" || mime == "image/gif" {
		if err := png.Encode(&buf, dst); err != nil {
			return raw, mime
		}
		return buf.Bytes(), "image/png"
	}
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return raw, mime
	}
	return buf.Bytes(), "image/jpeg"
}

// scaledDims returns dimensions with the longest side clamped to m, preserving
// aspect ratio (each side at least 1px).
func scaledDims(w, h, m int) (int, int) {
	if w >= h {
		nh := max(h*m/w, 1)
		return m, nh
	}
	nw := max(w*m/h, 1)
	return nw, m
}

// previewLosslessMaxBytes is the byte ceiling under which the desktop preview
// is served the capture bytes verbatim (tl 1178). The preview is painted for
// human eyes on a local surface — it has no vision-token budget — so this path
// has no resolution cap and no re-encode; feeding it the model-vision pipeline
// (compressForVision: rescale to maxVisionDim, JPEG q85 for lossy mimes) was
// exactly what blurred tl 1167's screenshot overlay on wide captures. The
// ceiling only guards the IPC hop: a base64 data URL is ~1.37x its payload,
// and handing the frontend a tens-of-MB string is a jank source of its own.
// Captures above the ceiling fall back to the vision pipeline so the data URL
// stays bounded; typical screenshots (hundreds of KB to a few MB) never hit
// that degrade path. The vision chain's own callers (attachments, refs) are
// untouched — this constant means nothing to them.
const previewLosslessMaxBytes = 4 << 20

// PreviewImageDataURL encodes in-memory image bytes for the desktop preview
// channel as a data URL, serving them verbatim unless the payload alone
// exceeds previewLosslessMaxBytes (then, and only then, the vision pipeline
// degrades it). An empty mime means detect-from-bytes (the tl 1167 screenshot
// preview bridge has no trusted mime to pass): bytes that are not a
// recognisable image are an explicit error, never an empty string — the
// overlay must show a visible failure, not silently paint black.
func PreviewImageDataURL(raw []byte, mime string) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("preview image payload is empty")
	}
	if len(raw) > maxImageAttachmentBytes {
		return "", fmt.Errorf("preview image payload exceeds %d bytes", maxImageAttachmentBytes)
	}
	if mime == "" {
		mime = detectedImageMime(raw)
		if mime == "" {
			return "", fmt.Errorf("preview payload is not a recognised image format")
		}
	}
	if len(raw) > previewLosslessMaxBytes {
		raw, mime = compressForVision(raw, mime)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}
