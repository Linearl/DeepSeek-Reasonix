package provider

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestStreamScannerDistinguishesTruncationFromMalformedEvents(t *testing.T) {
	for _, tc := range []struct {
		name, line  string
		interrupted bool
	}{
		{"cut string", `data: {"delta":"partial`, true},
		{"cut literal", `data: {"delta":nu`, true},
		{"malformed complete line", "data: {\"delta\":nu\n", false},
		{"malformed complete CRLF", "data: {\"delta\":nu\r\n", false},
		{"malformed earlier field", `data: {oops}`, false},
		{"html gateway reply", "data: <html>bad gateway</html>", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStreamScanner(strings.NewReader("event: delta\n"+tc.line), 1024*1024)
			for range 2 {
				if !s.Scan() {
					t.Fatal("missing lines")
				}
			}
			payload := strings.TrimSpace(strings.TrimPrefix(s.Text(), "data:"))
			var v any
			err := json.Unmarshal([]byte(payload), &v)
			if err == nil {
				t.Fatal("fixture must fail decode")
			}
			got := s.DecodeError("fixture", payload, err)
			if IsStreamInterrupted(got) != tc.interrupted {
				t.Fatalf("interrupted=%v error=%v", IsStreamInterrupted(got), got)
			}
			if tc.interrupted && !ClassifyRecovery(got).Retryable {
				t.Fatal("cut stream is not retryable")
			}
		})
	}
}

func TestStreamScannerCompleteTerminalWithoutNewlineStillDecodes(t *testing.T) {
	s := NewStreamScanner(strings.NewReader(`data: {"finish_reason":"stop"}`), 1024*1024)
	if !s.Scan() {
		t.Fatal("missing final line")
	}
	var value any
	if err := json.Unmarshal([]byte(strings.TrimPrefix(s.Text(), "data: ")), &value); err != nil {
		t.Fatal(err)
	}
	if s.Scan() || s.Err() != nil {
		t.Fatal("unexpected extra line or error")
	}
}

// tl1310 深查: SSE chunk 边界重组层钉死。字段证据（tool:heartbeat\r_task\r_upsert、
// expected\r_revision）里的 \r 以「转义序列」形态（backslash + r 两个字符）在
// wire 文本上抵达——Go encoding/json 对字符串内裸 0x0D 直接拒收（见下方
// WireCREscapeForms），能走到 catalog/schema 层的污染调用必然是转义形态。重组层
// 唯一被允许的变形是 bufio.ScanLines 剥掉 CRLF 行尾的 \r；本测试把含 \r 转义的
// SSE 体在每一个字节偏移（含 \ 与 r 两个转义字符之间）切开喂给真实 scanner，
// 断言行序列与单次扫描逐字节等同——重组层不丢字、不插字，插入层不在本仓。
func TestStreamScannerChunkBoundariesNeverAlterLines(t *testing.T) {
	const body = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"capability_id\":\"tool:heartbeat\r_task\r_upsert\"}"}}]}}]}` + "\r\n" +
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\r\n" +
		`data: [DONE]` + "\r\n"
	scanLines := func(r io.Reader) []string {
		sc := NewStreamScanner(r, 1024*1024)
		var lines []string
		for sc.Scan() {
			lines = append(lines, sc.Text())
		}
		if sc.Err() != nil {
			t.Fatalf("scanner error: %v", sc.Err())
		}
		return lines
	}
	want := scanLines(strings.NewReader(body))
	if len(want) != 3 {
		t.Fatalf("want 3 lines, got %d: %q", len(want), want)
	}
	if !strings.Contains(want[0], `tool:heartbeat\r_task\r_upsert`) {
		t.Fatalf("escape text must survive reassembly verbatim, got %q", want[0])
	}
	for split := 1; split < len(body); split++ {
		got := scanLines(io.MultiReader(bytes.NewReader([]byte(body[:split])), bytes.NewReader([]byte(body[split:]))))
		if len(got) != len(want) {
			t.Fatalf("split %d: %d lines, want %d", split, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("split %d line %d = %q, want %q — chunk boundary must never alter bytes", split, i, got[i], want[i])
			}
		}
	}
	// 行内孤 \r 不是行终止符：ScanLines 保留它，随后由 encoding/json 拒收。
	lone := scanLines(strings.NewReader("data: {\"a\x0db\": 1}\r\n"))
	if len(lone) != 1 || !strings.Contains(lone[0], "a\x0db") {
		t.Fatalf("mid-line raw CR must survive the scan, got %q", lone)
	}
}

// TestStreamScannerWireCREscapeForms pins the two wire forms of \r and their
// downstream visibility（tl1310 深查的形态学证据）:
//  1. the ESCAPE sequence (backslash + r) is valid JSON and decodes to a real
//     \r byte inside the string — the ONLY form that can reach and pollute the
//     capability proxy layers;
//  2. a RAW 0x0D byte inside a JSON string is rejected outright by
//     encoding/json — such an event dies at openai.go's DecodeError and never
//     reaches the catalog/schema layers the field evidence came from.
func TestStreamScannerWireCREscapeForms(t *testing.T) {
	var decoded map[string]any
	if err := json.Unmarshal([]byte(`{"expected\r_revision":3}`), &decoded); err != nil {
		t.Fatalf("escape form must parse: %v", err)
	}
	if _, ok := decoded["expected\r_revision"]; !ok {
		t.Fatalf("decoded key must carry the real CR byte: %#v", decoded)
	}
	if err := json.Unmarshal([]byte("{\"expected\x0d_revision\":3}"), &map[string]any{}); err == nil {
		t.Fatal("raw 0x0D inside a JSON string must fail decoding")
	}
}
