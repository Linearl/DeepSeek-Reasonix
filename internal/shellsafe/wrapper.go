package shellsafe

// Wrapper-family recognition and source-level see-through (task 895, 852 四问
// #1+#4 随件项). 把「wrapper 家族识别」收口在本包（命令家族判定的单一事实源，
// 零模型：仅 shellparse + mvdan AST + 标准库），sentinel 的穿透封堵复用这里的
// 判定，不再自建第二套家族名单（forbidden_sync_test 守护的是另一侧镜像）。
//
// SeeThroughWrappers 产出「对静态分析透明」的等价命令文本：
//
//   - 子 shell `(...)` 与花括号组 `{...}` 还原为内层源码（cwd/env 隔离对四条
//     底线规则无意义，内层命令才是被执行的实体）；
//   - 字面 wrapper 逐层解包：shell -c/-lc 的字面脚本、eval 的参数拼接、
//     sudo/env 与裸环境赋值前缀后的实际命令；
//   - 深度封顶 maxWrapperDepth（8，对齐 codex 的
//     MAX_DANGEROUS_COMMAND_WRAPPER_DEPTH），超深即停、不递归到栈底。
//
// 不解包的形态（保守维持调用方现状）：heredoc、解析失败、动态 payload
// （`bash -c "$X"`/`eval $CMD`）、命令替换 `$(...)`——这些形态返回原文，
// 由调用方自己的保守回退接管（sentinel：不可解包形态维持现保守回退）。
//
// 语义透明性约束：替换只允许「把实际被执行的文本换进去」——payload 字面、
// 内层源码、去前缀后的 argv（重引号保真）；重定向等挂在语句上、不在替换
// 范围内的源码原样保留。解包本身永远不产生判定，判定仍归调用方的规则。

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"reasonix/internal/shellparse"
)

// maxWrapperDepth 是 SeeThroughWrappers 的解包层数封顶。与 codex 的
// MAX_DANGEROUS_COMMAND_WRAPPER_DEPTH（8）对齐：超过这一深度的嵌套视为
// 病态构造，停止继续解包（已解出的部分照常返回），不追到无界。
const maxWrapperDepth = 8

// shellWrapperBases 是可 -c/-lc 承载字面脚本的 shell 名（basename 比对，
// 大小写不敏感、.exe 后缀剥离）。与 codex detect_shell_type 的 bash/sh/zsh
// 相比多收 dash/ksh/ash——POSIX shell 的 -c 语义一致，家族识别宁全勿漏
// （误收方向只会多解包一层字面文本，判定仍由调用方四规则把关）。
var shellWrapperBases = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true, "ksh": true, "ash": true,
}

type sourceRange struct{ start, end int }

type sourceReplacement struct {
	start, end int
	text       string
}

// SeeThroughWrappers 把 wrapper/子 shell 层改写为等价的内层源码文本，返回
// (改写后文本, 是否发生过改写)。原文不可解析或含 heredoc 时原样返回且
// changed=false——调用方必须把 changed=false 读作「无可透视层」而非「安全」。
func SeeThroughWrappers(command string) (string, bool) {
	current := command
	changedAll := false
	for depth := 0; depth < maxWrapperDepth; depth++ {
		next, changed, ok := seeThroughOnce(current)
		if !ok {
			// 当前文本解析不了（首遍=原文本来就不合法；后续遍=payload 内容
			// 不是合法 shell）。保留已解出的文本：更透明的文本只会让保守
			// 匹配看到更多，不会更少。
			break
		}
		current = next
		if !changed {
			break
		}
		changedAll = true
	}
	return current, changedAll
}

