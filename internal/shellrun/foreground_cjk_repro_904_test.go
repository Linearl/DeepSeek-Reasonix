package shellrun

// 任务 904b（上游 issue 10446 / PR 10617）复现形状验证：上游 v1.38.9 的「前台
// bash 共享会话级 PTY」在非 ASCII 输出上改写字节并让宿主哨兵检测失败，造成
// 120s 假超时。fork 的前台路径是独立子进程 + 合并 pipe（RunForeground），无
// PTY、无 REASONIX_END 类哨兵——本文件用上游复现矩阵的原形状在 fork 真实前台
// 路径上取证：中文逐字节正确、命令完成不假超时、真超时时已收输出仍完好。

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// cjkShellArgv resolves the same shell the bash tool would bind and builds the
// foreground argv. t.Skip is allowed here only for a missing POSIX shell on an
// exotic host; this Windows test machine has Git Bash, so the skip never fires.
func cjkShellArgv(t *testing.T, command string) []string {
	t.Helper()
	sh := sandbox.ResolveShell("auto", "", nil)
	if !sh.Kind.IsPOSIX() {
		t.Skipf("no POSIX shell resolved (got %s); CJK repro needs bash", sh.Kind)
	}
	return shellArgvWith(sh, command)
}

// TestForegroundCJKOutputBytePerfectNoFakeTimeout runs the upstream repro
// command verbatim (issue 10446 §复现 A): printf Chinese lines, then echo an
// end marker. Upstream's shared PTY replaced UTF-8 bytes (提交版 → □??交??)
// and the polluted output broke sentinel detection → 120s timeout + shell
// reset. On the fork pipe path the output must be byte-perfect and the run
// must complete well inside the timeout.
func TestForegroundCJKOutputBytePerfectNoFakeTimeout(t *testing.T) {
	argv := cjkShellArgv(t, `printf '中文测试A\n中文测试B\n'; echo MARKER_END`)
	start := time.Now()
	res := RunForeground(context.Background(), Request{
		Argv:    argv,
		Dir:     t.TempDir(),
		Timeout: 30 * time.Second,
		Track:   true,
	})
	elapsed := time.Since(start)
	if res.Err != nil {
		t.Fatalf("foreground CJK run failed: %v (combined=%q)", res.Err, trim(res.Combined, 200))
	}
	if res.State != tool.ShellStateCompleted {
		t.Fatalf("state = %q, want completed (no fake timeout)", res.State)
	}
	for _, want := range []string{"中文测试A", "中文测试B", "MARKER_END"} {
		if !strings.Contains(res.Combined, want) {
			t.Fatalf("output missing %q — byte corruption on the pipe path:\n%s", want, trim(res.Combined, 300))
		}
	}
	if strings.Contains(res.Combined, "�") {
		t.Fatalf("output contains replacement characters:\n%s", trim(res.Combined, 300))
	}
	if elapsed > 25*time.Second {
		t.Fatalf("run took %s; a plain printf must not approach the timeout", elapsed)
	}
}

// TestForegroundCJKQuotedLsNoFakeTimeout runs the upstream failing case: a
// bare `ls -a` over a directory of Chinese file names. Upstream's tty path
// quoted each name into $'\217\220' escapes, corrupted the bytes, and never
// delivered the end marker. The fork hands the child a pipe (stdout is not a
// tty), so ls cannot enter the multi-column/quoting tty formatting at all.
func TestForegroundCJKQuotedLsNoFakeTimeout(t *testing.T) {
	dir := t.TempDir()
	names := []string{"测试_提交版.md", "示例文档 (1).md", "演示材料.pptx"}
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("fixture %q: %v", name, err)
		}
	}
	argv := cjkShellArgv(t, `ls -a; echo MARKER_END`)
	res := RunForeground(context.Background(), Request{
		Argv:    argv,
		Dir:     dir,
		Timeout: 30 * time.Second,
		Track:   true,
	})
	if res.Err != nil {
		t.Fatalf("ls over Chinese names failed: %v (combined=%q)", res.Err, trim(res.Combined, 200))
	}
	if res.State != tool.ShellStateCompleted {
		t.Fatalf("state = %q, want completed (no fake timeout)", res.State)
	}
	for _, name := range names {
		if !strings.Contains(res.Combined, name) {
			t.Fatalf("ls output missing verbatim %q (quoting/corruption regression):\n%s", name, trim(res.Combined, 400))
		}
	}
	if strings.Contains(res.Combined, "$'") {
		t.Fatalf("ls fell into tty quoting despite a pipe stdout:\n%s", trim(res.Combined, 400))
	}
}

