package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRollingLogWriterRollsAtSizeCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.log")
	w := newRollingLogWriter(path)
	defer w.Close()

	// Write 5MB in ~512KB chunks: the 4MB cap must trigger exactly one roll,
	// leaving desktop.log.1 holding the bulk and desktop.log small again.
	chunk := []byte(strings.Repeat("x", 512<<10))
	for range 10 {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if info.Size() > desktopLogMaxSizeBytes {
		t.Fatalf("current log = %d bytes, want <= %d after rolling", info.Size(), desktopLogMaxSizeBytes)
	}
	rolled, err := os.Stat(w.rolledPath(1))
	if err != nil {
		t.Fatalf("rolled file desktop.log.1 missing: %v", err)
	}
	if rolled.Size() == 0 {
		t.Fatal("rolled file is empty")
	}
}

func TestRollingLogWriterKeepsBackupCeiling(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "desktop.log")
	w := newRollingLogWriter(path)
	defer w.Close()

	// Force enough rolls that a naive implementation would exceed 25 files.
	chunk := []byte(strings.Repeat("x", 512<<10))
	for range (desktopLogMaxSizeBytes/len(chunk) + 2) * (desktopLogMaxBackups + 5) {
		if _, err := w.Write(chunk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if got := len(entries); got > desktopLogMaxBackups {
		t.Fatalf("log dir holds %d files, want <= %d (ceiling)", got, desktopLogMaxBackups)
	}
}
