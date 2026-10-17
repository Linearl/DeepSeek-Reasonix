package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func renderArtifactExecute(t *testing.T, tool renderArtifactTool, args string) (string, error) {
	t.Helper()
	return tool.Execute(context.Background(), json.RawMessage(args))
}

func renderArtifactResultOf(t *testing.T, out string) renderArtifactResult {
	t.Helper()
	var parsed renderArtifactResult
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v\n%s", err, out)
	}
	return parsed
}

func TestRenderArtifactKindInference(t *testing.T) {
	cases := map[string]string{
		"report.md":      "md",
		"REPORT.MD":      "md",
		"notes.markdown": "md",
		"page.html":      "html",
		"page.HTM":       "html",
		"paper.pdf":      "pdf",
		"chart.png":      "image",
		"photo.JPEG":     "image",
		"logo.svg":       "image",
		"dump.txt":       "text",
		"data.csv":       "text",
		"noextension":    "text",
		"archive.tar.gz": "text",
	}
	for path, want := range cases {
		if got := artifactKindForPath(path); got != want {
			t.Errorf("artifactKindForPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRenderArtifactExplicitKindOverridesExtension(t *testing.T) {
	dir := t.TempDir()
	mdPath := filepath.Join(dir, "report.md")
	if err := os.WriteFile(mdPath, []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	out, err := renderArtifactExecute(t, tool, `{"path":"report.md","kind":"text"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := renderArtifactResultOf(t, out); got.Kind != "text" {
		t.Fatalf("kind = %q, want explicit text", got.Kind)
	}
}

func TestRenderArtifactRejectsUnknownKind(t *testing.T) {
	tool := renderArtifactTool{workDir: t.TempDir()}
	if _, err := renderArtifactExecute(t, tool, `{"path":"x.md","kind":"video"}`); err == nil || !strings.Contains(err.Error(), "unknown kind") {
		t.Fatalf("err = %v, want unknown-kind error", err)
	}
}

func TestRenderArtifactRelativePathResolvesAgainstWorkDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.md"), []byte("# t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	out, err := renderArtifactExecute(t, tool, `{"path":"r.md"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := renderArtifactResultOf(t, out)
	if got.Path != filepath.Join(dir, "r.md") {
		t.Fatalf("path = %q, want workdir-resolved", got.Path)
	}
	if got.Title != "r.md" {
		t.Fatalf("title = %q, want file name default", got.Title)
	}
	if got.Kind != "md" {
		t.Fatalf("kind = %q, want md", got.Kind)
	}
}

func TestRenderArtifactRelativePathWithoutWorkDirFails(t *testing.T) {
	tool := renderArtifactTool{}
	if _, err := renderArtifactExecute(t, tool, `{"path":"r.md"}`); err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("err = %v, want not-absolute error", err)
	}
}

func TestRenderArtifactMissingFileFails(t *testing.T) {
	tool := renderArtifactTool{workDir: t.TempDir()}
	_, err := renderArtifactExecute(t, tool, `{"path":"missing.md"}`)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("err = %v, want does-not-exist error", err)
	}
}

func TestRenderArtifactRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	if _, err := renderArtifactExecute(t, tool, `{"path":"sub"}`); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("err = %v, want directory error", err)
	}
}

func TestRenderArtifactRejectsExecutableTarget(t *testing.T) {
	dir := t.TempDir()
	// A .md path whose symlink resolves to an executable must be refused —
	// the same symlink check the opener family applies.
	if err := os.WriteFile(filepath.Join(dir, "evil.bat"), []byte("echo hi\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "evil.bat"), filepath.Join(dir, "doc.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	tool := renderArtifactTool{workDir: dir}
	if _, err := renderArtifactExecute(t, tool, `{"path":"doc.md"}`); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("err = %v, want executable-target refusal", err)
	}
}

func TestRenderArtifactDirectExecutableSuffixRefused(t *testing.T) {
	dir := t.TempDir()
	bat := filepath.Join(dir, "run.bat")
	if err := os.WriteFile(bat, []byte("echo hi\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	if _, err := renderArtifactExecute(t, tool, `{"path":"run.bat"}`); err == nil || !strings.Contains(err.Error(), "executable") {
		t.Fatalf("err = %v, want executable-target refusal", err)
	}
}

func TestRenderArtifactMarkdownPreviewBounded(t *testing.T) {
	dir := t.TempDir()
	var body strings.Builder
	for i := 0; i < renderArtifactPreviewMaxLines+20; i++ {
		body.WriteString("line\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "r.md"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	out, err := renderArtifactExecute(t, tool, `{"path":"r.md"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := renderArtifactResultOf(t, out)
	if got.Preview == "" {
		t.Fatal("preview missing for md card")
	}
	if !got.PreviewTruncated {
		t.Fatal("previewTruncated = false, want true for >40 lines")
	}
	if lines := strings.Count(got.Preview, "\n") + 1; lines > renderArtifactPreviewMaxLines {
		t.Fatalf("preview lines = %d, want <= %d", lines, renderArtifactPreviewMaxLines)
	}
}

func TestRenderArtifactBinaryPreviewDropped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.txt"), []byte{'a', 'b', 0x00, 'c'}, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	out, err := renderArtifactExecute(t, tool, `{"path":"r.txt"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := renderArtifactResultOf(t, out)
	if got.Preview != "" {
		t.Fatalf("preview = %q, want empty for binary content", got.Preview)
	}
	// The card itself must survive: header fields stay intact.
	if !got.OK || got.Kind != "text" {
		t.Fatalf("result = %+v, want ok text card", got)
	}
}

func TestRenderArtifactHtmlIsOpenOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	out, err := renderArtifactExecute(t, tool, `{"path":"p.html"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := renderArtifactResultOf(t, out)
	if got.Kind != "html" {
		t.Fatalf("kind = %q, want html", got.Kind)
	}
	if got.Preview != "" {
		t.Fatalf("preview = %q, want empty (html 首期不内嵌)", got.Preview)
	}
}

func TestRenderArtifactNoteAndTitlePassThrough(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := renderArtifactTool{workDir: dir}
	out, err := renderArtifactExecute(t, tool, `{"path":"r.md","title":"周报","note":"本周进展汇总"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := renderArtifactResultOf(t, out)
	if got.Title != "周报" || got.Note != "本周进展汇总" {
		t.Fatalf("title/note = %q/%q, want pass-through", got.Title, got.Note)
	}
}

func TestRenderArtifactEmptyPathFails(t *testing.T) {
	tool := renderArtifactTool{workDir: t.TempDir()}
	if _, err := renderArtifactExecute(t, tool, `{"path":"  "}`); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("err = %v, want required error", err)
	}
}

func TestRenderArtifactSchemaIsCanonicalObject(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(NewRenderArtifactTool("").Schema(), &schema); err != nil {
		t.Fatalf("schema not valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
	props, _ := schema["properties"].(map[string]any)
	if _, ok := props["path"]; !ok {
		t.Fatal("schema missing path property")
	}
}
