package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// 任务 945 验收①：陈旧并发头判定矩阵——真陈旧（并发 kind+超龄+写者同主机已死+
// 被 selected 线取代）才 retire；任何一条不满足即保留。判定为纯函数，全部注入。

func TestStaleConcurrentHeadDecisionMatrix(t *testing.T) {
	const selfHost = "host-a"
	now := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)
	aged := now.Add(-staleConcurrentHeadMinAge - time.Hour)
	selectedAt := now.Add(-time.Hour)
	fresh := now.Add(-time.Hour)
	deadPID := func(int) bool { return false }
	stale := staleHeadCandidate{id: "h-stale", kind: HeadKindConcurrent, writer: "w-dead", lastActivity: aged}
	deadWriter := &staleHeadWriterFact{pid: 424242, hostname: selfHost}

	tests := []struct {
		name             string
		c                staleHeadCandidate
		selected         string
		selectedActivity time.Time
		w                *staleHeadWriterFact
		pidAlive         func(int) bool
		selfHost         string
		want             bool
	}{
		{
			name: "真陈旧并发头 retire", c: stale, selected: SessionMainHead,
			selectedActivity: selectedAt, w: deadWriter, pidAlive: deadPID, selfHost: selfHost,
			want: true,
		},
		{
			name: "写者进程仍活着不误伤", c: stale, selected: SessionMainHead,
			selectedActivity: selectedAt, w: deadWriter, pidAlive: func(int) bool { return true }, selfHost: selfHost,
			want: false,
		},
		{
			name: "main 头永不自动 retire", c: staleHeadCandidate{id: SessionMainHead, kind: HeadKindMain, writer: "w-dead", lastActivity: aged},
			selected: "h-other", selectedActivity: selectedAt, w: deadWriter, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "用户 fork 头不动（显式版本资产）", c: staleHeadCandidate{id: "h-fork", kind: HeadKindFork, writer: "w-dead", lastActivity: aged},
			selected: SessionMainHead, selectedActivity: selectedAt, w: deadWriter, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "selected 头不动", c: stale, selected: "h-stale",
			selectedActivity: selectedAt, w: deadWriter, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "未超龄不 retire", c: staleHeadCandidate{id: "h-young", kind: HeadKindConcurrent, writer: "w-dead", lastActivity: fresh},
			selected: SessionMainHead, selectedActivity: selectedAt, w: deadWriter, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "写者未注册不 retire", c: stale, selected: SessionMainHead,
			selectedActivity: selectedAt, w: nil, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "写者 pid 缺失不 retire", c: stale, selected: SessionMainHead,
			selectedActivity: selectedAt, w: &staleHeadWriterFact{pid: 0, hostname: selfHost}, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "跨主机写者不 retire（无法验证死活）", c: stale, selected: SessionMainHead,
			selectedActivity: selectedAt, w: &staleHeadWriterFact{pid: 424242, hostname: "host-b"}, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "主机名为空不 retire", c: stale, selected: SessionMainHead,
			selectedActivity: selectedAt, w: &staleHeadWriterFact{pid: 424242, hostname: ""}, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
		{
			name: "selected 线未取代不 retire（main 静止在并发头之前）", c: stale, selected: SessionMainHead,
			selectedActivity: aged.Add(-time.Minute), w: deadWriter, pidAlive: deadPID, selfHost: selfHost,
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := staleConcurrentHeadDecision(tt.c, tt.selected, tt.selectedActivity, tt.w, tt.pidAlive, tt.selfHost, now, staleConcurrentHeadMinAge)
			if got != tt.want {
				t.Fatalf("decision = %v, want %v", got, tt.want)
			}
		})
	}
}

// staleHeadFixture builds a schema-2 log with main (sys,q1,a1,q2 + tail), a
// dead-writer concurrent head carrying an exclusive tail, and — when liveHead
// is set — a second concurrent head whose writer is still alive. main gets a
// tail message after both forks so the selected line supersedes them.
func staleHeadFixture(t *testing.T, path string, liveHead bool) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "test-host"
	}
	_, base := dagLinearLog(t, path)
	deadPID, alivePID := 424242, os.Getpid()

	dagAppend(t, path,
		sessionDAGEntry{Type: sessionDAGTypeWriter, Writer: "w-dead", PID: deadPID, Hostname: host, At: base.Add(2 * time.Minute)},
		sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "hdead", From: "U2", Kind: HeadKindConcurrent, Writer: "w-dead", At: base.Add(2 * time.Minute)},
	)
	d1 := dagMsg(provider.RoleAssistant, "dead-branch reply", "D1")
	d2 := dagMsg(provider.RoleUser, "dead-branch question", "D2")
	dagAppend(t, path,
		dagMessageEntry(t, "hdead", "U2", "t1", d1, base.Add(2*time.Minute+time.Second)),
		dagMessageEntry(t, "hdead", d1.ID, "t1", d2, base.Add(2*time.Minute+2*time.Second)),
	)
	if liveHead {
		dagAppend(t, path,
			sessionDAGEntry{Type: sessionDAGTypeWriter, Writer: "w-live", PID: alivePID, Hostname: host, At: base.Add(3 * time.Minute)},
			sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "hlive", From: "U2", Kind: HeadKindConcurrent, Writer: "w-live", At: base.Add(3 * time.Minute)},
		)
		l1 := dagMsg(provider.RoleAssistant, "live-branch reply", "L1")
		dagAppend(t, path, dagMessageEntry(t, "hlive", "U2", "t1", l1, base.Add(3*time.Minute+time.Second)))
	}
	// 用户回到 main 继续用：selected 线的活动晚于两个并发头。
	m5 := dagMsg(provider.RoleUser, "back on main", "U5")
	dagAppend(t, path,
		dagMessageEntry(t, SessionMainHead, "U2", "t2", m5, base.Add(10*time.Minute)),
		sessionDAGEntry{Type: sessionDAGTypeSelect, Head: SessionMainHead, Reason: "user", At: base.Add(10*time.Minute + time.Second)},
	)
}

