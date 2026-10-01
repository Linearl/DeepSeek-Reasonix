package shellrun

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// consoleDecoderCandidates is a test seam: it returns the platform's list of
// legacy decoders to try, most likely first. The default implementations live
// in codepage_windows.go (system code pages) and codepage_other.go (none).
var consoleDecoderCandidates = platformDecoderCandidates

// decodeConsoleOutput converts raw child-process bytes into displayable UTF-8
// text (upstream #11311 → #11327: Windows console programs write in the active
// code page — e.g. GBK/936 on Chinese systems — which used to reach the TUI and
// the session transcript as U+FFFD replacement characters).
//
// The cascade is intentionally conservative:
//  1. Valid UTF-8 passes through byte-for-byte (Git Bash / modern CLI tools),
//     so well-behaved output is never double-transcoded.
//  2. Each system code-page candidate gets one decode; the first result free of
//     errors and replacement characters wins.
//  3. If nothing decodes cleanly the original bytes are returned unchanged —
//     decoding never fails the command, corrupts output further, or blocks the
//     result path.
func decodeConsoleOutput(raw string) string {
	return decodeConsoleOutputWith(consoleDecoderCandidates(), raw)
}

func decodeConsoleOutputWith(candidates []encoding.Encoding, raw string) string {
	if raw == "" || utf8.ValidString(raw) {
		return raw
	}
	for _, enc := range candidates {
		if enc == nil {
			continue
		}
		decoded, ok := tryDecodeCodePage(enc, raw)
		if ok {
			return decoded
		}
	}
	return raw
}

// tryDecodeCodePage reports success only when the decode is complete and did
// not introduce replacement characters. Some x/text decoders substitute U+FFFD
// silently instead of returning an error, so both signals are required; a
// "successful" decode full of U+FFFD would swap one kind of mojibake for
// another.
func tryDecodeCodePage(enc encoding.Encoding, raw string) (string, bool) {
	decoded, _, err := transform.Bytes(enc.NewDecoder(), []byte(raw))
	if err != nil || strings.ContainsRune(string(decoded), '\uFFFD') {
		return "", false
	}
	return string(decoded), true
}

// chineseSupersetDecoder returns the GB18030 decoder when either Windows code
// page is Chinese. GB18030 is a strict superset of GBK, so it catches GBK
// variants the strict GBK decoder rejects; outside Chinese locales it would
// only turn arbitrary binary into confident CJK garbage, so it stays gated.
func chineseSupersetDecoder(cps []uint32) encoding.Encoding {
	for _, cp := range cps {
		if cp == 936 || cp == 54936 {
			return simplifiedchinese.GB18030
		}
	}
	return nil
}

// utf8SafeTrimTail keeps at most max bytes from the end of s without splitting
// a UTF-8 sequence at the cut point, so a decoded tail never opens with a
// dangling continuation byte.
func utf8SafeTrimTail(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	cut := len(s) - max
	for cut < len(s) && s[cut]&0xC0 == 0x80 {
		cut++
	}
	return s[cut:]
}
