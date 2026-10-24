package agent

import (
	"encoding/base64"
	"encoding/binary"

	"reasonix/internal/provider"
)

// 任务 1025: 图片 token 折算口径。此前图片按 base64 字符 × 1/4 折算（1010 的
// 单图 8KiB 封顶折出来约 2K tokens/图），对小图高估数倍、与 provider 真实计费
// 脱节，是把含图会话过早推向压缩的主力之一。现在按分辨率折算：嗅探图片头部的
// 宽高，按 provider 视觉tokenizer 的公开计价公式折算，并保留保守余量防止矫枉
// 过正（1010 的教训：图片被低估会让每个阈值都失明）。
const (
	// imageEstimateMaxDim mirrors the local vision pipeline (control.maxVisionDim)
	// and the provider-side downscale: longer side is charged at this cap, so a
	// 4K screenshot prices like a 1080p one — exactly as providers bill it.
	imageEstimateMaxDim = 1568

	// imageEstimateAreaDivisor is Anthropic's documented vision pricing
	// (tokens ≈ w×h/750 after long-edge downscale). Qwen-VL's tile grid and
	// GLM-4V land in the same band for common screenshot sizes, so the divisor
	// is the provider-aligned anchor.
	imageEstimateAreaDivisor = 750

	// imageEstimateUpliftPct adds a conservative share over the anchor so the
	// local reading stays at or above the cheaper providers (Claude exact,
	// Qwen close) and never re-opens the 1010 blind spot.
	imageEstimateUpliftPct = 15

	// imageEstimateTokenFloor keeps tiny UI icons from pricing at a handful of
	// tokens; providers bill framing/tile minimums that pure area misses.
	imageEstimateTokenFloor = 256

	// imageEstimateFallbackTokens prices images whose dimensions cannot be
	// sniffed (HTTP URLs, file ids, truncated or exotic headers) at the 任务
	// 1010 caliber: the 8KiB per-image byte cap at the ~4-chars-per-token cold
	// rate. It is deliberately the conservative end so an unreadable header
	// never under-prices a real screenshot.
	imageEstimateFallbackTokens = requestImageEstimateByteCap / 4
)

// imageEstimateTokens prices one stored image reference the way vision
// providers do — by pixel area after long-edge downscale, not by base64
// length. Data URLs are sniffed from their header bytes; anything unreadable
// falls back to the 任务 1010 flat cap.
//
// 任务 1116 件 3-5d（口径互指，勿"统一"）：这里是**计价口径**（任务 1025/1040
// 的显示读数：ContextUsedTokens 主读数用它）。与之相对，**判据口径**在
// session_health.go 的 estimateProjectionTokens（Diet 触发、健康检查用
// base64 ÷4 的保守上界）。两套口径用途不同、故意不同：计价要贴近 provider
// 实账，判据要保守上界（宁高勿低）。改任何一套前先读另一套的注释，禁止把
// 它们"统一"成一个数字——那会把 Diet 判据改盲（低估 ⇒ 超窗不触发）或把显示
// 读数改虚高。
func imageEstimateTokens(imageRef string) int {
	_, payload, ok := provider.ParseImageDataURL(imageRef)
	if !ok {
		return imageEstimateFallbackTokens
	}
	w, h, ok := sniffEncodedImageDimensions(payload)
	if !ok {
		return imageEstimateFallbackTokens
	}
	w, h = imageEstimateScaledDims(w, h)
	tokens := (w*h + imageEstimateAreaDivisor - 1) / imageEstimateAreaDivisor
	tokens = (tokens*(100+imageEstimateUpliftPct) + 99) / 100
	return max(tokens, imageEstimateTokenFloor)
}

// imageEstimateScaledDims clamps the longest side to imageEstimateMaxDim —
// but only when it actually exceeds the cap, mirroring compressForVision and
// the provider-side downscale (each side at least 1px).
func imageEstimateScaledDims(w, h int) (int, int) {
	long := max(w, h)
	if long <= imageEstimateMaxDim {
		return w, h
	}
	if w >= h {
		return imageEstimateMaxDim, max(h*imageEstimateMaxDim/w, 1)
	}
	return max(w*imageEstimateMaxDim/h, 1), imageEstimateMaxDim
}

// imageSniffMaxBytes bounds the base64 prefix decoded for header sniffing.
// 64KiB of decoded bytes covers every PNG/GIF/WebP/BMP header and virtually
// all JPEG segment chains (EXIF + ICC); a SOF hidden deeper than that falls
// back to the conservative flat price instead of decoding megabytes per image
// per estimate.
const imageSniffMaxBytes = 64 << 10

