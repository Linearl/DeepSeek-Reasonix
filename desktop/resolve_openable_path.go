package main

import (
	"os"
	"path/filepath"
	"strings"
)

// 任务 1060：会话转录选中路径文字菜单（用默认应用打开 / 打开所在目录）的
// 后端存在性校验探针。前端先做形态识别（盘符/UNC/开头斜杠/含分隔符），
// 这里再做权威判定；返回非空即菜单可以显示两项，空串即必须隐藏。
//
// 判定与打开链路（opener 家族，任务 944 门禁）完全一致：绝对路径走
// normalizeLocalOpenPath + openTargetPathAllowed，探针为真 ⟺ OpenLocalPath
// 能开（点击必有响应，不给死按钮）。相对路径用右侧边栏同款解析器
// （workspacePathForBase）按会话所属项目根解析：无项目根的会话（Global）
// 一律拒绝——绝不回落到 desktop 进程工作目录（那是误报源头）；
// 逃出项目根的相对路径同样拒绝，与右侧边栏权限面一致。
//
// 形态不合格/不存在/不可开一律返回 ("", nil)；error 只留给意外的 stat
// 失败（前端把一切假值都当作「不显示」，语义安全）。
func (a *App) ResolveOpenablePathForTab(tabID, path string) (string, error) {
	normalized, info, err := a.resolveExistingLocalPathForTab(tabID, path)
	if err != nil || normalized == "" {
		return "", err
	}
	if !openTargetPathAllowed(normalized, info) {
		return "", nil
	}
	return normalized, nil
}

// tl1641：产物栏（右栏·产物/参考 dock 行菜单）「在文件管理器中显示」「在内置
// 终端打开」两项的后端探针。与 ResolveOpenablePathForTab 共用同一个解析+
// 存在性判定面（resolveExistingLocalPathForTab），但不套 opener 门禁
// （openTargetPathAllowed / executableOpenSuffixes）：reveal 是 explorer
// /select 定位、终端是 cd 到所在目录，两者都不执行目标文件，可执行扩展名
// 黑名单对它们没有安全意义——.ps1/.bat 等「不可用默认应用打开」的类型同样
// 能被定位/被 cd（用户 tl1641 现场：.ps1 右键三项齐缺）。存在性 stat 保留：
// 文件已被移走时两项仍是死按钮，照旧隐藏。
func (a *App) ResolveRevealablePathForTab(tabID, path string) (string, error) {
	normalized, _, err := a.resolveExistingLocalPathForTab(tabID, path)
	return normalized, err
}

// resolveExistingLocalPathForTab is the shared resolution half of the two
// probes: trim, workspace-relative resolution against the tab's project root
// (Global tabs refuse relative paths — never the desktop cwd), localopen
// syntax normalization, and an existence stat. It returns the normalized
// absolute path plus the stat info so the opener probe can run its gate
// without a second stat. ("", nil) means "hide"; error is reserved for
// unexpected stat failures.
func (a *App) resolveExistingLocalPathForTab(tabID, path string) (string, os.FileInfo, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil, nil
	}
	if !filepath.IsAbs(path) {
		root, _, ok := a.workspaceTargetForTab(tabID)
		if !ok || strings.TrimSpace(root) == "" {
			return "", nil, nil
		}
		base, err := workspaceBaseFromRoot(root)
		if err != nil {
			return "", nil, nil
		}
		joined, ok, err := workspacePathForBase(base, path)
		if err != nil || !ok {
			return "", nil, nil
		}
		path = joined
	}
	normalized, err := normalizeLocalOpenPath(path)
	if err != nil {
		return "", nil, nil
	}
	info, err := os.Stat(normalized)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, err
	}
	return normalized, info, nil
}
