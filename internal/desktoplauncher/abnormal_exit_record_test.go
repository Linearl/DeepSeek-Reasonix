package desktoplauncher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tl1786: the death record must carry the attribution fields the incident
// lacked — the exit-code class (Reason), the dead PID's crash-fatal sink
// dump (Stack), and the dead PID's last lifecycle phase — and must keep
// schemaVersion 1 so already-running desktop decoders still consume it.
func TestWriteAbnormalExitRecordCarriesAttribution(t *testing.T) {
	base := t.TempDir()
	recordPath := filepath.Join(base, "lifecycle", "last-abnormal-exit.json")
	isolateAbnormalExitRecordPaths(t, base, recordPath)

	pid := 424242
	sinkDir := filepath.Join(base, "crash-fatal")
	if err := os.MkdirAll(sinkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sink := fmt.Sprintf("%s pid=%d started=%s\nfatal error: unexpected deadlock\n\ngoroutine 1 [running]:\nmain.main()\n\t/path/main.go:10\n",
		fatalCrashSinkHeaderPrefix, pid, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(sinkDir, fmt.Sprintf("%d.log", pid)), []byte(sink), 0o600); err != nil {
		t.Fatal(err)
	}
	lifecycleDir := filepath.Join(base, "diagnostics", "lifecycle")
	if err := os.MkdirAll(lifecycleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lifecycleDir, fmt.Sprintf("%d-run123.json", pid)),
		[]byte(`{"schemaVersion":2,"pid":424242,"phase":"running","startedAt":"2026-11-23T08:00:00Z","updatedAt":"2026-11-23T08:30:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	isolateAttributionLogMirror(t)
	writeAbnormalExitRecord(pid, 1, time.Now())

	body, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("abnormal-exit record missing: %v", err)
	}
	var record abnormalExitRecord
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("record not decodable: %v", err)
	}
	if record.SchemaVersion != 1 {
		t.Fatalf("schemaVersion = %d, want 1 (bump would strand running desktop decoders)", record.SchemaVersion)
	}
	if record.PID != pid || record.ExitCode != 1 {
		t.Fatalf("record = %+v", record)
	}
	if !strings.Contains(record.Reason, "exit=1") || !strings.Contains(record.Reason, "TerminateProcess") {
		t.Fatalf("Reason %q does not name the exit=1 class", record.Reason)
	}
	if !strings.Contains(record.Stack, "goroutine 1") || !strings.Contains(record.Stack, "fatal error:") {
		t.Fatalf("Stack lost the dump content: %q", record.Stack)
	}
	if strings.Contains(record.Stack, fatalCrashSinkHeaderPrefix) {
		t.Fatalf("Stack still carries the provenance header: %q", record.Stack)
	}
	if record.LifecyclePhase != "running" {
		t.Fatalf("LifecyclePhase = %q, want running", record.LifecyclePhase)
	}
	if record.CrashFatalSeen != true {
		t.Fatalf("CrashFatalSeen = false, want true (sink existed with a dump)")
	}
	// The attribution must also land in the desktop.log mirror — the incident
	// readers' usual place — with the reason and the stack size.
	mirror := readDesktopLogMirror(t)
	if !strings.Contains(mirror, "abnormal-exit attribution") || !strings.Contains(mirror, "exit=1") {
		t.Fatalf("desktop.log mirror lacks the attribution line: %q", mirror)
	}
}

// A header-only sink stub is the os.Exit / external-kill signature: the sink
// existed (CrashFatalSeen=true) but the Go runtime appended no dump, so
// Stack stays empty — the record must say so explicitly instead of omitting
// the fields.
func TestWriteAbnormalExitRecordHeaderOnlySink(t *testing.T) {
	base := t.TempDir()
	recordPath := filepath.Join(base, "lifecycle", "last-abnormal-exit.json")
	isolateAbnormalExitRecordPaths(t, base, recordPath)

	pid := 424243
	sinkDir := filepath.Join(base, "crash-fatal")
	if err := os.MkdirAll(sinkDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := fmt.Sprintf("%s pid=%d started=%s\n", fatalCrashSinkHeaderPrefix, pid, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(sinkDir, fmt.Sprintf("%d.log", pid)), []byte(stub), 0o600); err != nil {
		t.Fatal(err)
	}

	isolateAttributionLogMirror(t)
	writeAbnormalExitRecord(pid, 1, time.Now())

	body, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("abnormal-exit record missing: %v", err)
	}
	var record abnormalExitRecord
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("record not decodable: %v", err)
	}
	if record.CrashFatalSeen != true {
		t.Fatalf("CrashFatalSeen = false, want true (header-only stub is evidence)")
	}
	if record.Stack != "" {
		t.Fatalf("Stack = %q, want empty for a header-only stub", record.Stack)
	}
	// Raw JSON must carry the always-present fields even when empty.
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"reason", "stack"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("record JSON lacks the always-present %q field: %s", key, body)
		}
	}
}

// No sink, no lifecycle record: fields stay explicit-empty and the class line
// still explains the death; the write must never fail for missing evidence.
func TestWriteAbnormalExitRecordWithoutEvidence(t *testing.T) {
	base := t.TempDir()
	recordPath := filepath.Join(base, "lifecycle", "last-abnormal-exit.json")
	isolateAbnormalExitRecordPaths(t, base, recordPath)
	isolateAttributionLogMirror(t)

	writeAbnormalExitRecord(424244, -1073741819, time.Now())

	body, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("abnormal-exit record missing: %v", err)
	}
	var record abnormalExitRecord
	if err := json.Unmarshal(body, &record); err != nil {
		t.Fatalf("record not decodable: %v", err)
	}
	if !strings.Contains(record.Reason, "native fault") {
		t.Fatalf("Reason %q does not classify the NTSTATUS class", record.Reason)
	}
	if record.Stack != "" || record.CrashFatalSeen || record.LifecyclePhase != "" {
		t.Fatalf("unexpected evidence on an empty scene: %+v", record)
	}
}

func TestClassifyAbnormalExit(t *testing.T) {
	cases := []struct {
		code   int
		within string
	}{
		{1, "TerminateProcess"},
		{-1, "browser-process-crash"},
		{3, "home-migration"},
		{259, "STILL_ACTIVE"},
		{-1073741819, "native fault"},
		{42, "no known desktop sender"},
		{0, "unexpected"},
	}
	for _, tc := range cases {
		got := classifyAbnormalExit(tc.code)
		if !strings.Contains(got, tc.within) {
			t.Fatalf("classifyAbnormalExit(%d) = %q, want it to mention %q", tc.code, got, tc.within)
		}
	}
}

// isolateAbnormalExitRecordPaths points the record path and both evidence
// seams at a temp scene so tests never read the real user state.
func isolateAbnormalExitRecordPaths(t *testing.T, base, recordPath string) {
	t.Helper()
	oldRecord := abnormalExitRecordPath
	oldSink := crashFatalSinkPathFor
	oldDir := lifecycleRecordsDir
	abnormalExitRecordPath = func() string { return recordPath }
	crashFatalSinkPathFor = func(pid int) string {
		return filepath.Join(base, "crash-fatal", fmt.Sprintf("%d.log", pid))
	}
	lifecycleRecordsDir = func() string { return filepath.Join(base, "diagnostics", "lifecycle") }
	t.Cleanup(func() {
		abnormalExitRecordPath = oldRecord
		crashFatalSinkPathFor = oldSink
		lifecycleRecordsDir = oldDir
	})
}

// attributionMirrorPath is the fixed desktop.log mirror location the
// attribution tests use. A fixed path (not a lazy t.TempDir() closure) is
// load-bearing: the mirror seam is read at write time and again at assert
// time, and a per-call temp dir would split those across two directories.
var attributionMirrorPath string

// isolateAttributionLogMirror points desktopLogPath at one fixed temp file
// for the whole test.
func isolateAttributionLogMirror(t *testing.T) {
	t.Helper()
	old := desktopLogPath
	attributionMirrorPath = filepath.Join(t.TempDir(), "desktop.log")
	desktopLogPath = func() string { return attributionMirrorPath }
	t.Cleanup(func() { desktopLogPath = old })
}

// readDesktopLogMirror returns the mirrored launcher lines written through
// appendDesktopLogLine during the test.
func readDesktopLogMirror(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(attributionMirrorPath)
	if err != nil {
		t.Fatalf("read desktop log mirror: %v", err)
	}
	return string(body)
}
