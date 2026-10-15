package shellsafe

// Delete-wrapper unwrapping (task 903, upstream 11782/PR11786 语义重实施).
// 把 command/env/exec/builtin/sudo/nohup 包装逐层剥开，得到真正要执行的命令
// 基名与剩余参数。known=false 表示包装层选项无法静态识别——调用方必须按
// 动态命令 fail-closed，不得假设包装下没有递归删除。

import (
	"strings"

	"reasonix/internal/shellparse"
)

// UnwrapDeleteCommand strips known command wrappers from name/args and returns
// the underlying command base, its remaining arguments, and whether the
// unwrapping was conclusive.
func UnwrapDeleteCommand(name string, args []string) (string, []string, bool) {
	return unwrapDeleteCommand(name, args)
}

// CommandBase exposes the wrapper-family base-name resolver to the delete
// guard (directory-change recognition). It lowercases the basename and drops a
// Windows .exe suffix, matching unwrapDeleteCommand's view of a command word.
func CommandBase(name string) string { return commandBase(name) }

func unwrapDeleteCommand(name string, args []string) (string, []string, bool) {
	base := commandBase(name)
	for {
		switch base {
		case "command", "env", "exec", "builtin", "sudo", "nohup":
		default:
			return base, args, true
		}
		wrapper := base
		for len(args) > 0 {
			arg := args[0]
			if wrapper == "command" && (arg == "-v" || arg == "-V") {
				return "", nil, true
			}
			if arg == "--" {
				args = args[1:]
				break
			}
			if wrapper == "env" && shellparse.IsAssignment(arg) || wrapper == "command" && arg == "-p" {
				args = args[1:]
				continue
			}
			if arg == "" {
				return "", args, false
			}
			if strings.HasPrefix(arg, "-") {
				switch arg {
				case "-E", "-n", "-i", "--ignore-environment", "--non-interactive":
					args = args[1:]
					continue
				case "-u", "-g", "--user", "--group":
					if len(args) < 2 {
						return "", args, false
					}
					args = args[2:]
					continue
				default:
					return "", args, false
				}
			}
			break
		}
		if len(args) == 0 {
			return "", nil, true
		}
		base, args = commandBase(args[0]), args[1:]
	}
}
