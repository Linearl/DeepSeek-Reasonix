package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"reasonix/internal/config"
	"reasonix/internal/fileutil"
	fileencoding "reasonix/internal/fileutil/encoding"
)

// 任务 727 — 心跳会话轮换的实验室开关。
//
// 单一事实源纪律：开关直接桥接 %APPDATA%\reasonix\heartbeat-rotation.json
// 的 enabled 字段（task 500 定稿的文件，引擎与脚本共用）——不引入第二开
// 关源（没有 config.toml 镜像键，没有 UserDefaults）。缺文件或缺字段 = 内
// 置同值默认（true，与 task 500 的内置默认一致）；文件损坏 = 回退默认并
// 在状态里带 err 说明，绝不影响调度（引擎读自己的那份，这里只是 UI 面）。

// HeartbeatRotationStatusView is the lab card's read model: the effective
// switch, the bridged file path (so the card can name the source of truth)
// and a read/write error surfaced instead of swallowed.
type HeartbeatRotationStatusView struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
	Err     string `json:"err"`
}

func heartbeatRotationPath() string {
	dir := config.MemoryUserDir()
	if dir == "" {
		dir = "."
	}
	return filepath.Join(dir, "heartbeat-rotation.json")
}

// readHeartbeatRotationEnabled returns the effective enabled flag plus a
// diagnostic error string (empty = healthy read). Missing file/field reads
// as the task-500 built-in default (true); a corrupt file also reads as the
// default but reports why.
func readHeartbeatRotationEnabled() (bool, string) {
	path := heartbeatRotationPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, ""
		}
		return true, fmt.Sprintf("read failed: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(fileencoding.DecodeToUTF8(data), &doc); err != nil {
		return true, fmt.Sprintf("corrupt json (defaults in effect): %v", err)
	}
	enabled, ok := doc["enabled"].(bool)
	if !ok {
		return true, ""
	}
	return enabled, ""
}

// HeartbeatRotationStatus is the settings-card read binding.
func (a *App) HeartbeatRotationStatus() HeartbeatRotationStatusView {
	enabled, readErr := readHeartbeatRotationEnabled()
	return HeartbeatRotationStatusView{Enabled: enabled, Path: heartbeatRotationPath(), Err: readErr}
}

// SetHeartbeatRotationEnabled flips the enabled field in heartbeat-rotation.json
// in place — a read-modify-write that preserves every other field (thresholds,
// manualTaskIds, schemaVersion). A missing file is created with the task-500
// default shape. Takes effect on the engine's next scheduling tick that reads
// the file; the lab card notes this in its hint.
func (a *App) SetHeartbeatRotationEnabled(enabled bool) error {
	path := heartbeatRotationPath()
	doc := map[string]any{"schemaVersion": 1}
	if data, err := os.ReadFile(path); err == nil {
		var existing map[string]any
		if err := json.Unmarshal(fileencoding.DecodeToUTF8(data), &existing); err == nil && existing != nil {
			doc = existing
		}
	}
	doc["enabled"] = enabled
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return fileutil.AtomicWriteFile(path, b, 0o644)
}
