package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1371 验收：recovery 副本转正（tl1352 A）把副本的 pinned-context rename 上
// 主名时，归属（sessionId 字段）必须同步改写到主 stem。rename 只换文件名不换
// 字节——不改写的话，转正后的正主会话每轮读 pinned context 都被 belongs-to
// 校验拒绝（20261103 新11 现场：sessionId 残留 -recovery-21b887801d82d805）。
// consolidation 链 2026-09-13 已配同一改写（promoteRecoveryCopyToMain）；本件
// 把该机制接到 tl1352 转正链并共用同一个助手。

const tl1371CopySuffix = "21b887801d82d805" // 20261103 现场副本后缀同形（16 lower-hex）

func writeTL1371Pinned(t *testing.T, path, sessionID string, files []string) {
	t.Helper()
	doc := map[string]any{"schemaVersion": 1, "sessionId": sessionID, "files": files}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTL1371Pinned(t *testing.T, path string) (sessionID string, files []string, schemaVersion int) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SchemaVersion int      `json:"schemaVersion"`
		SessionID     string   `json:"sessionId"`
		Files         []string `json:"files"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return doc.SessionID, doc.Files, doc.SchemaVersion
}

// 核心腿：转正后主 stem 的 pinned-context 归属 = 主 id，files 原样保留；
// 转正前副本文件归属 = 副本 id（fork 时 desktop copyPinnedContextState 重建的
// 契约，此处钉住该前提）。
func Test1371PromotionRewritesPinnedContextOwner(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "topic.jsonl")
	copyID := "topic-recovery-" + tl1371CopySuffix
	copyPath := filepath.Join(dir, copyID+".jsonl")
	for _, p := range []string{main, copyPath} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copyPinned := store.SessionPinnedContext(copyPath)
	writeTL1371Pinned(t, copyPinned, copyID, []string{"docs/b.md", "docs/a.md"})

	// fork 期不变量：副本文件归属 = 副本 id（副本读自己那一份的前提）。
	if got, files, _ := readTL1371Pinned(t, copyPinned); got != copyID || len(files) != 2 {
		t.Fatalf("pre-promotion copy pinned = (%q, %v), want sessionId %q with 2 files", got, files, copyID)
	}

	if _, err := PromoteRecoveryBranchToMain(copyPath, main); err != nil {
		t.Fatalf("promote: %v", err)
	}

	mainPinned := store.SessionPinnedContext(main)
	gotID, gotFiles, schema := readTL1371Pinned(t, mainPinned)
	if gotID != BranchID(main) {
		t.Fatalf("promoted pinned sessionId = %q, want main stem %q", gotID, BranchID(main))
	}
	if !reflect.DeepEqual(gotFiles, []string{"docs/b.md", "docs/a.md"}) {
		t.Fatalf("promoted pinned files = %v, want the copy's list preserved order-for-order (identity-only rewrite)", gotFiles)
	}
	if schema != 1 {
		t.Fatalf("promoted pinned schemaVersion = %d, want 1", schema)
	}
	if _, err := os.Lstat(copyPinned); !os.IsNotExist(err) {
		t.Fatalf("copy pinned sidecar must be gone after the rename, err=%v", err)
	}
}

// 跳过形态①：副本没有 pinned-context 时，主会话自己那份一字不动（归属仍是
// 主 id，字节逐字相等）——原会话的 pinned context 不被副本触碰。
func Test1371PromotionWithoutCopyPinnedLeavesMainPinnedUntouched(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "topic.jsonl")
	copyPath := filepath.Join(dir, "topic-recovery-"+tl1371CopySuffix+".jsonl")
	for _, p := range []string{main, copyPath} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mainPinned := store.SessionPinnedContext(main)
	writeTL1371Pinned(t, mainPinned, "topic", []string{"keep.md"})
	before, err := os.ReadFile(mainPinned)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := PromoteRecoveryBranchToMain(copyPath, main); err != nil {
		t.Fatalf("promote: %v", err)
	}
	after, err := os.ReadFile(mainPinned)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("main pinned must stay byte-identical when the copy owns none: before=%s after=%s", before, after)
	}
	if gotID, _, _ := readTL1371Pinned(t, mainPinned); gotID != "topic" {
		t.Fatalf("main pinned sessionId = %q, want the original attribution untouched", gotID)
	}
}

// 跳过形态②：副本 pinned-context 没有 sessionId 字段（退化文件）——转正落位
// 后按字节原样保留，不发明归属（fail-open 不新增行为）。
func Test1371PromotionFieldlessPinnedSkipsRewrite(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "topic.jsonl")
	copyPath := filepath.Join(dir, "topic-recovery-"+tl1371CopySuffix+".jsonl")
	for _, p := range []string{main, copyPath} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copyPinned := store.SessionPinnedContext(copyPath)
	raw := []byte("{\"schemaVersion\":1,\"files\":[\"x.md\"]}\n")
	if err := os.WriteFile(copyPinned, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := PromoteRecoveryBranchToMain(copyPath, main); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, err := os.ReadFile(store.SessionPinnedContext(main))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatalf("fieldless pinned must be renamed untouched: got %s want %s", got, raw)
	}
}

// 双方都有时：副本（fork 后持续活跃的一侧）列表落主名、归属改写为主 stem；
// 被逐下的正主旧 pinned 必须先进白名单归档（tl1371 白名单登记），零丢失。
func Test1371PromotionArchivesDisplacedMainPinnedContext(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "topic.jsonl")
	copyID := "topic-recovery-" + tl1371CopySuffix
	copyPath := filepath.Join(dir, copyID+".jsonl")
	for _, p := range []string{main, copyPath} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeTL1371Pinned(t, store.SessionPinnedContext(main), "topic", []string{"main-only.md"})
	writeTL1371Pinned(t, store.SessionPinnedContext(copyPath), copyID, []string{"copy-new.md"})

	if _, err := PromoteRecoveryBranchToMain(copyPath, main); err != nil {
		t.Fatalf("promote: %v", err)
	}

	gotID, gotFiles, _ := readTL1371Pinned(t, store.SessionPinnedContext(main))
	if gotID != BranchID(main) || !reflect.DeepEqual(gotFiles, []string{"copy-new.md"}) {
		t.Fatalf("promoted pinned = (%q, %v), want main attribution with the copy's newer list", gotID, gotFiles)
	}
	matches, err := filepath.Glob(filepath.Join(dir, "topic.pre-repair-*"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("pre-promotion backup of the displaced main expected: %v err=%v", matches, err)
	}
	archivedID, archivedFiles, _ := readTL1371Pinned(t, filepath.Join(matches[0], "topic.pinned-context.json"))
	if archivedID != "topic" || !reflect.DeepEqual(archivedFiles, []string{"main-only.md"}) {
		t.Fatalf("archived pinned = (%q, %v), want the displaced main copy preserved whole", archivedID, archivedFiles)
	}
}

// 助手契约（三方共用：consolidation / tl1352 转正 / desktop 读侧自愈）：
// 无 sessionId 字段 → 跳过不写；坏 JSON → 报错（调用方按先例丢弃）；
// 正常 → 只动归属，其余字段保留；写入走原子置换。
func Test1371RewritePinnedContextSessionIDForms(t *testing.T) {
	dir := t.TempDir()

	t.Run("skips fieldless doc", func(t *testing.T) {
		path := filepath.Join(dir, "fieldless.pinned-context.json")
		raw := []byte("{\"schemaVersion\":1,\"files\":[\"x.md\"]}\n")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := RewritePinnedContextSessionID(path, "owner"); err != nil {
			t.Fatalf("fieldless doc must skip, got err %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(raw) {
			t.Fatalf("fieldless doc must stay byte-identical: got %s", got)
		}
	})

	t.Run("rejects corrupt doc loudly", func(t *testing.T) {
		path := filepath.Join(dir, "corrupt.pinned-context.json")
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := RewritePinnedContextSessionID(path, "owner"); err == nil {
			t.Fatal("corrupt doc must return an error so the caller can drop it")
		}
	})

	t.Run("rewrites only the identity", func(t *testing.T) {
		path := filepath.Join(dir, "normal.pinned-context.json")
		writeTL1371Pinned(t, path, "stale-recovery-"+tl1371CopySuffix, []string{"docs/b.md", "docs/a.md"})
		if err := RewritePinnedContextSessionID(path, "fresh"); err != nil {
			t.Fatalf("rewrite: %v", err)
		}
		gotID, gotFiles, schema := readTL1371Pinned(t, path)
		if gotID != "fresh" || schema != 1 || !reflect.DeepEqual(gotFiles, []string{"docs/b.md", "docs/a.md"}) {
			t.Fatalf("rewritten doc = (%q, %v, %d), want identity-only change", gotID, gotFiles, schema)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(string(raw), "\n") {
			t.Fatal("rewritten doc must keep the trailing newline shape")
		}
	})

	t.Run("missing file is an error", func(t *testing.T) {
		if err := RewritePinnedContextSessionID(filepath.Join(dir, "absent.json"), "owner"); !os.IsNotExist(err) {
			t.Fatalf("absent file err = %v, want not-exist", err)
		}
	})
}
