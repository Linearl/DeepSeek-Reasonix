package main

// tl 1211 structural guards for the removed tl-547 autopilot auto-guard:
// acceptance 9 (heartbeat task ids are server-generated only) and
// acceptance 10 (the legacy autoguard- sweep is precise and idempotent).

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/tool/builtin"
)

// TestHeartbeatLegacyAutoguardSweepRemovesOnlyPrefixedRows (acceptance 10):
// a legacy table containing autoguard- rows loses exactly those rows, keeps
// every other row field-for-field (including a hand-created guard-* task and
// a plain random-id task), and a repeated sweep produces no further write.
func TestHeartbeatLegacyAutoguardSweepRemovesOnlyPrefixedRows(t *testing.T) {
	isolateDesktopUserDirs(t)
	engine := newHeartbeatEngine(nil)
	handmade := HeartbeatTask{ID: "guard-fork3-x", Title: "fork开发-新10 自检", Prompt: "巡检", Interval: "20m", Enabled: true, CreatedAt: 1700000000000}
	plain := HeartbeatTask{ID: "un9tv9v13td5", Title: "普通心跳", Prompt: "p", Interval: "1h", Enabled: false, CreatedAt: 1700000000001}
	legacyEnabled := HeartbeatTask{ID: "autoguard-legacy-x", Title: "Autopilot guard: topic_old", Prompt: "旧守护", Interval: "30m", Enabled: true, TopicID: "topic_old", CreatedAt: 1700000000002}
	legacyDisabled := HeartbeatTask{ID: "autoguard-legacy-y", Title: "old disabled", Prompt: "p", Interval: "30m", Enabled: false, CreatedAt: 1700000000003}
	if err := engine.saveTasks([]HeartbeatTask{handmade, plain, legacyEnabled, legacyDisabled}); err != nil {
		t.Fatal(err)
	}

	engine.sweepLegacyAutopilotGuardTasks()
	if !engine.legacyGuardSwept {
		t.Fatal("a completed sweep must set legacyGuardSwept")
	}
	var after heartbeatConfig
	b, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Tasks) != 2 {
		t.Fatalf("sweep must keep exactly the 2 non-autoguard rows, got %d: %+v", len(after.Tasks), after.Tasks)
	}
	// 逐字段不变：剩余行与输入行完全相等。
	if !reflect.DeepEqual(after.Tasks[0], handmade) || !reflect.DeepEqual(after.Tasks[1], plain) {
		t.Fatalf("surviving rows must be field-identical, got %+v", after.Tasks)
	}

	// 幂等：模拟重启后再扫一次（标志复位），文件字节不变。
	engine.legacyGuardSwept = false
	snapshot1, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatal(err)
	}
	engine.sweepLegacyAutopilotGuardTasks()
	snapshot2, err := os.ReadFile(engine.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(snapshot1) != string(snapshot2) {
		t.Fatal("a second sweep on a clean table must not rewrite the file")
	}
}

// TestHeartbeatTaskIDIsServerGenerated (acceptance 9): no upsert submit path
// accepts a caller-chosen id. Creation mints its own server-side id, and an
// update naming an id that is not in the table — legacy autoguard- ids
// included — is refused instead of being silently created.
func TestHeartbeatTaskIDIsServerGenerated(t *testing.T) {
	a, engine := newTestAdapter(t)
	view := mustList(t, a)
	res, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
		Patch: builtin.HeartbeatTaskPatch{
			Provided: map[string]bool{"title": true, "prompt": true, "interval": true},
			Title:    "t", Prompt: "p", Interval: "30m",
		},
		ExpectedRevision: view.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 服务端生成的 id 形态即 generateHeartbeatTaskID 的契约：12 位小写字母
	// 数字，不带任何保留前缀——调用方无法通过请求字段控制它。
	if len(res.TaskID) != 12 || strings.ContainsAny(res.TaskID, "-_") {
		t.Fatalf("created id %q does not match the server generator contract", res.TaskID)
	}
	if res.TaskID == "autoguard-x" || strings.HasPrefix(res.TaskID, "autoguard-") {
		t.Fatalf("created id %q must never carry a reserved prefix", res.TaskID)
	}

	current := mustList(t, a)
	for _, picked := range []string{"autoguard-evil", "guard-evil", "my-custom-id"} {
		_, err := a.UpsertTask(builtin.HeartbeatUpsertRequest{
			Patch: builtin.HeartbeatTaskPatch{
				Provided: map[string]bool{"id": true, "title": true, "prompt": true, "interval": true},
				ID:       picked, Title: "t", Prompt: "p", Interval: "30m",
			},
			ExpectedRevision: current.Revision,
		})
		if err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("hand-picked id %q must be refused (no create via update), got %v", picked, err)
		}
	}
	fresh := mustList(t, a)
	if len(fresh.Tasks) != 1 || fresh.Tasks[0].ID != res.TaskID {
		t.Fatalf("refused creates must leave exactly the server-generated row, got %+v", fresh.Tasks)
	}
	_ = engine // engine kept for parity with newTestAdapter's other users
}
