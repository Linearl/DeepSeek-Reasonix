package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestVerdictExitCodeContract(t *testing.T) {
	// 任务 229 G1: positive verdicts (both spellings) exit 0; negative and
	// unknown verdicts must never silently exit 0.
	for verdict, want := range map[string]int{
		VerdictOK:      0,
		VerdictEmpty:   0,
		"CONFIRMED":    0,
		VerdictRefuted: 1,
		VerdictError:   1,
		"REFUTED":      1,
		"":             1,
		"bogus":        1,
	} {
		if got := verdictExitCode(verdict); got != want {
			t.Fatalf("verdictExitCode(%q)=%d want %d", verdict, got, want)
		}
	}
}

func TestWriteVerdictEnvelopeShape(t *testing.T) {
	var buf bytes.Buffer
	code, err := writeVerdictEnvelope(&buf, "tool-stats", VerdictOK, "2 session(s)", []map[string]any{{"tool": "read_file", "calls": 3}})
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 {
		t.Fatalf("exit=%d want 0", code)
	}
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Tool          string          `json:"tool"`
		Verdict       string          `json:"verdict"`
		Detail        string          `json:"detail"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("envelope not JSON: %v", err)
	}
	if env.SchemaVersion != verdictSchemaVersion || env.Tool != "tool-stats" || env.Verdict != VerdictOK {
		t.Fatalf("header mismatch: %+v", env)
	}
	if env.Detail != "2 session(s)" || len(env.Payload) == 0 {
		t.Fatalf("payload/detail missing: %+v", env)
	}
}

func TestRunToolStatsJSONContract(t *testing.T) {
	root := t.TempDir()
	sidecar := `{"tools":{"read_file":{"calls":10,"hardErrors":1,"softErrors":0},"grep":{"calls":4,"hardErrors":0,"softErrors":2}}}`
	if err := os.WriteFile(filepath.Join(root, "s1.toolstats.json"), []byte(sidecar), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runToolStats([]string{"--json", root}); code != 0 {
		t.Fatalf("exit=%d want 0", code)
	}
}

func TestRunToolStatsJSONEmptyDirIsContractEmpty(t *testing.T) {
	// An empty directory is a reportable non-failure: verdict empty, exit 0 —
	// same contract the text mode's "no toolstats sidecars" line encoded.
	if code := runToolStats([]string{"--json", t.TempDir()}); code != 0 {
		t.Fatalf("exit=%d want 0", code)
	}
}

func TestRunToolStatsUsageErrorExitCode(t *testing.T) {
	// Usage/input errors ride verdictExitUsage (2), never a verdict exit.
	if code := runToolStats([]string{"--bogus-flag"}); code != verdictExitUsage {
		t.Fatalf("exit=%d want 2", code)
	}
}
