package agent

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 884 验收①：多头链式形态（现场 11 头 = main + 10 条链式 concurrent
// fork，共享同一条大前缀）——旧「Σ每头整链」口径把共享前缀重复计 11 次，
// content×2≥logSize 恒成立、旋转被误拒；新「union 去重」口径放行。
//
// 控制组在测试里逐字回放旧口径（Σ整链 + 旧判据表达式），让「误拒」是被
// 断言的事实，而不是注释里的说法。
func TestDAGLogOversizedUnionDedupsSharedPrefix(t *testing.T) {
	SetEventsAutoRotation("manual", 4, 0)
	t.Cleanup(func() { SetEventsAutoRotation("manual", 4, 0) })

	path := dagTestSession(t)
	_, base := dagLinearLog(t, path)
	// 大共享前缀：main 链上一条 3MB 消息，后续每条链式 fork 全部继承它——
	// 这正是现场虚高的形态（链共享前缀被 Σ 口径重复计数）。
	big := dagMsg(provider.RoleUser, strings.Repeat("x", 3<<20), "BIG1")
	dagAppend(t, path, dagMessageEntry(t, SessionMainHead, "U2", "t1", big, base.Add(time.Minute)))

	// main + 10 条链式并发头：每条 fork 自上一头的尾节点，链共享整段前缀。
	parent, prevHead := big.ID, SessionMainHead
	for i := range 10 {
		hid := fmt.Sprintf("h%02d", i)
		at := base.Add(time.Duration(2+i) * time.Minute)
		dagAppend(t, path, sessionDAGEntry{
			Type: sessionDAGTypeFork, Head: prevHead, NewHead: hid, From: parent,
			Kind: HeadKindConcurrent, Writer: "w-" + hid, At: at,
		})
		tail := dagMsg(provider.RoleAssistant, "tail-"+hid, hid+"-tail")
		dagAppend(t, path, dagMessageEntry(t, hid, parent, "t1", tail, at.Add(time.Second)))
		parent, prevHead = tail.ID, hid
	}

	st := dagReplay(t, path)
	if got := len(st.liveHeads()); got != 11 {
		t.Fatalf("fixture heads = %d, want 11", got)
	}
	// 现场形态的门输入：日志 60MB（auto cap 50MB 之上；手动模式因子判据也走同一 content 口径）。
	st.size = 60 << 20

	// 控制组：任务 884 前的旧口径——Σ每头整链（materialize + digestAndSize 相加）。
	sum := int64(0)
	for _, id := range st.liveHeads() {
		msgs, _ := st.materialize(id)
		if _, size, err := digestAndSizeSessionMessages(msgs); err == nil {
			sum += size
		}
	}
	union := st.rotatedContentBytesLocked()
	if union <= 0 || sum <= union {
		t.Fatalf("fixture 前提不成立：union=%d 应远小于 Σ=%d（链共享前缀）", union, sum)
	}
	if sum*2 < st.size {
		t.Fatalf("控制组前提不成立：Σ=%d ×2 应 ≥ size=%d（旧口径才会给出 fold-give-up）", sum, st.size)
	}

	// 手动模式（默认配置）：共享因子判据内 Σ误拒 → union 放行。
	if eventsLogAboveFactor(st.size, sum, float64(sessionEventLogCompactFactor)) {
		t.Fatalf("控制组：Σ=%d 口径应被因子判据误拒（size=%d）", sum, st.size)
	}
	if !eventsLogAboveFactor(st.size, union, float64(sessionEventLogCompactFactor)) {
		t.Fatalf("union=%d 口径应越过因子判据（size=%d）", union, st.size)
	}
	if !sessionDAGLogOversized(st) {
		t.Fatalf("手动模式：union 口径必须放行旋转（union=%d size=%d）", union, st.size)
	}

	// auto + capMB=50（任务书事故配置）：Σ口径命中 cap 分支的 fold-give-up，
	// union 口径仍放行。
	SetEventsAutoRotation("auto", 4, 50)
	if eventsLogAboveFactor(st.size, sum, float64(sessionEventLogCompactFactor)) {
		t.Fatal("控制组：auto 因子判据下 Σ 口径应不越线")
	}
	if !(st.size > 50<<20 && sum*2 >= st.size) {
		t.Fatal("控制组：Σ 口径必须命中 cap 分支的 fold-give-up 形态")
	}
	if !sessionDAGLogOversized(st) {
		t.Fatalf("auto cap 模式：union 口径必须放行旋转（union=%d size=%d）", union, st.size)
	}

	// 反向护栏：union 自己给不出两倍收益时（log 不到 live 的 2×）依旧拒绝——
	// 修的是虚高估算，不是把门改成硬编码放行。
	SetEventsAutoRotation("manual", 4, 0)
	st.size = union + union/2
	if st.size <= sessionEventLogCompactFloor {
		t.Fatalf("护栏前提不成立：size=%d 应高于地板", st.size)
	}
	if sessionDAGLogOversized(st) {
		t.Fatalf("log=%d 不到 union=%d 的两倍时不应放行（fold 纪律保留）", st.size, union)
	}

	// 便宜早退的语义等价：log 不高于地板时判「不超限」，与旧口径一致。
	st.size = sessionEventLogCompactFloor
	if sessionDAGLogOversized(st) {
		t.Fatalf("size=%d 不高于地板时不应判超限", st.size)
	}
}
