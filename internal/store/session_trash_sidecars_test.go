package store

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tl1442-ext 清单源钉：SessionTrashSidecars 是删除/换位链侧车全集的唯一事实
// 源。这里钉它的构成（static 全集 + 实存的 pre-repair glob 实例）与 static
// 形态的确定性（identity-swap 按位配对的前提：同会话两个拼写产出等长同序）。
func TestSessionTrashSidecarsComposeStaticAndPreRepairGlob(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260923-045044.233465200-collab.jsonl")
	stem := strings.TrimSuffix(path, SessionTranscriptSuffix)
	for _, stamp := range []string{"20261102T025757", "20261105T052131"} {
		if err := os.MkdirAll(stem+".pre-repair-"+stamp, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	full := SessionTrashSidecars(path)
	static := SessionStaticTrashSidecars(path)
	want := append(append([]string(nil), static...),
		stem+".pre-repair-20261102T025757",
		stem+".pre-repair-20261105T052131",
	)
	if !slices.Equal(full, want) {
		t.Fatalf("SessionTrashSidecars drifted:\n got: %v\nwant: %v", full, want)
	}
	for _, artifact := range full {
		if !strings.HasPrefix(filepath.Base(artifact), strings.TrimSuffix(filepath.Base(path), SessionTranscriptSuffix)+".") {
			t.Fatalf("inventory entry %q does not carry the session stem prefix", artifact)
		}
	}
	// 守卫面：锁三件与 cleanup-pending 是运行时句柄，永不进清单。
	handles := map[string]bool{
		filepath.Base(SessionLockFile(path)):       true,
		filepath.Base(SessionLeaseLock(path)):      true,
		filepath.Base(SessionLeaseInfo(path)):      true,
		filepath.Base(SessionCleanupPending(path)): true,
	}
	for _, artifact := range full {
		if handles[filepath.Base(artifact)] {
			t.Fatalf("runtime handle %q leaked into the sidecar inventory", artifact)
		}
	}
}

// static 形态确定性：两条不同 stem 的清单必须等长同位（tl1390 两步换位按
// 索引配对的前提；glob 形态两侧数量可以不同，所以换位面必须用 static）。
func TestSessionStaticTrashSidecarsDeterministicPairing(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "topic.jsonl")
	copy := filepath.Join(dir, "topic-recovery-0123456789abcdef.jsonl")
	mainList := SessionStaticTrashSidecars(main)
	copyList := SessionStaticTrashSidecars(copy)
	if len(mainList) != len(copyList) {
		t.Fatalf("static lists diverge in length: main %d vs copy %d — identity-swap pairing would misalign", len(mainList), len(copyList))
	}
	mainStem := strings.TrimSuffix(filepath.Base(main), SessionTranscriptSuffix)
	copyStem := strings.TrimSuffix(filepath.Base(copy), SessionTranscriptSuffix)
	for i := range mainList {
		mainRel := strings.TrimPrefix(filepath.Base(mainList[i]), mainStem)
		copyRel := strings.TrimPrefix(filepath.Base(copyList[i]), copyStem)
		if mainRel != copyRel {
			t.Fatalf("static lists diverge at %d: main rel %q vs copy rel %q", i, mainRel, copyRel)
		}
	}
}
