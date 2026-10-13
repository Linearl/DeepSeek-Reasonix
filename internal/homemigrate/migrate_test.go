package homemigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
)

// sandboxedExecutor returns an Executor whose pointer and lock files live in a
// temp sandbox, so tests never touch the real per-user Reasonix home.
func sandboxedExecutor(t *testing.T) *Executor {
	t.Helper()
	sandbox := t.TempDir()
	exec := NewExecutor()
	exec.PointerPath = filepath.Join(sandbox, "home-pointer.json")
	exec.LockPath = filepath.Join(sandbox, "home-migrate.lock")
	return exec
}

// seedHome builds a small but representative Reasonix home: a config file, a
// nested skills tree, and the credential .env.
func seedHome(t *testing.T, home string) {
	t.Helper()
	files := map[string]string{
		"config.toml":            "default_model = \"test\"\n",
		".env":                   "ANTHROPIC_AUTH_TOKEN=sk-test-846\nOPENAI_API_KEY=sk-openai-846\n",
		"settings.json":          "{}\n",
		"skills/demo/SKILL.md":   "---\nname: demo\n---\nhello\n",
		"logs/desktop/log.txt":   "boot\n",
		"sessions/abc.jsonl":     "{}\n{}\n{}\n",
		"projects/p/s/sess.json": "{\"a\":1}\n",
	}
	for rel, content := range files {
		path := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// ackAll is the wizard's full-risk acknowledgement (all six ids).
func ackAll() []string {
	var ids []string
	for _, risk := range AllRisks() {
		ids = append(ids, string(risk.ID))
	}
	return ids
}

// 验收 3 的 Go 侧对位：六条风险必须全部存在、id 唯一、severity 合法。
func TestAllRisksCoversTheSixContractRisks(t *testing.T) {
	risks := AllRisks()
	if len(risks) != 6 {
		t.Fatalf("AllRisks() has %d entries, the task-846 contract pins exactly 6", len(risks))
	}
	want := map[RiskID]bool{
		RiskDualHomeSplit:          false,
		RiskPointerCorruptFallback: false,
		RiskInterruptedMigration:   false,
		RiskConcurrentInstance:     false,
		RiskOnedriveLock:           false,
		RiskCredentialEnv:          false,
	}
	for _, risk := range risks {
		if _, known := want[risk.ID]; !known {
			t.Fatalf("unexpected risk id %q", risk.ID)
		}
		if want[risk.ID] {
			t.Fatalf("duplicate risk id %q", risk.ID)
		}
		want[risk.ID] = true
		if risk.Severity != "warn" && risk.Severity != "error" {
			t.Fatalf("risk %q has severity %q, want warn|error", risk.ID, risk.Severity)
		}
		if !strings.HasPrefix(risk.DetailKey, "settings.homePointer.risk.") {
			t.Fatalf("risk %q detail key %q is not a settings.homePointer.risk.* locale key", risk.ID, risk.DetailKey)
		}
	}
	for id, seen := range want {
		if !seen {
			t.Fatalf("contract risk %q missing from AllRisks()", id)
		}
	}
}

func TestRequireAllRisksAcknowledged(t *testing.T) {
	all := ackAll()
	if err := RequireAllRisksAcknowledged(all); err != nil {
		t.Fatalf("full ack should pass: %v", err)
	}
	if err := RequireAllRisksAcknowledged(all[:5]); !errors.Is(err, ErrRisksNotAcknowledged) {
		t.Fatalf("partial ack should fail with ErrRisksNotAcknowledged, got %v", err)
	}
	if err := RequireAllRisksAcknowledged(append(append([]string{}, all...), "made-up")); err == nil || !strings.Contains(err.Error(), "unknown risk id") {
		t.Fatalf("unknown ack id should be refused, got %v", err)
	}
	if err := RequireAllRisksAcknowledged(nil); !errors.Is(err, ErrRisksNotAcknowledged) {
		t.Fatalf("empty ack should fail, got %v", err)
	}
}

func TestValidatePlan(t *testing.T) {
	source := filepath.Join(t.TempDir(), "src-home")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("same path refused", func(t *testing.T) {
		if _, err := ValidatePlan(source, source); !errors.Is(err, ErrTargetUnusable) {
			t.Fatalf("want ErrTargetUnusable, got %v", err)
		}
	})
	t.Run("nested target refused", func(t *testing.T) {
		inside := filepath.Join(source, "sub", "home")
		if _, err := ValidatePlan(source, inside); !errors.Is(err, ErrTargetUnusable) {
			t.Fatalf("nested target must be refused, got %v", err)
		}
	})
	t.Run("relative target refused", func(t *testing.T) {
		if _, err := ValidatePlan(source, "relative/home"); err == nil {
			t.Fatal("relative target must be refused")
		}
	})
	t.Run("non-empty target refused", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "occupied")
		if err := os.MkdirAll(target, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidatePlan(source, target); !errors.Is(err, ErrTargetUnusable) {
			t.Fatalf("occupied target must be refused, got %v", err)
		}
	})
	t.Run("missing target allowed", func(t *testing.T) {
		target := filepath.Join(t.TempDir(), "fresh")
		warnings, err := ValidatePlan(source, target)
		if err != nil {
			t.Fatalf("fresh target should validate: %v", err)
		}
		if len(warnings) != 0 {
			t.Fatalf("no warnings expected, got %v", warnings)
		}
	})
	t.Run("onedrive target warns", func(t *testing.T) {
		base := t.TempDir()
		target := filepath.Join(base, "OneDrive Personal", "reasonix-home")
		warnings, err := ValidatePlan(source, target)
		if err != nil {
			t.Fatalf("onedrive target may proceed with a warning: %v", err)
		}
		if len(warnings) != 1 || warnings[0] != "target-path-onedrive" {
			t.Fatalf("want exactly the onedrive warning, got %v", warnings)
		}
	})
}

// 验收 2 的主链路：copy → 校验 → 原子切指针 全流程成功 + .env 搬运 + ACL 重做。
func TestExecuteFullTransaction(t *testing.T) {
	exec := sandboxedExecutor(t)
	source := filepath.Join(t.TempDir(), "home")
	seedHome(t, source)
	target := filepath.Join(t.TempDir(), "migrated")

	// The pointer starts at the source (as if the user had migrated once
	// before) to prove the swap REPLACES it.
	if err := config.WriteHomePointerFile(exec.pointerPath(), source); err != nil {
		t.Fatal(err)
	}

	var copied int
	phases := map[Phase]bool{}
	final, detail := exec.Execute(Plan{Source: source, Target: target}, ackAll(), func(p Progress) {
		if p.Files > copied {
			copied = p.Files
		}
		phases[p.Phase] = true
	})
	if final != PhaseDone {
		t.Fatalf("migration ended in %q: %s", final, detail)
	}
	if !phases[PhaseVerify] || !phases[PhaseSwitch] {
		t.Fatalf("progress missed phases: %v", phases)
	}

	// Pointer now names the target.
	pointer, err := config.ReadHomePointerFile(exec.pointerPath())
	if err != nil || pointer == nil {
		t.Fatalf("pointer unreadable after migration: %v", err)
	}
	if !strings.EqualFold(pointer.Home, target) && pointer.Home != target {
		t.Fatalf("pointer home = %q, want %q", pointer.Home, target)
	}

	// Every seeded file arrived intact.
	for _, rel := range []string{"config.toml", ".env", "settings.json", "skills/demo/SKILL.md", "logs/desktop/log.txt", "sessions/abc.jsonl", "projects/p/s/sess.json"} {
		dst := filepath.Join(target, filepath.FromSlash(rel))
		if _, err := os.Stat(dst); err != nil {
			t.Fatalf("copied file missing: %s (%v)", rel, err)
		}
	}
	if got := readAll(t, filepath.Join(target, ".env")); !strings.Contains(got, "sk-test-846") {
		t.Fatalf("credential .env content lost: %q", got)
	}

	// The lock was released and the source is untouched.
	if _, err := os.Stat(exec.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("migration lock survived completion: %v", err)
	}
	if got := readAll(t, filepath.Join(source, ".env")); !strings.Contains(got, "sk-test-846") {
		t.Fatalf("source .env must stay (risk 6: cleanup is the user's call)")
	}

	// Bookkeeping files never travel.
	if _, err := os.Stat(filepath.Join(target, config.HomePointerFileName)); !os.IsNotExist(err) {
		t.Fatalf("pointer file must not be copied into the target home: %v", err)
	}

	// 验收 4 的 windows 分支：目标 .env 的 ACL 是重做的保护型 DACL。
	assertEnvACLRedone(t, filepath.Join(target, ".env"))
}

// assertEnvACLRedone checks the platform-specific redo contract.
func assertEnvACLRedone(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		sddl, protected, err := EnvACLOnDisk(path)
		if err != nil {
			t.Fatalf("EnvACLOnDisk: %v", err)
		}
		if !protected {
			t.Fatalf("copied .env DACL is not protected from inheritance: %s", sddl)
		}
		if !strings.HasPrefix(sddl, "D:P") {
			t.Fatalf("copied .env SDDL should start with a protected DACL: %s", sddl)
		}
		if strings.Count(sddl, "(A;") != 2 {
			t.Fatalf("copied .env should carry exactly two ACEs (user+SYSTEM): %s", sddl)
		}
		if !strings.Contains(sddl, ";SY)") && !strings.Contains(sddl, "SY") {
			t.Fatalf("SYSTEM grant missing: %s", sddl)
		}
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("copied .env mode = %o, want 600", perm)
	}
}

