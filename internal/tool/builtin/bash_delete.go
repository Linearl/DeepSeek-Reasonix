package builtin

// Recursive-delete launch guard (task 903, upstream 11782/PR11786 语义重实施).
// bash 工具在启动任何 shell 进程之前审一遍命令里的递归删除：目标必须是字面
// 路径、严格位于授予的写根之内（工作区 + additional_write_dirs + 会话私有
// 临时目录），home / 文件系统根 / 工作区自身及其祖先在任何审批模式下都拒绝。
// 这是执行前护栏、不是 OS 沙箱：脚本文件、任意程序、文件系统竞态不在其范围。
//
// 拒绝码沿用上游语义（bash.<cause> → 此处 shell.<cause>）：
//
//	shell.delete_sequence     递归删除前只能是字面 cd；赋值/嵌套控制流不建立安全性
//	shell.delete_nonliteral   目标含变量/展开/通配/管道输入，不可静态界定
//	shell.destructive_target  目标不在任何授予根内，或是 home/根/工作区祖先
//	shell.delete_option       删除选项无法识别
//	shell.syntax_error        命令语法错误，无法分析
//	shell.parser_unavailable  宿主 PowerShell 解析进程不可用
//	shell.parser_timeout      解析超时/被取消
//	shell.analysis_unknown    动态解释器 payload / 动态命令带递归删除参数

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"reasonix/internal/sandbox"
	"reasonix/internal/shellparse"
	"reasonix/internal/shellsafe"
)

// deleteRefusal is a typed pre-launch refusal carrying a stable cause code so
// the model can distinguish "blocked before execution" from an execution
// failure and retry with a bounded command instead of guessing.
type deleteRefusal struct {
	code    string
	message string
}

func (e deleteRefusal) Error() string {
	return fmt.Sprintf("Shell command refused before execution: %s (refusal: %s)", e.message, e.code)
}

func refuseDelete(code, message string) error {
	return deleteRefusal{code: code, message: message}
}

// deleteRefusalCode extracts the cause code from a delete refusal.
func deleteRefusalCode(err error) (string, bool) {
	var refusal deleteRefusal
	if errors.As(err, &refusal) {
		return refusal.code, true
	}
	return "", false
}

const (
	CodeDestructiveTarget    = "shell.destructive_target"
	CodeShellAnalysisUnknown = "shell.analysis_unknown"
	CodeDeleteSequence       = "shell.delete_sequence"
	CodeDeleteNonliteral     = "shell.delete_nonliteral"
	CodeDeleteOption         = "shell.delete_option"
	CodeShellSyntax          = "shell.syntax_error"
	CodeParserUnavailable    = "shell.parser_unavailable"
	CodeParserTimeout        = "shell.parser_timeout"
)

// refuseDestructiveDelete runs the recursive-delete review over command for
// the bound shell. A nil result means no recursive delete needs intervention;
// a non-nil error is a hard refusal that must not launch any process.
func (b bash) refuseDestructiveDelete(ctx context.Context, sh sandbox.Shell, command string) error {
	b.sessionTemp = b.sessionTempManager(ctx)
	seen := deleteJudgement{}
	err := b.judgeDelete(ctx, sh, command, 0, false, &seen)
	if err == nil && seen.containsDelete && seen.dynamicPayload {
		return refuseDelete(CodeShellAnalysisUnknown, "Use a literal interpreter payload or split it from the recursive delete.")
	}
	return err
}

// deleteJudgement accumulates facts across a whole script (including nested
// interpreter payloads): whether a recursive delete was seen, and whether any
// dynamic interpreter payload could not be resolved.
type deleteJudgement struct{ containsDelete, dynamicPayload bool }

