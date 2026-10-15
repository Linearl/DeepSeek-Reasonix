package shellsafe

// Recursive-delete extent analysis (task 903, upstream 11782/PR11786 语义重实施).
// 判定一条命令调用是否构成递归删除、目标是否字面、选项是否可识别——本包是
// 「什么算递归删除」的单一事实源，bash builtin 的执行前护栏消费这里的结论。
// 判错方向约束：识别不出的选项/命令一律标 UnknownOption / DynamicCommand，
// 由调用方 fail-closed 拒绝；本层不放行任何它看不懂的形态。

import "strings"

// DeleteExtent is what one command invocation does to the filesystem, as far
// as static analysis can tell. Targets are the positional operands (empty
// string when an operand is missing or dynamic). Recursive reports a
// recursive/cascading delete. UnknownOption marks an option the analyzer does
// not recognize (the delete's behavior is not fully known). DynamicCommand
// marks a nested command line whose own analysis was impossible.
type DeleteExtent struct {
	Targets        []string
	Recursive      bool
	UnknownOption  bool
	DynamicCommand bool
}

// AnalyzeRecursiveDelete reports the delete extent of invoking name with args.
// powerShell selects PowerShell parameter semantics (dashed parameters, common
// parameters with values) over POSIX option clustering. A non-delete command
// yields the zero extent; `find`/`xargs`/`cmd /c` are unwrapped so recursive
// deletes reached through them are still seen.
func AnalyzeRecursiveDelete(name string, args []string, powerShell bool) DeleteExtent {
	base, args, known := unwrapDeleteCommand(name, args)
	if !known {
		return recursiveArgumentDelete(args, powerShell)
	}
	switch base {
	case "cmd":
		return cmdDelete(args)
	case "xargs":
		return recursiveArgumentDelete(args, powerShell)
	case "find":
		return findDelete(args)
	case "rm", "remove-item", "ri", "del", "erase", "rd", "rmdir", "":
		return directDelete(base, args, powerShell)
	default:
		return DeleteExtent{}
	}
}

// recursiveArgumentDelete treats every argument as a possible nested command
// line (xargs-style): the first argument whose own extent is recursive makes
// the whole call a recursive delete with an unresolvable target.
func recursiveArgumentDelete(args []string, powerShell bool) DeleteExtent {
	for i, arg := range args {
		e := AnalyzeRecursiveDelete(arg, args[i+1:], powerShell)
		if e.Recursive {
			e.Targets = []string{""}
			return e
		}
	}
	return DeleteExtent{}
}

// cmdDelete unwraps `cmd /c …` and re-analyzes the inner command line.
// A recursive /s delete whose line carries cmd metacharacters (escapes,
// percent expansion, pipes, chained commands) cannot be statically bounded.
func cmdDelete(args []string) DeleteExtent {
	if len(args) == 0 || !strings.EqualFold(args[0], "/c") {
		return DeleteExtent{}
	}
	if len(args) == 2 {
		args = strings.Fields(args[1])
	} else {
		args = args[1:]
	}
	if len(args) == 0 {
		return DeleteExtent{}
	}
	recursive, complex := false, false
	for _, arg := range args {
		recursive = recursive || strings.EqualFold(arg, "/s")
		complex = complex || strings.ContainsAny(arg, "^%&|\r\n")
	}
	if recursive && complex {
		return DeleteExtent{Recursive: true, DynamicCommand: true}
	}
	return AnalyzeRecursiveDelete(args[0], args[1:], true)
}

// findDelete extracts -delete / -exec recursive deletes from a find line. The
// positional targets are find's start paths; -exec deletes add their own
// operands (with {} filtered out and an unresolvable marker for -execdir,
// whose cwd differs from the caller's).
func findDelete(args []string) DeleteExtent {
	e := DeleteExtent{}
	var extra []string
	for i, arg := range args {
		switch arg {
		case "-delete":
			e.Recursive = true
		case "-follow", "-L":
			e.UnknownOption = true
		case "-exec", "-execdir":
			if i+1 >= len(args) {
				continue
			}
			nested := findExecDelete(args[i+1:])
			e.Recursive = e.Recursive || nested.Recursive
			e.UnknownOption = e.UnknownOption || nested.UnknownOption
			if nested.Recursive {
				extra = append(extra, nested.Targets...)
				if arg == "-execdir" {
					extra = append(extra, "")
				}
			}
		}
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			break
		}
		e.Targets = append(e.Targets, arg)
	}
	if len(e.Targets) == 0 {
		e.Targets = []string{"."}
	}
	e.Targets = append(e.Targets, extra...)
	return e
}

// findExecDelete analyzes the -exec payload up to its `;`/`+` terminator and
// drops the {} placeholder from the reported targets.
func findExecDelete(args []string) DeleteExtent {
	tail := args[1:]
	for j, v := range tail {
		if v == ";" || v == "+" {
			tail = tail[:j]
			break
		}
	}
	e := AnalyzeRecursiveDelete(args[0], tail, false)
	var targets []string
	for _, target := range e.Targets {
		if target != "{}" {
			targets = append(targets, target)
		}
	}
	e.Targets = targets
	return e
}
