package boot

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"reasonix/internal/memory"
	"reasonix/internal/skill"
)

// tl1229 一次基线读数的量具（REASONIX_TL1229_BASELINE=1 门控，CI 跳过）：
// 对真实记忆目录做只读分段测量——memory.Load / memory.Compose /
// BuildRecallIndex / skills 发现。绝不写真实目录（MigrateV2 与
// MaterializeLegacyPinned 会落盘，不在本量具里；那两段的生产读数来自
// boot: prompt segments 日志行本身）。
//
// 运行（读数见 tl1228/1229 交付回执）：
//
//	REASONIX_TL1229_BASELINE=1 go test ./internal/boot/ -run TestPromptSegmentsBaseline -v
//
// 可用 REASONIX_TL1229_MEMORY_DIR 指向别的记忆目录（默认
// %AppData%/reasonix/memory），REASONIX_TL1229_SKILL_ROOT 指向 skills 根。
func TestPromptSegmentsBaseline(t *testing.T) {
	if os.Getenv("REASONIX_TL1229_BASELINE") != "1" {
		t.Skip("set REASONIX_TL1229_BASELINE=1 for the real-directory baseline reading")
	}
	memDir := os.Getenv("REASONIX_TL1229_MEMORY_DIR")
	if memDir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			t.Skipf("no user config dir: %v", err)
		}
		memDir = filepath.Join(base, "reasonix", "memory")
	}
	if st, err := os.Stat(memDir); err != nil || !st.IsDir() {
		t.Skipf("memory dir %s missing", memDir)
	}
	root := os.Getenv("REASONIX_TL1229_SKILL_ROOT")
	if root == "" {
		root, _ = os.Getwd()
	}

	// memory.Load: 目录发现 + auto-memory 索引串。
	started := time.Now()
	mem := memory.Load(memory.Options{CWD: root, UserDir: memDir})
	loadDur := time.Since(started)

	// memory.Compose: # Memory 段组装（prompt 阶段的大头嫌疑）。
	started = time.Now()
	composed := memory.Compose("BASE", mem)
	composeDur := time.Since(started)
	memSection := len(composed) - len("BASE")

	// BuildRecallIndex: 召回池读入 + tokenize（懒构建、首次召回付费）。
	store := memory.StoreFor(memDir, root)
	started = time.Now()
	memory.BuildRecallIndex(store)
	recallDur := time.Since(started)
	docs, terms := 0, 0
	if stats := memory.LastRecallIndexBuildStats(); stats != nil {
		docs, terms = stats.Docs, stats.Terms
	}

	// skills 发现（双 store 同款共享 parse cache 的第一遍）。
	started = time.Now()
	skillStore := skill.New(skill.Options{ProjectRoot: root, Stderr: io.Discard})
	skills := skillStore.List()
	skillDur := time.Since(started)

	t.Logf("TL1229 baseline memory_dir=%s", memDir)
	t.Logf("TL1229 baseline memory_load=%s memory_compose=%s memory_section_chars=%d",
		loadDur.Round(time.Millisecond), composeDur.Round(time.Millisecond), memSection)
	t.Logf("TL1229 baseline recall_index_build=%s docs=%d terms=%d",
		recallDur.Round(time.Millisecond), docs, terms)
	t.Logf("TL1229 baseline skills_list=%s skills=%d", skillDur.Round(time.Millisecond), len(skills))
	if memSection <= 0 && docs == 0 {
		t.Logf("TL1229 baseline: empty memory pool at %s", memDir)
	}
}
