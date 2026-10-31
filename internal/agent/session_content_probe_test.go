package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// tl1228 阶段一回归：有界内容探测必须与全量 LoadSession 的 HasContent 同判。
// 每个用例先经真实重放（loadReferenceContent）取基准答案，再断言探测一致。
//
// tl1295 语义改述：tl1227 读侧单路径化后，schema-2 DAG 日志是弃读封存态——
// LoadSession 不再重放 DAG 图，落 .jsonl checkpoint 兜底（无 checkpoint 报
// 缺失）。本文件里的 schema-2 fixture 因此全部读作 sealed 形态：两判据的
// 共同事实源是 checkpoint（有则按它、无则双双报错映射 false）。DAG 字节里
// 的结构语义（fork/rewind/patch/…）自 tl1227 起不再有任何读路径消费方，
// 旧注释按 DAG 语义断言的意图随之退役；tl1295 之前 probe 仍私读 DAG 字节，
// 与 full-load 各答一个事实源，8 子用例红即此分歧。
// sealed 现实主力形态（DAG 日志 + checkpoint 共存、checkpoint 拥有读取权）
// 的双向钉在 TestContentProbeSealedDAGCheckpointOwnsReads。

// loadReferenceContent answers through the full LoadSession path — the exact
// contract the desktop probe callers had before tl1228. A failed load reads as
// no content, matching sessionFileHasConversationContent.
func loadReferenceContent(t *testing.T, path string) bool {
	t.Helper()
	s, err := LoadSession(path)
	if err != nil {
		return false
	}
	return s.HasContent()
}

// probeMatchesFullLoad pins the tl1228 phase-1 core invariant on one fixture:
// the bounded probe and the full load answer identically.
func probeMatchesFullLoad(t *testing.T, path string) {
	t.Helper()
	want := loadReferenceContent(t, path)
	if got := SessionHasConversationContent(path); got != want {
		t.Fatalf("probe=%v full-load=%v (must agree)", got, want)
	}
}

