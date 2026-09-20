package agent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Task 193 long fix: when the persisted event index shows the message count
// approaching the replay record caps, Save must compact the log even though
// its byte size is still under the size gate - otherwise a live turn pushes
// the record count past the replay cap and freezes the session.
func TestSaveCompactsWhenEventIndexNearRecordCap(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "cap.events.jsonl")
	var b strings.Builder
	// 6 messages + a replace snapshot: index MessageCount stays small here, so
	// the test drives the near-cap branch through a hand-written index below.
	for i := 0; i < 6; i++ {
		b.WriteString(`{"schema_version":1,"type":"append","revision":` + strconv.Itoa(i+1) + `,"base_revision":` + strconv.Itoa(i) + `,"message_index":` + strconv.Itoa(i) + `,"messages":[{"role":"user","content":"m` + strconv.Itoa(i) + `"}]}` + "\n")
	}
	if err := os.WriteFile(logPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	// Hand-write an index whose MessageCount is at the compact trigger.
	cap := sessionEventReplayMaxRecords - sessionEventReplayCompactHeadroom
	idx := `{"schema_version":1,"log_size":` + strconv.FormatInt(int64(b.Len()), 10) + `,"message_count":` + strconv.Itoa(cap+1) + `,"revision":6}`
	indexPath := SessionEventIndexPath(logPath)
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte(idx), 0o600); err != nil {
		t.Fatal(err)
	}
	if !sessionEventIndexNearCap(logPath) {
		t.Fatal("index near cap not detected")
	}
	// A small index stays false (byte gate decides).
	small := `{"schema_version":1,"log_size":10,"message_count":3,"revision":1}`
	if err := os.WriteFile(indexPath, []byte(small), 0o600); err != nil {
		t.Fatal(err)
	}
	if sessionEventIndexNearCap(logPath) {
		t.Fatal("small index must not trigger near-cap")
	}
}
