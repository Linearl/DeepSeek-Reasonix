package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

func effectiveWriteRoots(ctx context.Context, set *sandbox.WritableRootSet, fallback []string) []string {
	if set != nil {
		// 任务 634：full access（yolo，task 257）在写工具的静态边界上放行。
		// 预检层（agent.applyWriteAccess）与控制器门（WritableRootSet.Missing）
		// 都按 unbounded 短路，但本函数此前仍返回基线列表，confine 检查把
		// yolo 会话拦在 workspace 边界内（外部反馈 634 问题①；存量会话基线
		// 未含新增 allow 目录时问题②同源于此）。空列表 = confine 不设边界；
		// 会话数据 guard 在 confineWrite/confinePreview 中照常生效，yolo 不
		// 豁免 Reasonix 自有会话存储（与 boot 注释口径一致）。
		if set.Unbounded() {
			return nil
		}
		return set.Effective(ctx)
	}
	if extra := sandbox.PerCallWriteRoots(ctx); len(extra) > 0 {
		return sandbox.CollapseWriteRoots(append(append([]string{}, fallback...), extra...))
	}
	return fallback
}

func declareParentWriteDirs(workDir string, paths ...string) (tool.WriteAccessDeclaration, error) {
	var dirs []string
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			return tool.WriteAccessDeclaration{}, fmt.Errorf("path is required")
		}
		resolved := resolveIn(workDir, p)
		dir := filepath.Dir(resolved)
		if dir == "" || dir == "." {
			continue
		}
		dirs = append(dirs, dir)
	}
	return tool.WriteAccessDeclaration{Directories: dirs}, nil
}

func declareFilePathWriteAccess(workDir string, args json.RawMessage) (tool.WriteAccessDeclaration, error) {
	var p struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return tool.WriteAccessDeclaration{}, fmt.Errorf("invalid args: %w", err)
	}
	return declareParentWriteDirs(workDir, p.Path)
}

// BindWriteRootSet attaches a live writable-root manager to a built-in writer
// or bash tool so later session grants are visible without replacing the registry.
func BindWriteRootSet(tl tool.Tool, set *sandbox.WritableRootSet) tool.Tool {
	if set == nil {
		return tl
	}
	switch t := tl.(type) {
	case writeFile:
		t.rootSet = set
		return t
	case editFile:
		t.rootSet = set
		return t
	case screenshotTool:
		t.rootSet = set
		return t
	case multiEdit:
		t.rootSet = set
		return t
	case moveFile:
		t.rootSet = set
		return t
	case notebookEdit:
		t.rootSet = set
		return t
	case deleteRange:
		t.rootSet = set
		return t
	case deleteSymbol:
		t.rootSet = set
		return t
	case bash:
		t.rootSet = set
		return t
	default:
		return tl
	}
}
