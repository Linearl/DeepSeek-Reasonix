package serve

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
)

// Escape tests for task 417 (go/path-injection, serve domain): every handler
// that turns a remote client-supplied session name into a filesystem path
// must prove the result stays inside the session directory.

func TestSessionFileForNameRejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Dir(dir)

	inside := func(abs string) bool {
		return pathInsideDir(dir, abs)
	}

	for _, name := range []string{"../escape", "..\\escape", "a/b", "a\\b", "../../escape", "./ok/../escape", "..", ".", ""} {
		absDir, abs, err := sessionFileForName(dir, name)
		if err == nil {
			t.Fatalf("sessionFileForName(%q) = %q, want rejection", name, abs)
		}
		if abs != "" || absDir != "" {
			t.Fatalf("sessionFileForName(%q) returned paths alongside its error", name)
		}
		// Nothing may have materialized inside or outside the session dir.
		if _, err := os.Stat(filepath.Join(parent, "escape.jsonl")); err == nil {
			t.Fatalf("name %q created escape.jsonl outside the session dir", name)
		}
		if inside(filepath.Join(parent, "escape.jsonl")) {
			t.Fatal("containment probe is broken: parent path reported inside")
		}
	}

	absDir, abs, err := sessionFileForName(dir, "legit")
	if err != nil {
		t.Fatalf("sessionFileForName(\"legit\"): %v", err)
	}
	if !inside(abs) || filepath.Dir(abs) != filepath.Clean(absDir) {
		t.Fatalf("legit name resolved to %q (dir %q): not directly inside session dir", abs, absDir)
	}
	if !strings.HasSuffix(abs, "legit.jsonl") {
		t.Fatalf("legit name resolved to %q, want .jsonl suffix", abs)
	}
}

func TestSessionFileForNameRejectsAbsoluteNames(t *testing.T) {
	dir := t.TempDir()
	// Absolute-path names (POSIX root, Windows drive-letter forms) must be
	// rejected like traversal: they carry separators, and even a separator-
	// free drive-relative name ("C:evil") must only ever resolve inside the
	// session directory (task 779 guard, go/path-injection triage).
	for _, name := range []string{"/etc/evil", "\\evil", `C:\evil`, "C:evil", "/abs/path"} {
		_, abs, err := sessionFileForName(dir, name)
		if strings.ContainsAny(name, `/\`) {
			if err == nil {
				t.Fatalf("sessionFileForName(%q) = %q, want rejection", name, abs)
			}
			continue
		}
		// Separator-free ("C:evil"): the resolver accepts it but the resolved
		// path must still sit inside the session dir.
		if err != nil {
			t.Fatalf("sessionFileForName(%q): %v", name, err)
		}
		if !pathInsideDir(dir, abs) {
			t.Fatalf("sessionFileForName(%q) resolved to %q: escaped the session dir", name, abs)
		}
	}
	// The containment probe itself must refuse absolute escapes (both the
	// session-side check and the marker-derivative argument in the takeover
	// chain rest on it).
	for _, outside := range []string{`C:\Windows\evil.jsonl`, "/etc/evil.jsonl"} {
		if pathInsideDir(dir, outside) {
			t.Fatalf("pathInsideDir(%q) = true for outside path %q", dir, outside)
		}
	}
}

func TestTakeoverMarkerPathStaysInsideSessionDir(t *testing.T) {
	// Task 779 guard for the 539/579 takeover chain (go/path-injection):
	// serve.go's os.WriteFile and pollTakeoverYield/tryTakeoverAcquire's
	// os.Remove/os.ReadFile all operate on TakeoverRequestMarkerPath(abs).
	// The derivation is a fixed-suffix rewrite of the containment-proven abs,
	// so the marker must land in the very same session directory — this test
	// pins that invariant so a future derivation change cannot silently
	// reintroduce an escape.
	dir := t.TempDir()
	for _, name := range []string{"legit", "C:evil", "dot.dot"} {
		absDir, abs, err := sessionFileForName(dir, name)
		if err != nil {
			t.Fatalf("sessionFileForName(%q): %v", name, err)
		}
		marker := agent.TakeoverRequestMarkerPath(abs)
		if !pathInsideDir(absDir, marker) {
			t.Fatalf("marker %q escaped session dir %q (session path %q)", marker, absDir, abs)
		}
		if filepath.Dir(marker) != filepath.Clean(absDir) {
			t.Fatalf("marker %q not directly inside %q", marker, absDir)
		}
		if filepath.Ext(marker) != ".takeover-request" {
			t.Fatalf("marker %q lost the .takeover-request suffix", marker)
		}
	}

	// End-to-end: a traversal name must die at sessionFileForName and never
	// reach the marker derivation — no marker file may materialize outside
	// the session dir (complements TestTakeoverSessionRejectsTraversalName,
	// which covers the HTTP handler path).
	parent := filepath.Dir(dir)
	for _, name := range []string{"../escape", "..\\escape", "../../escape"} {
		_, _, err := sessionFileForName(dir, name)
		if err == nil {
			t.Fatalf("sessionFileForName(%q) accepted a traversal name", name)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "escape.jsonl.takeover-request")); err == nil {
		t.Fatal("takeover marker materialized outside the session dir")
	}
}

func TestTakeoverSessionRejectsTraversalName(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Dir(dir)
	active := filepath.Join(dir, "active.jsonl")
	if err := os.WriteFile(active, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctrl := control.New(control.Options{SessionDir: dir, SessionPath: active})
	server := New(ctrl, NewBroadcaster(), config.ServeConfig{})
	defer server.Close()

	rec := httptest.NewRecorder()
	server.takeoverSession(rec, httptest.NewRequest(http.MethodPost, "/takeover-session", strings.NewReader(`{"name":"../evil"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("takeover traversal name status = %d, want 400", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(parent, "evil.jsonl")); err == nil {
		t.Fatal("takeover created evil.jsonl outside the session dir")
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.jsonl.takeover-request")); err == nil {
		t.Fatal("takeover wrote a takeover-request marker for a traversal name")
	}

	// A well-formed name for a session that does not exist must stay on the
	// not-found path: the request never reaches lease acquisition.
	rec = httptest.NewRecorder()
	server.takeoverSession(rec, httptest.NewRequest(http.MethodPost, "/takeover-session", strings.NewReader(`{"name":"ghost"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("takeover missing session status = %d, want 404", rec.Code)
	}
}

func TestRemoveSessionFilesRefusesOutsidePath(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()
	outside := filepath.Join(outsideDir, "victim.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionFiles(dir, outside); err == nil {
		t.Fatal("removeSessionFiles accepted a path outside the session dir")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside victim was removed despite the refusal: %v", err)
	}

	inside := filepath.Join(dir, "target.jsonl")
	if err := os.WriteFile(inside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := removeSessionFiles(dir, inside); err != nil {
		t.Fatalf("removeSessionFiles refused an inside path: %v", err)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("inside transcript survived removal: %v", err)
	}
}
