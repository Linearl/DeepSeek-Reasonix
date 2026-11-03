package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/store"
)

// tl1371 验收（读侧自愈 + fork 归属）：recovery fork/转正写坏的 pinned-context
// （sessionId 残留副本 -recovery-<16hex> 归属）不再让正主会话每轮被 belongs-to
// 校验拒绝——读取时剥离副本后缀同根即容忍并就地原子回写正主归属；剥离后仍不
// 同根 = 真串台，维持拒绝。fork 侧写对的前提不变：副本文件归属 = 副本 id，正
// 主文件一字不动。
//
// stem 用带点现场同形（模型名 v2.6）：BranchID 只剥 .jsonl，内部点必须原样保留，
// 剥离判定才能在真实 id 上成立。

const tl1371MainStem = "20261103-171530.123-mimo-v2.6-flash"
const tl1371CopyID = tl1371MainStem + "-recovery-21b887801d82d805" // 20261103 新11 副本后缀同形

func tl1371WritePinnedRaw(t *testing.T, path string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func tl1371PinnedRaw(t *testing.T, sessionID string, files []string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"schemaVersion": 1, "sessionId": sessionID, "files": files})
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func tl1371ReadPinnedBytes(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ① 损坏归属（副本后缀、剥离后同根）读 → 容忍 + 就地回写正主归属 + 读成功。
func Test1371PinnedContextSelfHealRewritesRecoveredOwner(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, tl1371MainStem+".jsonl")
	sidecar := store.SessionPinnedContext(sessionPath)
	tl1371WritePinnedRaw(t, sidecar, tl1371PinnedRaw(t, tl1371CopyID, []string{"docs/b.md", "docs/a.md", "docs/a.md"}))

	state, err := loadPinnedContextState(sessionPath)
	if err != nil {
		t.Fatalf("recovery-suffix owner must be tolerated and healed, got err: %v", err)
	}
	if state.SessionID != tl1371MainStem {
		t.Fatalf("state sessionId = %q, want the owning session %q", state.SessionID, tl1371MainStem)
	}
	if !reflect.DeepEqual(state.Files, []string{"docs/a.md", "docs/b.md"}) {
		t.Fatalf("files = %v, want the copy's list normalized", state.Files)
	}

	gotID, gotFiles, _ := readTL1371Sidecar(t, sidecar)
	if gotID != tl1371MainStem {
		t.Fatalf("sidecar sessionId on disk = %q, want healed to %q", gotID, tl1371MainStem)
	}
	if !reflect.DeepEqual(gotFiles, []string{"docs/b.md", "docs/a.md", "docs/a.md"}) {
		t.Fatalf("healed sidecar files = %v, want the stored list preserved order-for-order", gotFiles)
	}
}

// ② 真串台（剥离后仍不同根）→ 维持拒绝，文件一字不动。
func Test1371PinnedContextSelfHealStillRejectsForeignOwner(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, tl1371MainStem+".jsonl")
	sidecar := store.SessionPinnedContext(sessionPath)
	for _, tc := range []struct {
		name string
		id   string
	}{
		{name: "plain foreign id", id: "another-session"},
		{name: "foreign recovery id", id: "another-session-recovery-21b887801d82d805"},
		{name: "malformed suffix", id: tl1371MainStem + "-recovery-notahex"},
		{name: "empty id", id: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := tl1371PinnedRaw(t, tc.id, []string{"x.md"})
			tl1371WritePinnedRaw(t, sidecar, raw)
			_, err := loadPinnedContextState(sessionPath)
			if err == nil || !strings.Contains(err.Error(), "belongs to session") {
				t.Fatalf("foreign owner %q must stay rejected, got err %v", tc.id, err)
			}
			if got := tl1371ReadPinnedBytes(t, sidecar); string(got) != string(raw) {
				t.Fatalf("rejected sidecar must stay byte-identical: got %s want %s", got, raw)
			}
		})
	}
}

// ③ 自愈后再次读 = 正常路径：归属已对齐，读成功且盘上字节稳定（不再反复回写）。
func Test1371PinnedContextSecondReadAfterHealIsNormalPath(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, tl1371MainStem+".jsonl")
	sidecar := store.SessionPinnedContext(sessionPath)
	tl1371WritePinnedRaw(t, sidecar, tl1371PinnedRaw(t, tl1371CopyID, []string{"keep.md"}))

	if _, err := loadPinnedContextState(sessionPath); err != nil {
		t.Fatalf("first (healing) read: %v", err)
	}
	healedBytes := tl1371ReadPinnedBytes(t, sidecar)

	state, err := loadPinnedContextState(sessionPath)
	if err != nil {
		t.Fatalf("second read must be the normal path, got err: %v", err)
	}
	if state.SessionID != tl1371MainStem || !reflect.DeepEqual(state.Files, []string{"keep.md"}) {
		t.Fatalf("second read state = %+v", state)
	}
	if got := tl1371ReadPinnedBytes(t, sidecar); string(got) != string(healedBytes) {
		t.Fatalf("healed file must stay stable across reads: got %s want %s", got, healedBytes)
	}
}

