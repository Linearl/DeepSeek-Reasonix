package serve

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/stats"
)

// minimalPNG returns a valid 1x1 white PNG.
func minimalPNG() []byte {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.White)
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestUploadAttachmentJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	// serve.New opens the process-local usage catalog under the redirected
	// REASONIX_HOME; on Windows its SQLite handle keeps the TempDir cache
	// file locked past the test, reding t.TempDir's RemoveAll cleanup
	// (2026-09-22 登记，任务496片5清零). Registered after t.TempDir so LIFO
	// runs it before that cleanup.
	t.Cleanup(func() { _ = stats.CloseUsageCatalogs(context.Background()) })

	projRoot := filepath.Join(home, "project")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, WorkspaceRoot: projRoot})
	srv := httptest.NewServer(New(ctrl, bc, config.ServeConfig{}).Handler())
	defer srv.Close()

	pngData := minimalPNG()
	b64 := base64.StdEncoding.EncodeToString(pngData)
	body, _ := json.Marshal(map[string]string{
		"data": b64,
		"mime": "image/png",
	})

	resp, err := http.Post(srv.URL+"/attachments", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	ref := result["ref"]
	if ref == "" {
		t.Fatal("ref is empty")
	}
	if !strings.Contains(ref, ".reasonix/attachments/") {
		t.Fatalf("ref %q does not contain .reasonix/attachments/", ref)
	}
	if !strings.HasSuffix(ref, ".png") {
		t.Fatalf("ref %q does not end with .png", ref)
	}
	// Verify the file was actually written (relative to project root).
	if _, err := os.Stat(filepath.Join(projRoot, ref)); err != nil {
		t.Fatalf("file not written: %v", err)
	}
}

func TestUploadAttachmentNoData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Cleanup(func() { _ = stats.CloseUsageCatalogs(context.Background()) })

	projRoot := filepath.Join(home, "project")
	os.MkdirAll(projRoot, 0o755)

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, WorkspaceRoot: projRoot})
	srv := httptest.NewServer(New(ctrl, bc, config.ServeConfig{}).Handler())
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{"mime": "image/png"})
	resp, err := http.Post(srv.URL+"/attachments", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

// GET /attachment must serve back exactly the bytes POST /attachments stored —
// the upload->display loop the web composer relies on (tl1813).
func TestGetAttachmentRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Cleanup(func() { _ = stats.CloseUsageCatalogs(context.Background()) })

	projRoot := filepath.Join(home, "project")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, WorkspaceRoot: projRoot})
	srv := httptest.NewServer(New(ctrl, bc, config.ServeConfig{}).Handler())
	defer srv.Close()

	body, _ := json.Marshal(map[string]string{
		"data": base64.StdEncoding.EncodeToString(minimalPNG()),
		"mime": "image/png",
	})
	up, err := http.Post(srv.URL+"/attachments", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer up.Body.Close()
	if up.StatusCode != 200 {
		t.Fatalf("upload status = %d, want 200", up.StatusCode)
	}
	var result map[string]string
	if err := json.NewDecoder(up.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	ref := result["ref"]
	if ref == "" {
		t.Fatal("upload returned empty ref")
	}

	get, err := http.Get(srv.URL + "/attachment?path=" + url.QueryEscape(ref))
	if err != nil {
		t.Fatal(err)
	}
	defer get.Body.Close()
	if get.StatusCode != 200 {
		t.Fatalf("get status = %d, want 200", get.StatusCode)
	}
	if ct := get.Header.Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content type = %q, want image/png", ct)
	}
	served, err := io.ReadAll(get.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(served, minimalPNG()) {
		t.Fatalf("served bytes differ from uploaded png (got %d bytes)", len(served))
	}
}

func TestGetAttachmentRejectsEscapes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	t.Cleanup(func() { _ = stats.CloseUsageCatalogs(context.Background()) })

	projRoot := filepath.Join(home, "project")
	if err := os.MkdirAll(projRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	// a secret outside the attachments dir must not be reachable
	if err := os.WriteFile(filepath.Join(projRoot, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projRoot, ".reasonix", "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}

	bc := NewBroadcaster()
	ctrl := control.New(control.Options{Sink: bc, WorkspaceRoot: projRoot})
	srv := httptest.NewServer(New(ctrl, bc, config.ServeConfig{}).Handler())
	defer srv.Close()

	cases := []struct {
		name string
		path string
	}{
		{"traversal", ".reasonix/attachments/../../secret.txt"},
		{"backslash traversal", `.reasonix/attachments\..\..\secret.txt`},
		{"absolute", filepath.Join(projRoot, "secret.txt")},
		{"outside root", "secret.txt"},
		{"nested", ".reasonix/attachments/sub/x.png"},
		{"non-image ext", ".reasonix/attachments/x.txt"},
		{"missing file", ".reasonix/attachments/clipboard-absent.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := http.Get(srv.URL + "/attachment?path=" + url.QueryEscape(tc.path))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				t.Fatalf("path %q served with 200, want rejection", tc.path)
			}
		})
	}
}