func (b bash) judgeDelete(ctx context.Context, sh sandbox.Shell, command string, depth int, priorUnsafe bool, seen *deleteJudgement) error {
	if depth > 12 {
		return refuseDelete(CodeShellAnalysisUnknown, "Nested interpreter extent is unknown; use a direct delete with a literal workspace path.")
	}
	analysis, err := shellparse.AnalyzeDeleteCalls(command)
	powerShell := sh.Kind == sandbox.ShellPowerShell
	if powerShell {
		analysis, err = analyzePowerShellDelete(ctx, sh, command)
	}
	if err != nil {
		code := CodeShellSyntax
		message := "Fix the shell syntax and retry: " + err.Error()
		if powerShell {
			code = CodeParserUnavailable
			message = "The host shell parser is unavailable; retry when the host parser is restored."
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = CodeParserTimeout
			message = "The host shell parser timed out or was canceled; retry the call."
		}
		return refuseDelete(code, message)
	}
	if analysis.SyntaxError != "" {
		return refuseDelete(CodeShellSyntax, "Fix the shell syntax and retry: "+analysis.SyntaxError)
	}
	unsafe := priorUnsafe
	directories := []string{b.deleteCwd()}
	if len(b.sb.WriteRoots) == 0 {
		b.sb.WriteRoots = []string{b.deleteCwd()}
	}
	for _, call := range analysis.Calls {
		payload, inner, interpreter := deleteInterpreter(call, sh)
		if interpreter {
			for _, arg := range call.Args {
				if arg == "" {
					seen.dynamicPayload = true
				}
			}
			if payload == "" {
				seen.dynamicPayload = true
			} else {
				for _, dir := range directories {
					child := b
					child.workDir = dir
					if err := child.judgeDelete(ctx, inner, payload, depth+1, unsafe || call.Unsafe, seen); err != nil {
						return err
					}
				}
			}
			unsafe = true
			continue
		}
		if deleteDirectoryChange(call.Name) {
			next, known := inferDeleteDirectories(directories, call)
			directories = next
			unsafe = unsafe || !known
			continue
		}
		extent := shellsafe.AnalyzeRecursiveDelete(call.Name, call.Args, powerShell)
		if !extent.Recursive {
			unsafe = true
			continue
		}
		seen.containsDelete = true
		if call.Name == "" || extent.DynamicCommand {
			return refuseDelete(CodeShellAnalysisUnknown, "A dynamic executable has recursive-delete arguments; use a literal executable and a literal workspace path.")
		}
		if unsafe || call.Unsafe {
			return refuseDelete(CodeDeleteSequence, "Split the call; only literal directory changes may precede a recursive delete. Assignments and nested control flow cannot establish a safe target.")
		}
		if extent.UnknownOption {
			return refuseDelete(CodeDeleteOption, "Use known delete options and common parameters with explicit values.")
		}
		for _, target := range extent.Targets {
			if !literalDeletePath(target) {
				return refuseDelete(CodeDeleteNonliteral, "Use a literal target path strictly inside the workspace or granted write roots; expansions, variables, globs and pipeline input are not bounded.")
			}
			for _, dir := range directories {
				scoped := b
				scoped.workDir = dir
				if !scoped.boundedDeleteTarget(target) {
					return refuseDelete(CodeDestructiveTarget, "Use a target strictly inside the workspace or granted write roots; home, filesystem roots, workspace ancestors and out-of-scope paths are protected in every approval mode.")
				}
			}
		}
	}
	return nil
}

// deleteDirectoryChange reports whether the command only changes the working
// directory (the one statement the sequence rules allow before a recursive
// delete, because the guard re-checks the target from every possible cwd).
func deleteDirectoryChange(name string) bool {
	switch shellsafe.CommandBase(name) {
	case "cd", "set-location", "sl", "pushd", "push-location":
		return true
	}
	return false
}

// inferDeleteDirectories extends the cwd set with a literal directory-change
// argument. A non-literal or multi-argument change is unknown: the guard keeps
// the previous set and flags the sequence unsafe.
func inferDeleteDirectories(directories []string, call shellparse.DeleteCall) ([]string, bool) {
	if len(directories) >= 64 || call.Unsafe || len(call.Args) != 1 || !literalDeletePath(call.Args[0]) {
		return directories, false
	}
	previous := append([]string(nil), directories...)
	for _, dir := range previous {
		path := call.Args[0]
		if !filepath.IsAbs(path) {
			path = dir + string(filepath.Separator) + path
		}
		directories = append(directories, path)
	}
	return directories, true
}

// literalDeletePath rejects any target that is not a plain literal path: empty,
// or containing a variable/expansion/tilde/glob/quote/nul marker.
func literalDeletePath(target string) bool {
	return target != "" && !strings.ContainsAny(target, "$%~*?[]`\x00")
}

// deleteCwd is the directory relative targets resolve against.
func (b bash) deleteCwd() string {
	if b.workDir != "" {
		return b.workDir
	}
	cwd, _ := os.Getwd()
	return cwd
}

// boundedDeleteTarget reports whether a literal recursive-delete target stays
// strictly inside one granted root. Refused: dynamic path syntax, Windows
// drive-relative/provider paths, filesystem roots, home itself or any
// ancestor of home, the workspace itself or any ancestor of it, a granted
// root itself (children only), and anything outside every root. Symlinks are
// resolved before the comparison so a link cannot smuggle the delete out.
func (b bash) boundedDeleteTarget(target string) bool {
	if target == "" || strings.ContainsAny(target, "$%~*?[]`\x00") {
		return false
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(target, "/") || runtime.GOOS == "windows" && strings.HasPrefix(target, "\\") && !filepath.IsAbs(target) {
		return false
	}
	// Provider-qualified and drive-relative paths do not have ordinary filesystem scope.
	if strings.Contains(target, ":") && (!filepath.IsAbs(target) || strings.Contains(strings.TrimPrefix(target, filepath.VolumeName(target)), ":")) {
		return false
	}
	cwd := b.workDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	if !filepath.IsAbs(target) {
		target = cwd + string(filepath.Separator) + target
	}
	resolved, err := realPath(target)
	if err != nil || filepath.Dir(resolved) == resolved {
		return false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	home, err = realPath(home)
	if err != nil || withinFold(resolved, home) {
		return false
	}
	workspacePath := b.workDir
	if len(b.sb.WriteRoots) > 0 {
		workspacePath = b.sb.WriteRoots[0]
	}
	workspace, err := realPath(workspacePath)
	if err != nil || withinFold(resolved, workspace) {
		return false
	}
	roots := b.sb.WriteRoots
	if len(roots) == 0 {
		roots = []string{workspace}
	}
	if temp := b.sessionTemp.Dir(); temp != "" {
		roots = append(append([]string(nil), roots...), temp)
	}
	for _, root := range realRoots(roots) {
		if within(root, resolved) && !withinFold(resolved, root) {
			return true
		}
	}
	return false
}
