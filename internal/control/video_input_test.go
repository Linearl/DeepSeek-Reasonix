package control

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func writeVideoFixtures(t *testing.T) (temp, attachRef, workspaceFile string) {
	t.Helper()
	temp = t.TempDir()
	attachmentsDir := filepath.Join(temp, ".reasonix", "attachments")
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	attachRef = ".reasonix/attachments/clip.mp4"
	if err := os.WriteFile(filepath.Join(temp, filepath.FromSlash(attachRef)), []byte("ftypfake-mp4-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceFile = filepath.Join(temp, "take.webm")
	if err := os.WriteFile(workspaceFile, []byte("EBMLfake-webm-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return temp, attachRef, workspaceFile
}

// tl1767：视频 @引用以注记块进上下文（不按文本读出二进制乱码），注记
// 文案随门控两态切换——与任务 1008 音频同构。
func TestResolveRefsVideoAttachmentNote(t *testing.T) {
	temp, attachRef, workspaceFile := writeVideoFixtures(t)

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	videoOn := true
	line := "watch @" + attachRef + " and @" + workspaceFile
	block, errs := (&Controller{frozenVideoInput: &videoOn}).ResolveRefs(context.Background(), line)
	if len(errs) != 0 {
		t.Fatalf("ResolveRefs errors = %v", errs)
	}
	if !strings.Contains(block, `<video path="`+attachRef+`">`) {
		t.Fatalf("expected video attachment block, got: %s", block)
	}
	if !strings.Contains(block, "attached as video input") {
		t.Fatalf("gate-on note should claim attachment, got: %s", block)
	}
	if strings.Contains(block, "ftypfake-mp4-bytes") || strings.Contains(block, "EBMLfake-webm-bytes") {
		t.Fatalf("video bytes must never be dumped as text, got: %s", block)
	}

	videoOff := false
	block, errs = (&Controller{frozenVideoInput: &videoOff}).ResolveRefs(context.Background(), line)
	if len(errs) != 0 {
		t.Fatalf("ResolveRefs errors = %v", errs)
	}
	if !strings.Contains(block, "only when the selected model supports video input") {
		t.Fatalf("gate-off note should state the gate, got: %s", block)
	}
}

// tl1767：门关时解析为空（请求保持无视频字节面），门开时附件与工作区
// 文件都解析成对应 MIME 的 data URL。
func TestResolveTurnVideoGateAndCandidates(t *testing.T) {
	temp, attachRef, workspaceFile := writeVideoFixtures(t)

	videoOff := false
	off := &Controller{workspaceRoot: temp, frozenVideoInput: &videoOff}
	if got := off.resolveTurnVideo("@" + attachRef); got != nil {
		t.Fatalf("gate-off resolve = %v, want nil", got)
	}

	videoOn := true
	on := &Controller{workspaceRoot: temp, frozenVideoInput: &videoOn}
	got := on.resolveTurnVideo("compare @" + attachRef + " with @" + workspaceFile)
	if len(got) != 2 {
		t.Fatalf("gate-on resolve = %v, want two data URLs", got)
	}
	if !strings.HasPrefix(got[0], "data:video/mp4;base64,") {
		t.Fatalf("attachment url = %q, want video/mp4 data URL", got[0])
	}
	if !strings.HasPrefix(got[1], "data:video/webm;base64,") {
		t.Fatalf("workspace url = %q, want video/webm data URL", got[1])
	}
	if _, _, ok := provider.ParseVideoDataURL(got[0]); !ok {
		t.Fatalf("attachment data URL must parse as video: %q", got[0])
	}
}

// tl1767：视频附件保存链——扩展名白名单 + 50MB 上限；伪装扩展名拒收。
func TestSaveVideoAttachmentDataURLValidates(t *testing.T) {
	temp := t.TempDir()
	const marker = ";base64,"
	payload := base64Of("ftypfake-mp4-bytes")
	if _, err := SaveVideoAttachmentDataURLIn(temp, "clip.mp4", "data:video/mp4"+marker+payload); err != nil {
		t.Fatalf("valid mp4 rejected: %v", err)
	}
	if _, err := SaveVideoAttachmentDataURLIn(temp, "clip.txt", "data:video/mp4"+marker+payload); err == nil {
		t.Fatal("a .txt masquerading as video must be rejected")
	}
	big := strings.Repeat("A", provider.MaxVideoAttachmentBytes+1)
	if _, err := SaveVideoAttachmentDataURLIn(temp, "clip.mp4", "data:video/mp4"+marker+base64Of(big)); err == nil {
		t.Fatal("oversized video must be rejected")
	}
	if _, err := SaveVideoAttachmentFileIn(temp, filepath.Join(temp, "take.webm")); err == nil {
		t.Fatal("missing dropped file must error")
	}
}

func base64Of(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}
