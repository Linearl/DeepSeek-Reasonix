package tool

import (
	"encoding/json"
	"strings"
	"testing"
)

// tl1554: the sink of the repo-wide name-domain sanitizers. Both pollution
// forms must be covered everywhere a model-supplied name is resolved or
// echoed: real control bytes (JSON "\r" decodes to a bare CR) and literal
// escape text (backslash+r as two characters — the form a model produces
// when it copies a previously escaped echo back, the 2026-11-11
// eight-failure self-reinforcement loop). Values are out of scope by design
// and must keep their exact bytes.

func TestStripControlCharacters(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		wantStrip bool
	}{
		{name: "clean untouched", in: "tool:read_file", want: "tool:read_file"},
		{name: "bare CR removed", in: "tool:read\r_file", want: "tool:read_file", wantStrip: true},
		{name: "DEL removed", in: "to\u007fol", want: "tool", wantStrip: true},
		{name: "utf-8 preserved", in: "任务\r名", want: "任务名", wantStrip: true},
		{name: "literal escape text NOT byte-stripped", in: `read\r_file`, want: `read\r_file`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, stripped := StripControlCharacters(test.in)
			if got != test.want || stripped != test.wantStrip {
				t.Fatalf("StripControlCharacters(%q) = (%q, %v), want (%q, %v)", test.in, got, stripped, test.want, test.wantStrip)
			}
		})
	}
}

func TestSanitizeNameTokenCoversBothPollutionForms(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		want      string
		wantStrip bool
	}{
		{name: "clean passthrough", in: "read_file", want: "read_file"},
		{name: "real CR form", in: "expected\r_revision", want: "expected_revision", wantStrip: true},
		{name: "literal escape form", in: `expected\r_revision`, want: "expected_revision", wantStrip: true},
		{name: "literal newline and tab forms", in: `mess\nage\t`, want: "message", wantStrip: true},
		{name: "mixed real and literal", in: "capabilit\ry\r_id", want: "capability_id", wantStrip: true},
		{name: "trims space after strip", in: "\r talk_to_session \n", want: "talk_to_session", wantStrip: true},
		{name: "backslash not part of a named escape is kept", in: `read\q_file`, want: `read\q_file`},
		{name: "utf-8 key with real CR", in: "任务\r名", want: "任务名", wantStrip: true},
		{name: "all-control cleans to empty", in: "\r\r\n", want: "", wantStrip: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, stripped := SanitizeNameToken(test.in)
			if got != test.want || stripped != test.wantStrip {
				t.Fatalf("SanitizeNameToken(%q) = (%q, %v), want (%q, %v)", test.in, got, stripped, test.want, test.wantStrip)
			}
			// Idempotence bounce: sanitizing an already-clean token never
			// changes it again (the echo-defense call sites rely on this).
			again, againStripped := SanitizeNameToken(got)
			if again != got || againStripped {
				t.Fatalf("sanitize not idempotent: %q -> (%q, %v)", got, again, againStripped)
			}
		})
	}
}

// TestSanitizeArgumentKeysLiteralEscapeForm extends the tl1310 key cleanup to
// the decoded-literal form: a JSON source "expected\\r_revision" decodes to a
// key carrying the two-character `\r` text, which the byte-only cleaner
// missed. Values — including legitimate literal escape text — stay
// byte-for-byte.
func TestSanitizeArgumentKeysLiteralEscapeForm(t *testing.T) {
	got, count := SanitizeArgumentKeys(json.RawMessage(`{"name":"hb","expected\\r_revision":3}`))
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decoded: %v", err)
	}
	if _, ok := decoded["expected_revision"]; !ok {
		t.Fatalf("literal-escape key not cleaned: %s", got)
	}
	if string(decoded["name"]) != `"hb"` {
		t.Fatalf("clean member rewritten: %s", got)
	}

	// A value carrying literal escape text is content, not a key: preserved.
	got, count = SanitizeArgumentKeys(json.RawMessage(`{"prompt":"line\\rbreak","expected\\r_revision":1}`))
	if count != 1 {
		t.Fatalf("count = %d, want 1 (only the key counts)", count)
	}
	if !strings.Contains(string(got), `line\\rbreak`) {
		t.Fatalf("value with literal escape text rewritten: %s", got)
	}

	// Real-byte form still cleaned (tl1310 regression guard).
	got, count = SanitizeArgumentKeys(json.RawMessage(`{"expected\r_revision":1}`))
	if count != 1 || !strings.Contains(string(got), `"expected_revision"`) {
		t.Fatalf("real-byte key not cleaned: count=%d got=%s", count, got)
	}

	// Clean payload: byte-identical, zero count.
	clean := json.RawMessage(`{"prompt":"a\nb\r\nc","expected_revision":3}`)
	got, count = SanitizeArgumentKeys(clean)
	if count != 0 || string(got) != string(clean) {
		t.Fatalf("clean payload changed: count=%d got=%s", count, got)
	}
}

