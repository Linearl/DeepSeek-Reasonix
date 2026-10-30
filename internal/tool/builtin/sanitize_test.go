package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizePathArg(t *testing.T) {
	wd := t.TempDir()

	t.Run("relative joins workDir and cleans", func(t *testing.T) {
		got := sanitizePathArg(wd, "sub/../x.txt")
		if got != filepath.Join(wd, "x.txt") {
			t.Fatalf("sanitizePathArg = %q, want %q", got, filepath.Join(wd, "x.txt"))
		}
	})
	t.Run("redundant separators collapse", func(t *testing.T) {
		got := sanitizePathArg(wd, filepath.Join("a", "", "b") + string(filepath.Separator))
		want := filepath.Join(wd, "a", "b")
		if got != want {
			t.Fatalf("sanitizePathArg = %q, want %q", got, want)
		}
	})
	t.Run("absolute honored verbatim but cleaned", func(t *testing.T) {
		abs := filepath.Join(wd, "deep", "..", "file.txt")
		got := sanitizePathArg(wd, abs)
		want := filepath.Join(wd, "file.txt")
		if got != want {
			t.Fatalf("sanitizePathArg = %q, want %q", got, want)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("absolute arg must stay absolute, got %q", got)
		}
	})
	t.Run("traversal out of workDir stays a path decision for confine, not the helper", func(t *testing.T) {
		// The helper normalizes; the deny/allow boundary is confine's job
		// (write tools refuse, read tools allow unless forbid-roots). Documents
		// that no NEW rejection lives here.
		got := sanitizePathArg(wd, filepath.Join("..", "outside.txt"))
		want := filepath.Join(filepath.Dir(wd), "outside.txt")
		if got != want {
			t.Fatalf("sanitizePathArg = %q, want %q", got, want)
		}
	})
	t.Run("empty arg resolves to workDir", func(t *testing.T) {
		if got := sanitizePathArg(wd, ""); got != filepath.Clean(wd) {
			t.Fatalf("sanitizePathArg = %q, want %q", got, filepath.Clean(wd))
		}
	})
	t.Run("empty in empty workDir passes through", func(t *testing.T) {
		if got := sanitizePathArg("", ""); got != "" {
			t.Fatalf("sanitizePathArg = %q, want empty passthrough", got)
		}
	})
	t.Run("process-cwd relative arg keeps relative semantics", func(t *testing.T) {
		if got := sanitizePathArg("", filepath.Join("a", "..", "b")); got != "b" {
			t.Fatalf("sanitizePathArg = %q, want %q", got, "b")
		}
	})
}

func TestSanitizeReadPathKeepsExternalDisplay(t *testing.T) {
	root := t.TempDir()
	resolver := NewPathResolver()
	resolver.RegisterReadRoot("vault", root)

	rp := sanitizeReadPath("", "vault/sub/../notes.md", resolver)
	if !rp.External {
		t.Fatalf("expected external alias resolution")
	}
	if rp.Path != filepath.Join(root, "notes.md") {
		t.Fatalf("Path = %q, want %q", rp.Path, filepath.Join(root, "notes.md"))
	}
	if rp.DisplayPath != "vault/notes.md" {
		t.Fatalf("DisplayPath = %q, want token/slash form %q", rp.DisplayPath, "vault/notes.md")
	}

	plain := sanitizeReadPath(root, "a//b.txt", nil)
	if plain.External {
		t.Fatalf("plain path must not be flagged external")
	}
	if plain.Path != filepath.Join(root, "a", "b.txt") {
		t.Fatalf("Path = %q, want %q", plain.Path, filepath.Join(root, "a", "b.txt"))
	}
}

func TestSanitizeWriteTargetEnforcesRoots(t *testing.T) {
	wd := t.TempDir()
	other := t.TempDir()

	t.Run("unconfined returns cleaned target", func(t *testing.T) {
		got, err := sanitizeWriteTarget(context.Background(), wd, "x/../out.txt", nil, nil, SessionDataGuard{}, ManagedConfigPaths{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Join(wd, "out.txt") {
			t.Fatalf("target = %q, want %q", got, filepath.Join(wd, "out.txt"))
		}
	})
	t.Run("inside roots passes", func(t *testing.T) {
		got, err := sanitizeWriteTarget(context.Background(), wd, "in.txt", nil, []string{wd}, SessionDataGuard{}, ManagedConfigPaths{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != filepath.Join(wd, "in.txt") {
			t.Fatalf("target = %q, want %q", got, filepath.Join(wd, "in.txt"))
		}
	})
	t.Run("outside roots refused", func(t *testing.T) {
		_, err := sanitizeWriteTarget(context.Background(), wd, filepath.Join(other, "evil.txt"), nil, []string{wd}, SessionDataGuard{}, ManagedConfigPaths{})
		if err == nil || !strings.Contains(err.Error(), "outside the writable roots") {
			t.Fatalf("want confine error, got %v", err)
		}
	})
}

func TestSanitizePreviewTargetEnforcesRoots(t *testing.T) {
	wd := t.TempDir()
	other := t.TempDir()

	if got, err := sanitizePreviewTarget(context.Background(), wd, "p.txt", nil, []string{wd}, SessionDataGuard{}, ManagedConfigPaths{}); err != nil || got != filepath.Join(wd, "p.txt") {
		t.Fatalf("sanitizePreviewTarget = %q, %v", got, err)
	}
	_, err := sanitizePreviewTarget(context.Background(), wd, filepath.Join(other, "evil.txt"), nil, []string{wd}, SessionDataGuard{}, ManagedConfigPaths{})
	if err == nil || !strings.Contains(err.Error(), "outside the writable roots") {
		t.Fatalf("want confine error, got %v", err)
	}
}

func TestSanitizeFetchURL(t *testing.T) {
	const wantErr = "url must be an absolute http(s) address"
	for _, raw := range []string{
		"", "notaurl", "//example.com/x", "file:///etc/passwd",
		"ftp://example.com", "javascript:alert(1)", "gopher://example.com",
	} {
		if _, err := sanitizeFetchURL(raw); err == nil || err.Error() != wantErr {
			t.Fatalf("sanitizeFetchURL(%q) err = %v, want %q", raw, err, wantErr)
		}
	}
	for _, raw := range []string{"https://example.com/x?a=1", "http://example.com", "HTTP://EXAMPLE.COM"} {
		u, err := sanitizeFetchURL(raw)
		if err != nil {
			t.Fatalf("sanitizeFetchURL(%q) unexpected error: %v", raw, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("sanitizeFetchURL(%q) scheme = %q", raw, u.Scheme)
		}
	}
}

// TestWebFetchRefusesNonHTTP pins the web_fetch entry on the URL-family
// helper: the fetch policy error surfaces before any request is built. (An
// empty url fails earlier with "url is required" and is covered by the helper
// table above.)
func TestWebFetchRefusesNonHTTP(t *testing.T) {
	const wantErr = "url must be an absolute http(s) address"
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com", "javascript:alert(1)"} {
		args, err := json.Marshal(map[string]string{"url": raw})
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		if _, err := (webFetch{}).Execute(context.Background(), args); err == nil || err.Error() != wantErr {
			t.Fatalf("web_fetch url=%q err = %v, want %q", raw, err, wantErr)
		}
	}
}
