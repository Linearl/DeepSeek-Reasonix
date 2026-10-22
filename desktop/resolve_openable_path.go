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
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if !filepath.IsAbs(path) {
		root, _, ok := a.workspaceTargetForTab(tabID)
		if !ok || strings.TrimSpace(root) == "" {
			return "", nil
		}
		base, err := workspaceBaseFromRoot(root)
		if err != nil {
			return "", nil
		}
		joined, ok, err := workspacePathForBase(base, path)
		if err != nil || !ok {
			return "", nil
		}
		path = joined
	}
	normalized, err := normalizeLocalOpenPath(path)
	if err != nil {
		return "", nil
	}
	info, err := os.Stat(normalized)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !openTargetPathAllowed(normalized, info) {
		return "", nil
	}
	return normalized, nil
}