// seeThroughOnce 执行一轮改写：收集最外层的可替换节点（子 shell / 花括号组 /
// 字面 wrapper 调用 / 环境赋值前缀），按偏移从右往左拼接。ok=false 表示
// 文本不可静态透视（解析失败或 heredoc），调用方保留原文。
func seeThroughOnce(command string) (next string, changed bool, ok bool) {
	if strings.TrimSpace(command) == "" {
		return command, false, true
	}
	file, err := shellparse.ParseBash(command)
	if err != nil || shellparse.HasHereDoc(file) {
		return command, false, false
	}

	var replaced []sourceRange
	var repls []sourceReplacement
	collect := func(start, end int, text string) {
		repls = append(repls, sourceReplacement{start: start, end: end, text: text})
		replaced = append(replaced, sourceRange{start: start, end: end})
	}
	insideReplaced := func(pos int) bool {
		for _, r := range replaced {
			if pos >= r.start && pos < r.end {
				return true
			}
		}
		return false
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		if node == nil {
			return false
		}
		start, end := int(node.Pos().Offset()), int(node.End().Offset())
		if start < 0 || end > len(command) || end <= start {
			return false
		}
		// 已被外层替换覆盖的节点不再进入：替换范围整体取最外层，嵌套层
		// 留给下一轮（深度封顶在调用方循环上）。
		if insideReplaced(start) {
			return false
		}
		switch n := node.(type) {
		case *syntax.Subshell:
			innerStart, innerEnd := int(n.Lparen.Offset())+1, int(n.Rparen.Offset())
			if innerStart > innerEnd || innerEnd > len(command) {
				return false
			}
			collect(start, end, command[innerStart:innerEnd])
			return false
		case *syntax.Block:
			innerStart, innerEnd := int(n.Lbrace.Offset())+1, int(n.Rbrace.Offset())
			if innerStart > innerEnd || innerEnd > len(command) {
				return false
			}
			collect(start, end, command[innerStart:innerEnd])
			return false
		case *syntax.CallExpr:
			text, ok := callReplacement(n)
			if !ok {
				return true // 非 wrapper：继续下钻（内层也不会有 wrapper 之外的可替换节点）
			}
			collect(start, end, text)
			return false
		}
		return true
	})

	if len(repls) == 0 {
		return command, false, true
	}
	// 偏移从右往左应用，左侧偏移保持有效。
	for i := len(repls) - 1; i >= 0; i-- {
		r := repls[i]
		command = command[:r.start] + r.text + command[r.end:]
	}
	return command, true, true
}

// callReplacement 计算一次调用的替换文本。ok=false 表示这不是可透视层
// （动态参数、非 wrapper、空 payload），调用方保留原节点。
func callReplacement(call *syntax.CallExpr) (string, bool) {
	if call == nil || len(call.Args) == 0 {
		return "", false
	}
	fields := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		value, ok := shellparse.StaticWord(arg)
		if !ok {
			return "", false // 动态参数：payload 不可知，维持现状
		}
		fields = append(fields, value)
	}
	if len(call.Assigns) > 0 {
		// 裸环境赋值前缀（FOO=1 git push …）与 env 包装同族：赋值不改变
		// 执行哪个命令，去掉前缀即透视（shellsafe.staticArgv 同一归类）。
		return quoteFields(fields), true
	}
	return wrapperPayload(fields)
}

// wrapperPayload 按 wrapper 家族返回实际执行的命令文本。空 payload 一律
// 不替换（`bash -c ”` 换成空串会把复合命令改出语法错误）。
func wrapperPayload(fields []string) (string, bool) {
	if len(fields) == 0 {
		return "", false
	}
	base := commandBase(fields[0])
	switch {
	case shellWrapperBases[base] && len(fields) >= 3 && (fields[1] == "-c" || fields[1] == "-lc"):
		// codex extract_bash_command 同形：shell + (-c|-lc) + 字面脚本。
		// 脚本后的额外词是 $0/$1 位置参数，不属于被执行文本。
		if fields[2] == "" {
			return "", false
		}
		return fields[2], true
	case base == "eval" && len(fields) >= 2:
		// eval 把参数用空格拼接后交给 shell 求值。
		payload := strings.Join(fields[1:], " ")
		if strings.TrimSpace(payload) == "" {
			return "", false
		}
		return payload, true
	case base == "sudo":
		rest := skipSudoOptions(fields)
		if len(rest) == 0 {
			return "", false
		}
		return quoteFields(rest), true
	case base == "env":
		rest := skipEnvOptions(fields)
		if len(rest) == 0 {
			return "", false
		}
		return quoteFields(rest), true
	}
	return "", false
}

