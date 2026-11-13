package agent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tl1561: 大工具返回被截断时，把完整原件落盘到会话私有临时目录，恢复提示改为
// 「已落 <绝对路径>，用 read_file 读」。这条通道替代旧的 result_ref 引导——ref
// 是对原件的内容哈希（toolResultRef），RawContent 在 CI 摘要（summarizeCIOutput
// 换原件）、tl1172 会话维护裁剪（>64KiB 头尾替换）、legacy 无 RawContent 记录三
// 条路径上都会漂移，跨调用内容哈希 ref 结构性不可靠（竞品对照结论①：六仓库均
// 不做跨调用 ref，落盘即正解）。
//
// 落盘位置选 sessiontemp generation 目录的 tool-output-archive/ 子目录：
//   - 目录生命周期现成——/new、/clear、切分支、换会话恢复时 Rotate 删除整代
//     （deleteGenerationLocked → safeRemoveDir），崩溃残留由 24h 陈旧清扫兜底，
//     不需要本件再建清理任务；
//   - 已是 WriteScopeScratch 且 forbidReadRoots 默认不覆盖 OS 临时目录，
//     read_file 按 marker 里的绝对路径可直接读回；
//   - 文件名 trunc-<unixnano>-<tool>-<callID>.txt：时间戳保唯一（写入走
//     O_EXCL，撞名重试），工具名与 call ID 经消毒便于人工辨认与按前缀清理。
const toolOutputArchiveDirName = "tool-output-archive"

// archiveTruncatedToolOutput writes the full original of a truncated tool
// result into the session private temporary directory and returns its absolute
// path. Best effort: any failure logs and returns "" so the truncation marker
// falls back to the re-run guidance instead of promising an unreadable path.
func (a *Agent) archiveTruncatedToolOutput(toolName, toolCallID, original string) string {
	if a == nil || a.svc.sessionTemp == nil || original == "" {
		return ""
	}
	lease, err := a.svc.sessionTemp.Acquire()
	if err != nil {
		slog.Debug("agent: tool output archive unavailable (tl1561)", "err", err)
		return ""
	}
	defer lease.Release()
	dir := filepath.Join(lease.Dir(), toolOutputArchiveDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Debug("agent: tool output archive mkdir failed (tl1561)", "dir", dir, "err", err)
		return ""
	}
	base := fmt.Sprintf("trunc-%d-%s-%s.txt",
		time.Now().UnixNano(),
		sanitizeArchiveToken(toolName, 64),
		sanitizeArchiveToken(toolCallID, 96))
	path := filepath.Join(dir, base)
	// O_EXCL keeps concurrent/rapid-fire truncations from silently overwriting
	// each other on coarse-clock filesystems; a few suffix retries absorb the
	// same-nanosecond collision without unbounded growth.
	for attempt := range 4 {
		tryPath := path
		if attempt > 0 {
			tryPath = filepath.Join(dir, fmt.Sprintf("%s-%d.txt", strings.TrimSuffix(base, ".txt"), attempt))
		}
		f, err := os.OpenFile(tryPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			slog.Debug("agent: tool output archive write failed (tl1561)", "path", tryPath, "err", err)
			return ""
		}
		_, writeErr := f.WriteString(original)
		closeErr := f.Close()
		if writeErr != nil || closeErr != nil {
			slog.Debug("agent: tool output archive write failed (tl1561)", "path", tryPath, "err", writeErr, "close", closeErr)
			_ = os.Remove(tryPath)
			return ""
		}
		return tryPath
	}
	slog.Debug("agent: tool output archive name collision exhausted (tl1561)", "base", base)
	return ""
}

// sanitizeArchiveToken keeps [A-Za-z0-9._-] and bounds the token so the
// archived filename stays a single safe path segment regardless of tool name
// or call ID content.
func sanitizeArchiveToken(value string, limit int) string {
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= limit {
			break
		}
	}
	if b.Len() == 0 {
		return "unknown"
	}
	return b.String()
}
