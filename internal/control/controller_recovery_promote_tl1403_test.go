package control

import (
	"sync"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
)

// tl1403 验收②：延迟转正落地后必须通知宿主（OnSessionPromoted）——
// 桌面 tab 的租约/路径/前端身份是在 fork 时挂到副本上的（OnSessionRecovered），
// 转正把控制器搬回主文件而宿主不知情 = 显示面/写入面第二次分家
// （2026-11-05 14:10 现场：controller save 写 215a5c，前端 surface 挂 b26cee）。
func Test1403PromotionNotifiesHostSurface(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var notifications []SessionPromotionInfo
	ctrlA, _, path := divergedControllers1352(t, dir, "notify.jsonl", event.Discard)
	ctrlA.SetOnSessionPromoted(func(info SessionPromotionInfo) error {
		mu.Lock()
		notifications = append(notifications, info)
		mu.Unlock()
		return nil
	})
	copyPath := ctrlA.SessionPath()
	if copyPath == path {
		t.Fatalf("fork must isolate first: controller still on the main file")
	}

	// 保存周期结算：主文件安静 → 副本转正回主文件。
	for range 6 {
		if err := ctrlA.SnapshotDurable(); err != nil {
			t.Fatalf("snapshot durable: %v", err)
		}
		if ctrlA.SessionPath() == path {
			break
		}
	}
	if got := ctrlA.SessionPath(); got != path {
		t.Fatalf("controller path = %s, want the main file after promotion settled", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notifications) != 1 {
		t.Fatalf("promotion notifications = %d (%+v), want exactly 1", len(notifications), notifications)
	}
	if notifications[0].CopyPath != copyPath {
		t.Fatalf("notification copy path = %q, want %q", notifications[0].CopyPath, copyPath)
	}
	if notifications[0].MainPath != path {
		t.Fatalf("notification main path = %q, want %q", notifications[0].MainPath, path)
	}
}

// tl1403 验收②反面：宿主回调失败不得回滚转正（字节已搬，回滚只会制造第二个
// 分叉现场），只留日志痕迹。
func Test1403PromotionSurvivesHostNotificationFailure(t *testing.T) {
	dir := t.TempDir()
	ctrlA, _, path := divergedControllers1352(t, dir, "failnotice.jsonl", event.Discard)
	ctrlA.SetOnSessionPromoted(func(SessionPromotionInfo) error {
		return agent.ErrSessionRecoveryNotNeeded // 任意非 nil 错误
	})
	for range 6 {
		if err := ctrlA.SnapshotDurable(); err != nil {
			t.Fatalf("snapshot durable: %v", err)
		}
		if ctrlA.SessionPath() == path {
			break
		}
	}
	if got := ctrlA.SessionPath(); got != path {
		t.Fatalf("controller path = %s, want the main file even though the host handler failed", got)
	}
}
