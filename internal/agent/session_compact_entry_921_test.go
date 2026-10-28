package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 921 验收①（死会话形态端到端，对应现场 20260922：104MB events /
// 40MB live）：日志滞留大量不可达行（并发头已 retire、旋转从未发生——
// maintainDAGLog 只在保存时判定，死会话等不到下一次保存）。空闲期兜底
// sweep 的生产动作 agent.CompactSessionFile 强制维护重写，把日志压回 live
// 水位（cap 内），且转写逐字保留。
//
// 这里的 retire 是夹具构造（模拟「用户早已关掉并发分支、字节还躺在日志里」
// 的现场既成事实），产品行为只是清理已不可达的字节——rotateSessionDAG
// 从建仓起就丢弃不可达节点，本件只是让死会话也能走到这个入口。
func TestCompactSessionFileClearsUnreachableRowsUnderCap(t *testing.T) {
	t.Skip("tl1227 DAG 读退役: compact 维护链仍在（probe.dag 门后），读回断言依赖已删读面；存量 DAG 日志由 MigrateSessionDAGLog 一次性迁移为 schema-1，读侧单路径化后本场景不再可达")
	path := dagTestSession(t)
	_, base := dagLinearLog(t, path)
	// main 链 4 条小消息（live 水位），随后一条 6MB 大消息撑大 live。
	big := dagMsg(provider.RoleUser, strings.Repeat("x", 6<<20), "BIG1")
	dagAppend(t, path, dagMessageEntry(t, SessionMainHead, "U2", "t1", big, base.Add(time.Minute)))

	// 并发头 + 独占大尾巴（3MB×2）：真实现场「陈旧并发头」的字节形态。
	forkAt := base.Add(2 * time.Minute)
	dagAppend(t, path, sessionDAGEntry{
		Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "hdead", From: big.ID,
		Kind: HeadKindConcurrent, Writer: "w-dead", At: forkAt,
	})
	t1 := dagMsg(provider.RoleAssistant, strings.Repeat("y", 3<<20), "D1")
	t2 := dagMsg(provider.RoleUser, strings.Repeat("z", 3<<20), "D2")
	dagAppend(t, path,
		dagMessageEntry(t, "hdead", big.ID, "t1", t1, forkAt.Add(time.Second)),
		dagMessageEntry(t, "hdead", t1.ID, "t1", t2, forkAt.Add(2*time.Second)),
	)

	// 用户切回 main 再关掉该分支（现场既成事实）：retire 之后独占链不可达，
	// 但字节仍留在日志里——旋转只在保存路径判定，死会话永远等不到。
	dagAppend(t, path, sessionDAGEntry{
		Type: sessionDAGTypeSelect, Head: SessionMainHead, Reason: "user", At: forkAt.Add(3 * time.Second),
	})
	if err := RetireSessionHead(path, "hdead"); err != nil {
		t.Fatalf("retire: %v", err)
	}
	before, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if before.Size() < 9<<20 {
		t.Fatalf("夹具前提不成立：日志 %d 字节应含 6MB live + 6MB 不可达尾巴", before.Size())
	}

	// 兜底 sweep 的生产动作：lease 空闲证明 + SaveRewriteCompact 强制 rotate。
	freedBefore, after, err := CompactSessionFile(path)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if freedBefore != before.Size() {
		t.Fatalf("entry before=%d, want the stat'd log size %d", freedBefore, before.Size())
	}
	info, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	// 重写后只剩 live 链（sys + q1 + a1 + q2 + BIG1 ≈ 6MB），不可达尾巴被清。
	if info.Size() >= before.Size()-5<<20 {
		t.Fatalf("不可达尾巴未被清掉：%d → %d", before.Size(), info.Size())
	}
	if after != info.Size() {
		t.Fatalf("entry after=%d, want the rewritten log size %d", after, info.Size())
	}
	raw, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), strings.Repeat("y", 32)) {
		t.Fatal("retired 头的独占字节必须被物理清除")
	}

	// 数据完整性（884 验收口径）：可见消息数不变、末 N 条逐字一致。
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded.Messages) != 5 {
		t.Fatalf("visible messages = %d, want 5 (sys,q1,a1,q2,BIG1)", len(loaded.Messages))
	}
	if got := loaded.Messages[len(loaded.Messages)-1]; got.ID != "BIG1" || !strings.HasPrefix(got.Content, "xxx") {
		t.Fatalf("last message must survive verbatim: id=%s prefix=%q", got.ID, got.Content[:min(16, len(got.Content))])
	}
	// 转写后的日志仍被正常重放：heads 只剩 main，代数已进位。
	st := dagReplay(t, path)
	if len(st.liveHeads()) != 1 || st.liveHeads()[0] != SessionMainHead {
		t.Fatalf("live heads = %v, want only main", st.liveHeads())
	}
	if st.generation != 2 {
		t.Fatalf("generation = %d, want 2 (one rotation)", st.generation)
	}
}
