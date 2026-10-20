package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
)

// 任务 1010 需求 3：物化断层只检测、提示，全程零文件改动。修复动作归
// 任务 1011 的右键入口。

// gapFixture writes a materialized .jsonl checkpoint with `materialized`
// lines, a stale meta sidecar, and an events log whose live chain holds
// eventMsgs messages with fresh activity — the incident shape: the sidecar
// stopped days ago while events kept going.
func gapFixture(t *testing.T, materialized, eventMsgs int) (dir, sessionPath string, st *sessionDAGState) {
	t.Helper()
	dir = t.TempDir()
	sessionPath = filepath.Join(dir, "s.jsonl")
	var b strings.Builder
	for i := 0; i < materialized; i++ {
		b.WriteString("{\"Role\":\"user\",\"Content\":\"x\"}\n")
	}
	if err := os.WriteFile(sessionPath, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-72 * time.Hour)
	// touchUpdated=false：显式写入过期的 UpdatedAt（现场里 meta 停在 10-16）。
	if err := UpdateBranchMeta(sessionPath, false, func(current *BranchMeta) error {
		*current = BranchMeta{ID: "s", UpdatedAt: base}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: base}}
	parent := ""
	// events 消息用近期活动时间：现场形状是「events 一直在写、meta 停在三天前」。
	for i := 0; i < eventMsgs; i++ {
		at := time.Now().Add(-time.Duration(eventMsgs-i) * time.Second)
		m := dagMsg(provider.RoleUser, "m", dagTestID(i))
		entries = append(entries, dagMessageEntry(t, SessionMainHead, parent, "t1", m, at))
		parent = m.ID
	}
	dagAppend(t, sessionPath, entries...)
	st = dagReplay(t, sessionPath)
	return dir, sessionPath, st
}

func dagTestID(i int) string {
	return "ID" + strings.Repeat("x", 4) + string(rune('A'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('0'+(i/26/26)%10)) + string(rune('0'+i%10))
}

func TestMaterializationGapDetected(t *testing.T) {
	_, sessionPath, st := gapFixture(t, 100, 200)
	ev, ok := detectMaterializationGap(sessionPath, st)
	if !ok {
		t.Fatal("a 100-message gap must be detected")
	}
	if ev.Kind != HeadEventMaterializationLag {
		t.Fatalf("kind = %q", ev.Kind)
	}
	if ev.Messages != 100 || ev.OtherMessages != 200 {
		t.Fatalf("readings = %d/%d, want 100/200", ev.Messages, ev.OtherMessages)
	}
}

func TestMaterializationGapHealthyQuiet(t *testing.T) {
	// 健康形状一：meta 与 events 同样新（新鲜门内）——不扫描、不提示。
	dir, sessionPath, st := gapFixture(t, 100, 200)
	if err := UpdateBranchMeta(sessionPath, false, func(current *BranchMeta) error {
		current.UpdatedAt = time.Now()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := detectMaterializationGap(sessionPath, st); ok {
		t.Fatal("a fresh sidecar must not surface a gap notice")
	}
	// 健康形状二：缺 .jsonl 检查点——无法比较，不提示。
	noJSONL := filepath.Join(t.TempDir(), "n.jsonl")
	entries := []sessionDAGEntry{{Type: sessionDAGTypeLog, Generation: 1, At: time.Now()}}
	entries = append(entries, dagMessageEntry(t, SessionMainHead, "", "t1", dagMsg(provider.RoleUser, "x", "U1"), time.Now()))
	dagAppend(t, noJSONL, entries...)
	st2 := dagReplay(t, noJSONL)
	if _, ok := detectMaterializationGap(noJSONL, st2); ok {
		t.Fatal("a missing checkpoint must not fabricate a gap")
	}
	_ = dir
}

func TestMaterializationGapBelowThresholdQuiet(t *testing.T) {
	_, sessionPath, st := gapFixture(t, 190, 200)
	if _, ok := detectMaterializationGap(sessionPath, st); ok {
		t.Fatal("save-lag noise under the threshold must stay quiet")
	}
}

func TestMaterializationGapDetectionWritesNothing(t *testing.T) {
	// 验收 3 的零文件改动断言：检测前后目录内每个文件的内容 hash 不变。
	dir, sessionPath, st := gapFixture(t, 100, 200)
	before := hashDirTree(t, dir)
	if _, ok := detectMaterializationGap(sessionPath, st); !ok {
		t.Fatal("fixture must detect")
	}
	after := hashDirTree(t, dir)
	if len(before) != len(after) {
		t.Fatalf("file set changed: %d -> %d", len(before), len(after))
	}
	for name, sum := range before {
		if after[name] != sum {
			t.Fatalf("file %s changed during detection", name)
		}
	}
}

func hashDirTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		out[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