func dagWriteCheckpoint(t *testing.T, path string, roles ...provider.Role) {
	t.Helper()
	var b strings.Builder
	for _, role := range roles {
		b.WriteString(`{"id":"` + strings.ToLower(string(role)) + `-1","role":"` + string(role) + `","content":"x"}` + "\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dagCaseLog writes log header + messages chained on main (ids M0..Mn).
func dagCaseLog(t *testing.T, path string, msgs ...provider.Message) []string {
	t.Helper()
	base := time.Date(2026, 1, 8, 10, 0, 0, 0, time.UTC)
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	ids := make([]string, 0, len(msgs))
	parent := ""
	for i, m := range msgs {
		id := "M" + string(rune('0'+i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", dagMsg(m.Role, m.Content, id), base.Add(time.Duration(i)*time.Second)))
		parent = id
		ids = append(ids, id)
	}
	dagAppend(t, path, entries...)
	return ids
}

func TestContentProbeDAGMatchesFullLoad(t *testing.T) {
	t.Run("linear with user message", func(t *testing.T) {
		path := dagTestSession(t)
		dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""),
			dagMsg(provider.RoleAssistant, "a1", ""))
		probeMatchesFullLoad(t, path)
	})
	t.Run("system only", func(t *testing.T) {
		path := dagTestSession(t)
		dagCaseLog(t, path, dagMsg(provider.RoleSystem, "sys", ""))
		probeMatchesFullLoad(t, path)
	})
	t.Run("rewound to system", func(t *testing.T) {
		path := dagTestSession(t)
		ids := dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypeRewind, Head: SessionMainHead, To: ids[0], Cause: "test", At: time.Now()})
		probeMatchesFullLoad(t, path)
	})
	t.Run("rewound to zero", func(t *testing.T) {
		path := dagTestSession(t)
		dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypeRewind, Head: SessionMainHead, To: "", Cause: "test", At: time.Now()})
		probeMatchesFullLoad(t, path)
	})
	t.Run("fork select carries content", func(t *testing.T) {
		path := dagTestSession(t)
		ids := dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		base := time.Now()
		dagAppend(t, path,
			sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "F", From: ids[0], Kind: HeadKindFork, At: base},
			dagMessageEntry(t, "F", ids[0], "t2", dagMsg(provider.RoleUser, "alt", "FA"), base.Add(time.Second)),
			sessionDAGEntry{Type: sessionDAGTypeSelect, Head: "F", At: base.Add(2 * time.Second)},
		)
		probeMatchesFullLoad(t, path)
	})
	t.Run("retired selected head falls back to live main", func(t *testing.T) {
		path := dagTestSession(t)
		ids := dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		base := time.Now()
		dagAppend(t, path,
			sessionDAGEntry{Type: sessionDAGTypeFork, Head: SessionMainHead, NewHead: "F", From: ids[0], Kind: HeadKindFork, At: base.Add(time.Second)},
			sessionDAGEntry{Type: sessionDAGTypeSelect, Head: "F", At: base.Add(2 * time.Second)},
			sessionDAGEntry{Type: sessionDAGTypeRetire, Head: "F", At: base.Add(3 * time.Second)},
		)
		probeMatchesFullLoad(t, path)
	})
	t.Run("patch to system role on the only user message", func(t *testing.T) {
		path := dagTestSession(t)
		ids := dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		patch, err := encodeSessionDAGMessage(dagMsg(provider.RoleSystem, "redacted", ids[1]))
		if err != nil {
			t.Fatal(err)
		}
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypePatch, Head: SessionMainHead, Target: ids[1], Msgs: patch, At: time.Now()})
		probeMatchesFullLoad(t, path)
	})
	t.Run("patch keeps assistant content", func(t *testing.T) {
		path := dagTestSession(t)
		ids := dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""),
			dagMsg(provider.RoleAssistant, "a1", ""))
		patch, err := encodeSessionDAGMessage(dagMsg(provider.RoleSystem, "redacted", ids[1]))
		if err != nil {
			t.Fatal(err)
		}
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypePatch, Head: SessionMainHead, Target: ids[1], Msgs: patch, At: time.Now()})
		probeMatchesFullLoad(t, path)
	})
	t.Run("redact to system on the only user message", func(t *testing.T) {
		path := dagTestSession(t)
		ids := dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		raw, err := encodeSessionDAGMessage(dagMsg(provider.RoleSystem, "redacted", ids[1]))
		if err != nil {
			t.Fatal(err)
		}
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypeRedact, Head: SessionMainHead, Targets: map[string]json.RawMessage{ids[1]: raw}, At: time.Now()})
		probeMatchesFullLoad(t, path)
	})
	t.Run("torn line behind which content follows", func(t *testing.T) {
		// Sealed（tl1295 改述）：DAG 字节含 torn line 与后续内容均不被读——
		// 两判据同落无 checkpoint 的封存缺失，同答 false。
		path := dagTestSession(t)
		ids := dagCaseLog(t, path, dagMsg(provider.RoleSystem, "sys", ""))
		f, err := os.OpenFile(store.SessionEventLog(path), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		f.WriteString("{ this line is torn\n")
		f.Close()
		dagAppend(t, path, dagMessageEntry(t, SessionMainHead, ids[0], "t2", dagMsg(provider.RoleUser, "q1", "U9"), time.Now()))
		probeMatchesFullLoad(t, path)
	})
	t.Run("duplicate message entry skipped", func(t *testing.T) {
		path := dagTestSession(t)
		ids := dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		// Sealed（tl1295 改述）：重放层的 duplicate 语义已无读路径，两判据
		// 同落无 checkpoint 封存缺失，同答 false。
		dagAppend(t, path, dagMessageEntry(t, SessionMainHead, ids[0], "t2", dagMsg(provider.RoleUser, "dup", ids[1]), time.Now()))
		probeMatchesFullLoad(t, path)
	})
	t.Run("orphan chain root degrades to shorter transcript", func(t *testing.T) {
		path := dagTestSession(t)
		dagCaseLog(t, path, dagMsg(provider.RoleSystem, "sys", ""))
		// Sealed（tl1295 改述）：orphan root 的链降解语义已无读路径，同答 false。
		dagAppend(t, path, dagMessageEntry(t, SessionMainHead, "ghost", "t2", dagMsg(provider.RoleUser, "q1", "U9"), time.Now()))
		probeMatchesFullLoad(t, path)
	})
	t.Run("unsupported entry type refuses", func(t *testing.T) {
		path := dagTestSession(t)
		dagCaseLog(t, path, dagMsg(provider.RoleSystem, "sys", ""))
		dagAppend(t, path, sessionDAGEntry{Type: "from_the_future", Head: SessionMainHead, At: time.Now()},
			dagMessageEntry(t, SessionMainHead, "", "t2", dagMsg(provider.RoleUser, "q1", "U9"), time.Now()))
		// Sealed（tl1295 改述）：未知条目型在旧 DAG 扫描里是硬错；sealed 后
		// 两判据都不读 DAG 字节，同落无 checkpoint 缺失，同答 false。
		probeMatchesFullLoad(t, path)
	})
	t.Run("checkpoint owns reads for a sealed dag log", func(t *testing.T) {
		// tl1295 改述（原意图「empty dag log does not fall back to checkpoint」
		// 随 tl1227 封存兜底退役）：checkpoint 是 sealed 日志的唯一可读面——
		// DAG 字节哪怕为空/为活日志，作答的也是 checkpoint。probe 与
		// full-load 必须同答 true（该用例红的方向曾是 full-load 独真）。
		path := dagTestSession(t)
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypeLog, Generation: 1, At: time.Now()})
		dagWriteCheckpoint(t, path, provider.RoleUser)
		probeMatchesFullLoad(t, path)
	})
	t.Run("future schema refuses", func(t *testing.T) {
		path := dagTestSession(t)
		body := `{"schema_version":99,"type":"log"}` + "\n"
		if err := os.WriteFile(store.SessionEventLog(path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		probeMatchesFullLoad(t, path)
	})
	t.Run("missing transcript", func(t *testing.T) {
		probeMatchesFullLoad(t, filepath.Join(t.TempDir(), "missing.jsonl"))
	})
	t.Run("budget refusal", func(t *testing.T) {
		path := dagTestSession(t)
		restore := SetSessionReplayLimitsForTest(SessionReplayLimits{MaxBytes: 64, MaxRecords: 1000, MaxMessages: 1000, MaxCollectionItems: 1000})
		defer restore()
		dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		// Sealed（tl1295 改述）：全量在此按预算拒载；probe 不读 DAG 字节，
		// 同落无 checkpoint 缺失——同答 false。
		probeMatchesFullLoad(t, path)
	})
}

// TestContentProbeSealedDAGCheckpointOwnsReads pins the tl1295 homogenization
// on the sealed schema-2 shapes that actually exist in the wild (a schema-2
// events log plus a checkpoint — the tl1281SealedSession shape): the
// checkpoint is the single answer source for BOTH verdicts, in both
// directions. Regression钉 for the tl1295 divergence: before it, the probe
// read the DAG bytes while the full load read the checkpoint, so the two
// callers could disagree on the very same directory.
func TestContentProbeSealedDAGCheckpointOwnsReads(t *testing.T) {
	t.Run("dag content is invisible when the checkpoint is empty", func(t *testing.T) {
		// 权威 DAG 里有 user 内容、checkpoint 为空 → 两判据都必须答 false：
		// 封存语义下 DAG 字节不参与判定（若 probe 偷读 DAG 回答 true 即红）。
		path := dagTestSession(t)
		dagCaseLog(t, path,
			dagMsg(provider.RoleSystem, "sys", ""),
			dagMsg(provider.RoleUser, "q1", ""))
		dagWriteCheckpoint(t, path, provider.RoleSystem)
		probeMatchesFullLoad(t, path)
		if SessionHasConversationContent(path) {
			t.Fatal("sealed DAG bytes must not answer through the probe when the checkpoint has no content")
		}
	})
	t.Run("checkpoint content answers even when the dag log is empty", func(t *testing.T) {
		// DAG 日志只有 header、checkpoint 有 user → 两判据都必须答 true：
		// checkpoint 是唯一可读面（若 probe 私读 DAG 答 false 即红）。
		path := dagTestSession(t)
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypeLog, Generation: 1, At: time.Now()})
		dagWriteCheckpoint(t, path, provider.RoleSystem, provider.RoleUser)
		probeMatchesFullLoad(t, path)
		if !SessionHasConversationContent(path) {
			t.Fatal("checkpoint content must answer for a sealed dag log through the probe too")
		}
	})
}

