package boot

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/fileutil"
	fileencoding "reasonix/internal/fileutil/encoding"
	"reasonix/internal/sandbox"
)

// The "- Shell:" line sits inside the provider-cached system-prompt prefix,
// but unlike the tool probes rendered around it (environment.ProbeOptions.
// SnapshotDir) the label was re-resolved from PATH discovery on every boot:
// a GUI-launched desktop resolving a different PATH than a login shell, or a
// flaky bash health probe, silently rewrote the line and invalidated every
// session's prefix cache. tl1614 gives the label the persistence sibling the
// probes already have — resolve once, persist under the shared cache root,
// keep serving that value on later boots. Config stays authoritative: an
// explicit [tools.shell] change re-anchors the snapshot, while a resolution
// that flips under unchanged config keeps the persisted label and reports
// the flap instead of rewriting the prompt prefix silently.
const shellLabelSnapshotVersion = 1

// shellLabelSnapshot is the persisted label plus the [tools.shell] config it
// was resolved under. The config pair is the re-anchor key: an explicit
// config change adopts the fresh resolution, and only a mismatch under
// unchanged config counts as a flap.
type shellLabelSnapshot struct {
	Version  int       `json:"version"`
	Prefer   string    `json:"prefer"`
	Path     string    `json:"path"`
	Label    string    `json:"label"`
	StoredAt time.Time `json:"stored_at"`
}

// shellLabelSnapshotPath lives beside the tool-probe snapshots under
// <cache>/environment/ but under a fixed distinct name; probe files are keyed
// probes-<fingerprint hash>.json, so the two snapshot domains cannot collide
// (tl1614 验收：快照键分域).
func shellLabelSnapshotPath(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "environment", "shell-label.json")
}

func loadShellLabelSnapshot(dir string) (shellLabelSnapshot, bool) {
	path := shellLabelSnapshotPath(dir)
	if path == "" {
		return shellLabelSnapshot{}, false
	}
	b, err := fileencoding.ReadFileUTF8(path)
	if err != nil {
		return shellLabelSnapshot{}, false
	}
	var snap shellLabelSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return shellLabelSnapshot{}, false
	}
	if snap.Version != shellLabelSnapshotVersion || strings.TrimSpace(snap.Label) == "" {
		return shellLabelSnapshot{}, false
	}
	return snap, true
}

// saveShellLabelSnapshot mirrors the tool-probe snapshot write: temp file plus
// rename, so a crash mid-write can never leave a torn snapshot behind.
func saveShellLabelSnapshot(dir string, snap shellLabelSnapshot) {
	path := shellLabelSnapshotPath(dir)
	if path == "" {
		return
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".shell-label.*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return
	}
	if err := fileutil.ReplaceFile(tmpPath, path); err != nil {
		os.Remove(tmpPath)
	}
}

// shellLabelConfigKey normalizes the [tools.shell] config pair into the
// re-anchor key. prefer is lower-cased with "" folded into "auto" (the values
// resolveShell treats identically); "powershell" and "pwsh" stay distinct
// because their candidate search order differs and can resolve different
// executables. path is trimmed only — normalization here only has to be
// stable across boots reading the same config, and any real edit still
// changes the key.
func shellLabelConfigKey(prefer, path string) (string, string) {
	prefer = strings.ToLower(strings.TrimSpace(prefer))
	if prefer == "" {
		prefer = "auto"
	}
	return prefer, strings.TrimSpace(path)
}

// shellLabelFlap describes a boot resolution that disagrees with the
// persisted label while the [tools.shell] config is unchanged.
type shellLabelFlap struct {
	From string // persisted label kept for rendering
	To   string // fresh boot resolution that was rejected
}

// stableShellLabel is resolvedShellLabel behind the persisted debounce. First
// boot adopts and persists the resolution; an explicit [tools.shell] config
// change re-anchors silently (config is authoritative); a resolution that
// flips under unchanged config keeps the persisted label and reports the flap
// through slog plus a sink notice instead of rewriting the prompt prefix
// silently. CacheDir loss (cache wipe, version drift) degrades to the first
// boot path: the fresh resolution is adopted and stability resumes from
// there — the flap report covers the case where both values exist and differ.
func stableShellLabel(shell sandbox.Shell, cfgPrefer, cfgPath, cacheDir string, sink event.Sink) string {
	resolved := resolvedShellLabel(shell, cfgPath)
	prefer, path := shellLabelConfigKey(cfgPrefer, cfgPath)
	snap, ok := loadShellLabelSnapshot(cacheDir)
	if !ok || snap.Prefer != prefer || snap.Path != path {
		saveShellLabelSnapshot(cacheDir, shellLabelSnapshot{
			Version:  shellLabelSnapshotVersion,
			Prefer:   prefer,
			Path:     path,
			Label:    resolved,
			StoredAt: time.Now(),
		})
		return resolved
	}
	if snap.Label == resolved {
		return resolved
	}
	reportShellLabelFlap(sink, shellLabelFlap{From: snap.Label, To: resolved})
	return snap.Label
}

// reportShellLabelFlap turns an unattended label flip into a visible signal:
// one structured slog warning plus one frontend notice, both carrying the
// from→to values and a report hint (tl1614 B).
func reportShellLabelFlap(sink event.Sink, flap shellLabelFlap) {
	slog.Warn("boot: shell label resolved differently than the persisted value",
		"from", flap.From,
		"to", flap.To,
		"action", "keeping the persisted label",
		"hint", "if this was not a configuration change, please report it")
	if sink == nil {
		return
	}
	sink.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelWarn,
		Text: fmt.Sprintf(
			"Shell label changed without a [tools.shell] config change (from %q to %q); keeping the previous label. If this was not a configuration change, please report it.",
			flap.From, flap.To),
	})
}
