package shellrun

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"

	"reasonix/internal/proc"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// Progress-path coverage for the code-page decode (upstream #11327): live
// progress chunks used to be forwarded as raw child bytes, so GBK text showed
// replacement characters in the TUI while the final result was fine. The
// writer must buffer until a safe decode point (newline / flush), decode with
// the same cascade as the final output, and keep ASCII live.

// TestProgressDecodesSplitCodePageAndUTF8Characters feeds the writer one byte
// at a time — the worst-case pipe behavior — and requires the joined progress
// to read exactly like the child's text in either encoding.
func TestProgressDecodesSplitCodePageAndUTF8Characters(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"GBK", encodeGBK(t, "参数\n"), "参数\n"},
		{"GBK without newline", encodeGBK(t, "参数"), "参数"},
		{"UTF-8", "参数\n", "参数\n"},
		{"UTF-8 without newline", "参数", "参数"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got strings.Builder
			w := newProgressWriter(func(s string) { got.WriteString(s) }, 1<<20, progressOutputTruncated)
			for i := range len(tc.data) {
				if _, err := w.Write([]byte{tc.data[i]}); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			w.Flush()
			if got.String() != tc.want {
				t.Fatalf("progress = %q, want %q", got.String(), tc.want)
			}
			if strings.ContainsRune(got.String(), '\uFFFD') {
				t.Fatalf("progress invented replacement characters: %q", got.String())
			}
		})
	}
}

// TestProgressKeepsASCIIPartialsLive pins the liveness rule: bytes below
// utf8.RuneSelf mean the same in UTF-8 and the code pages, so dot-progress and
// prompts must still arrive without waiting for a newline or Flush.
func TestProgressKeepsASCIIPartialsLive(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	var got strings.Builder
	w := newProgressWriter(func(s string) { got.WriteString(s) }, 1<<20, progressOutputTruncated)
	if _, err := w.Write([]byte("building...")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got.String() != "building..." {
		t.Fatalf("partial ASCII progress = %q, want live forwarding", got.String())
	}
}

// TestProgressCapDoesNotSplitCodePageCharacter writes more GBK than the emit
// budget holds: the clipped text must end on a whole character ("参"), never
// mid-character and never as raw bytes just because the budget ran out.
func TestProgressCapDoesNotSplitCodePageCharacter(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	var got strings.Builder
	w := newProgressWriter(func(s string) { got.WriteString(s) }, 4, progressOutputTruncated)
	data := encodeGBK(t, "参数\n") // 5 raw bytes: 2 + 2 + newline
	if _, err := w.Write([]byte(data)); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Flush()
	want := "参" + progressOutputTruncated
	if got.String() != want {
		t.Fatalf("bounded progress = %q, want %q", got.String(), want)
	}
	if strings.ContainsRune(got.String(), '\uFFFD') {
		t.Fatalf("clipped progress invented replacement characters: %q", got.String())
	}
}

// TestForegroundCodePageProgressMatchesFinalOutput exercises the integration
// path with a child whose GBK output is split across pipe reads mid-character:
// joined live progress and the final combined output must agree, and both must
// be readable.
func TestForegroundCodePageProgressMatchesFinalOutput(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	raw := encodeGBK(t, "错误：参数格式不正确\n")

	var progress strings.Builder
	res := RunForeground(context.Background(), Request{
		Argv:     []string{"irrelevant"},
		Progress: func(chunk string) { progress.WriteString(chunk) },
		Run: func(_ context.Context, cmd *exec.Cmd, _ proc.RunOptions) (*proc.TrackedCommand, error) {
			// Split inside 数's two-byte sequence, as a real pipe would.
			if _, err := io.WriteString(cmd.Stdout, raw[:5]); err != nil {
				return nil, err
			}
			if _, err := io.WriteString(cmd.Stdout, raw[5:]); err != nil {
				return nil, err
			}
			return nil, nil
		},
	})
	if res.Err != nil {
		t.Fatalf("RunForeground: %v", res.Err)
	}
	want := "错误：参数格式不正确\n"
	if res.Combined != want {
		t.Fatalf("final output = %q, want %q", res.Combined, want)
	}
	if got := progress.String(); got != want {
		t.Fatalf("progress = %q, want %q", got, want)
	}
}

// TestProgressKeepsUTF8OutputUntouched guards the no-double-transcode contract
// on the live path: valid UTF-8 must pass through byte-for-byte with legacy
// decoders configured, whichever way pipe reads chunk it.
func TestProgressKeepsUTF8OutputUntouched(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	const payload = "中文 UTF-8 进度 ✅ done 42%\n"
	for _, at := range []int{1, 2, len(payload) / 2} {
		var got strings.Builder
		w := newProgressWriter(func(s string) { got.WriteString(s) }, 1<<20, progressOutputTruncated)
		if _, err := w.Write([]byte(payload[:at])); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := w.Write([]byte(payload[at:])); err != nil {
			t.Fatalf("write: %v", err)
		}
		w.Flush()
		if got.String() != payload {
			t.Fatalf("UTF-8 progress (split at %d) was modified:\n got %q\nwant %q", at, got.String(), payload)
		}
	}
}

// TestProgressUndecodableFallsBackToRaw pins the safety net on the live path:
// bytes no candidate decodes cleanly are forwarded unchanged, never dropped
// and never turned into an error.
func TestProgressUndecodableFallsBackToRaw(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	raw := string([]byte{0xFF, 0xFE, 0x81, 'A'})
	var got strings.Builder
	w := newProgressWriter(func(s string) { got.WriteString(s) }, 1<<20, progressOutputTruncated)
	if _, err := w.Write([]byte(raw)); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Flush()
	if got.String() != raw {
		t.Fatalf("undecodable progress was mutated:\n got %q\nwant %q", got.String(), raw)
	}
}

// TestProgressFlushAfterTruncatedIsNoop: after the cap fires, Flush must not
// resurrect output or emit a second marker.
func TestProgressFlushAfterTruncatedIsNoop(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	var got strings.Builder
	w := newProgressWriter(func(s string) { got.WriteString(s) }, 8, progressOutputTruncated)
	if _, err := w.Write([]byte(strings.Repeat("x", 16))); err != nil {
		t.Fatalf("write: %v", err)
	}
	w.Flush()
	before := got.String()
	w.Flush()
	if got.String() != before {
		t.Fatalf("second Flush changed output: %q -> %q", before, got.String())
	}
	if n := strings.Count(got.String(), progressOutputTruncated); n != 1 {
		t.Fatalf("markers = %d, want 1", n)
	}
}
