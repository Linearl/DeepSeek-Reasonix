//go:build windows

package shellrun

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// TestPlatformDecoderCandidatesRealSystem exercises the real Windows code page
// lookup (no injection): the candidate list must never be empty, and on a
// Chinese-locale system (console code page 936) real GBK console bytes must
// decode through the production path. On other locales the GBK branch is
// simply not asserted — the file is Windows-only, nothing is skipped.
func TestPlatformDecoderCandidatesRealSystem(t *testing.T) {
	encs := platformDecoderCandidates()
	if len(encs) == 0 {
		t.Fatal("windows must expose at least one code page decoder")
	}
	cp, err := windows.GetConsoleOutputCP()
	if err != nil {
		t.Fatalf("GetConsoleOutputCP: %v", err)
	}
	if cp != 936 {
		t.Logf("console output code page %d is not Chinese; skipping GBK branch", cp)
		return
	}
	enc, ok := encodingForCodePage(cp)
	if !ok {
		t.Fatalf("code page %d has no decoder mapping", cp)
	}
	if _, isGBK := enc.(interface{ String() string }); !isGBK {
		t.Fatalf("code page 936 mapped to unexpected decoder %T", enc)
	}
	raw := encodeGBK(t, "ipconfig 输出：以太网适配器 本地连接")
	got := decodeConsoleOutput(raw)
	if !strings.Contains(got, "以太网适配器") {
		t.Fatalf("real-system GBK output not decoded: %q", trim(got, 120))
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("real-system decode introduced replacement characters: %q", trim(got, 120))
	}
}
