package checkpoint

import (
	"path/filepath"
	"runtime"
	"testing"
)

// tl1476: the preflight classification must label every unsafe form
// (workspace escape, missing payload, permission denial) and leave normal
// targets safe. The verdict is advisory — these tests pin the classifier only.
func TestPreflightFileSafetyForms(t *testing.T) {
	root := t.TempDir()
	write := func(name string, data []byte) string {
		t.Helper()
		p := filepath.Join(root, name)
		if err := writeFileForTest(p, data); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return name
	}
	normal := write("normal.txt", []byte("hello"))
	readonly := write("readonly.txt", []byte("locked"))
	if err := chmodReadOnlyForTest(filepath.Join(root, readonly)); err != nil {
		t.Fatalf("chmod readonly: %v", err)
	}

	files := []string{
		normal,
		readonly,
		"missing-with-writable-parent.txt",
		"../escape.txt",
		"conflicted-path.txt",
		"payloadless.txt",
	}
	conflicts := []RewindConflict{
		{Path: "conflicted-path.txt", Reason: ConflictPathUnsafe},
		{Path: "payloadless.txt", Reason: ConflictMissingPayload},
		{Reason: ConflictBusyWriter}, // global conflict without a path is ignored
	}

	s := &Store{root: root}
	got := s.preflightFileSafety(files, conflicts)
	byPath := map[string]RewindFileSafety{}
	for _, entry := range got {
		byPath[entry.Path] = entry
	}
	if len(got) != len(files) {
		t.Fatalf("expected %d classified entries, got %d (%v)", len(files), len(got), got)
	}
	if e := byPath[normal]; !e.Safe || e.Reason != "" {
		t.Fatalf("writable target must be safe, got %+v", e)
	}
	if e := byPath[readonly]; e.Safe || e.Reason != ConflictPermissionDenied {
		t.Fatalf("read-only target must be permission_denied, got %+v", e)
	}
	if e := byPath["missing-with-writable-parent.txt"]; !e.Safe {
		t.Fatalf("missing target with writable parent must stay safe (commit re-checks), got %+v", e)
	}
	if e := byPath["../escape.txt"]; e.Safe || e.Reason != ConflictPathUnsafe {
		t.Fatalf("root-escaping target must be path_unsafe, got %+v", e)
	}
	if e := byPath["conflicted-path.txt"]; e.Safe || e.Reason != ConflictPathUnsafe {
		t.Fatalf("conflict-registered target must inherit its reason, got %+v", e)
	}
	if e := byPath["payloadless.txt"]; e.Safe || e.Reason != ConflictMissingPayload {
		t.Fatalf("missing-payload target must inherit missing_payload, got %+v", e)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Path > got[i].Path {
			t.Fatalf("classification must be path-sorted, got %v", got)
		}
	}
}

func TestPreflightFileSafetyEmptyInputs(t *testing.T) {
	s := &Store{root: t.TempDir()}
	if got := s.preflightFileSafety(nil, nil); got != nil {
		t.Fatalf("nil files must classify to nil, got %v", got)
	}
	if got := s.preflightFileSafety([]string{""}, nil); len(got) != 0 {
		t.Fatalf("empty path entries must be skipped, got %v", got)
	}
}

func TestProbeRewindWriteDeniedMissingParent(t *testing.T) {
	root := t.TempDir()
	// A missing target under a writable parent is creatable: not denied.
	if probeRewindWriteDenied(filepath.Join(root, "absent.txt")) {
		t.Fatalf("missing target with writable parent must not be denied")
	}
	if runtime.GOOS == "windows" {
		return // directory read-only attributes do not gate file creation on Windows
	}
	locked := filepath.Join(root, "locked")
	if err := mkdirReadOnlyForTest(locked); err != nil {
		t.Fatalf("mkdir readonly: %v", err)
	}
	if !probeRewindWriteDenied(filepath.Join(locked, "absent.txt")) {
		t.Fatalf("missing target under an unwritable parent must be denied")
	}
}
