package control

import (
	"context"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestTL1571FoldedLogForkBoundaryReconciliation 复现 tl1571 事故的坐标错位形态，
// 并把修复后的对账契约钉死在 control 面：
//
// 事故链：Diet fold 物理改写转录（752→304），checkpoint 边界仍持旧坐标系；
// 之后多次 rewind/recovery 让 bound 表里新旧坐标共存、同一消息索引被多个
// checkpoint turn 声明。前端若用「显示用户气泡序号」冒充 rewind 目标，两个
// 编号域数值重叠（0..10 vs 0..89），后端 boundary(序号) 命中一条完全不相关
// 的旧 checkpoint（turn-8@95），分叉停在 [95] 而目标在 [304]——多回退 209 条。
//
// 修复后的合法目标来源只有一个：CheckpointTurnsByMessageIndex（消息索引→
// checkpoint turn，display 序号回退已在 TS 侧收口）。本钉验证该链路端到端：
//
//	T1 map 自洽：每个 idx→turn 满足 boundary(turn)==idx——多 turn 声明同一
//	   索引时取最小 turn 的消解也不例外。前端经 map 解析出的 turn 落点必然
//	   精确等于点击的消息索引（±0）。
//	T2 分叉对账：用 map 解析的 turn 走 ForkSession，新会话 ForkMessageIndex
//	   == 目标索引，分叉内容 == src[:目标索引]——目标之前消息一条不丢。
//	T3 毒化封口：折叠后索引漂移、map 覆盖不到的用户气泡在 Go 侧就拿不到
//	   rewind 目标（display 序号无处注入；TS 侧 transcriptGrouping 钉住回退
//	   收口）。
//	T4 分叉 meta Turns == 分叉内容里真实 authored turns——分叉 UI 轮次与内容
//	   一致（事故里「显示 1 轮」是错误 boundary 的诚实投影，不是计数 bug）。
func TestTL1571FoldedLogForkBoundaryReconciliation(t *testing.T) {
	dir := t.TempDir()
	prov := &scriptedTurns{turns: [][]provider.Chunk{
		textTurn("first answer"),
		textTurn("second answer"),
		textTurn("third answer"),
		textTurn("post-fold answer"),
	}}
	ag := agent.New(prov, tool.NewRegistry(), agent.NewSession("sys"), agent.Options{}, event.Discard)
	c := New(Options{
		Runner:     ag,
		Executor:   ag,
		SessionDir: dir,
		Label:      "test",
		Sink:       event.Discard,
	})
	sessionPath := agent.NewSessionPath(dir, "test")
	c.SetSessionPath(sessionPath)
	ctx := context.Background()
	for _, prompt := range []string{"one prompt", "two prompt", "three prompt"} {
		if err := c.runTurnWithRaw(ctx, prompt, prompt); err != nil {
			t.Fatalf("turn %q: %v", prompt, err)
		}
	}
	// 折叠前：[sys(0), u0(1), a0(2), u1(3), a1(4), u2(5), a2(6)]，
	// bound = {0:1, 1:3, 2:5}（每个边界都是当时用户气泡的消息索引）。
	sess := ag.Session()
	snapshot := sess.Snapshot()
	if len(snapshot) != 7 {
		t.Fatalf("pre-fold log = %d messages, want 7: %+v", len(snapshot), snapshot)
	}

	// Diet fold 同构手术：把头部 u0..a1 折成一条本地占位（真实 fold 的占位是
	// user 角色文案），消息索引整体前移，checkpoint 边界不随动——旧坐标从此
	// 指向与当初不同的消息。折叠后：[sys(0), fold(1), u2(2), a2(3)]。
	folded := append([]provider.Message(nil), snapshot[:1]...)
	folded = append(folded, provider.Message{
		Role:    provider.RoleUser,
		Content: "[本地折叠] 已省略 4 条早期消息（tl1571 测试折叠占位）",
	})
	folded = append(folded, snapshot[5:]...)
	sess.Messages = folded

	// 折叠后的会话继续使用：新 turn 在新坐标系开 checkpoint（bound[3]=4）。
	if err := c.runTurnWithRaw(ctx, "four prompt", "four prompt"); err != nil {
		t.Fatalf("post-fold turn: %v", err)
	}
	// 事故同构：一条更老坐标系的 checkpoint（11-11 的 turn-8）与折叠后新
	// checkpoint 声明同一消息索引（事故里是 95 同被 turn-8/turn-51 占用）。
	c.checkpoints.mu.Lock()
	c.checkpoints.bound[8] = 4
	c.checkpoints.mu.Unlock()

	// post-fold 日志：[sys(0), fold(1), u2(2), a2(3), u4(4), a4(5)]，
	// bound = {0:1, 1:3, 2:5, 3:4, 8:4}。
	byIndex := c.CheckpointTurnsByMessageIndex()

	// T1a：冲突消解取最小 turn（3 < 8），且其边界精确等于该索引。
	if got := byIndex[4]; got != 3 {
		t.Fatalf("turnsByMessageIndex[4] = %d, want live turn 3 (min-resolution over stale turn 8)", got)
	}
	if boundary, ok := c.checkpoints.boundary(byIndex[4]); !ok || boundary != 4 {
		t.Fatalf("boundary(turn %d) = %d,%v, want exactly 4", byIndex[4], boundary, ok)
	}
	// T1b：整表自洽——map 提供的每个 rewind 目标落点都等于其索引本身。
	for idx, turn := range byIndex {
		boundary, ok := c.checkpoints.boundary(turn)
		if !ok || boundary != idx {
			t.Fatalf("map[%d] = turn %d with boundary %d,%v — rewind target must land exactly on its message index", idx, turn, boundary, ok)
		}
	}
	// T3：折叠后索引漂移的用户气泡（u2 从 5 漂到 2）不在 map 里——Go 侧不为
	// 它提供任何 rewind 目标，显示序号（旧代码会送 1，撞 stale turn-1@3）没有
	// 注入口。
	if _, poisoned := byIndex[2]; poisoned {
		t.Fatalf("shifted bubble index 2 must not resolve to any rewind turn: %v", byIndex)
	}
	// T1c：恰好仍落在原消息上的旧坐标系 checkpoint（占位在 1，stale turn-0 边
	// 界也是 1）保持精确——索引对得上才可提供，与「新旧坐标」无关。
	if got := byIndex[1]; got != 0 {
		t.Fatalf("turnsByMessageIndex[1] = %d, want stale turn 0 (its boundary still lands on index 1)", got)
	}

	// T2：经 map 解析的 turn 分叉——boundary == 目标索引 ±0，目标之前消息全在。
	newPath, err := c.ForkSession(byIndex[4], "")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if c.SessionPath() == newPath {
		t.Fatal("ForkSession must leave the parent controller on its own session")
	}
	meta, ok, err := agent.LoadBranchMeta(newPath)
	if err != nil || !ok {
		t.Fatalf("LoadBranchMeta fork meta: ok=%v err=%v", ok, err)
	}
	if meta.ForkTurn != byIndex[4] || meta.ForkMessageIndex != 4 {
		t.Fatalf("fork meta = turn %d index %d, want turn %d index 4 (±0)", meta.ForkTurn, meta.ForkMessageIndex, byIndex[4])
	}
	forked, err := agent.LoadSession(newPath)
	if err != nil {
		t.Fatalf("LoadSession fork: %v", err)
	}
	forkMsgs := forked.Snapshot()
	if len(forkMsgs) != 4 {
		t.Fatalf("fork content = %d messages, want exactly the 4 pre-target messages: %+v", len(forkMsgs), forkMsgs)
	}
	if forkMsgs[2].Role != provider.RoleUser || agent.StripTransientUserBlocks(forkMsgs[2].Content) != "three prompt" {
		t.Fatalf("fork message[2] = %+v, want the surviving u2 bubble intact before the target", forkMsgs[2])
	}
	for _, m := range forkMsgs {
		if m.Role == provider.RoleUser && m.Content == "four prompt" {
			t.Fatalf("fork contains the target turn itself: %+v", forkMsgs)
		}
	}

	// T4：分叉 meta 的轮次计数 == 分叉内容里的真实 authored turns（折叠占位
	// 也是 user 角色气泡，与真实 fold 行为一致）。
	if want := 2; meta.Turns != want {
		t.Fatalf("fork meta turns = %d, want %d authored turns in the forked content", meta.Turns, want)
	}
}

// TestTL1571UncoveredTurnIsRefusedNotMisresolved 钉住第二道闸：即便调用方绕过
// 前端、把一个不在 checkpoint 表里的编号（如折叠后气泡的显示序号）直接送给
// 后端，fork/rewind 必须诚实拒绝，而不是把编号硬解到一条不相关的旧边界。
// （事故里 turn=8 之所以能命中，是因为 8 恰好是 bound 表里的旧 checkpoint；
// 显示序号超出表覆盖范围时 backend 本就拒绝——这里钉住该拒绝不被回归。）
func TestTL1571UncoveredTurnIsRefusedNotMisresolved(t *testing.T) {
	c, _, _ := runTwoTurns(t)
	// runTwoTurns 跑了两轮：bound={0:1,1:3}，m.turn=2。turn 2 从未开过
	// checkpoint（那是「下一轮」的编号）——它必须不可用。
	if _, ok := c.checkpoints.boundary(2); ok {
		t.Fatal("precondition: turn 2 has no checkpoint")
	}
	if _, err := c.ForkSession(2, ""); err == nil {
		t.Fatal("fork with an uncovered turn number must be refused")
	}
	if err := c.Rewind(2, RewindConversation); err == nil {
		t.Fatal("conversation rewind with an uncovered turn number must be refused")
	}
}
