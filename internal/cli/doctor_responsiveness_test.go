package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/eventwire"
	"reasonix/internal/turnevent"
)

// writeResponsivenessFixture writes a minimal schema-2 turn ledger beside a
// session file: one completed turn, one open turn with no output.
func writeResponsivenessFixture(t *testing.T, dir string) string {
	t.Helper()
	sessionPath := filepath.Join(dir, "20261009-resp-test.jsonl")
	ledger := strings.TrimSuffix(sessionPath, ".jsonl") + ".turns.jsonl"
	now := time.Now()
	type rec struct {
		RecordType string `json:"recordType"`
		turnevent.Envelope
	}
	var b strings.Builder
	appendRec := func(turnID string, seq uint64, kind string, status event.TurnStatus, at time.Time, prompt int) {
		line, err := json.Marshal(rec{
			RecordType: "event",
			Envelope: turnevent.Envelope{
				SchemaVersion: 2, SessionID: "resp-test", TurnID: turnID, Sequence: seq,
				Kind: kind, Status: status, CreatedAt: at.UnixMilli(),
				Event: func() eventwire.Event {
					e := eventwire.Event{Kind: kind, Text: "…"}
					if prompt > 0 {
						e.Usage = &eventwire.Usage{PromptTokens: 1000, ContextPromptTokens: prompt}
					}
					return e
				}(),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	t1 := now.Add(-time.Hour)
	appendRec("turn_1", 1, "turn_started", event.TurnInProgress, t1, 0)
	appendRec("turn_1", 2, "reasoning", event.TurnInProgress, t1.Add(6*time.Second), 0)
	appendRec("turn_1", 3, "usage", event.TurnCompleted, t1.Add(20*time.Second), 700000)
	appendRec("turn_2", 4, "turn_started", event.TurnInProgress, now.Add(-8*time.Minute), 0)
	if err := os.WriteFile(ledger, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionPath, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	return sessionPath
}

func TestDoctorResponsivenessCommandText(t *testing.T) {
	sessionPath := writeResponsivenessFixture(t, t.TempDir())
	var out string
	rc := 0
	out = captureStdout(t, func() {
		rc = doctorCommand([]string{"responsiveness", sessionPath}, "test-version")
	})
	if rc != 0 {
		t.Fatalf("doctor responsiveness rc = %d, want 0", rc)
	}
	for _, want := range []string{
		"verdict: first_response_wait",
		"criterion 1 — last write",
		"criterion 2 — turn state",
		"criterion 3 — first-response history",
		"700k tokens",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorResponsivenessCommandJSON(t *testing.T) {
	sessionPath := writeResponsivenessFixture(t, t.TempDir())
	var out string
	out = captureStdout(t, func() {
		// Go flag semantics: flags must precede the positional session ref.
		if rc := doctorCommand([]string{"responsiveness", "--json", sessionPath}, "test-version"); rc != 0 {
			t.Fatalf("doctor responsiveness --json rc != 0")
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if decoded["verdict"] != "first_response_wait" {
		t.Fatalf("verdict = %v, want first_response_wait", decoded["verdict"])
	}
	if decoded["latestPromptTokens"].(float64) != 700000 {
		t.Fatalf("latestPromptTokens = %v, want 700000", decoded["latestPromptTokens"])
	}
}

func TestDoctorResponsivenessRejectsBadRef(t *testing.T) {
	// A session id that does not exist must fail with an error, not a report.
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), hex.EncodeToString(raw))
	if rc := doctorCommand([]string{"responsiveness", missing}, "test-version"); rc == 0 {
		t.Fatalf("missing session rc = 0, want non-zero")
	}
	if rc := doctorCommand([]string{"responsiveness"}, "test-version"); rc != 2 {
		t.Fatalf("missing argument rc = %d, want 2 (usage)", rc)
	}
}
