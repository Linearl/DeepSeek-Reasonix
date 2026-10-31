package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// tl1083 顺带修复的回归钉：snapToRuneBoundary 对 lo==len(s)（空尾形态）必须
// 返回空串而不是索引越界 panic。触达形态 = 分块摘要碎片的省略预算摊到单条
// 消息不足 contentElideOversizedDiv ⇒ cutChars=1 ⇒ tailChars=0 ⇒
// elideOversizedContentsAt / keepHeadTail 以 (s, len(s), len(s)) 取尾。该
// panic 在 pressure 分块道开通前仅 manual/overflow 可达，开通后自动压缩
// 随时可踩（TestPressurePersistentSizeFailureStaysVisibleAfterChunkedLane
// 的 fixture 即原始现场）。
func TestSnapToRuneBoundaryTotalOverEmptyTail(t *testing.T) {
	ascii := strings.Repeat("word ", 400) // the incident's 2000-byte shape
	multi := strings.Repeat("汉", 300)    // 3-byte runes: boundary snapping matters

	cases := []struct {
		name string
		s    string
		lo   int
		hi   int
		want string
	}{
		{"empty tail at len", ascii, len(ascii), len(ascii), ""},
		{"empty tail on multibyte", multi, len(multi), len(multi), ""},
		{"empty string", "", 0, 0, ""},
		{"head keep", ascii, 0, 1, "w"},
		{"tail keep snaps to rune start", multi, len(multi) - 2, len(multi), "汉"}, // 2 bytes back lands mid-rune, snapped to the full rune
		{"hi clamps beyond len", ascii, 0, len(ascii) + 100, ascii},
	}
	for _, c := range cases {
		got := snapToRuneBoundary(c.s, c.lo, c.hi)
		if got != c.want {
			t.Fatalf("%s: snapToRuneBoundary(%q…, %d, %d) = %q, want %q", c.name, c.s[:min(10, len(c.s))], c.lo, c.hi, got, c.want)
		}
		if !utf8.ValidString(got) {
			t.Fatalf("%s: result %q is not valid UTF-8", c.name, got)
		}
	}
}
