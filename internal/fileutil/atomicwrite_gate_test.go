package fileutil

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestNoHandRolledTmpStaging 是 tl1616 门禁断言（上限+新增即红，学
// store/session_path_spellings_test.go 判例）：生产 Go 代码禁止手搓固定名
// `<path>+".tmp"` 暂存写（os.WriteFile/WriteFile 系 + rename 三连）。正规件是
// 本包的 AtomicWriteFile / AtomicWriteFileStrict / AtomicCreateFile /
// StageAtomicWrite+PublishStagedWrite（唯一暂存名 + fsync + 失败清理 +
// 瞬态锁重试）。手搓三缺口：固定名并发互踩、无 fsync 掉电窗口、rename 失败
// 残留累积。
//
// 流式写入（无字节切片形态，如 zstd 压缩/JSONL 变换）拿不到上述字节切片
// API，等价形态 = os.CreateTemp 唯一暂存名 + 显式 Sync + 失败清理 + rename，
// 不含 `+".tmp"` 拼接，天然过闸。
func TestNoHandRolledTmpStaging(t *testing.T) {
	banned := regexp.MustCompile(`\+\s*"\.tmp"`)
	allowlist := map[string]string{
		// checkpoint 交易层：publish/backup 暂存名按事务 ID+序号唯一生成，
		// 写入走 secureWriteNew（沙箱根内 O_EXCL claim），是另一层专属安全
		// 语义（tl1616 勘察判定：非固定名互踩面，不迁）。
		filepath.Join("internal", "checkpoint", "transaction.go"): "transaction-local unique staging names via secureWriteNew (O_EXCL claim layer)",
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile))) // internal/fileutil -> repo root
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found from %s: %v", thisFile, err)
	}

	var violations []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".git" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if _, allowed := allowlist[rel]; allowed {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(b), "\n") {
			if idx := strings.Index(line, "//"); idx >= 0 {
				line = line[:idx]
			}
			if banned.MatchString(line) {
				violations = append(violations, rel+":"+strconv.Itoa(i+1)+" hand-rolls a fixed-name .tmp staging write — use fileutil.AtomicWriteFile / AtomicWriteFileStrict / AtomicCreateFile / StageAtomicWrite+PublishStagedWrite (tl1616 gate)")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	for _, v := range violations {
		t.Error(v)
	}
}