// RedoEnvACL itself is directly testable on a loose file.
func TestRedoEnvACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("KEY=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RedoEnvACL(path); err != nil {
		t.Fatalf("RedoEnvACL: %v", err)
	}
	assertEnvACLRedone(t, path)
}

// CopyEnvFile never carries the source's permissions contract is asserted by
// copying from a loose source and re-checking the destination.
func TestCopyEnvFileRedoesACL(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src", ".env")
	dst := filepath.Join(dir, "dst", ".env")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("TOKEN=abc\n"), 0o666); err != nil {
		t.Fatal(err)
	}
	if err := CopyEnvFile(src, dst); err != nil {
		t.Fatalf("CopyEnvFile: %v", err)
	}
	if got := readAll(t, dst); got != "TOKEN=abc\n" {
		t.Fatalf("content mismatch: %q", got)
	}
	assertEnvACLRedone(t, dst)
}

// 回滚 1：copy 失败 ⇒ 指针不动，锁释放。
func TestExecuteRollsBackOnCopyFailure(t *testing.T) {
	exec := sandboxedExecutor(t)
	source := filepath.Join(t.TempDir(), "home")
	seedHome(t, source)
	target := filepath.Join(t.TempDir(), "migrated")

	exec.copyTreeFn = func(src, dst string, progress func(Progress)) error {
		return fmt.Errorf("disk full mid-copy")
	}
	final, detail := exec.Execute(Plan{Source: source, Target: target}, ackAll(), nil)
	if final != PhaseAborted || !strings.Contains(detail, "copy") {
		t.Fatalf("want aborted at copy, got %q: %s", final, detail)
	}
	if pointer, err := config.ReadHomePointerFile(exec.pointerPath()); err != nil || pointer != nil {
		t.Fatalf("pointer must not exist after a failed copy: (%v, %v)", pointer, err)
	}
	if _, err := os.Stat(exec.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("lock must be released on rollback: %v", err)
	}
}

