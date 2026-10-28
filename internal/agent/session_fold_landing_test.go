package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl 1186 —— fold-landing 口径回归。现场形态（1073 A/B 成因② + 1172 回板）：
// 深分叉多头会话「视图 11MiB / 链 ~100MB」，视图口径的收益估计判「能省 ~89%」，
// 而折叠真实产物（幸存头链 union）几乎不缩（freed ∈ [−217, 0]）。本组测试把
// 口径失真场景钉死：可退休头形态的落地≈视图（放行并真实折回视图尺寸），
// 不可退休头形态的落地=链 union（如实拒绝，空转归零）。fixture 构造失败一律
// Fatal，不静默。

const foldLandingBigFieldBytes = 64 << 10

// foldLandingDeepBranchSessionAt builds the tl 1186 field shape on a schema-2
// log: a small selected main chain plus `heads` concurrent heads of one dead
// writer, each carrying one 64KiB message — the union dominates the log while
// the selected chain stays tiny. writerHost decides retireability: the self
// host keeps the pid-backed retire path open; anything else reads as "writer
// on another host" and the heads stay.
func foldLandingDeepBranchSessionAt(t *testing.T, sessionPath, writerHost string, heads int) {
	t.Helper()
	host := writerHost
	if host == "" {
		h, err := os.Hostname()
		if err != nil || h == "" {
			t.Fatalf("fixture needs the self hostname: %v", err)
		}
		host = h
	}
	_, base := dagLinearLog(t, sessionPath)
	const deadWriter = "w-dead-tl1186"
	entries := []sessionDAGEntry{
		{Type: sessionDAGTypeWriter, Writer: deadWriter, PID: 424242, Hostname: host, At: base.Add(9 * time.Second)},
	}
	for i := 0; i < heads; i++ {
		at := base.Add(time.Duration(10+i) * time.Second)
		entries = append(entries,
			sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: headIDFor(i), From: "A1", Kind: HeadKindConcurrent, Writer: deadWriter, At: at},
			dagMessageEntry(t, headIDFor(i), "A1", "turn-big", dagMsg(provider.RoleUser, strings.Repeat("X", foldLandingBigFieldBytes), bigMessageIDFor(i)), at.Add(time.Second)),
		)
	}
	// 主链收尾晚于所有并发头：selected 线取代它们（retire 判据的 superseded 条件）。
	entries = append(entries, dagMessageEntry(t, SessionMainHead, "U2", "turn-late", dagMsg(provider.RoleUser, "q3", "U3"), base.Add(60*time.Second)))
	dagAppend(t, sessionPath, entries...)
}

func headIDFor(i int) string { return "C" + string(rune('1'+i)) }
func bigMessageIDFor(i int) string {
	return "B" + string(rune('1'+i))
}

// 验收①（口径失真回归·真实收益断言）：可退休并发头形态——估计口径给出视图
// 量级的落地预测，10% 门槛数字不动而判定翻转（链口径拒、落地口径放），且
// CompactSessionFile 真实把日志折回视图量级（≈ 视图 11MiB / 链 100MB 的
// 123MB→~11MiB 形态在可控 fixture 上的等价物）。
func TestFoldLandingPredictsViewLandingForRetireableHeads(t *testing.T) {
	t.Skip("tl1227 DAG 读退役: fold-landing 预测读回经已删读面；存量 DAG 日志由 MigrateSessionDAGLog 一次性迁移为 schema-1，读侧单路径化后本场景不再可达")
	path := dagTestSession(t)
	foldLandingDeepBranchSessionAt(t, path, "", 3)

	logSize := sessionEventLogSize(path)
	if logSize <= 0 {
		t.Fatalf("fixture must produce a non-empty event log")
	}
	st := dagReplay(t, path)
	st.mu.RLock()
	union := st.rotatedContentBytesLocked()
	st.mu.RUnlock()

	landing := SessionFoldLandingBytes(path)
	if landing <= 0 {
		t.Fatalf("retireable heads must yield an honest landing prediction, got %d", landing)
	}
	// 三个 64KiB 头链可退休：落地预测必须收缩 ≥90%（union 口径的十分之一以下）。
	if landing*10 > union {
		t.Fatalf("landing must predict the retire-driven shrink: landing=%d union=%d", landing, union)
	}

	// 同一门槛数字（10% 不动）下的口径翻转：
	//   链口径（rotatedContent≈日志体积）如实拒绝——现场 foldProfitable(107, 96.5)=false 的形态；
	//   落地口径（退休后≈视图）放行。
	if EventsFoldProfitable(logSize, union) {
		t.Fatalf("chain-口径 must refuse: logSize=%d union=%d (field shape freed∈[−217,0])", logSize, union)
	}
	if !EventsFoldProfitable(logSize, landing) {
		t.Fatalf("landing-口径 must pass: logSize=%d landing=%d", logSize, landing)
	}

	// 真实收益断言：折叠确实落地到视图量级（≥90% 字节真实释放）。
	before, after, err := CompactSessionFile(path)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if before != logSize {
		t.Logf("log grew between estimate and compact: %d -> %d", logSize, before)
	}
	if after > before/10 {
		t.Fatalf("fold must land near the view size: before=%d after=%d landing=%d", before, after, landing)
	}
}

