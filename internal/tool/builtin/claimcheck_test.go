package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaimCheckToolVerdictShape(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "routes", "dev"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "routes", "dev", "dev.workflow.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t0 := claimCheck{}
	// A missing claim on a real file is REFUTED with hits and tried evidence.
	out, err := t0.Execute(context.Background(), json.RawMessage(
		`{"claim":"missing","pattern":"dev.workflow.yaml","bases":[`+quoteJSON(root)+`]}`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Verdict   string `json:"verdict"`
		TotalHits int    `json:"totalHits"`
		Uncertain bool   `json:"uncertain"`
		Scope     string `json:"scope"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("verdict is not structured JSON: %v\n%s", err, out)
	}
	if result.Verdict != "REFUTED" || result.TotalHits != 1 {
		t.Fatalf("verdict=%s hits=%d (node_modules-free tree should hit once)", result.Verdict, result.TotalHits)
	}
	if !strings.Contains(result.Scope, "existence-only") {
		t.Fatal("scope guard missing from tool output")
	}

	// exists on an absent path → REFUTED.
	out2, err := t0.Execute(context.Background(), json.RawMessage(
		`{"claim":"exists","path":` + quoteJSON(filepath.Join(root, "ghost.txt")) + `}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, `"REFUTED"`) {
		t.Fatalf("exists on absent path = %s", out2)
	}
}

func TestClaimCheckToolUsageErrorsAreNamed(t *testing.T) {
	t0 := claimCheck{}
	if _, err := t0.Execute(context.Background(), json.RawMessage(`{"claim":"missing"}`)); err == nil || !strings.Contains(err.Error(), "pass {claim") {
		t.Fatalf("empty invocation must carry a usage hint, got %v", err)
	}
	if _, err := t0.Execute(context.Background(), json.RawMessage(`{"claim":"dunno","path":"x"}`)); err == nil {
		t.Fatal("unknown claim must fail")
	}
}

func TestClaimCheckToolContractRegistration(t *testing.T) {
	// The contract documentation and snip stance guards cover every builtin;
	// keep claim_check compliant from day one.
	if _, err := os.Stat(filepath.Join("..", "..", "..", "docs", "TOOL_CONTRACT.md")); err == nil {
		blob, readErr := os.ReadFile(filepath.Join("..", "..", "..", "docs", "TOOL_CONTRACT.md"))
		if readErr == nil && !strings.Contains(string(blob), "| `claim_check` | true |") {
			t.Fatalf("claim_check missing from docs/TOOL_CONTRACT.md main table")
		}
	}
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
