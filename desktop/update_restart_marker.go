package main

// 任务461-P2: 「更新重启标记」— 区分「更新触发的重启」与「正常重启」。
//
// 根因（任务461 P2，用户 2026-10-03 裁决）：auto-resume（task 450/254/49-A2
// 家族）对任何启动都恢复「运行中」会话，未检查启动原因——用户手动重启后，此
// 前卡在工具调用里的会话被自动唤醒，再次调用同一工具、二次阻塞。而设置页文案
// 「自主更新重启后自动恢复」承诺的语义本就只限更新重启。
//
// 修法（裁决①）：更新驱动的重启路径在提交换版后写入本标记（正常关闭、手动
// 重启、手动打开都不写）；下一次启动在恢复点消费标记——有标记才允许自动恢复，
// 无标记则本轮整体禁用，并把上一次重启登记的未触发名册清空（带日志点名，遵守
// 1545 反静默丢失原则），使手动重启登记的会话永远不会在日后某次无关的更新重启
// 里延迟复活（与 task 254/263「不得在无关重启里复活」同一原则）。
//
// 标记是一次性凭据：启动时读后即删。写入点（本包内四处，均为提交成功之后、
// 拉起 launcher 之前）：
//   - restart_update.go restartAndUpdateExempt（发布 staging 构建，reason=publish）
//   - version_switch.go switchToVersionExempt（回退已装版本，reason=switch）
//   - updater_app.go installDebUpdate / installPortableUpdate（官方更新通道，reason=updater）
//   - restart_update.go restartActiveVersionExempt（restart_update 工具的纯重启
//     action，任务 520：不换版本但承诺「重启并继续」，reason=restart）。普通重启
//     RestartDesktop 仍不写——手动重启不得打开自动恢复门禁。
//
// 状态判据（验收）：update-restart-marker.json 存在 = 上一次退出是更新重启；
// 不存在 = 普通启动。消费后文件必删，标记不跨两次启动生效。

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"reasonix/internal/config"
)

// updateRestartMarkerName is the marker file's name under the user state dir.
const updateRestartMarkerName = "update-restart-marker.json"

// restartResumeDroppedMarker is the greppable token for roster entries dropped
// because their staging restart was NOT an update restart (the 未入册/未续跑
// family convention: every silently-lost resume must be named on the log).
const restartResumeDroppedMarker = "非更新不续"

// updateRestartMarker is the on-disk shape. Reason records which update path
// wrote it (publish | switch | updater); version is the version label the
// relaunch was switching to when known.
type updateRestartMarker struct {
	At      int64  `json:"at"`
	PID     int    `json:"pid"`
	Reason  string `json:"reason,omitempty"`
	Version string `json:"version,omitempty"`
}

func updateRestartMarkerPath() string {
	return filepath.Join(config.MemoryUserDir(), updateRestartMarkerName)
}

// writeUpdateRestartMarker records that this process is handing off to a
// relaunch that continues an update. Written only by the update-driven restart
// paths after their swap is committed — a failure before that point must never
// leave a marker that would auto-resume sessions on an unrelated later launch.
func writeUpdateRestartMarker(reason, version string) {
	marker := updateRestartMarker{At: time.Now().Unix(), PID: os.Getpid(), Reason: reason, Version: version}
	data, err := json.Marshal(marker)
	if err == nil {
		if mkErr := os.MkdirAll(filepath.Dir(updateRestartMarkerPath()), 0o755); mkErr != nil {
			err = mkErr
		} else {
			tmp := updateRestartMarkerPath() + ".tmp"
			if writeErr := os.WriteFile(tmp, data, 0o644); writeErr != nil {
				err = writeErr
			} else if renameErr := os.Rename(tmp, updateRestartMarkerPath()); renameErr != nil {
				err = renameErr
			}
		}
	}
	if err != nil {
		// A missing marker is the SAFE failure direction: the next launch just
		// declines auto-resume (manual review), it never resumes blindly.
		slog.Error("restart: 更新重启标记写入失败（下一次启动将不自动恢复会话）", "reason", reason, "err", err)
		return
	}
	slog.Info("restart: 更新重启标记已写入（下一次启动按更新重启恢复）", "reason", reason, "version", version)
}

// updateRestartGate caches the once-per-process consume decision: tabs restore
// runs per tab, but the marker must be read (and deleted) exactly once.
var (
	updateRestartGateOnce sync.Once
	updateRestartGateOpen bool
)

// updateRestartResumeAllowed reports whether this launch continues an
// update-driven relaunch. First call consumes the marker; the verdict —
// including the roster drop on a non-update launch — is computed once and
// reused for every tab restored afterwards.
func updateRestartResumeAllowed() bool {
	updateRestartGateOnce.Do(func() { consumeUpdateRestartGate() })
	return updateRestartGateOpen
}

// resetUpdateRestartGateForTest re-arms the once gate so a test can exercise
// both verdicts in one process.
func resetUpdateRestartGateForTest() {
	updateRestartGateOnce = sync.Once{}
	updateRestartGateOpen = false
}

// consumeUpdateRestartGate reads and deletes the marker. No marker (the
// common manual-launch case) closes the gate AND drops the unfired resume
// roster: entries staged by a manual restart's grace window must not resurface
// on some much later update restart — a deferred surprise resume, not a repair.
func consumeUpdateRestartGate() {
	data, err := os.ReadFile(updateRestartMarkerPath())
	if err != nil {
		updateRestartGateOpen = false
		dropUnfiredAutonomousUpdateRoster("非更新启动")
		slog.Info("restart: 无更新重启标记；本轮禁用自动恢复（手动重启/正常打开不唤醒会话）")
		return
	}
	_ = os.Remove(updateRestartMarkerPath())
	var marker updateRestartMarker
	_ = json.Unmarshal(data, &marker)
	updateRestartGateOpen = true
	slog.Info("restart: 更新重启标记命中；本轮按 450 语义自动恢复", "reason", marker.Reason, "version", marker.Version, "age_s", time.Now().Unix()-marker.At)
}

// dropUnfiredAutonomousUpdateRoster clears the task-254 roster on a launch
// that is not an update relaunch. Every dropped session is named on the log
// (1545: a resume that will not happen must never be silent).
func dropUnfiredAutonomousUpdateRoster(cause string) {
	state := readAutonomousUpdateResumeFile()
	if len(state.Sessions) == 0 {
		return
	}
	for _, entry := range state.Sessions {
		slog.Warn("restart: 非更新启动，弃置未触发的自动恢复登记（"+restartResumeDroppedMarker+"；如需继续请手动打开该会话）", "session", entry.Path, "stagedAt", entry.StagedAt, "cause", cause)
	}
	if err := writeAutonomousUpdateResumeFile(autonomousUpdateResumeFile{Sessions: []autonomousUpdateResumeEntry{}}); err != nil {
		slog.Error("restart: 弃置自动恢复登记失败", "cause", cause, "err", err)
	}
}
