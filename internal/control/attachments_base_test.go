package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A minimal valid 1x1 PNG: 8-byte signature + IHDR + IDAT + IEND.
var testPNGBase = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89, 0x00, 0x00, 0x00,
	0x0D, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9C, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0D, 0x0A, 0x2D, 0xB4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
}

func writeFileBase(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// The relaunch path is the whole point of base-aware attachments: a desktop
// process whose working directory is the install root must still resolve the
// stored reference against the owning workspace root.
func TestImageDataURLInResolvesAgainstBaseDespiteWorkingDirectory(t *testing.T) {
	base := t.TempDir()
	src := writeFileBase(t, t.TempDir(), "shot.png", testPNGBase)

	rel, err := SaveImageFileIn(base, src)
	if err != nil {
		t.Fatalf("SaveImageFileIn: %v", err)
	}
	if !strings.HasPrefix(rel, ".reasonix/attachments/") {
		t.Fatalf("stored reference %q is not under .reasonix/attachments/", rel)
	}
	stored, err := os.Stat(filepath.Join(base, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("stored file missing under base: %v", err)
	}
	if stored.Size() == 0 {
		t.Fatal("stored file is empty")
	}

	// Move the process working directory somewhere unrelated, like a launcher
	// relaunch into the install root does.
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	elsewhere := t.TempDir()
	if err := os.Chdir(elsewhere); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	if _, err := ImageDataURL(rel); err == nil {
		t.Fatal("CWD-based lookup unexpectedly succeeded; test is not proving the fix")
	}
	if _, err := ImageDataURLIn(base, rel); err != nil {
		t.Fatalf("ImageDataURLIn with base: %v", err)
	}
}

// The CLI keeps the historical behaviour: an empty base means "process working
// directory", so existing callers must not change where attachments land.
func TestSaveImageFileEmptyBaseKeepsWorkingDirectoryBehaviour(t *testing.T) {
	cwd := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(prev) }()

	src := writeFileBase(t, t.TempDir(), "shot.png", testPNGBase)
	rel, err := SaveImageFileIn("", src)
	if err != nil {
		t.Fatalf("SaveImageFile with empty base: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cwd, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("attachment not stored under the working directory: %v", err)
	}
}

// cleanAttachmentPath must keep rejecting escapes even when a base is given.
func TestCleanAttachmentPathBaseStillRejectsEscape(t *testing.T) {
	base := t.TempDir()
	if _, err := cleanAttachmentPath(base, `C:\windows\evil.png`); err == nil {
		t.Fatal("absolute path accepted")
	}
	if _, err := cleanAttachmentPath(base, `../outside.png`); err == nil {
		t.Fatal("parent escape accepted")
	}
	if _, err := cleanAttachmentPath(base, `notattachments/file.png`); err == nil {
		t.Fatal("path outside .reasonix/attachments accepted")
	}
}