// commandBase 归一化命令词为家族比对用的 basename：路径前缀剥掉、大小写
// 折叠、.exe 后缀剥掉（Windows 双状态可执行形态）。
func commandBase(word string) string {
	base := strings.ToLower(filepath.Base(word))
	return strings.TrimSuffix(base, ".exe")
}

// skipSudoOptions 返回 sudo 之后的实际命令 argv。sudo 的取值旗标单独跳过
// （-u user 会把用户名误当命令头）；未收录的旗标只跳旗标本身——判错方向
// 恒为「命令头落在参数上、规则不命中」（漏），永远不会把无害命令推成命中
// （误杀），与 sentinel 误杀面控制的约束一致。
func skipSudoOptions(fields []string) []string {
	i := 1
	for i < len(fields) {
		tok := fields[i]
		if tok == "--" {
			i++
			break
		}
		if strings.HasPrefix(tok, "-") && tok != "-" {
			if sudoOptionTakesValue(tok) && i+1 < len(fields) {
				i += 2
				continue
			}
			i++
			continue
		}
		break
	}
	return fields[i:]
}

func sudoOptionTakesValue(tok string) bool {
	switch tok {
	case "-u", "-g", "-p", "-C", "-R", "-U",
		"--user", "--group", "--prompt", "--close-from", "--chroot",
		"--host", "--role", "--type", "--other-user":
		return true
	default:
		return false
	}
}

// skipEnvOptions 返回 env 之后的实际命令 argv：跳过赋值、-i/--ignore-
// environment、取值旗标（-u/-S）与 `--` 分隔符。语义对齐 GNU env 与 codex
// dangerous_command_match_for_env（codex 不跳 -u/-S 的取值，此处补上——
// 判错方向同 sudo：只可能漏，不可能误杀）。
func skipEnvOptions(fields []string) []string {
	i := 1
	for i < len(fields) {
		tok := fields[i]
		if tok == "--" {
			i++
			break
		}
		if tok == "-i" || tok == "--ignore-environment" {
			i++
			continue
		}
		if (tok == "-u" || tok == "-S") && i+1 < len(fields) {
			i += 2
			continue
		}
		if shellparse.IsAssignment(tok) {
			i++
			continue
		}
		break
	}
	return fields[i:]
}

// quoteFields 把静态 argv 拼回可再解析的 shell 源码：安全字符集原样输出
// （`--force`、路径等保持裸词形态，后续文本级规则的词边界匹配不受引号
// 干扰），其余单引号包裹（内嵌单引号按 `'\”` 转义）。空词输出 `”`
// （裸空词会在再解析时消失）。
func quoteFields(fields []string) string {
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = quoteField(f)
	}
	return strings.Join(quoted, " ")
}

func quoteField(field string) string {
	if field != "" && isBareSafeField(field) {
		return field
	}
	return "'" + strings.ReplaceAll(field, "'", `'\''`) + "'"
}

// isBareSafeField 判断词可以不加引号回写：字母数字与 shell 非特殊字符集
// （含路径字符与 `~`——裸 `~` 的再解析形态与原文一致，文本级规则按原书写
// 形态匹配）。刻意排除 `$` “ ` “ 等展开字符：StaticWord 产出的静态词不会
// 含未转义展开，但引号内字面量可能带它们，必须重新加引号防止回写后变成
// 真实展开。
func isBareSafeField(field string) bool {
	for i := 0; i < len(field); i++ {
		c := field[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '_' || c == '@' || c == '%' || c == '+' || c == '=' || c == ':' ||
			c == ',' || c == '.' || c == '/' || c == '~' || c == '-' || c == '^':
		default:
			return false
		}
	}
	return true
}