// ④ 回写失败不影响本次读取（fail-open）：注入写失败，读取仍成功返回正主归属，
// 盘上文件保持原样，下一轮再自愈。
func Test1371PinnedContextHealWriteFailureDoesNotFailTheRead(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, tl1371MainStem+".jsonl")
	sidecar := store.SessionPinnedContext(sessionPath)
	damaged := tl1371PinnedRaw(t, tl1371CopyID, []string{"keep.md"})
	tl1371WritePinnedRaw(t, sidecar, damaged)

	restore := healPinnedContextOwnerFn
	healPinnedContextOwnerFn = func(path, ownerID string) error {
		return errors.New("injected heal write failure")
	}
	defer func() { healPinnedContextOwnerFn = restore }()

	state, err := loadPinnedContextState(sessionPath)
	if err != nil {
		t.Fatalf("a failed heal write must not fail the read, got err: %v", err)
	}
	if state.SessionID != tl1371MainStem || !reflect.DeepEqual(state.Files, []string{"keep.md"}) {
		t.Fatalf("tolerated read state = %+v", state)
	}
	if got := tl1371ReadPinnedBytes(t, sidecar); string(got) != string(damaged) {
		t.Fatalf("failed heal must leave the file untouched: got %s want %s", got, damaged)
	}
}

// ⑤ 无 sessionId 字段的退化文件：按现状跳过（fail-open 不新增行为）——解码到
// 预置本会话 id 的 state 上等于「归属=本会话」，读取按现状接受，不发明新归属、
// 不触发自愈、文件一字不动。
func Test1371PinnedContextFieldlessDocKeepsStatusQuo(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, tl1371MainStem+".jsonl")
	sidecar := store.SessionPinnedContext(sessionPath)
	raw := []byte("{\"schemaVersion\":1,\"files\":[]}\n")
	tl1371WritePinnedRaw(t, sidecar, raw)

	state, err := loadPinnedContextState(sessionPath)
	if err != nil {
		t.Fatalf("fieldless doc must keep the status-quo acceptance, got err %v", err)
	}
	if state.SessionID != tl1371MainStem || len(state.Files) != 0 {
		t.Fatalf("fieldless doc state = %+v, want the pre-seeded current-session state", state)
	}
	if got := tl1371ReadPinnedBytes(t, sidecar); string(got) != string(raw) {
		t.Fatalf("fieldless doc must stay byte-identical (no heal write): got %s want %s", got, raw)
	}
}

// fork 侧前提（任务书 ①②）：fork 拷贝后副本文件归属 = 副本 id（副本读自己的
// 不报 belongs-to），正主文件归属不变（字节逐字相等）。
func Test1371ForkCopyAttributionTargetsCopyIDAndLeavesOriginalUntouched(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, tl1371MainStem+".jsonl")
	copyPath := filepath.Join(dir, tl1371CopyID+".jsonl")
	if err := savePinnedContextState(original, []string{"docs/b.md", "docs/a.md"}); err != nil {
		t.Fatal(err)
	}
	originalBytes := tl1371ReadPinnedBytes(t, store.SessionPinnedContext(original))

	if err := copyPinnedContextState(original, copyPath); err != nil {
		t.Fatalf("fork copy: %v", err)
	}

	copiedID, copiedFiles, _ := readTL1371Sidecar(t, store.SessionPinnedContext(copyPath))
	if copiedID != tl1371CopyID {
		t.Fatalf("copy pinned sessionId = %q, want the copy's own id %q", copiedID, tl1371CopyID)
	}
	if !reflect.DeepEqual(copiedFiles, []string{"docs/a.md", "docs/b.md"}) {
		t.Fatalf("copy pinned files = %v, want the original's normalized list", copiedFiles)
	}
	// 副本读自己的那一份：不报 belongs-to（fork 后隔离期两边各读各的）。
	if state, err := loadPinnedContextState(copyPath); err != nil {
		t.Fatalf("copy must read its own sidecar without a belongs-to error: %v", err)
	} else if !reflect.DeepEqual(state.Files, []string{"docs/a.md", "docs/b.md"}) {
		t.Fatalf("copy read state files = %v", state.Files)
	}

	// 原会话那份：归属不变，字节逐字相等。
	if got := tl1371ReadPinnedBytes(t, store.SessionPinnedContext(original)); string(got) != string(originalBytes) {
		t.Fatalf("original pinned must stay byte-identical across the fork copy: got %s want %s", got, originalBytes)
	}
	if gotID, _, _ := readTL1371Sidecar(t, store.SessionPinnedContext(original)); gotID != tl1371MainStem {
		t.Fatalf("original pinned sessionId = %q, want %q untouched", gotID, tl1371MainStem)
	}
}

func readTL1371Sidecar(t *testing.T, path string) (sessionID string, files []string, schemaVersion int) {
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

// 剥离判定直接单测：同根副本 id → 同根；外来/退化 → 不同根或 not-ok。
func Test1371StoredOwnerRootPeel(t *testing.T) {
	const hexSuffix = "21b887801d82d805" // 20261103 新11 副本后缀同形（16 lower-hex）
	if root, ok := recoveryRootIDForStoredPinnedOwner(tl1371CopyID); !ok || root != tl1371MainStem {
		t.Fatalf("peel(%q) = (%q, %v), want root %q", tl1371CopyID, root, ok, tl1371MainStem)
	}
	nested := tl1371MainStem + "-recovery-aaaa55ccbbddeeff-recovery-" + hexSuffix
	if root, ok := recoveryRootIDForStoredPinnedOwner(nested); !ok || root != tl1371MainStem {
		t.Fatalf("nested peel(%q) = (%q, %v), want root %q", nested, root, ok, tl1371MainStem)
	}
	if _, ok := recoveryRootIDForStoredPinnedOwner("another-session"); ok {
		t.Fatal("foreign id without recovery suffix must not peel")
	}
	if _, ok := recoveryRootIDForStoredPinnedOwner(""); ok {
		t.Fatal("empty stored id must not peel")
	}
	if _, ok := recoveryRootIDForStoredPinnedOwner(tl1371MainStem + "-recovery-SHORT"); ok {
		t.Fatal("malformed suffix must not peel")
	}
}
