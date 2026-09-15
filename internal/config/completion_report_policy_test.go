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
