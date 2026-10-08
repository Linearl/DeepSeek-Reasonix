package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// topicMigrationMarker records a completed legacy→topic migration for the
// directory's current session signature. New CLI sessions and same-size
// rewrites change the signature and force re-migration without relying only on
// coarse directory mtimes.
// v2 also re-evaluates recovery-named sessions that v1 skipped by filename.
const topicMigrationMarker = ".topics-migrated-v2"
const topicIndexRepairMarker = ".topic-indexes-repaired-v2"

const (
	migrationFingerprintWindow    = int64(2 << 10)
	migrationFullFingerprintLimit = int64(256 << 10)
)

func invalidateTopicDirMarkers(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	var errs []error
	for _, marker := range []string{topicMigrationMarker, topicIndexRepairMarker} {
		if err := os.Remove(filepath.Join(dir, marker)); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func topicDirMarkerDone(dir, marker string) bool {
	dir = strings.TrimSpace(dir)
	marker = strings.TrimSpace(marker)
	if dir == "" || marker == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(dir, marker))
	if err != nil {
		return false
	}
	sig, err := sessionDirMigrationSignature(dir)
	if err != nil {
		// Transient directory read failure: treat as not done so the next
		// reconcile retries rather than permanently skipping migration.
		return false
	}
	// Accept both signature content and legacy empty markers that still match
	// only when the directory has no session files (empty sig of empty dir).
	got := strings.TrimSpace(string(data))
	if got == "" {
		// Legacy empty marker: valid only when the dir currently has no
		// migratable session/meta files. Any new transcript must re-run.
		return sig == emptySessionDirSignature
	}
	return got == sig
}

const emptySessionDirSignature = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func sessionDirMigrationSignature(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !migrationSignatureArtifact(name) {
			continue
		}
		record, err := migrationArtifactSignature(filepath.Join(dir, name), name)
		if err != nil {
			return "", err
		}
		lines = append(lines, record)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

func migrationSignatureArtifact(name string) bool {
	return store.IsSessionTranscriptName(name) ||
		store.IsSessionEventLogName(name) ||
		strings.HasSuffix(name, store.SessionMetaFileSuffix)
}

// migrationArtifactSignature (task 195) covers exactly what the migration and
// repair decisions read, plus one content probe, nothing more:
//
//	transcript: name + FIRST LINE — the migration/repair branches read topic
//	           id, title, recovery flag and preview presence from the
//	           sidecar, never transcript bytes, so the first line is only a
//	           guard against a same-size in-place rewrite with a restored
//	           mtime (an existing contract). Append-only writes add lines and
//	           never touch the first, so ordinary turns keep the marker valid
//	           however short the file is — a head window could not say that,
//	           because short files carry their whole content inside it;
//	sidecar:    name + structural projection (topic id, custom title, recovery
//	           flag, preview presence) — autosave revision bumps and activity
//	           timestamps no longer move it;
//	events:     name only — migration never reads its bytes, and it appends on
//	           every turn.
//
// A rewrite that touches only tail lines (leaving the first line intact) no
// longer invalidates the marker — accepted: migration/repair branch on sidecar
// fields, which move with the rewrite's own metadata, and the old form's
// per-turn invalidation is exactly the cost task 195 removes.
func migrationArtifactSignature(path, name string) (string, error) {
	if store.IsSessionEventLogName(name) {
		if _, err := os.Stat(path); err != nil {
			return "", err
		}
		return fmt.Sprintf("%q\tevents", name), nil
	}
	if strings.HasSuffix(name, store.SessionMetaFileSuffix) {
		projection, err := migrationMetaProjection(path)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%q\tmeta\t%s", name, projection), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("migration signature artifact %q is a directory", path)
	}
	// First line only: read a bounded prefix and cut at the newline. A
	// single-line file (the same-size rewrite fixture) falls through to its
	// whole content, so an in-place rewrite still moves the signature.
	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	line := buf[:n]
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i+1]
	}
	sum := sha256.Sum256(line)
	return fmt.Sprintf("%q\tfirst\t%s", name, hex.EncodeToString(sum[:])), nil
}

// migrationMetaProjection reduces a branch-meta sidecar to the fields the
// migration/repair passes actually branch on. Autosave rewrites the sidecar
// (revision bump) without changing any of them, which is precisely the churn
// that used to invalidate the marker on every turn.
func migrationMetaProjection(metaPath string) (string, error) {
	sessionPath := strings.TrimSuffix(metaPath, ".meta")
	meta, ok, err := agent.LoadBranchMeta(sessionPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return "none", nil
	}
	preview := "0"
	if strings.TrimSpace(meta.Preview) != "" {
		preview = "1"
	}
	return fmt.Sprintf("topic=%s|title=%s|recovered=%t|preview=%s",
		meta.TopicID, meta.CustomTitle, meta.Recovered, preview), nil
}

func topicMigrationDone(dir string) bool {
	return topicDirMarkerDone(dir, topicMigrationMarker)
}

func topicIndexRepairDone(dir string) bool {
	return topicDirMarkerDone(dir, topicIndexRepairMarker)
}

func markTopicDirMarkerDone(dir, marker string) {
	dir = strings.TrimSpace(dir)
	marker = strings.TrimSpace(marker)
	if dir == "" || marker == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	sig, err := sessionDirMigrationSignature(dir)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, marker), []byte(sig+"\n"), 0o644)
}

func markTopicMigrationDone(dir string) {
	markTopicDirMarkerDone(dir, topicMigrationMarker)
}

func markTopicIndexRepairDone(dir string) {
	markTopicDirMarkerDone(dir, topicIndexRepairMarker)
}
