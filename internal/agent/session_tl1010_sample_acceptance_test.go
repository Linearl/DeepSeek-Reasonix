package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 任务 1010 验收：用 20261019 ai-passport 实测样本做端到端验收。
// 设置 TL1010_SAMPLE_DIR 指向备份目录（六件全量）后运行；未设置时跳过，
// CI 与日常全量测试不受影响。样本不入库——它属于用户的会话数据。

func tl1010SampleDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("TL1010_SAMPLE_DIR")
	if dir == "" {
		t.Skip("TL1010_SAMPLE_DIR not set; sample acceptance runs only against the 20261019 backup")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("sample dir: %v", err)
	}
	return dir
}

func hashSampleFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		out[e.Name()] = hex.EncodeToString(sum[:])
	}
	return out
}

func copySample(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func sampleSessionPath(dir string) string {
	return filepath.Join(dir, "20261015-031109.286033400-session.jsonl")
}

// TestTL1010SampleLoadBoundedAndInteractive 验收 1：加载样本会话有界时间内
// 完成（先可交互的首屏尾窗路径 + 全量回放路径都不无限转圈）。
func TestTL1010SampleLoadBoundedAndInteractive(t *testing.T) {
	dir := copySample(t, tl1010SampleDir(t))
	sessionPath := sampleSessionPath(dir)

	deadline := time.Now().Add(120 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	started := time.Now()
	res, err := loadSessionTranscriptTail(ctx, sessionPath, defaultSessionReplayLimits, nil)
	if err != nil {
		t.Fatalf("first-paint tail load: %v", err)
	}
	if len(res.msgs) == 0 {
		t.Fatal("first paint produced no messages")
	}
	t.Logf("first paint: %d messages, tail_truncated=%v, %s", len(res.msgs), res.tailTruncated, time.Since(started).Round(time.Millisecond))

	started = time.Now()
	full, err := loadSessionTranscript(ctx, sessionPath, defaultSessionReplayLimits, nil)
	if err != nil {
		t.Fatalf("full load: %v", err)
	}
	if time.Now().After(deadline) {
		t.Fatal("load exceeded the bounded deadline")
	}
	t.Logf("full load: %d messages, %s", len(full.msgs), time.Since(started).Round(time.Millisecond))
}

// TestTL1010SampleReadings 验收 3/4 读数：样本必须读出物化断层与选中头
// 落后（events 最新 7087 条 vs 已物化 6611 条；rotation 曾把选中压回旧链）。
func TestTL1010SampleReadings(t *testing.T) {
	dir := copySample(t, tl1010SampleDir(t))
	sessionPath := sampleSessionPath(dir)
	st := dagReplay(t, sessionPath)

	// 需求 3：物化断层读数。
	ev, ok := detectMaterializationGap(sessionPath, st)
	if !ok {
		t.Fatal("the incident sample must surface a materialization gap")
	}
	if ev.Kind != HeadEventMaterializationLag || ev.Messages != 6611 || ev.OtherMessages != 7087 {
		t.Fatalf("gap readings = %d/%d kind=%s, want 6611/7087 materialization_lag", ev.Messages, ev.OtherMessages, ev.Kind)
	}

	// 需求 4：rotation 曾把选中钉在更旧更短的 01M51VB；新守卫下 rotation
	// 不再重钉它（buildRotatedSessionDAG 是纯快照，不写文件）。
	selected := st.selectedHead()
	domID, selLen, domLen, dominated := st.dominatingLiveHead(selected)
	if !dominated {
		t.Fatalf("selected head %s must be dominated in the sample", selected)
	}
	entries, err := buildRotatedSessionDAG(st, time.Now())
	if err != nil {
		t.Fatalf("build rotated: %v", err)
	}
	for _, e := range entries {
		if e.Type == sessionDAGTypeSelect {
			t.Fatalf("rotation re-pinned %s despite domination by %s (%d vs %d messages)", e.Head, domID, selLen, domLen)
		}
	}
	// 落后读数事件带两链计数。
	events := loadHeadEvents(st, selected, sessionPath)
	behind := false
	for _, e := range events {
		if e.Kind == HeadEventSelectedHeadBehind {
			behind = true
		}
	}
	if !behind {
		t.Fatalf("behind event missing from load events: %+v", events)
	}
}

// TestTL1010SampleZeroFileMutation 验收 3 的零文件改动断言：加载、检测、
// 版本列举、rotation 快照全程跑完之后，备份目录六个文件的内容 hash 逐一
// 不变——本任务全程不写会话历史数据的硬证据。
func TestTL1010SampleZeroFileMutation(t *testing.T) {
	dir := copySample(t, tl1010SampleDir(t))
	sessionPath := sampleSessionPath(dir)
	before := hashSampleFiles(t, dir)

	ctx := context.Background()
	if _, err := loadSessionTranscriptTail(ctx, sessionPath, defaultSessionReplayLimits, nil); err != nil {
		t.Fatalf("tail load: %v", err)
	}
	if _, err := loadSessionTranscript(ctx, sessionPath, defaultSessionReplayLimits, nil); err != nil {
		t.Fatalf("full load: %v", err)
	}
	if _, _, _, err := loadSessionMessages(sessionPath); err != nil {
		t.Fatalf("compat load: %v", err)
	}
	st := dagReplay(t, sessionPath)
	if _, ok := detectMaterializationGap(sessionPath, st); !ok {
		t.Fatal("gap must be detected")
	}
	if _, err := ListSessionHeads(sessionPath); err != nil {
		t.Fatalf("list heads: %v", err)
	}
	if _, err := buildRotatedSessionDAG(st, time.Now()); err != nil {
		t.Fatalf("rotation snapshot: %v", err)
	}

	after := hashSampleFiles(t, dir)
	if len(before) != len(after) {
		t.Fatalf("file set changed: %d -> %d", len(before), len(after))
	}
	for name, sum := range before {
		if after[name] != sum {
			t.Fatalf("session file %s was modified during the acceptance pass", name)
		}
	}
}