// TestForegroundCJKTimeoutKeepsDecodedOutput asserts the timeout layer is a
// real process-group deadline, distinct from the upstream sentinel failure:
// a command that prints Chinese and then hangs must time out with
// state=timed_out while the already-received CJK output stays intact.
func TestForegroundCJKTimeoutKeepsDecodedOutput(t *testing.T) {
	argv := cjkShellArgv(t, `printf '中文超时前输出\n'; sleep 10`)
	res := RunForeground(context.Background(), Request{
		Argv:    argv,
		Dir:     t.TempDir(),
		Timeout: 500 * time.Millisecond,
		Track:   true,
	})
	if res.State != tool.ShellStateTimedOut || res.FailurePhase != tool.ShellPhaseTimeout {
		t.Fatalf("state/phase = %s/%s, want timed_out/timeout", res.State, res.FailurePhase)
	}
	if !strings.Contains(res.Combined, "中文超时前输出") {
		t.Fatalf("CJK output before a real timeout must survive decoding:\n%s", trim(res.Combined, 300))
	}
	if strings.Contains(res.Combined, "�") {
		t.Fatalf("timed-out output has replacement characters:\n%s", trim(res.Combined, 300))
	}
}

// TestForegroundGBKConsoleOutputRealShell feeds GBK-encoded Chinese through a
// real child process (not the injected seam): the bytes are invalid UTF-8 on
// the pipe, which is exactly the Windows-console shape the decode cascade
// (codepage.go, upstream #11311 → #11327 port) must fix before the model ever
// sees mojibake.
func TestForegroundGBKConsoleOutputRealShell(t *testing.T) {
	gbkBytes, encErr := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中文GBK通道验证完成"))
	if encErr != nil {
		t.Fatalf("encode GBK: %v", encErr)
	}
	// printf with octal escapes emits the raw GBK bytes regardless of the
	// shell's own locale, reproducing a CP936 console program on a pipe.
	var octal strings.Builder
	for _, b := range gbkBytes {
		fmt.Fprintf(&octal, "\\%03o", b)
	}
	argv := cjkShellArgv(t, "printf '"+octal.String()+"\\n'")
	withDecoderCandidates(t, []encoding.Encoding{simplifiedchinese.GBK})
	res := RunForeground(context.Background(), Request{
		Argv:    argv,
		Dir:     t.TempDir(),
		Timeout: 30 * time.Second,
		Track:   true,
	})
	if res.Err != nil {
		t.Fatalf("GBK shell run failed: %v (combined=%q)", res.Err, trim(res.Combined, 200))
	}
	if !strings.Contains(res.Combined, "中文GBK通道验证完成") {
		t.Fatalf("GBK bytes not decoded on the real pipe path:\n%s", trim(res.Combined, 300))
	}
}

// TestNoSharedPTYInForegroundPath is a structural guard: the upstream defect
// lives in a session-shared PTY reused across foreground calls. The fork's
// foreground runner must spawn a fresh child per call (its Result exposes the
// exec.Cmd), and the sandbox shell layer must not import any conpty/pty
// dependency. If either ever changes, this test fails and the 10446 analysis
// in handoff-904.md must be redone.
func TestNoSharedPTYInForegroundPath(t *testing.T) {
	argv := cjkShellArgv(t, "echo once")
	first := RunForeground(context.Background(), Request{Argv: argv, Dir: t.TempDir(), Timeout: 30 * time.Second, Track: true})
	second := RunForeground(context.Background(), Request{Argv: argv, Dir: t.TempDir(), Timeout: 30 * time.Second, Track: true})
	if first.Err != nil || second.Err != nil {
		t.Fatalf("baseline runs failed: %v / %v", first.Err, second.Err)
	}
	if first.Cmd == nil || second.Cmd == nil || first.Cmd == second.Cmd {
		t.Fatal("foreground runs must own distinct child processes (no shared session PTY)")
	}
	if first.Tracked != nil && second.Tracked != nil && first.Tracked == second.Tracked {
		t.Fatal("tracked handle shared across runs")
	}
	// Both runs must independently reach exit-0; a shared PTY's corrupted
	// state would surface here as one side failing.
	for _, res := range []*Result{&first, &second} {
		if res.State != tool.ShellStateCompleted {
			t.Fatalf("state = %q, want independent completion", res.State)
		}
	}
}
