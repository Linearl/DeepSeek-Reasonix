package main

// tl1565（G3 打开能力）：附件卡片右键「复制内容」的读侧桥。opener 家族
// （OpenLocalPath / ResolveOpenablePathForTab，任务 944 门禁）的读侧延伸：
// 同一道 normalizeLocalOpenPath + openTargetPathAllowed 门禁（拒绝可执行
// 目标与 symlink 落点可执行），在此之上只多一个「文本读回」——用户显式
// 点选的附件，内容进的是用户自己的剪贴板，与用资源管理器打开文件复制同权。
//
// 上限与 workspace ReadFileForTab 的 filePreviewLimit（2 MiB）同款：超限
// 诚实报错，不静默截断（截断文本进剪贴板=用户拿到不完整内容还不知道）。
// 二进制探测：起始 8 KiB 内出现 NUL 字节即拒绝——PNG/PDF/DOCX 等按此全部
// 拦下（它们的头几 KB 必含 NUL），纯文本（UTF-8/GBK 等）不含 NUL 放行，
// 编码转码不做（剪贴板语义是「原样文本」，与工作区面板预览同口径）。

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

const readLocalTextFileLimit = 2 * 1024 * 1024 // 2 MiB — same bound as workspace file previews

// ReadLocalTextFile returns the text content of one local file for the
// attachment card's copy-content action. The gate mirrors OpenLocalPath
// (normalize + executable-target refusal); directories and binary payloads
// are refused with explicit errors — the frontend surfaces them as toasts,
// never silently.
func (a *App) ReadLocalTextFile(path string) (string, error) {
	path, err := normalizeLocalOpenPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("refusing to read directory %q as text", path)
	}
	if !openTargetPathAllowed(path, info) {
		return "", fmt.Errorf("refusing to read executable target %q", path)
	}
	if info.Size() > readLocalTextFileLimit {
		return "", fmt.Errorf("file exceeds the %d MiB text-copy limit", readLocalTextFileLimit/(1024*1024))
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, readLocalTextFileLimit+1))
	if err != nil {
		return "", err
	}
	if len(data) > readLocalTextFileLimit {
		return "", fmt.Errorf("file exceeds the %d MiB text-copy limit", readLocalTextFileLimit/(1024*1024))
	}
	// Binary sniff: NUL in the first 8 KiB. Keep the bound small so a large
	// text file whose NUL (if any) sits deeper is not misjudged on the prefix.
	sniff := data
	if len(sniff) > 8*1024 {
		sniff = sniff[:8*1024]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "", fmt.Errorf("file %q is not text content", path)
	}
	return string(data), nil
}
