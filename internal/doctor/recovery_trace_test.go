package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const forkTraceFixture = `{"at":"2026-11-05T14:10:17.6+08:00","outcome":"forked_recovery_branch","decision":"fork","reason":"diverged","trigger":"turn_end","disk_writer":"self_process","peer_trigger":"turn_end","chain":"Controller.snapshotActivityIfChanged@controller.go:1"}
{"at":"2026-11-05T23:59:59+08:00","outcome":"forked_recovery_branch","decision":"fork","reason":"diverged","trigger":"tab_autosave","disk_writer":"other_process","disk_writer_pid":4242}
{"at":"2026-11-06T00:51:01.8+08:00","outcome":"forked_file_lock_recovery","decision":"fork","reason":"shutdown_lock_timeout","trigger":"shutdown"}
{"at":"2026-11-06T01:00:00+08:00","outcome":"forked_recovery_branch","existing_recovery":true,"decision":"fork","reason":"diverged","trigger":"turn_end","disk_writer":"self_process","peer_trigger":"not_traced"}
{"at":"2026-11-01T16:58:18.5+08:00","outcome":"forked_recovery_branch","kind":"diverged","caller":"controller.go:5331"}
{"at":"2026-11-06T02:00:00+08:00","outcome":"adopted_disk_within_fork_brake","decision":"adopt","reason":"fork_brake","trigger":"turn_autosave"}
{"at":12345,"outcome":"forked_recovery_branch"}
not-json
`

// TestCollectRecoveryForksAggregatesAcrossWorkspaces pins the tl1443 doctor
// census: physical forks per local day, per trigger and per disk-side cause,
// traced decisions, pre-tl1443 records as "untraced", and self-heal backup
// copies (byte-identical lines) counted once.
func TestCollectRecoveryForksAggregatesAcrossWorkspaces(t *testing.T) {
	global, project := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(global, "private-topic.conflicts.jsonl"), []byte(forkTraceFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(global, "private-topic.pre-repair-20261106T000000")
	if err := os.MkdirAll(backup, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, "private-topic.conflicts.jsonl"), []byte(forkTraceFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	projectLine := `{"at":"2026-11-06T09:00:00+08:00","outcome":"moved_to_stable_recovery","decision":"fork","reason":"external_removal","trigger":"tab_autosave"}` + "\n"
	trashed := filepath.Join(project, ".trash", "other.jsonl-deleted-1")
	if err := os.MkdirAll(trashed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(trashed, "other.conflicts.jsonl"), []byte(projectLine), 0o600); err != nil {
		t.Fatal(err)
	}
	// Checkpoint/inbox/job dirs never hold conflict logs; the census does not
	// walk them, so a stray file there is not counted.
	ckpt := filepath.Join(project, "other.ckpt")
	if err := os.MkdirAll(ckpt, 0o755); err != nil {
		t.Fatal(err)
	}
	stray := `{"at":"2026-11-07T00:00:00+08:00","outcome":"forked_recovery_branch"}` + "\n"
	if err := os.WriteFile(filepath.Join(ckpt, "stray.conflicts.jsonl"), []byte(stray), 0o600); err != nil {
		t.Fatal(err)
	}

	forks := collectRecoveryForks([]string{global, project, global, ""})
	if forks.SessionDirs != 2 || forks.Records != 8 || forks.Forks != 6 || forks.UnreadEntries != 0 || forks.ScanAborts != 0 {
		t.Fatalf("census = %+v, want 2 dirs / 8 records / 6 forks", forks)
	}
	wantDays := map[string]int{"2026-11-01": 1, "2026-11-05": 2, "2026-11-06": 2}
	if !reflect.DeepEqual(forks.ForksByDay, wantDays) {
		t.Fatalf("forks by day = %v, want %v", forks.ForksByDay, wantDays)
	}
	wantTriggers := map[string]int{"turn_end": 1, "tab_autosave": 2, "shutdown": 1, "untraced": 2}
	if !reflect.DeepEqual(forks.ForksByTrigger, wantTriggers) {
		t.Fatalf("forks by trigger = %v, want %v", forks.ForksByTrigger, wantTriggers)
	}
	wantCauses := map[string]int{"self_process/turn_end": 1, "other_process": 1, "shutdown_lock_timeout": 1, "external_removal": 1, "untraced": 2}
	if !reflect.DeepEqual(forks.ForksByCause, wantCauses) {
		t.Fatalf("forks by cause = %v, want %v", forks.ForksByCause, wantCauses)
	}
	wantDecisions := map[string]int{"fork/diverged": 3, "fork/shutdown_lock_timeout": 1, "fork/external_removal": 1, "adopt/fork_brake": 1}
	if !reflect.DeepEqual(forks.Decisions, wantDecisions) {
		t.Fatalf("decisions = %v, want %v", forks.Decisions, wantDecisions)
	}

	text := RenderText(Report{Forks: forks})
	for _, line := range []string{
		"recovery forks (all workspaces)",
		"scanned      2 session dirs, 8 distinct records, 6 forks",
		"by day       2026-11-01:1 2026-11-05:2 2026-11-06:2",
		"by trigger   tab_autosave:2 untraced:2 shutdown:1 turn_end:1",
		"by cause     untraced:2 external_removal:1 other_process:1 self_process/turn_end:1 shutdown_lock_timeout:1",
		"decisions    fork/diverged:3 adopt/fork_brake:1 fork/external_removal:1 fork/shutdown_lock_timeout:1",
	} {
		if !strings.Contains(text, line) {
			t.Fatalf("text report lacks %q:\n%s", line, text)
		}
	}
	raw, err := json.Marshal(forks)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-topic", "controller.go", "4242", global} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("aggregate fork census leaked %q: %s", secret, raw)
		}
	}
}

func TestRenderRecoveryForksKeepsRecentDaysOnly(t *testing.T) {
	days := map[string]int{}
	for i := 1; i <= recoveryTraceDays+3; i++ {
		day := "2026-10-0" + string(rune('0'+i))
		if i >= 10 {
			day = "2026-10-" + string(rune('0'+i/10)) + string(rune('0'+i%10))
		}
		days[day] = i
	}
	var b strings.Builder
	renderRecoveryForks(&b, ForkCensusReport{ForksByDay: days})
	if strings.Contains(b.String(), "2026-10-01:") || !strings.Contains(b.String(), "2026-10-17:") {
		t.Fatalf("day line must keep the newest %d days: %s", recoveryTraceDays, b.String())
	}
	var empty strings.Builder
	renderRecoveryForks(&empty, ForkCensusReport{})
	if strings.Contains(empty.String(), "by day") || strings.Contains(empty.String(), "by trigger") {
		t.Fatalf("an empty census must render only its scan line: %q", empty.String())
	}
}
