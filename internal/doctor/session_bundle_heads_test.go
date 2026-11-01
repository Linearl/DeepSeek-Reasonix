package doctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1280: the schema-2 head fixture (ForkHead + multi-head manifest) left with
// the write-path slow lane. ListSessionHeads is a shim that always returns
// nil, so a schema-1 session leaves LogFormat/Heads empty — the bundle still
// carries the event log itself, and the manifest must not leak REASONIX_HOME.
func TestWriteSessionBundleListsSessionLogHeads(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	dir := filepath.Join(home, "projects", "workspace", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "log-session.jsonl")
	sess := agent.NewSession("system")
	sess.Add(provider.Message{Role: provider.RoleUser, Content: "shared question"})
	sess.Add(provider.Message{Role: provider.RoleAssistant, Content: "shared answer"})
	if err := sess.Save(path); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "diag.zip")
	if _, err := WriteSessionBundle(SessionBundleOptions{Version: "test-version", SessionRef: path, OutputPath: out, Now: time.Unix(1700000000, 0)}); err != nil {
		t.Fatalf("WriteSessionBundle: %v", err)
	}
	files := zipFiles(t, out)
	var manifest SessionBundleManifest
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatalf("manifest JSON: %v", err)
	}
	if strings.Contains(string(files["manifest.json"]), home) {
		t.Fatalf("manifest leaked REASONIX_HOME path:\n%s", files["manifest.json"])
	}
	if len(manifest.Sessions) != 1 {
		t.Fatalf("manifest sessions = %+v, want the log alone: heads are not chain members", manifest.Sessions)
	}
	entry := manifest.Sessions[0]
	// tl1280: ListSessionHeads is the always-nil shim, so the schema-1 log
	// reports LogFormat 0 and no head rows — the event log is the only
	// durable surface the bundle carries.
	if entry.LogFormat != 0 || len(entry.Heads) != 0 {
		t.Fatalf("manifest entry = %+v, want empty head projection", entry)
	}
	if len(manifest.Missing) != 0 {
		t.Fatalf("missing = %v, want none", manifest.Missing)
	}
	wantLog := filepath.ToSlash(filepath.Join("sessions", safeBundleName(agent.BranchID(path)), filepath.Base(store.SessionEventLog(path))))
	if _, ok := files[wantLog]; !ok {
		t.Fatalf("bundle lacks the event log %q; have %v", wantLog, keysOf(files))
	}
}

func keysOf(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for name := range files {
		out = append(out, name)
	}
	return out
}