// 回滚 2：校验失败 ⇒ 指针不动（事务从未切过去）。
func TestExecuteRollsBackOnVerifyFailure(t *testing.T) {
	exec := sandboxedExecutor(t)
	source := filepath.Join(t.TempDir(), "home")
	seedHome(t, source)
	target := filepath.Join(t.TempDir(), "migrated")

	exec.verifyFn = func(src, dst string) error {
		return errors.New("byte count mismatch")
	}
	final, detail := exec.Execute(Plan{Source: source, Target: target}, ackAll(), nil)
	if final != PhaseAborted || !strings.Contains(detail, "verify") {
		t.Fatalf("want aborted at verify, got %q: %s", final, detail)
	}
	if pointer, _ := config.ReadHomePointerFile(exec.pointerPath()); pointer != nil {
		t.Fatalf("pointer must stay absent after failed verify, got %+v", pointer)
	}
}

// 回滚 3：切指针失败 ⇒ 旧指针原样保留（temp+rename 的原子语义）。
func TestExecuteKeepsOldPointerWhenSwitchFails(t *testing.T) {
	exec := sandboxedExecutor(t)
	source := filepath.Join(t.TempDir(), "home")
	seedHome(t, source)
	target := filepath.Join(t.TempDir(), "migrated")
	if err := config.WriteHomePointerFile(exec.pointerPath(), source); err != nil {
		t.Fatal(err)
	}

	exec.switchFn = func(pointerPath, home string) error {
		return fmt.Errorf("rename refused")
	}
	final, _ := exec.Execute(Plan{Source: source, Target: target}, ackAll(), nil)
	if final != PhaseAborted {
		t.Fatalf("want aborted at switch, got %q", final)
	}
	pointer, err := config.ReadHomePointerFile(exec.pointerPath())
	if err != nil || pointer == nil || !sameFilePath(pointer.Home, source) {
		t.Fatalf("old pointer must survive a failed switch, got (%+v, %v)", pointer, err)
	}
}

