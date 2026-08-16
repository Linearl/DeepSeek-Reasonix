package serve

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/control"
)

// fakeCtrlForUpload is a minimal SessionAPI that only answers WorkspaceRoot.
type fakeCtrlForUpload struct {
	control.SessionAPI // nil-embed: only WorkspaceRoot is exercised
	root               string
}

func (f *fakeCtrlForUpload) WorkspaceRoot() string { return f.root }

func newUploadServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	srv := &Server{ctrl: &fakeCtrlForUpload{root: root}}
	return srv, root
}

// 1x1 PNG.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0d, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestUploadAttachmentRawBody(t *testing.T) {
	srv, root := newUploadServer(t)
	req := httptest.NewRequest(http.MethodPost, "/attachments", bytes.NewReader(tinyPNG))
	req.Header.Set("Content-Type", "image/png")
	rec := httptest.NewRecorder()
	srv.uploadAttachment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasPrefix(resp.Ref, ".reasonix/attachments/") || !strings.HasSuffix(resp.Ref, ".png") {
		t.Fatalf("ref = %q, want .reasonix/attachments/*.png", resp.Ref)
	}
	abs := filepath.Join(root, filepath.FromSlash(resp.Ref))
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("file not written at %s: %v", abs, err)
	}
}

func TestUploadAttachmentRejectsNonImageContentType(t *testing.T) {
	srv, _ := newUploadServer(t)
	req := httptest.NewRequest(http.MethodPost, "/attachments", bytes.NewReader(tinyPNG))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	rec := httptest.NewRecorder()
	srv.uploadAttachment(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 for non-image content type", rec.Code)
	}
}

func TestUploadAttachmentRejectsNonImage(t *testing.T) {
	srv, _ := newUploadServer(t)
	req := httptest.NewRequest(http.MethodPost, "/attachments", bytes.NewReader([]byte("hello world, not an image")))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	srv.uploadAttachment(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestUploadAttachmentRejectsEmptyAndOversize(t *testing.T) {
	srv, _ := newUploadServer(t)
	reqEmpty := httptest.NewRequest(http.MethodPost, "/attachments", bytes.NewReader(nil))
	reqEmpty.Header.Set("Content-Type", "image/png")
	rec := httptest.NewRecorder()
	srv.uploadAttachment(rec, reqEmpty)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty: status = %d, want 400", rec.Code)
	}
	reqBig := httptest.NewRequest(http.MethodPost, "/attachments", bytes.NewReader(bytes.Repeat([]byte{0}, maxServeUploadBytes+1)))
	reqBig.Header.Set("Content-Type", "image/png")
	rec2 := httptest.NewRecorder()
	srv.uploadAttachment(rec2, reqBig)
	if rec2.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize: status = %d, want 413", rec2.Code)
	}
}

