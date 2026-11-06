package main

// tl1441 ③（能力对齐）：群聊附件落盘桌面绑定面。
//
// 口径（任务书第一步探查已定）：channel_send 无附件字段，图片走「先落盘再发
// 路径引用」——群聊是全局实体（消息目录在 SessionCollabMailDir），附件落盘
// 锚点用全局工作区根（globalWorkspaceRoot/.reasonix/attachments），复用会话
// 附件同一存储原语（control.SaveImageDataURLIn），零 schema 扩展；发送时
// 消息体带 @<绝对路径> 引用（composer 提交路径既有组装链，AI 成员按路径
// 读文件，human 成员由 ChannelMessageStream 渲染缩略图）。
//
// 与会话附件链（SavePastedImage/AttachmentDataURL，withActiveWorkspace 锚定
// 活动工作区）的关键差异：群聊 tab 无会话工作区可锚——活动工作区视「当前
// 激活的会话 tab」而定，图片会落进无关项目的 .reasonix/attachments；群聊
// 附件必须锚定群聊自己的家（全局工作区），两链并存互不污染。
//
// scope 安全（tl1421 探针惯例）：写侧文件名由存储原语自生成（无用户可控
// 路径拼接）；读侧只接受解析后落在全局工作区 .reasonix/attachments 内的
// 路径——逃出即拒（cleanAttachmentPath 相对+包含校验原样复用），symlink/
// 尺寸上限由 readAttachmentImage 原样把守。

import (
	"errors"
	"path/filepath"
	"strings"

	"reasonix/internal/control"
)

var (
	errChannelAttachmentEmpty   = errors.New("channel attachment path is empty")
	errChannelAttachmentOutside = errors.New("channel attachment path is outside the global workspace attachments")
)

// SaveChannelAttachment stores one pasted/screenshot image data URL under the
// global workspace .reasonix/attachments and returns the ABSOLUTE path — the
// channel message body references files by absolute path so receivers in any
// workspace can resolve them (the relative form only resolves against its
// owning workspace root, which channel members do not share).
func (a *App) SaveChannelAttachment(dataURL string) (string, error) {
	rel, err := control.SaveImageDataURLIn(globalWorkspaceRoot(), dataURL)
	if err != nil {
		return "", err
	}
	return filepath.Join(globalWorkspaceRoot(), filepath.FromSlash(rel)), nil
}

// ChannelAttachmentDataURL reads a channel attachment image back as a preview
// data URL. tl1421 probe discipline: the path must resolve INSIDE the channel
// attachment root (global workspace .reasonix/attachments); absolute paths are
// canonical (what the send chain embeds), the relative form is accepted as the
// storage primitive's native shape. Escapes are rejected, not widened.
// tl1465: message bodies reference attachments with the @-token grammar, and
// the renderer passes the token verbatim — normalize the leading @ away HERE,
// before any validation, so the containment check sees the bare path. The @
// strip is a prefix trim only; everything after it still has to clear the
// relative/containment/symlink gauntlet, so an "@.." escape gains nothing.
func (a *App) ChannelAttachmentDataURL(path string) (string, error) {
	root := globalWorkspaceRoot()
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errChannelAttachmentEmpty
	}
	trimmed = strings.TrimPrefix(trimmed, "@")
	trimmed = strings.TrimSpace(trimmed)
	if trimmed == "" {
		return "", errChannelAttachmentEmpty
	}
	rel := trimmed
	if filepath.IsAbs(trimmed) {
		absRoot, err := filepath.Abs(root)
		if err != nil {
			return "", err
		}
		absPath, err := filepath.Abs(trimmed)
		if err != nil {
			return "", err
		}
		if absPath != absRoot && !strings.HasPrefix(absPath, absRoot+string(filepath.Separator)) {
			return "", errChannelAttachmentOutside
		}
		rel, err = filepath.Rel(absRoot, absPath)
		if err != nil {
			return "", err
		}
	}
	return control.ImageDataURLIn(root, filepath.ToSlash(rel))
}