// 回滚 0：六条风险未全部确认 ⇒ 事务根本不启动。
func TestExecuteRefusesUnacknowledgedRisks(t *testing.T) {
	exec := sandboxedExecutor(t)
	source := filepath.Join(t.TempDir(), "home")
	seedHome(t, source)
	target := filepath.Join(t.TempDir(), "migrated")

	exec.copyTreeFn = func(src, dst string, progress func(Progress)) error {
		t.Fatal("copy must not run before the risk gate passes")
		return nil
	}
	final, detail := exec.Execute(Plan{Source: source, Target: target}, ackAll()[:3], nil)
	if final != PhaseAborted || !strings.Contains(detail, "acknowledg") {
		t.Fatalf("want aborted at the risk gate, got %q: %s", final, detail)
	}
}

// 风险 4：迁移互斥。活锁拒绝执行；死主锁（PID 不可探测为活）可被接管。
func TestExecuteHonorsMigrationLock(t *testing.T) {
	exec := sandboxedExecutor(t)
	lockPath := exec.lockPath()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(t.TempDir(), "home")
	seedHome(t, source)
	target := filepath.Join(t.TempDir(), "migrated")

	t.Run("live lock refuses", func(t *testing.T) {
		if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("pid=%d\nstarted_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(lockPath) })
		if !migrationInProgressAt(lockPath) {
			t.Fatal("a lock owned by a live PID must report migration in progress")
		}
		final, detail := exec.Execute(Plan{Source: source, Target: target}, ackAll(), nil)
		if final != PhaseAborted || !strings.Contains(detail, "lock held") {
			t.Fatalf("want aborted with lock error, got %q: %s", final, detail)
		}
		if _, err := os.Stat(lockPath); err != nil {
			t.Fatalf("foreign lock must survive a refused attempt: %v", err)
		}
	})

	t.Run("dead lock is stolen", func(t *testing.T) {
		// Pick a PID that cannot exist anywhere on the system.
		dead := findDeadPID(t)
		if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("pid=%d\nstarted_at=%s\n", dead, time.Now().UTC().Format(time.RFC3339))), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(lockPath) })
		final, detail := exec.Execute(Plan{Source: source, Target: target}, ackAll(), nil)
		if final != PhaseDone {
			t.Fatalf("stale lock should be stolen, got %q: %s", final, detail)
		}
	})

	t.Run("ancient lock is stolen by age", func(t *testing.T) {
		if err := os.WriteFile(lockPath, []byte("pid=0\nstarted_at=old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		stale := time.Now().Add(-2 * lockStaleAge)
		if err := os.Chtimes(lockPath, stale, stale); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(lockPath) })
		final, _ := exec.Execute(Plan{Source: source, Target: target}, ackAll(), nil)
		if final != PhaseDone {
			t.Fatalf("ancient lock should yield, got %q", final)
		}
	})
}

