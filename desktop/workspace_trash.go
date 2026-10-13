package main

// 任务 855 铁律：文件面板的「删除」（回收站语义）必须把条目送进操作系统
// 回收站/废纸篓（Windows Shell API / macOS trashItemAtURL / Linux gio trash），
// 禁止用 os.RemoveAll、os.Rename 或自建 .trash 目录冒充回收站。彻底删除
// （os.RemoveAll）是独立的显式操作，不在此文件实现。

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