// sniffEncodedImageDimensions reads the pixel dimensions of a base64 image
// payload from its format header alone. No codec is invoked; the magic bytes
// decide the layout, not the declared media type.
func sniffEncodedImageDimensions(b64 string) (int, int, bool) {
	head, ok := decodeBase64Prefix(b64, imageSniffMaxBytes)
	if !ok {
		return 0, 0, false
	}
	switch {
	case hasPrefixBytes(head, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		if len(head) < 24 {
			return 0, 0, false
		}
		return saneDimensions(int(binary.BigEndian.Uint32(head[16:])), int(binary.BigEndian.Uint32(head[20:])))
	case hasPrefixBytes(head, []byte("GIF8")):
		if len(head) < 10 {
			return 0, 0, false
		}
		return saneDimensions(int(binary.LittleEndian.Uint16(head[6:])), int(binary.LittleEndian.Uint16(head[8:])))
	case hasPrefixBytes(head, []byte{'B', 'M'}):
		if len(head) < 26 {
			return 0, 0, false
		}
		// BITMAPINFOHEADER: height is negative for top-down rows.
		w := absDimension(int(int32(binary.LittleEndian.Uint32(head[18:]))))
		h := absDimension(int(int32(binary.LittleEndian.Uint32(head[22:]))))
		return saneDimensions(w, h)
	case hasPrefixBytes(head, []byte("RIFF")) && len(head) >= 30 && hasPrefixBytes(head[8:], []byte("WEBP")):
		return sniffWebPDimensions(head[12:])
	default:
		return sniffJPEGDimensions(head)
	}
}

// sniffWebPDimensions reads the canvas size from the VP8/VP8L/VP8X chunk.
func sniffWebPDimensions(chunk []byte) (int, int, bool) {
	switch {
	case hasPrefixBytes(chunk, []byte("VP8X")):
		// Layout: "VP8X" + 4-byte size + flags(1) + reserved(3) + canvas w/h
		// minus one, 24-bit little-endian each.
		if len(chunk) < 18 {
			return 0, 0, false
		}
		w := int(chunk[12]) | int(chunk[13])<<8 | int(chunk[14])<<16
		h := int(chunk[15]) | int(chunk[16])<<8 | int(chunk[17])<<16
		return saneDimensions(w+1, h+1)
	case hasPrefixBytes(chunk, []byte("VP8L")):
		// Layout: "VP8L" + 4-byte size + signature 0x2F + 14-bit w/h minus one.
		if len(chunk) < 13 || chunk[8] != 0x2F {
			return 0, 0, false
		}
		bits := binary.LittleEndian.Uint32(chunk[9:])
		return saneDimensions(int(bits&0x3FFF)+1, int((bits>>14)&0x3FFF)+1)
	case hasPrefixBytes(chunk, []byte("VP8 ")):
		// Lossy keyframe: "VP8 " + 4-byte size + 3-byte frame tag + 3-byte
		// start code (9D 01 2A), then 14-bit dimensions.
		if len(chunk) < 18 || chunk[11] != 0x9D || chunk[12] != 0x01 || chunk[13] != 0x2A {
			return 0, 0, false
		}
		w := int(binary.LittleEndian.Uint16(chunk[14:]) & 0x3FFF)
		h := int(binary.LittleEndian.Uint16(chunk[16:]) & 0x3FFF)
		return saneDimensions(w, h)
	}
	return 0, 0, false
}

// sniffJPEGDimensions walks the segment chain until a frame header (SOFn)
// carries the dimensions. Returns false when the chain ends inside the
// decoded prefix — the conservative fallback covers the rest.
func sniffJPEGDimensions(head []byte) (int, int, bool) {
	if len(head) < 4 || head[0] != 0xFF || head[1] != 0xD8 || head[2] != 0xFF {
		return 0, 0, false
	}
	i := 2
	for i+4 <= len(head) {
		if head[i] != 0xFF {
			return 0, 0, false
		}
		marker := head[i+1]
		if marker == 0xFF { // padding byte, keep scanning
			i++
			continue
		}
		if marker == 0xD8 || (marker >= 0xD0 && marker <= 0xD9) { // standalone markers
			i += 2
			continue
		}
		if i+4 > len(head) {
			return 0, 0, false
		}
		segLen := int(binary.BigEndian.Uint16(head[i+2:]))
		if segLen < 2 {
			return 0, 0, false
		}
		if marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
			if i+9 > len(head) {
				return 0, 0, false
			}
			h := int(binary.BigEndian.Uint16(head[i+5:]))
			w := int(binary.BigEndian.Uint16(head[i+7:]))
			return saneDimensions(w, h)
		}
		i += 2 + segLen
	}
	return 0, 0, false
}

// decodeBase64Prefix decodes at most maxBytes of payload from a base64 string,
// rounding the input down to a whole quantum so a truncated tail never fails
// the decode.
func decodeBase64Prefix(b64 string, maxBytes int) ([]byte, bool) {
	if b64 == "" {
		return nil, false
	}
	chars := min(len(b64), (maxBytes*4+2)/3)
	chars -= chars % 4
	if chars <= 0 {
		return nil, false
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(chars))
	n, err := base64.StdEncoding.Decode(decoded, []byte(b64[:chars]))
	if err != nil {
		return nil, false
	}
	return decoded[:n], true
}

func hasPrefixBytes(b, prefix []byte) bool {
	if len(b) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if b[i] != p {
			return false
		}
	}
	return true
}

// absDimension folds a negative (top-down) height; saneDimensions rejects
// non-positive or absurd sizes so a corrupt header cannot poison the estimate.
func absDimension(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func saneDimensions(w, h int) (int, int, bool) {
	if w <= 0 || h <= 0 || w > 1<<16 || h > 1<<16 {
		return 0, 0, false
	}
	return w, h, true
}
