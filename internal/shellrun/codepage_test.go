package shellrun

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"

	"reasonix/internal/proc"
	"reasonix/internal/tool"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

func withDecoderCandidates(t *testing.T, encs []encoding.Encoding) {
	t.Helper()
	prev := consoleDecoderCandidates
	consoleDecoderCandidates = func() []encoding.Encoding { return encs }
	t.Cleanup(func() { consoleDecoderCandidates = prev })
}

func encodeGBK(t *testing.T, s string) string {
	t.Helper()
	out, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(s))
	if err != nil {
		t.Fatalf("encode GBK: %v", err)
	}
	return string(out)
}

// TestDecodeConsoleOutputKeepsValidUTF8 pins the no-double-transcode contract:
// output that is already valid UTF-8 must pass through byte-for-byte even when
// legacy decoders are available.
func TestDecodeConsoleOutputKeepsValidUTF8(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	const raw = "中文 UTF-8 输出 ✅\nsecond line"
	if got := decodeConsoleOutput(raw); got != raw {
		t.Fatalf("valid UTF-8 was modified:\n got %q\nwant %q", got, raw)
	}
}

// TestDecodeConsoleOutputDecodesGBK is the core #11311 repro: console bytes in
// the system code page (GBK/936) must reach the transcript as readable text.
func TestDecodeConsoleOutputDecodesGBK(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	raw := encodeGBK(t, "中文批次输出完成\n适配器加载成功")
	got := decodeConsoleOutput(raw)
	if !strings.Contains(got, "中文批次输出完成") || !strings.Contains(got, "适配器加载成功") {
		t.Fatalf("GBK output not decoded: %q", trim(got, 120))
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("decoded output still has replacement characters: %q", trim(got, 120))
	}
}

// TestDecodeConsoleOutputFallsBackWhenUndecodable pins the safety net: bytes
// that no candidate can decode cleanly must come back unchanged, never
// half-decoded garbage and never an error.
func TestDecodeConsoleOutputFallsBackWhenUndecodable(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	raw := string([]byte{0xFF, 0xFE, 0x81, 'A'})
	if utf8.ValidString(raw) {
		t.Fatal("test payload must not be valid UTF-8")
	}
	if got := decodeConsoleOutput(raw); got != raw {
		t.Fatalf("undecodable output was mutated:\n got %q\nwant %q", got, raw)
	}
	// No candidates at all (non-Windows platforms) also passes through.
	withDecoderCandidates(t, nil)
	if got := decodeConsoleOutput(raw); got != raw {
		t.Fatalf("empty candidate list mutated output: %q", got)
	}
}

// TestRunForegroundDecodesSystemCodePageOutput exercises the integration path:
// the combined output returned to the model and the failure tail shown on tool
// cards are both decoded before leaving the runner.
func TestRunForegroundDecodesSystemCodePageOutput(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	raw := encodeGBK(t, "中文批次输出完成\n")

	res := RunForeground(context.Background(), Request{
		Argv: []string{"irrelevant"},
		Run: func(_ context.Context, cmd *exec.Cmd, _ proc.RunOptions) (*proc.TrackedCommand, error) {
			if _, err := io.WriteString(cmd.Stdout, raw); err != nil {
				return nil, err
			}
			return nil, nil
		},
	})
	if res.Err != nil {
		t.Fatalf("RunForeground: %v", res.Err)
	}
	if !strings.Contains(res.Combined, "中文批次输出完成") {
		t.Fatalf("combined output not decoded: %q", trim(res.Combined, 120))
	}

	// Failure path: the tail must carry the decoded diagnostics too, still
	// bounded by OutputTailMaxBytes after decoding.
	rawFail := encodeGBK(t, "错误：配置文件缺失 "+strings.Repeat("详", 200)+"\n")
	resFail := RunForeground(context.Background(), Request{
		Argv: []string{"irrelevant"},
		Run: func(_ context.Context, cmd *exec.Cmd, _ proc.RunOptions) (*proc.TrackedCommand, error) {
			if _, err := io.WriteString(cmd.Stdout, rawFail); err != nil {
				return nil, err
			}
			return nil, &exec.ExitError{}
		},
	})
	if resFail.Err == nil || resFail.State != tool.ShellStateFailed {
		t.Fatalf("expected failed state, got %q err=%v", resFail.State, resFail.Err)
	}
	if !strings.Contains(resFail.OutputTail, "错误：配置文件缺失") {
		t.Fatalf("failure tail not decoded: %q", trim(resFail.OutputTail, 120))
	}
	if len(resFail.OutputTail) > tool.OutputTailMaxBytes {
		t.Fatalf("decoded tail %d exceeds cap %d", len(resFail.OutputTail), tool.OutputTailMaxBytes)
	}
}

// TestRunForegroundKeepsUTF8OutputUntouched guards the other half of the
// contract end to end: a UTF-8-producing shell must not be double-transcoded
// just because legacy decoders are configured.
func TestRunForegroundKeepsUTF8OutputUntouched(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	const payload = "中文 UTF-8 输出保持原样 ✅\n"
	res := RunForeground(context.Background(), Request{
		Argv: []string{"irrelevant"},
		Run: func(_ context.Context, cmd *exec.Cmd, _ proc.RunOptions) (*proc.TrackedCommand, error) {
			if _, err := io.WriteString(cmd.Stdout, payload); err != nil {
				return nil, err
			}
			return nil, errors.New("exit")
		},
	})
	if res.Combined != payload {
		t.Fatalf("UTF-8 payload was modified:\n got %q\nwant %q", res.Combined, payload)
	}
}

// TestDecodeConsoleOutputAllCandidatesRejectedFallsBack covers the ordering
// rule: an earlier candidate that decodes "successfully" into replacement
// characters must not win; a later clean decode does.
func TestDecodeConsoleOutputAllCandidatesRejectedFallsBack(t *testing.T) {
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	raw := string([]byte{0xFF, 0x41, 0xFF, 0x42})
	if got := decodeConsoleOutput(raw); got != raw {
		t.Fatalf("rejected decode did not fall back:\n got %q\nwant %q", got, raw)
	}
}

func TestUTF8SafeTrimTail(t *testing.T) {
	s := "abc中文字" // 12 bytes: 3 ASCII runes + 3 three-byte CJK runes
	if got := utf8SafeTrimTail(s, 100); got != s {
		t.Fatalf("short string changed: %q", got)
	}
	// Cap 4 lands inside 文 (bytes 6-8); the cut must slide to the start of 字.
	got := utf8SafeTrimTail(s, 4)
	if got != "字" {
		t.Fatalf("tail = %q, want %q", got, "字")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("tail split a UTF-8 sequence: %q", got)
	}
	if got := utf8SafeTrimTail(s, 0); got != s {
		t.Fatalf("non-positive cap must return s unchanged, got %q", got)
	}
}
