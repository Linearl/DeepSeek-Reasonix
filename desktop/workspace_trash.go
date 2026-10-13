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
