package main

// 任务 855 铁律：「删除」（回收站语义）必须经平台 API 送进系统回收站/废纸篓，
// 禁止 os.RemoveAll/os.Rename 或自建 .trash 冒充；彻底删除是独立显式操作。

import "errors"

// workspaceTrashPlatform 由平台文件提供（_windows/_darwin/_linux）。
// 测试通过替换该变量钉死「删除必须经过平台回收站 API」这条路径。
var workspaceTrashPlatform = trashPlatformPaths

// trashWorkspacePaths sends every path to the OS trash. Partial failures are
// aggregated; the caller reports per-path outcomes.
func trashWorkspacePaths(paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if err := workspaceTrashPlatform(paths); err != nil {
		return err
	}
	return nil
}

var errTrashUnsupported = errors.New("move to trash is not supported on this platform")

// errTrashLongPath 是「树内存在超长路径、回收站收不下（收下=只能永久删除）」
// 的稳定哨兵（tl1658）：平台预检拒绝长路径时必须 %w 包装它，调用方靠
// errors.Is 识别这一类失败并给出出路（短路径中转后入桶 / 明示永久删除），
// 而不是把它当成一次普通的 trash 失败——tl1658 现场②正是这条出路缺失，
// 保守拒绝变成了单删/批量两条路全堵的死锁。
var errTrashLongPath = errors.New("items exceed the recycle bin's maximum path length")
