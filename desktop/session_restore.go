package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/store"
)

func restoreTrashedSessionFile(dir, path string) error {
	_, key, itemDir, err := validateTrashedSessionPath(dir, path)
	if err != nil {
		return err
	}
	target := filepath.Join(dir, key)
	replaceDiscardableTranscript := false
	if _, err := os.Stat(target); err == nil {
		discardable, err := liveSessionDiscardable(target)
		if err != nil {
			return err
		}
		if !discardable {
			return fmt.Errorf("session already exists: %s", key)
		}
		replaceDiscardableTranscript = true
	} else if !os.IsNotExist(err) {
		return err
	}
	// Preflight every owned destination the bucket will actually restore
	// before removing the verified-empty transcript. A newly created inbox or
	// other sidecar may already hold work even while the transcript itself is
	// still discardable.
	if err := checkRestoreSessionArtifactConflicts(dir, itemDir, target, key, replaceDiscardableTranscript); err != nil {
		return err
	}
	if err := checkRestoreSubagentConflicts(dir, itemDir); err != nil {
		return err
	}
	if replaceDiscardableTranscript {
		if err := removeDesktopSessionArtifacts(target); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, artifact := range sessionTrashArtifacts(target, key) {
		if err := movePathIfExists(filepath.Join(itemDir, artifact.name), artifact.src); err != nil {
			return err
		}
	}
	// tl1442-ext: entries the delete-side catch-all sweep carried into the
	// bucket (stem-prefixed shapes outside the explicit inventory) come back
	// the same way — the sweep's zero-residue promise must not cost the
	// restored session its bytes.
	if err := restoreSweptStragglers(dir, itemDir, target, key); err != nil {
		return err
	}
	if err := restoreSubagentArtifacts(dir, itemDir); err != nil {
		return err
	}
	return os.RemoveAll(itemDir)
}

// restoreSweptStragglers maps every remaining stem-prefixed bucket entry back
// onto the restored session's stem. The meta file and the subagents directory
// are not stem-prefixed and keep their own restore mechanisms.
func restoreSweptStragglers(dir, itemDir, target, key string) error {
	keyStem := strings.TrimSuffix(key, store.SessionTranscriptSuffix)
	targetStem := strings.TrimSuffix(filepath.Base(target), store.SessionTranscriptSuffix)
	entries, err := os.ReadDir(itemDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == key || !strings.HasPrefix(name, keyStem+".") {
			continue // the transcript itself rode the listed-artifact loop
		}
		dstName := name
		if keyStem != targetStem {
			dstName = targetStem + strings.TrimPrefix(name, keyStem)
		}
		if err := movePathIfExists(filepath.Join(itemDir, name), filepath.Join(dir, dstName)); err != nil {
			return err
		}
	}
	return nil
}

// checkRestoreSessionArtifactConflicts refuses the restore when any session-
// owned artifact already exists at its destination — the transcript
// special-cased by allowDiscardableTranscript excepted. The check runs the
// FULL inventory (not just what the bucket carries): the replace path below
// it tears the live set via removeDesktopSessionArtifacts, so this preflight
// is what stands between that teardown and live sidecars the restore must not
// discard (the "active metadata" contract). tl1442-ext adds the swept-
// straggler form: stem-prefixed bucket entries outside the inventory must
// find their mapped destinations free too.
func checkRestoreSessionArtifactConflicts(dir, itemDir, target, key string, allowDiscardableTranscript bool) error {
	for _, artifact := range sessionTrashArtifacts(target, key) {
		if allowDiscardableTranscript && artifact.src == target {
			continue
		}
		if _, err := os.Lstat(artifact.src); err == nil {
			return fmt.Errorf("session artifact already exists: %s", artifact.name)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	keyStem := strings.TrimSuffix(key, store.SessionTranscriptSuffix)
	targetStem := strings.TrimSuffix(filepath.Base(target), store.SessionTranscriptSuffix)
	entries, err := os.ReadDir(itemDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == key || !strings.HasPrefix(name, keyStem+".") {
			continue // the transcript rides the listed check; meta/subagents their own
		}
		dstName := name
		if keyStem != targetStem {
			dstName = targetStem + strings.TrimPrefix(name, keyStem)
		}
		if _, err := os.Lstat(filepath.Join(dir, dstName)); err == nil {
			return fmt.Errorf("session artifact already exists: %s", name)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
