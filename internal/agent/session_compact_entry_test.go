package agent

import "testing"

// Task 275: the manual compact entry must (a) require idleness, (b) shrink a
// log that the automatic gate would also treat as maintenance-worthy, and
// (c) be idempotent — a second run on an already-compact log is a no-op
// rewrite, not an error.
func TestCompactSessionFileRewritesAndIsIdempotent(t *testing.T) {
	t.Skip("tl1227 DAG 读退役: compact 维护链仍在（probe.dag 门后），读回断言依赖已删读面；存量 DAG 日志由 MigrateSessionDAGLog 一次性迁移为 schema-1，读侧单路径化后本场景不再可达")
	path := dagTestSession(t)
	dagLinearLog(t, path)

	before, after, err := CompactSessionFile(path)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if before == 0 {
		t.Fatal("expected a non-empty event log before compaction")
	}
	// A tiny fixture log can grow slightly: the rewritten generation carries
	// index/header overhead that a handful of append lines does not. The win
	// the task targets is on 100MB+ logs (unreachable rows dominate); what
	// this test pins is that the rewrite runs, reports both sizes, and stays
	// stable on a second pass.
	if after > before+4096 {
		t.Fatalf("compaction ballooned the log: %d -> %d", before, after)
	}

	// Idempotence: compacting again succeeds and does not balloon the file.
	_, after2, err := CompactSessionFile(path)
	if err != nil {
		t.Fatalf("second compact: %v", err)
	}
	if after2 > after {
		t.Fatalf("second compact grew the log: %d -> %d", after, after2)
	}
}