func findDeadPID(t *testing.T) int {
	t.Helper()
	for pid := 4000000; pid < 4000000+500; pid++ {
		if !pidAlive(pid) {
			return pid
		}
	}
	t.Skip("no provably dead PID found")
	return 0
}

// ResetToDefault 删除指针文件；数据不回搬（报告口径）。
func TestResetToDefault(t *testing.T) {
	exec := sandboxedExecutor(t)
	pointerPath := exec.pointerPath()
	if err := config.WriteHomePointerFile(pointerPath, filepath.Join(t.TempDir(), "custom")); err != nil {
		t.Fatal(err)
	}
	if err := exec.ResetToDefault(); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := os.Stat(pointerPath); !os.IsNotExist(err) {
		t.Fatalf("pointer should be gone: %v", err)
	}
	// Resetting again is a no-op, not an error.
	if err := exec.ResetToDefault(); err != nil {
		t.Fatalf("second reset should tolerate absence: %v", err)
	}
}

// copyTree 不搬运指针与锁等簿记文件，也不跟随符号链接。
func TestCopyTreeSkipsBookkeepingAndSymlinks(t *testing.T) {
	src := filepath.Join(t.TempDir(), "home")
	dst := filepath.Join(t.TempDir(), "copy")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config.toml"), []byte("a=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, config.HomePointerFileName), []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "home-migrate.lock"), []byte("pid=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(config.HomePointerFileName, filepath.Join(src, "link.toml")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copyTree(src, dst, nil); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	for _, name := range []string{"config.toml", ".env"} {
		if name != "config.toml" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dst, name)); err != nil {
			t.Fatalf("regular file missing: %v", err)
		}
	}
	for _, name := range []string{config.HomePointerFileName, "home-migrate.lock", "link.toml"} {
		if _, err := os.Stat(filepath.Join(dst, name)); !os.IsNotExist(err) {
			t.Fatalf("%s must not travel: %v", name, err)
		}
	}
}

// verifyCopy 抓得到缺文件/缺字节；.env 哈希不一致也算失败。
func TestVerifyCopyCatchesMismatches(t *testing.T) {
	src := filepath.Join(t.TempDir(), "home")
	dst := filepath.Join(t.TempDir(), "copy")
	seedHome(t, src)
	if err := copyTree(src, dst, nil); err != nil {
		t.Fatal(err)
	}
	if err := verifyCopy(src, dst); err != nil {
		t.Fatalf("clean copy must verify: %v", err)
	}

	// Remove one file → count mismatch.
	if err := os.Remove(filepath.Join(dst, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if err := verifyCopy(src, dst); !errors.Is(err, ErrVerifyMismatch) {
		t.Fatalf("missing file must fail verify, got %v", err)
	}
	if err := copyTree(src, dst, nil); err != nil {
		t.Fatal(err)
	}

	// Tamper with the credential copy → hash mismatch. Same byte length, so
	// only the .env hash can catch it.
	tampered := strings.Replace(readAll(t, filepath.Join(dst, ".env")), "sk-test-846", "sk-tampered", 1)
	if err := os.WriteFile(filepath.Join(dst, ".env"), []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := verifyCopy(src, dst); !errors.Is(err, ErrVerifyMismatch) || !strings.Contains(err.Error(), ".env hash") {
		t.Fatalf("tampered .env must fail verify with a hash complaint, got %v", err)
	}
}

func TestEnvFileFingerprint(t *testing.T) {
	a := EnvFileFingerprint([]byte("A=1\n"))
	b := EnvFileFingerprint([]byte("A=2\n"))
	if a == b {
		t.Fatal("different contents must fingerprint differently")
	}
	if a != EnvFileFingerprint([]byte("A=1\n")) {
		t.Fatal("identical contents must fingerprint identically")
	}
	if len(a) != 64 {
		t.Fatalf("sha256 hex should be 64 chars, got %d", len(a))
	}
}
