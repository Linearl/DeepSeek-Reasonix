package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/store"
)

// appendTailEntry grows an existing test event log, so the incremental scan can
// be exercised the way a running turn grows the real file.
func appendTailEntry(t *testing.T, sessionPath string, entry map[string]any) {
	t.Helper()
	line, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal entry: %v", err)
	}
	f, err := os.OpenFile(store.SessionEventLog(sessionPath), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("open event log for append: %v", err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatalf("append entry: %v", err)
	}
}

func readTailTestLog(t *testing.T, sessionPath string) []byte {
	t.Helper()
	b, err := os.ReadFile(store.SessionEventLog(sessionPath))
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	return b
}

func TestLoadSessionUserTimesRangeReadsOnlyTheAppend(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	writeTailEventLog(t, sessionPath, []map[string]any{
		tailMessageEntry("m1", "user", 1000, "first"),
		tailMessageEntry("m2", "assistant", 2000, "reply"),
	})
	tail, _ := LoadSessionUserTimesTail(sessionPath, 0)
	size := int64(len(readTailTestLog(t, sessionPath)))

	appendTailEntry(t, sessionPath, tailMessageEntry("m3", "user", 3000, "second"))
	appendTailEntry(t, sessionPath, tailPatchEntry("m1", 4000))

	appended, err := LoadSessionUserTimesRange(sessionPath, size)
	if err != nil {
		t.Fatalf("range scan: %v", err)
	}
	if appended["m3"] != 3000 {
		t.Fatalf("appended user message missing from the range scan: %v", appended)
	}
	// A later patch wins in the forward scan, which is what lets the caller merge
	// range results on top of an older tail result.
	if appended["m1"] != 4000 {
		t.Fatalf("later patch must overwrite the earlier time, got %v", appended)
	}
	if _, found := appended["m2"]; found {
		t.Fatalf("records before the offset must not be re-reported: %v", appended)
	}
	if tail["m1"] != 1000 {
		t.Fatalf("tail scan regression: %v", tail)
	}
}

func TestLoadSessionUserTimesRangeFromTornOffsetKeepsGoing(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	writeTailEventLog(t, sessionPath, []map[string]any{
		tailMessageEntry("m1", "user", 111, "a"),
		tailMessageEntry("m2", "user", 222, "b"),
	})
	full := readTailTestLog(t, sessionPath)
	// Land the offset mid-record: the partial leading line must be dropped
	// without losing the records that follow it.
	torn := int64(len(full) / 3)

	times, err := LoadSessionUserTimesRange(sessionPath, torn)
	if err != nil {
		t.Fatalf("range scan: %v", err)
	}
	if _, found := times["m1"]; found {
		// The first record is the torn one; if the split landed in the second
		// record instead, m1 is legitimately complete.
		if _, second := times["m2"]; !second {
			t.Fatalf("range scan lost every record after a torn offset: %v", times)
		}
	}
	if times["m2"] != 222 {
		t.Fatalf("records after the torn line must survive: %v", times)
	}
}

func TestLoadSessionUserTimesRangePastEndIsEmpty(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "s.jsonl")
	writeTailEventLog(t, sessionPath, []map[string]any{tailMessageEntry("m1", "user", 1, "a")})
	size := int64(len(readTailTestLog(t, sessionPath)))

	times, err := LoadSessionUserTimesRange(sessionPath, size+4096)
	if err != nil {
		t.Fatalf("range scan past EOF: %v", err)
	}
	if len(times) != 0 {
		t.Fatalf("expected no records past the end, got %v", times)
	}
}