// 验收②（1172 顾虑的正面回答）：不可退休头形态——用户 fork 资产/跨主机写者
// 保守保留，幸存集=全体 live 头，落地=链 union（同一表达式测量，必须精确
// 相等），估计如实拒绝，重写实测不删字节（空转归零的依据）。
func TestFoldLandingKeepsChainUnionWhenHeadsNotRetireable(t *testing.T) {
	t.Skip("tl1227 DAG 读退役: fold-landing 预测读回经已删读面；存量 DAG 日志由 MigrateSessionDAGLog 一次性迁移为 schema-1，读侧单路径化后本场景不再可达")
	path := dagTestSession(t)
	foldLandingDeepBranchSessionAt(t, path, "tl1186-foreign-host", 3)

	logSize := sessionEventLogSize(path)
	st := dagReplay(t, path)
	st.mu.RLock()
	union := st.rotatedContentBytesLocked()
	st.mu.RUnlock()

	landing := SessionFoldLandingBytes(path)
	if landing != union {
		t.Fatalf("no retireable head: landing must equal the chain union exactly: landing=%d union=%d", landing, union)
	}
	if EventsFoldProfitable(logSize, landing) {
		t.Fatalf("estimate must refuse the no-shrink fold: logSize=%d landing=%d", logSize, landing)
	}
	// 产物口径验证：真实重写确实不缩（现场同一形态 freed −164）。
	before, after, err := CompactSessionFile(path)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if after <= before/2 {
		t.Fatalf("unretireable heads must not fold away: before=%d after=%d", before, after)
	}
}

// 验收③：schema-1 日志没有诚实预测（折叠产物=转录本身，调用方的既有代理
// 口径就是落地口径）⇒ 返回 0，调用方回退。
func TestFoldLandingReturnsZeroForSchemaOneLog(t *testing.T) {
	path := dagTestSession(t)
	logPath := store.SessionEventLog(path)
	if err := os.WriteFile(logPath, []byte("{\"schema_version\":1,\"type\":\"replace\"}\n"), 0o600); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if got := SessionFoldLandingBytes(path); got != 0 {
		t.Fatalf("schema-1 log must report no prediction, got %d", got)
	}
}

// 验收④：归档态日志绝不为「估计」物化（物化是行为变更不是测量）⇒ 返回 0
// 且明文保持缺失。
func TestFoldLandingDoesNotMaterializeArchivedLog(t *testing.T) {
	path := dagTestSession(t)
	foldLandingDeepBranchSessionAt(t, path, "", 2)
	plain := sessionEventLogPlainPath(path)
	zst := sessionEventLogZstPathFor(path)
	if plain == "" || zst == "" {
		t.Fatalf("fixture paths must resolve: plain=%q zst=%q", plain, zst)
	}
	if err := os.Rename(plain, zst); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if got := SessionFoldLandingBytes(path); got != 0 {
		t.Fatalf("archived log must report no prediction, got %d", got)
	}
	if _, err := os.Stat(plain); !os.IsNotExist(err) {
		t.Fatalf("estimating must not materialize the archive: plain stat err=%v", err)
	}
}
