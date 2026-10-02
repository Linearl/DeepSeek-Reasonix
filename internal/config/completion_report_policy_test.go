package config

import (
	"strings"
	"testing"
)

func TestCompletionReportPolicyFields(t *testing.T) {
	for _, field := range []string{"交付物", "变更", "验证", "未做 / 风险", "无"} {
		if !strings.Contains(CompletionReportPolicy, field) {
			t.Fatalf("CompletionReportPolicy missing %q", field)
		}
	}
}

// TestCompletionReportPolicyFaithfulReporting pins the honest-reporting clause
// (20261002 prompt research, report §4.1 #3): failures surface with their
// output, skipped steps stay visible, and "done" is claimed only when true.
func TestCompletionReportPolicyFaithfulReporting(t *testing.T) {
	for _, want := range []string{
		"Report outcomes faithfully",
		"say so and include the output",
		"skipped or left unverified",
		`state "done and verified" only when it is`,
	} {
		if !strings.Contains(CompletionReportPolicy, want) {
			t.Fatalf("CompletionReportPolicy missing %q", want)
		}
	}
}

// TestCompletionReportPolicyRealNewlines guards the raw-string trap: the field
// list once shipped with literal backslash-n sequences because the const used
// Go raw strings while telling the model to write "each field on its own line".
func TestCompletionReportPolicyRealNewlines(t *testing.T) {
	if !strings.Contains(CompletionReportPolicy, "\n- 交付物:") {
		t.Fatal("CompletionReportPolicy fields must be separated by real newlines")
	}
	if strings.Contains(CompletionReportPolicy, "\\n") {
		t.Fatalf("CompletionReportPolicy contains literal backslash-n: %q", CompletionReportPolicy)
	}
}