func TestContentProbeLegacyJSONL(t *testing.T) {
	t.Run("user checkpoint", func(t *testing.T) {
		path := dagTestSession(t)
		dagWriteCheckpoint(t, path, provider.RoleSystem, provider.RoleUser)
		probeMatchesFullLoad(t, path)
	})
	t.Run("system-only checkpoint", func(t *testing.T) {
		path := dagTestSession(t)
		dagWriteCheckpoint(t, path, provider.RoleSystem)
		probeMatchesFullLoad(t, path)
	})
	t.Run("empty checkpoint", func(t *testing.T) {
		path := dagTestSession(t)
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		probeMatchesFullLoad(t, path)
	})
}

func TestContentProbeSchemaOneLog(t *testing.T) {
	t.Run("user message via flat route", func(t *testing.T) {
		useSchemaOneLog(t)
		path := dagTestSession(t)
		dagSavedSession(t, path, "q1")
		probeMatchesFullLoad(t, path)
	})
	t.Run("system only via flat route", func(t *testing.T) {
		useSchemaOneLog(t)
		path := dagTestSession(t)
		dagSavedSession(t, path)
		probeMatchesFullLoad(t, path)
	})
}

func TestContentProbeSnapshotTierSeam(t *testing.T) {
	// 两档接缝：第一档（快照）先问，ok=true 直接作答；ok=false 落到第二档
	// （events light scan）。nil（今日默认态）等于第一档休眠。
	// tl1295：第二档对 sealed schema-2 残余的回答面是 checkpoint（与
	// LoadSession 封存兜底同源），fixture 按该现实形态带一个 checkpoint。
	path := dagTestSession(t)
	dagCaseLog(t, path,
		dagMsg(provider.RoleSystem, "sys", ""),
		dagMsg(provider.RoleUser, "q1", ""))
	dagWriteCheckpoint(t, path, provider.RoleUser)

	t.Run("dormant by default", func(t *testing.T) {
		if sessionContentSnapshotProbe != nil {
			t.Fatalf("tier-1 seam must be nil until tl1173 step-2 installs it")
		}
		if !SessionHasConversationContent(path) {
			t.Fatal("tier-2 events scan must answer on its own")
		}
	})
	t.Run("tier-1 answer wins when ok", func(t *testing.T) {
		called := 0
		sessionContentSnapshotProbe = func(string) (bool, bool) { called++; return false, true }
		defer func() { sessionContentSnapshotProbe = nil }()
		if SessionHasConversationContent(path) {
			t.Fatal("tier-1 ok=false-content must be the verdict")
		}
		if called != 1 {
			t.Fatalf("tier-1 consulted %d times, want 1", called)
		}
	})
	t.Run("tier-1 miss falls through to events", func(t *testing.T) {
		sessionContentSnapshotProbe = func(string) (bool, bool) { return true, false }
		defer func() { sessionContentSnapshotProbe = nil }()
		if !SessionHasConversationContent(path) {
			t.Fatal("tier-1 ok=false must fall through to the events scan")
		}
	})
}
