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
		// resumePastTornLine semantics: the bad line is a hole, entries behind
		// it count. The full replay reads the user message behind the hole, so
		// the probe must too.
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
		// Re-append the same id with different content: the replay warns and
		// keeps the FIRST body, so the answer must not flip either way.
		dagAppend(t, path, dagMessageEntry(t, SessionMainHead, ids[0], "t2", dagMsg(provider.RoleUser, "dup", ids[1]), time.Now()))
		probeMatchesFullLoad(t, path)
	})
	t.Run("orphan chain root degrades to shorter transcript", func(t *testing.T) {
		path := dagTestSession(t)
		dagCaseLog(t, path, dagMsg(provider.RoleSystem, "sys", ""))
		// A user message whose parent was never seen: chainIDs stops at the
		// orphan root, and the real transcript degrades to the seen prefix.
		dagAppend(t, path, dagMessageEntry(t, SessionMainHead, "ghost", "t2", dagMsg(provider.RoleUser, "q1", "U9"), time.Now()))
		probeMatchesFullLoad(t, path)
	})
	t.Run("unsupported entry type refuses", func(t *testing.T) {
		path := dagTestSession(t)
		dagCaseLog(t, path, dagMsg(provider.RoleSystem, "sys", ""))
		dagAppend(t, path, sessionDAGEntry{Type: "from_the_future", Head: SessionMainHead, At: time.Now()},
			dagMessageEntry(t, SessionMainHead, "", "t2", dagMsg(provider.RoleUser, "q1", "U9"), time.Now()))
		probeMatchesFullLoad(t, path)
	})
	t.Run("empty dag log does not fall back to checkpoint", func(t *testing.T) {
		path := dagTestSession(t)
		dagAppend(t, path, sessionDAGEntry{Type: sessionDAGTypeLog, Generation: 1, At: time.Now()})
		dagWriteCheckpoint(t, path, provider.RoleUser) // a stale checkpoint must not answer for a live log
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
		probeMatchesFullLoad(t, path)
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
	path := dagTestSession(t)
	dagCaseLog(t, path,
		dagMsg(provider.RoleSystem, "sys", ""),
		dagMsg(provider.RoleUser, "q1", ""))

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
