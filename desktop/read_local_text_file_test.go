package main

// tl1565（G3）Go 侧契约测试：附件卡片「复制内容」读桥 ReadLocalTextFile。
// 门禁与 opener 家族同源（normalize + 可执行拒绝 + symlink 落点检查），
// 这里钉读侧特有面：目录拒读、二进制（NUL 头）拒读、超上限拒读（诚实报
// 错不截断）、相对路径拒收、file:// 形态接受。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLocalTextFileHappyPathAndRefusals(t *testing.T) {
	app := &App{ctx: context.Background()}
	dir := t.TempDir()

	// 1) 普通文本读回。
	textPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(textPath, []byte("# 标题\n正文第一行"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := app.ReadLocalTextFile(textPath)
	if err != nil {
		t.Fatalf("ReadLocalTextFile(text): %v", err)
	}
	if got != "# 标题\n正文第一行" {
		t.Fatalf("unexpected text payload: %q", got)
	}

	// 2) file:/// URL 形态与裸路径等价（opener 家族同款归一化）。
	fileURL := "file:///" + filepath.ToSlash(textPath)
	if gotURL, err := app.ReadLocalTextFile(fileURL); err != nil || gotURL != got {
		t.Fatalf("ReadLocalTextFile(file://): err=%v payload=%q", err, gotURL)
	}

	// 3) 目录拒读。
	if _, err := app.ReadLocalTextFile(dir); err == nil {
		t.Fatal("directory read must be refused")
	}

	// 4) 相对路径拒收（normalize 的绝对性要求——前端探针 ResolveOpenablePath
	// 负责先解析成绝对路径）。
	if _, err := app.ReadLocalTextFile("relative/notes.md"); err == nil {
		t.Fatal("relative path must be refused")
	}

	// 5) 不存在的路径报错（诚实失败，前端 toast）。
	if _, err := app.ReadLocalTextFile(filepath.Join(dir, "missing.md")); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestReadLocalTextFileRefusesExecutableTargets(t *testing.T) {
	app := &App{ctx: context.Background()}
	dir := t.TempDir()

	batPath := filepath.Join(dir, "run.bat")
	if err := os.WriteFile(batPath, []byte("@echo off\r\necho hi\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := app.ReadLocalTextFile(batPath)
	if err == nil {
		t.Fatal("executable target (.bat) must be refused even though its bytes are text")
	}
	if !strings.Contains(err.Error(), "executable") {
		t.Fatalf("refusal should name the executable gate, got %v", err)
	}

	// .exe 同族（字节必然带 NUL，两道门都要独立生效）。
	exePath := filepath.Join(dir, "tool.exe")
	if err := os.WriteFile(exePath, []byte("MZ\x00\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadLocalTextFile(exePath); err == nil {
		t.Fatal("executable target (.exe) must be refused")
	}
}

func TestReadLocalTextFileRefusesBinaryPayload(t *testing.T) {
	app := &App{ctx: context.Background()}
	dir := t.TempDir()

	// PNG 头（含 NUL）——附件卡片最常见二进制形态。
	pngPath := filepath.Join(dir, "image.png")
	if err := os.WriteFile(pngPath, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadLocalTextFile(pngPath); err == nil {
		t.Fatal("binary payload (NUL header) must be refused")
	}
}

func TestReadLocalTextFileRefusesOversizedPayload(t *testing.T) {
	app := &App{ctx: context.Background()}
	dir := t.TempDir()

	big := make([]byte, readLocalTextFileLimit+1)
	for i := range big {
		big[i] = byte('a' + i%26)
	}
	bigPath := filepath.Join(dir, "big.log")
	if err := os.WriteFile(bigPath, big, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadLocalTextFile(bigPath); err == nil {
		t.Fatal("oversized payload must be refused (no silent truncation)")
	}

	// 恰好在上限内的文件放行。
	ok := make([]byte, readLocalTextFileLimit)
	for i := range ok {
		ok[i] = byte('a' + i%26)
	}
	okPath := filepath.Join(dir, "ok.log")
	if err := os.WriteFile(okPath, ok, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := app.ReadLocalTextFile(okPath); err != nil || len(got) != readLocalTextFileLimit {
		t.Fatalf("at-limit payload must pass: err=%v len=%d", err, len(got))
	}
}