// TestNormalizeArgumentsHealsDirtyKeys pins the direct-call choke: every
// caller of NormalizeArguments — direct tool calls included — now receives
// key-healed bytes, so schema validation, permission, evidence, and execution
// see the same cleaned arguments.
func TestNormalizeArgumentsHealsDirtyKeys(t *testing.T) {
	got := NormalizeArguments(json.RawMessage(`{"expected\r_revision":3}`))
	if string(got) != `{"expected_revision":3}` {
		t.Fatalf("dirty key not healed by NormalizeArguments: %s", got)
	}
	// Literal escape form.
	got = NormalizeArguments(json.RawMessage(`{"expected\\r_revision":3}`))
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("decoded: %v", err)
	}
	if _, ok := decoded["expected_revision"]; !ok {
		t.Fatalf("literal-escape key not healed: %s", got)
	}
	// Historical contract intact.
	if got := NormalizeArguments(nil); string(got) != `{}` {
		t.Fatalf("nil arguments = %s, want {}", got)
	}
	if got := NormalizeArguments(json.RawMessage(`null`)); string(got) != `{}` {
		t.Fatalf("null arguments = %s, want {}", got)
	}
	if got := NormalizeArguments(json.RawMessage(`  {"a":1}  `)); string(got) != `{"a":1}` {
		t.Fatalf("clean arguments rewritten: %s", got)
	}
}

// TestValidationEchoesCleanKeys pins the violation-echo defense: an unhealed
// instance passed straight to ValidateJSONSchemaValue (the shape third-party
// callers use) must produce violation paths and expected text without the
// pollution — no bare CR bytes, no literal escape text — for BOTH forms.
func TestValidationEchoesCleanKeys(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"additionalProperties":false,"required":["name"]}`)
	tests := []struct {
		name string
		raw  string
	}{
		{name: "real CR instance key", raw: `{"name":"x","expected\r_revision":1}`},
		{name: "literal escape instance key", raw: `{"name":"x","expected\\r_revision":1}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := ValidateJSONSchemaValue(schema, json.RawMessage(test.raw))
			if result.CompileErr != nil || len(result.Violations) == 0 {
				t.Fatalf("violations = %+v compile=%v, want at least one", result.Violations, result.CompileErr)
			}
			for _, violation := range result.Violations {
				if strings.ContainsRune(violation.Path, '\r') || strings.Contains(violation.Path, `\r`) {
					t.Fatalf("violation path echoes pollution: %q", violation.Path)
				}
				if strings.ContainsRune(violation.Expected, '\r') || strings.Contains(violation.Expected, `\r`) {
					t.Fatalf("violation expected echoes pollution: %q", violation.Expected)
				}
			}
			joined := make([]string, 0, len(result.Violations))
			for _, violation := range result.Violations {
				joined = append(joined, violation.Expected)
			}
			if !strings.Contains(strings.Join(joined, " "), "expected_revision") {
				t.Fatalf("cleaned key form missing from echo: %v", result.Violations)
			}
		})
	}
}

func TestSanitizeErrorLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "clean untouched", in: "capability id tool:x is missing a tool name", want: "capability id tool:x is missing a tool name"},
		{name: "embedded CR removed, layout kept", in: "line one\r still\r line one\nline two", want: "line one still line one\nline two"},
		{name: "CRLF normalized to LF", in: "a\r\nb", want: "a\nb"},
		{name: "windows path backslash kept", in: `no such file C:\readme.txt`, want: `no such file C:\readme.txt`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SanitizeErrorLines(test.in); got != test.want {
				t.Fatalf("SanitizeErrorLines(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
