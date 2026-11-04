package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1390 验收：副本链转正收敛（2026-11-04 事故 15:13 形态）。
//
// 事故：copy1 转正登记在队时又从 copy1 fork 出 copy2，旧实现覆写 pending
// 指针 → copy2→copy1 把 recovery 名当 main，真主被弃，双副本并存且各自
// 被写。本件钉住三面：
//  1. 链上再 fork 的新登记指向链条根主路径（不再落 recovery 名）；
//  2. 控制器换道到 copy2 后，结算把 copy2 搬上真主、控制器回真主；
//  3. 转正调用包在账本 quiesce 里（编译期接线由本测试路径走到；句柄
//     占用机制本身由 agent 包 tl1390 事务测试与 turnevent quiesce 测试钉）。
func Test1390ForkOffForkConvergesOntoTrueMain(t *testing.T) {
	dir := t.TempDir()
	ctrlA, _, path := divergedControllers1352(t, dir, "chained.jsonl", event.Discard)
	copy1 := ctrlA.SessionPath()
	if copy1 == path {
		t.Fatalf("fork must isolate first: controller still on the main file")
	}

	// 事故现场：从 copy1 再 fork 出 copy2，控制器换道 copy2。
	copy2 := filepath.Join(dir, "chained-recovery-98fbf8f073c1660d.jsonl")
	sess2 := agent.NewSession("sys")
	sess2.Add(provider.Message{Role: provider.RoleUser, Content: "copy2 content"})
	if err := sess2.Save(copy2); err != nil {
		t.Fatal(err)
	}
	ctrlA.armPendingRecoveryPromotion(copy1, agent.RecoveryBranchInfo{Path: copy2})
	loaded2, err := agent.LoadSession(copy2)
	if err != nil {
		t.Fatal(err)
	}
	ctrlA.Resume(loaded2, copy2)
	if got := ctrlA.SessionPath(); got != copy2 {
		t.Fatalf("controller must live on copy2 after the chain fork, on %s", got)
	}

	// 保存周期结算：真主安静 → copy2 搬上真主（quiesce 包裹下账本句柄不挡）。
	for range 6 {
		if err := ctrlA.SnapshotDurable(); err != nil {
			t.Fatalf("post-chain snapshot: %v", err)
		}
		if ctrlA.SessionPath() == path {
			break
		}
	}
	if got := ctrlA.SessionPath(); got != path {
		t.Fatalf("controller path = %s, want the TRUE main file after chained promotion settled", got)
	}
	// copy2 已整体上调（副本字节=真主内容）。
	if _, err := os.Lstat(copy2); !os.IsNotExist(err) {
		t.Fatalf("copy2 must rename onto the true main, lstat err=%v", err)
	}
	disk, err := agent.LoadSession(path)
	if err != nil {
		t.Fatalf("reload true main: %v", err)
	}
	if !strings.Contains(requestMessagesText(disk.Snapshot()), "copy2 content") {
		t.Fatal("true main must hold the promoted copy2 transcript")
	}
	// copy1 是有意的孤儿副本（fork 前向复制使其字节被 copy2 覆盖，外部清理
	// 通路收敛）；双写已经终止——控制器与账本都活在真主上。剩余副本数不得
	// 超过 {copy1 孤儿} 这一件。
	if n := recoveryCopyCount1352(t, path); n > 1 {
		t.Fatalf("recovery copies left = %d, want at most the copy1 orphan", n)
	}
}
