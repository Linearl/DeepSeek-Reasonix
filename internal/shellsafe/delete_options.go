package shellsafe

import "strings"

// Delete option parsing (task 903, upstream 11782/PR11786 语义重实施). 两套
// 语义按调用方 shell 选择：POSIX 选项簇（-rf 合并）与 PowerShell 独立参数
// （-Recurse / -Path / 公共参数）。判错方向约束：未知选项一律标
// UnknownOption，由调用方 fail-closed；`--` 之后按目标处理。

func directDelete(base string, args []string, powerShell bool) DeleteExtent {
	e := DeleteExtent{}
	options := true
	psParameters := powerShell && (base == "remove-item" || base == "ri" || base == "rm" || base == "del" || base == "")
	cmdParameters := powerShell && (base == "del" || base == "erase" || base == "rd" || base == "rmdir")
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "-") {
			if psParameters {
				i += powerShellDeleteOption(&e, arg, args[i+1:])
			} else {
				posixDeleteOption(&e, arg)
			}
			continue
		}
		if cmdParameters && strings.HasPrefix(arg, "/") {
			cmdDeleteOption(&e, arg)
			continue
		}
		e.Targets = append(e.Targets, arg)
	}
	if !e.Recursive {
		return DeleteExtent{}
	}
	if len(e.Targets) == 0 {
		e.Targets = []string{""}
	}
	return e
}

// powerShellDeleteOption consumes one dashed PowerShell parameter and returns
// how many following arguments it swallowed. `-Recurse:$false` is not
// recursive; -Path/-LiteralPath take their value as a positional target;
// common parameters (-ErrorAction and friends) accept an inline value or one
// following value token; anything else is unknown.
func powerShellDeleteOption(e *DeleteExtent, arg string, rest []string) int {
	key, value, inline := strings.Cut(strings.TrimPrefix(strings.ToLower(arg), "-"), ":")
	switch {
	case key != "" && strings.HasPrefix("recurse", key):
		e.Recursive = e.Recursive || value != "$false"
	case key != "" && strings.HasPrefix("force", key):
	case key == "literalpath" || key == "path":
	case key == "erroraction" || key == "ea" || key == "warningaction" || key == "wa" || key == "informationaction" || key == "confirm":
		if !inline {
			if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
				return 1
			}
			e.UnknownOption = true
		}
	case key == "verbose" || key == "debug" || key == "whatif":
	default:
		e.UnknownOption = true
	}
	return 0
}

// posixDeleteOption consumes a POSIX rm-style option cluster. `r`/`R` anywhere
// in the cluster means recursive; any letter outside the known rm set makes
// the option unknown.
func posixDeleteOption(e *DeleteExtent, arg string) {
	if strings.HasPrefix(arg, "--") {
		if strings.HasPrefix("--recursive", arg) {
			e.Recursive = true
		} else if arg != "--force" && arg != "--verbose" && arg != "--dir" {
			e.UnknownOption = true
		}
		return
	}
	e.Recursive = e.Recursive || strings.ContainsAny(arg[1:], "rR")
	e.UnknownOption = e.UnknownOption || strings.Trim(arg[1:], "rfRvidI") != ""
}

// cmdDeleteOption consumes a cmd.exe rd/del style switch. Only /s (recursive),
// /q and /f are known.
func cmdDeleteOption(e *DeleteExtent, arg string) {
	switch strings.ToLower(arg) {
	case "/s":
		e.Recursive = true
	case "/q", "/f":
	default:
		e.UnknownOption = true
	}
}