func TestRetireStaleConcurrentHeadsRetiresOnlyStale(t *testing.T) {
	path := dagTestSession(t)
	staleHeadFixture(t, path, true)
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "test-host"
	}
	now := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)

	before, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	retired, err := RetireStaleConcurrentHeads(path, now, func(pid int) bool { return pid == os.Getpid() }, host)
	if err != nil {
		t.Fatalf("retire stale: %v", err)
	}
	if len(retired) != 1 || retired[0] != "hdead" {
		t.Fatalf("retired = %v, want [hdead] (live writer's head kept)", retired)
	}

	// 数据保守性：retire 只 append marker，日志不缩、旧头字节仍在。
	after, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() < before.Size() {
		t.Fatalf("retire 不得缩日志：%d → %d", before.Size(), after.Size())
	}
	raw, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "dead-branch reply") || !strings.Contains(string(raw), "dead-branch question") {
		t.Fatal("retire 不得删除用户消息字节")
	}

	// 重放确认：hdead 已退役、hlive 与 main 完好。
	st := dagReplay(t, path)
	if !st.heads["hdead"].retired {
		t.Fatal("hdead 应为 retired")
	}
	if st.heads["hlive"] == nil || st.heads["hlive"].retired {
		t.Fatal("hlive（写者仍活着）不得被 retire")
	}
	if st.heads[SessionMainHead].retired {
		t.Fatal("main 不得被 retire")
	}
}

// 任务 945 验收②③（与 921 sweep 联动端到端）：陈旧并发头带 6MB 独占尾巴，
// 判定 retire（产品行为，非夹具手工 retire）→ CompactSessionFile（sweep 生产
// 动作）物理清除其字节 → 会话可正常打开、main 链逐字保留、heads 只剩 main。
func TestRetireStaleConcurrentHeadsThenSweepCompactsAndSessionOpens(t *testing.T) {
	path := dagTestSession(t)
	_, base := dagLinearLog(t, path)
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "test-host"
	}
	// main 追加 6MB 大消息撑住 live 水位；并发头独占 3MB×2 尾巴（真实形态）。
	big := dagMsg(provider.RoleUser, strings.Repeat("x", 6<<20), "BIG1")
	dagAppend(t, path, dagMessageEntry(t, SessionMainHead, "U2", "t2", big, base.Add(time.Minute)))
	dagAppend(t, path,
		sessionDAGEntry{Type: sessionDAGTypeWriter, Writer: "w-dead", PID: 424242, Hostname: host, At: base.Add(2 * time.Minute)},
		sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "hdead", From: big.ID, Kind: HeadKindConcurrent, Writer: "w-dead", At: base.Add(2 * time.Minute)},
	)
	d1 := dagMsg(provider.RoleAssistant, strings.Repeat("y", 3<<20), "D1")
	d2 := dagMsg(provider.RoleUser, strings.Repeat("z", 3<<20), "D2")
	dagAppend(t, path,
		dagMessageEntry(t, "hdead", big.ID, "t1", d1, base.Add(2*time.Minute+time.Second)),
		dagMessageEntry(t, "hdead", d1.ID, "t1", d2, base.Add(2*time.Minute+2*time.Second)),
	)
	// 用户回到 main 继续用（selected 线取代并发头），此后会话不再有保存。
	m5 := dagMsg(provider.RoleUser, "back on main", "U5")
	dagAppend(t, path,
		dagMessageEntry(t, SessionMainHead, big.ID, "t2", m5, base.Add(10*time.Minute)),
		sessionDAGEntry{Type: sessionDAGTypeSelect, Head: SessionMainHead, Reason: "user", At: base.Add(10*time.Minute + time.Second)},
	)

	now := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)
	retired, err := RetireStaleConcurrentHeads(path, now, func(int) bool { return false }, host)
	if err != nil {
		t.Fatalf("retire stale: %v", err)
	}
	if len(retired) != 1 || retired[0] != "hdead" {
		t.Fatalf("retired = %v, want [hdead]", retired)
	}

	// 921 sweep 生产动作：lease 空闲证明 + 强制 rotate 清不可达字节。
	freedBefore, after, err := CompactSessionFile(path)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	info, err := os.Stat(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if after != info.Size() || info.Size() >= freedBefore-5<<20 {
		t.Fatalf("陈旧头尾巴未被清：%d → %d", freedBefore, info.Size())
	}
	raw, err := os.ReadFile(store.SessionEventLog(path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), strings.Repeat("y", 32)) {
		t.Fatal("retired 头的独占字节必须被物理清除")
	}

	// 会话可正常打开，转写逐字保留。
	loaded, err := LoadSession(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(loaded.Messages) != 6 {
		t.Fatalf("visible messages = %d, want 6 (sys,q1,a1,q2,BIG1,U5)", len(loaded.Messages))
	}
	last := loaded.Messages[len(loaded.Messages)-1]
	if last.ID != "U5" || last.Content != "back on main" {
		t.Fatalf("last message must survive verbatim: id=%s content=%q", last.ID, last.Content)
	}
	st := dagReplay(t, path)
	if len(st.liveHeads()) != 1 || st.liveHeads()[0] != SessionMainHead {
		t.Fatalf("live heads = %v, want only main", st.liveHeads())
	}
	if st.generation != 2 {
		t.Fatalf("generation = %d, want 2 (one rotation)", st.generation)
	}
}
