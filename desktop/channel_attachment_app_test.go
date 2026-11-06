package main

// tl1441 ③ Go 侧契约测试：群聊附件落盘（全局工作区锚点）+ 读侧路径逃出拒绝
// + 改写草稿借道（无可用控制器=静默空串）。
//
// 口径（任务书第一步探查）：channel_send 无附件字段——图片先落盘（全局工作
// 区 .reasonix/attachments，群聊自己的家，不落无关项目的活动工作区）再以
// @<绝对路径> 进消息体；读侧只认解析后落在该目录内的路径（tl1421 探针惯例）。

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newChannelAttachmentTestApp(t *testing.T) *App {
	t.Helper()
	// agent_import_app_test.go 同款：单测环境手工装 App（ctx 就绪，零标签页
	// ——恰好覆盖「无就绪控制器」的借道降级面）。
	return &App{ctx: context.Background()}
}

func TestSaveChannelAttachmentLandsInGlobalWorkspace(t *testing.T) {
	app := newChannelAttachmentTestApp(t)
	root := globalWorkspaceRoot()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(root, ".reasonix")) })

	// 1x1 PNG（最小合法图片字节，detectedImageMime 可识别）。
	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	abs, err := app.SaveChannelAttachment(png)
	if err != nil {
		t.Fatalf("SaveChannelAttachment: %v", err)
	}
	if !filepath.IsAbs(abs) {
		t.Fatalf("channel attachment path must be absolute (receivers in any workspace must resolve it), got %q", abs)
	}
	attachmentsRoot := filepath.Join(root, ".reasonix", "attachments")
	if !strings.HasPrefix(abs, attachmentsRoot+string(filepath.Separator)) {
		t.Fatalf("attachment landed outside the global attachments root: %q", abs)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("stored attachment stat: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("stored attachment is empty")
	}
}

func TestChannelAttachmentDataURLRoundTripAndEscape(t *testing.T) {
	app := newChannelAttachmentTestApp(t)
	root := globalWorkspaceRoot()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(root, ".reasonix")) })

	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	abs, err := app.SaveChannelAttachment(png)
	if err != nil {
		t.Fatalf("SaveChannelAttachment: %v", err)
	}
	dataURL, err := app.ChannelAttachmentDataURL(abs)
	if err != nil {
		t.Fatalf("ChannelAttachmentDataURL(abs): %v", err)
	}
	if !strings.HasPrefix(dataURL, "data:image/") {
		t.Fatalf("preview round-trip must return an image data URL, got prefix %q", dataURL[:min(24, len(dataURL))])
	}

	// tl1421 探针惯例：逃出全局附件目录的路径一律拒绝（含其他工作区附件、
	// 任意绝对路径、空串）。
	outside := filepath.Join(root, "secret.png")
	if _, err := app.ChannelAttachmentDataURL(outside); err == nil {
		t.Fatal("a path outside the channel attachments root must be rejected")
	}
	if _, err := app.ChannelAttachmentDataURL(""); err == nil {
		t.Fatal("an empty path must be rejected")
	}
	if _, err := app.ChannelAttachmentDataURL(filepath.Join(filepath.Dir(root), "elsewhere", ".reasonix", "attachments", "x.png")); err == nil {
		t.Fatal("a sibling-workspace attachment path must be rejected")
	}
}

// tl1465：@ 前缀规范化——消息体以 @-token 语法引用附件（@C:\… 或
// @.reasonix/attachments/…），渲染端历史上有原样透传的形态；读桥必须把
// 前导 @ 剥掉再校验，否则文件明明在目录内被判「outside」。规范化只剥前缀，
// 剥完仍要过 相对/包含/symlink 全套闸门，逃逸增益为零。
func TestChannelAttachmentDataURLNormalizesAtPrefix(t *testing.T) {
	app := newChannelAttachmentTestApp(t)
	root := globalWorkspaceRoot()
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(root, ".reasonix")) })

	png := "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	abs, err := app.SaveChannelAttachment(png)
	if err != nil {
		t.Fatalf("SaveChannelAttachment: %v", err)
	}

	// 带前导 @ 的绝对路径（用户实测事故形态）必须读回成功。
	dataURL, err := app.ChannelAttachmentDataURL("@" + abs)
	if err != nil {
		t.Fatalf("ChannelAttachmentDataURL(@abs): %v", err)
	}
	if !strings.HasPrefix(dataURL, "data:image/") {
		t.Fatal("@-prefixed absolute path must round-trip as an image data URL")
	}
	// @ + 相对形态（存储原语原生形状的 @ 引用）同样放行。
	if _, err := app.ChannelAttachmentDataURL("@"+filepath.ToSlash(filepath.Join(".reasonix", "attachments", filepath.Base(abs)))); err != nil {
		t.Fatalf("ChannelAttachmentDataURL(@relative): %v", err)
	}
	// @ 剥掉后仍是逃逸路径 → 照拒：目录外绝对路径、.. 穿越、兄弟工作区、
	// 其他盘符、裸 @、@..。
	if _, err := app.ChannelAttachmentDataURL("@" + filepath.Join(root, "secret.png")); err == nil {
		t.Fatal("@-prefixed path outside the attachments root must stay rejected")
	}
	if _, err := app.ChannelAttachmentDataURL("@.reasonix/attachments/../../secret.png"); err == nil {
		t.Fatal("@-prefixed .. traversal must stay rejected")
	}
	if _, err := app.ChannelAttachmentDataURL("@" + filepath.Join(filepath.Dir(root), "elsewhere", ".reasonix", "attachments", "x.png")); err == nil {
		t.Fatal("@-prefixed sibling-workspace path must stay rejected")
	}
	if _, err := app.ChannelAttachmentDataURL("@Q:\\elsewhere\\.reasonix\\attachments\\x.png"); err == nil {
		t.Fatal("@-prefixed other-drive path must stay rejected")
	}
	if _, err := app.ChannelAttachmentDataURL("@"); err == nil {
		t.Fatal("a bare @ must be rejected")
	}
	if _, err := app.ChannelAttachmentDataURL("@   "); err == nil {
		t.Fatal("an @-only whitespace token must be rejected")
	}
}

func TestRewriteDraftForChannelEmptyAndBorrowEmpty(t *testing.T) {
	app := newChannelAttachmentTestApp(t)
	// 空草稿=快速空串（契约：静默降级为「不改写」）。
	if got := app.RewriteDraftForChannel("  ", "", 0); got != "" {
		t.Fatalf("empty draft must return empty, got %q", got)
	}
	// 无任何就绪会话控制器（单测环境典型态）=空串，不 panic 不等待。
	if got := app.RewriteDraftForChannel("帮我改写这句话", "", 0); got != "" {
		t.Fatalf("no ready controller must degrade to empty, got %q", got)
	}
}
