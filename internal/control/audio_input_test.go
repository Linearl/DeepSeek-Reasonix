package control

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/provider"
)

func writeAudioFixtures(t *testing.T) (temp, attachRef, workspaceFile string) {
	t.Helper()
	temp = t.TempDir()
	attachmentsDir := filepath.Join(temp, ".reasonix", "attachments")
	if err := os.MkdirAll(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	attachRef = ".reasonix/attachments/clip.mp3"
	if err := os.WriteFile(filepath.Join(temp, filepath.FromSlash(attachRef)), []byte("ID3fake-mp3-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceFile = filepath.Join(temp, "tone.wav")
	if err := os.WriteFile(workspaceFile, []byte("RIFFfake-wav-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	return temp, attachRef, workspaceFile
}

// 任务 1008：音频 @引用以注记块进上下文（不按文本读出二进制乱码），注记
// 文案随门控两态切换。
func TestResolveRefsAudioAttachmentNote(t *testing.T) {
	temp, attachRef, workspaceFile := writeAudioFixtures(t)

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(temp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	audioOn := true
	line := "listen to @" + attachRef + " and @" + workspaceFile
	block, errs := (&Controller{frozenAudioInput: &audioOn}).ResolveRefs(context.Background(), line)
	if len(errs) != 0 {
		t.Fatalf("ResolveRefs errors = %v", errs)
	}
	if !strings.Contains(block, `<audio path="`+attachRef+`">`) {
		t.Fatalf("expected audio attachment block, got: %s", block)
	}
	if !strings.Contains(block, "attached as audio input") {
		t.Fatalf("gate-on note should claim attachment, got: %s", block)
	}
	if strings.Contains(block, "ID3fake-mp3-bytes") || strings.Contains(block, "RIFFfake-wav-bytes") {
		t.Fatalf("audio bytes must never be dumped as text, got: %s", block)
	}

	audioOff := false
	block, errs = (&Controller{frozenAudioInput: &audioOff}).ResolveRefs(context.Background(), line)
	if len(errs) != 0 {
		t.Fatalf("ResolveRefs errors = %v", errs)
	}
	if !strings.Contains(block, "only when the selected model supports audio input") {
		t.Fatalf("gate-off note should state the gate, got: %s", block)
	}
}

// 任务 1008：门关时解析为空（请求保持无音频字节面），门开时附件与工作区
// 文件都解析成对应 MIME 的 data URL。
func TestResolveTurnAudioGateAndCandidates(t *testing.T) {
	temp, attachRef, workspaceFile := writeAudioFixtures(t)

	audioOff := false
	off := &Controller{workspaceRoot: temp, frozenAudioInput: &audioOff}
	if got := off.resolveTurnAudio("@" + attachRef); got != nil {
		t.Fatalf("gate-off resolve = %v, want nil", got)
	}

	audioOn := true
	on := &Controller{workspaceRoot: temp, frozenAudioInput: &audioOn}
	got := on.resolveTurnAudio("compare @" + attachRef + " with @" + workspaceFile)
	if len(got) != 2 {
		t.Fatalf("gate-on resolve = %v, want two data URLs", got)
	}
	if !strings.HasPrefix(got[0], "data:audio/mpeg;base64,") {
		t.Fatalf("attachment url = %q, want audio/mpeg data URL", got[0])
	}
	if !strings.HasPrefix(got[1], "data:audio/wav;base64,") {
		t.Fatalf("workspace url = %q, want audio/wav data URL", got[1])
	}
	// The wire format the construction layer derives must name the family.
	mime, _, ok := provider.ParseAudioDataURL(got[0])
	if !ok || provider.AudioWireFormat(mime) != "mp3" {
		mime2, _, _ := provider.ParseAudioDataURL(got[0])
		t.Fatalf("wire format for %s (mime %q) = %q, want mp3", got[0], mime2, provider.AudioWireFormat(mime))
	}
}
