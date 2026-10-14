package sentinel

// Hard-forbidden rules (task 410, 底线一): operations refused in every
// approval mode, yolo included. v1 minimal list from the task body:
//
//	force_push_protected   git push --force/-f rewriting a protected branch
//	delete_critical_path   deleting .git, the workspace root, filesystem
//	                       roots, or Reasonix data directories (.reasonix/
//	                       .codegraph/home/state)
//	exfil_credentials      uploading .env / credential paths to the network
//	write_system_config    writing the user-global Reasonix config or the
//	                       credential store (settings page is the only door)
//
// Every rule is a pure function over the call's command text (or file path):
// no I/O beyond string matching, no subprocess, no model. Compound bash
// commands are evaluated segment by segment so `git add . && git commit -m x
// && git push --force origin main` cannot hide the push in the tail.

import (
	"os"
	"regexp"
	"strings"

	"reasonix/internal/shellparse"
	"reasonix/internal/shellsafe"
)

// Stable rule ids. Config [sentinel] disabled_rules references these strings.
const (
	RuleForcePushProtected = "force_push_protected"
	RuleDeleteCritical     = "delete_critical_path"
	RuleExfilCredentials   = "exfil_credentials"
	RuleWriteSystemConfig  = "write_system_config"
)

// homeDir is the registered Reasonix home directory (slash-normalized, no
// trailing separator), used for tilde expansion and data-dir protection.
var homeDir string

// SetHomeDir registers the Reasonix home directory for tilde expansion and
// home-deletion protection. "" clears it.
func SetHomeDir(dir string) { homeDir = normalizePath(dir) }

// checkForbidden evaluates the hard-forbidden rules for one tool call's
// command text. The first matching rule wins.
func checkForbidden(toolName, subject string) Verdict {
	switch {
	case toolName == "bash":
		return checkForbiddenBash(subject)
	case isFileMutationTool(toolName):
		return checkForbiddenPathTool(subject)
	default:
		return Verdict{}
	}
}

// isFileMutationTool mirrors permission.IsFileMutationTool without importing
// the permission package: the sentinel import surface is pinned by
// zero_model_test.go and stays intentionally minimal.
func isFileMutationTool(toolName string) bool {
	switch toolName {
	case "write_file", "edit_file", "multi_edit", "move_file", "notebook_edit", "delete_range", "delete_symbol":
		return true
	default:
		return false
	}
}

// bashSegments splits a compound command into simple-command segments. When
// the parser refuses (heredocs, unbalanced quotes, ...) the whole line is one
// segment, which keeps matching conservative: more text, not less.
func bashSegments(cmd string) []string {
	if parts, split, ok := shellparse.SplitTopLevel(cmd); ok && split && len(parts) > 0 {
		return parts
	}
	return []string{cmd}
}

func checkForbiddenBash(cmd string) Verdict {
	// 任务 895 穿透封堵（实验开关 sentinel_penetration_guard）：先做源级
	// 透视——wrapper（bash/sh -c/eval/sudo/env/裸赋值）解包、子 shell 与
	// 花括号组展平——再走四规则。解包本身永不产生判定，命中仍只可能来自
	// 四规则；开关关时这段完全不执行，文本与判定路径逐位等于 895 前现状。
	// heredoc/动态 payload/解析失败形态 SeeThroughWrappers 原文返回，由下方
	// 各规则既有的保守回退接管（维持现保守回退）。
	if PenetrationGuardEnabled() {
		cmd, _ = shellsafe.SeeThroughWrappers(cmd)
	}
	segs := bashSegments(cmd)
	for _, seg := range segs {
		if v := checkForcePushSegment(seg); v.Blocked {
			return v
		}
		if v := checkDeleteCriticalSegment(seg); v.Blocked {
			return v
		}
		if v := checkWriteSystemConfigSegment(seg); v.Blocked {
			return v
		}
	}
	return checkExfilCredentialsSegments(segs)
}

func checkForbiddenPathTool(subject string) Verdict {
	// move_file subjects concatenate "source destination"; the substring
	// check covers both directions — a move is as good as a write.
	if v := checkWriteSystemConfigPath(subject); v.Blocked {
		return v
	}
	return Verdict{}
}

// ─── force push ─────────────────────────────────────────────────────────────

// forceFlagRe matches the force flags of git push. `--force-with-lease` is
// deliberately NOT matched: it refuses when the remote moved underneath, so
// it is the guarded form the alternative path already recommends.
var forceFlagRe = regexp.MustCompile(`(?:^|\s)(?:--force|-f)(?:\s|$)`)

// checkForcePushSegment blocks `git push` invocations that would rewrite a
// protected branch. Parsing is positional: the first non-flag word after
// `push` is the remote (skipped — `origin` is not a branch), the remaining
// non-flag words are refspecs whose src and dst sides are both checked. A
// bare force push (no refspec) is also blocked: the target branch is
// unknowable from the command line alone, and the reason tells the caller to
// name the branch explicitly — a non-protected target passes on the retry,
// a protected one stays blocked.
//
// Task 851 只读自查：`--dry-run`/`-n` 只演算推送、不落盘，push 的只读形态——
// 演算放行，真实改写照拦。
//
// Task 895 穿透封堵（开关开时）：① git 判定跳过全局选项（-C/-c/--git-dir
// 等，对标 codex find_git_subcommand）——`git -C <dir> push --force` 不再
// 因 fields[1]≠push 放行；② force/dry-run 判定改按字段（引号感知）且只扫
// push 子命令之后的参数——全局选项值里碰巧出现的 `-f`/`-n` 字样不会误中；
// 顺带闭掉 `git push '--force'`（引号包裹旗标躲过词边界正则）的漏拦。
// 开关关时下面走 895 前的原路径，行为逐位不变。
func checkForcePushSegment(seg string) Verdict {
	fields, malformed := shellparse.StaticFields(seg)
	if malformed != "" {
		// 字段不可解析时不放行 dry-run：文本级豁免可被引号构造绕过
		// （`git push --force origin "x -n y"` 含 ` -n `），硬禁区误放的
		// 代价高于误拦 dry-run，该分支保持一律拦。
		if isGitPushHead(seg) && forceFlagRe.MatchString(seg) {
			return forcePushVerdict("unknown")
		}
		return Verdict{}
	}
	if len(fields) < 2 {
		return Verdict{}
	}
	subIdx := 1
	if PenetrationGuardEnabled() {
		idx, ok := gitPushSubcommandIndex(fields)
		if !ok {
			return Verdict{}
		}
		subIdx = idx
		if !hasPushForceFlag(fields[subIdx+1:]) {
			return Verdict{}
		}
		if hasPushDryRunFlag(fields[subIdx+1:]) {
			return Verdict{}
		}
	} else {
		if len(fields) < 3 || !strings.EqualFold(fields[0], "git") || !strings.EqualFold(fields[1], "push") {
			return Verdict{}
		}
		if !forceFlagRe.MatchString(seg) {
			return Verdict{}
		}
		if hasPushDryRunFlag(fields) {
			return Verdict{}
		}
	}
	protected := protectedBranchList()
	sawRefspec := false
	seenRemote := false
	for _, f := range fields[subIdx+1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		if !seenRemote {
			// First non-flag word is the remote name or URL, never a branch.
			seenRemote = true
			continue
		}
		sawRefspec = true
		word := strings.TrimPrefix(f, "+")
		for _, side := range strings.SplitN(word, ":", 3) {
			branch := strings.ToLower(lastPathSegment(strings.TrimPrefix(side, "refs/heads/")))
			if branchMatchesProtected(branch, protected) {
				return forcePushVerdict(branch)
			}
		}
	}
	if !sawRefspec {
		// Bare force push (remote only or nothing): target unknowable.
		return forcePushVerdict("unknown")
	}
	return Verdict{}
}

func isGitPushHead(seg string) bool {
	lower := strings.ToLower(strings.TrimSpace(seg))
	return strings.HasPrefix(lower, "git push ") || lower == "git push"
}

// hasPushDryRunFlag reports whether a parsed git push carries the dry-run
// flag. `-n` aggregates into short-flag clusters (`-nf`) via git
// parse-options; among git push's short flags only -n contains an 'n'. 字段
// 解析是引号感知的，引号消息里的 `-n` 字样不会误中。
func hasPushDryRunFlag(fields []string) bool {
	for _, f := range fields {
		if strings.EqualFold(f, "--dry-run") {
			return true
		}
		if strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "--") && strings.ContainsRune(f[1:], 'n') {
			return true
		}
	}
	return false
}

// gitPushSubcommandIndex (任务 895) 返回 git 的 push 子命令下标：跳过 git
// 全局选项后再找子命令，对标 codex is_dangerous_command.rs 的
// find_git_subcommand——`git -C <dir> push --force` 这类全局选项前缀不再让
// fields[1]≠push 的浅判定放行。首个非旗标词即子命令：不是 push 即返回未找到
// （与 codex 的 subcommands 列表判定同义，防把后面的 refspec 误认成子命令）。
// 命令词按 basename 比对（/usr/bin/git、git.exe 同样识别）。
func gitPushSubcommandIndex(fields []string) (int, bool) {
	if len(fields) < 2 || !isGitCommandWord(fields[0]) {
		return 0, false
	}
	skipValue := false
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		if skipValue {
			skipValue = false
			continue
		}
		if gitGlobalTakesValue(f) {
			skipValue = true
			continue
		}
		if gitGlobalInlineValue(f) {
			continue
		}
		if f == "--" || strings.HasPrefix(f, "-") {
			continue
		}
		if strings.EqualFold(f, "push") {
			return i, true
		}
		return 0, false
	}
	return 0, false
}

func isGitCommandWord(word string) bool {
	base := strings.ToLower(word)
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".exe")
	return base == "git"
}

// gitGlobalTakesValue 是取值与选项分写的 git 全局选项（codex 同名单）。
func gitGlobalTakesValue(f string) bool {
	switch f {
	case "-C", "-c", "--config-env", "--exec-path", "--git-dir",
		"--namespace", "--super-prefix", "--work-tree":
		return true
	default:
		return false
	}
}

// gitGlobalInlineValue 是值内联在选项里的形态（--git-dir=<path>、-C<path>、
// -c<name>=<value>）。`-C`/`-c` 要求长度 >2，裸旗标走分写分支。
func gitGlobalInlineValue(f string) bool {
	if strings.HasPrefix(f, "--config-env=") || strings.HasPrefix(f, "--exec-path=") ||
		strings.HasPrefix(f, "--git-dir=") || strings.HasPrefix(f, "--namespace=") ||
		strings.HasPrefix(f, "--super-prefix=") || strings.HasPrefix(f, "--work-tree=") {
		return true
	}
	return (strings.HasPrefix(f, "-C") || strings.HasPrefix(f, "-c")) && len(f) > 2
}

// hasPushForceFlag 按字段判定 push 的 force 旗标（引号感知：StaticFields 已
// 把 `'--force'` 还原为裸词）。`--force` 精确匹配；短旗标簇取 -f 前缀
// （`-f`/`-fn`；`--force-with-lease` 起双横线不在其列，守卫形态照旧放行）。
// 只该喂 push 子命令之后的参数——全局选项值不在其中。
func hasPushForceFlag(fields []string) bool {
	for _, f := range fields {
		if f == "--force" {
			return true
		}
		if strings.HasPrefix(f, "-f") && !strings.HasPrefix(f, "--") {
			return true
		}
	}
	return false
}

func branchMatchesProtected(branch string, protected []string) bool {
	branch = strings.ToLower(strings.TrimSpace(branch))
	if branch == "" {
		return false
	}
	for _, p := range protected {
		if branch == p {
			return true
		}
	}
	return false
}

func lastPathSegment(s string) string {
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func forcePushVerdict(branch string) Verdict {
	target := branch
	if target == "unknown" {
		target = "未知分支（命令未写明）"
	}
	return Verdict{
		Blocked: true,
		Rule:    RuleForcePushProtected,
		Reason: "blocked by sentinel rule force_push_protected: git push --force/-f 不能用于改写保护分支 " + target +
			"（任何审批模式含 yolo 下都拦截）。" +
			"替代路径：普通 `git push` 推送新提交；确需覆盖远端历史，先把改动存成 patch 或推到新分支（git push origin HEAD:refs/heads/recovery-<日期>），等人工确认后由人执行覆盖。" +
			"若目标确实是非保护分支，请显式写出分支名重试。",
	}
}

// ─── critical deletion ──────────────────────────────────────────────────────

// deleteVerbRe matches rm-family deletion commands at segment head. PowerShell
// spellings included; compound segments are checked per segment.
var deleteVerbRe = regexp.MustCompile(`(?i)^(rm|rmdir|unlink|del|erase|remove-item|ri)\b`)

// criticalDataDirRe matches Reasonix-owned data directory path components at
// word boundaries so `.github`/`.gitignore` never match. `\.git` requires the
// component to end at a separator or string end.
var criticalDataDirRe = regexp.MustCompile(`(?:^|[/\\])(\.(?:git|reasonix|codegraph))(?:[/\\]|$)`)

// exactRootTargets are deletion targets that mean "everything": the
// filesystem root, home, drive roots, the workspace root itself, and their
// wildcard spellings. Matched exactly against the path argument — subpaths
// (`C:/Users/me/project`) are ordinary deletions and must pass.
var exactRootTargets = map[string]bool{
	"/": true, "//": true, "/*": true, "/*/*": true,
	"~": true, "~/": true, "~/*": true,
	".": true, "./": true, "./*": true, "..": true, "../": true, "../*": true,
	"*": true, "**": true,
}

// driveRootRe matches a bare Windows drive root (`C:\`, `d:/`, `E:\\*`).
var driveRootRe = regexp.MustCompile(`(?i)^[A-Za-z]:[/\\]\*{0,2}$`)

// topDirRe matches system top-level directories whose wholesale deletion is
// never a routine operation (`/home`, `/Users`, `/etc`, `/usr`, `/var`,
// `/Windows`, `C:\Windows`, ...). Subpaths pass.
var topDirRe = regexp.MustCompile(`(?i)^(?:/|[A-Za-z]:[/\\])(?:home|users|etc|usr|var|windows|program files(?: \(x86\))?|programdata)(?:[/\\])?$`)

func checkDeleteCriticalSegment(seg string) Verdict {
	fields, malformed := shellparse.StaticFields(seg)
	if malformed != "" {
		// Opaque segment: fall back to the unambiguous signal only (a delete
		// verb directly naming a data dir component).
		if deleteVerbRe.MatchString(seg) && criticalDataDirRe.MatchString(seg) {
			return deleteCriticalVerdict("git/数据目录（不可解析命令形态）")
		}
		return Verdict{}
	}
	if len(fields) == 0 || !deleteVerbRe.MatchString(fields[0]) {
		return Verdict{}
	}
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") || f == "" {
			continue
		}
		if v := deleteTargetVerdict(f); v.Blocked {
			return v
		}
	}
	return Verdict{}
}

// deleteTargetVerdict classifies one deletion target argument.
func deleteTargetVerdict(f string) Verdict {
	if criticalDataDirRe.MatchString(f) {
		return deleteCriticalVerdict(lastPathSegment(strings.TrimRight(f, "/\\")))
	}
	if exactRootTargets[f] || driveRootRe.MatchString(f) || topDirRe.MatchString(f) {
		return deleteCriticalVerdict(f)
	}
	// The registered Reasonix home: deleting it or anything under it.
	if expanded := expandTilde(f); expanded != "" && homeDir != "" {
		if expanded == homeDir || strings.HasPrefix(expanded, homeDir+"/") {
			return deleteCriticalVerdict(f)
		}
	}
	return Verdict{}
}

func deleteCriticalVerdict(target string) Verdict {
	return Verdict{
		Blocked: true,
		Rule:    RuleDeleteCritical,
		Reason: "blocked by sentinel rule delete_critical_path: 删除 " + target +
			" 属硬禁区（.git/工作区根/文件系统根/Reasonix 数据目录，任何审批模式含 yolo 下都拦截）。" +
			"替代路径：删除具体文件或普通构建产物目录；确需清理数据目录，先说明影响并把清单交给用户人工执行。",
	}
}

// ─── credential exfiltration ────────────────────────────────────────────────

// credentialPathRe matches credential-bearing file references the agent has
// no business uploading. The prefix class keeps prose words out ("env" in a
// sentence never matches; ".env" as a path/argument does).
var credentialPathRe = regexp.MustCompile(`(?i)(?:^|[/\\@"\s=])(\.env|\.ssh|\.git-credentials|\.netrc|id_rsa|id_ed25519|\.pem|\.pfx|\.p12)(?:[/\\@"\s]|$)`)

// credentialDirRe matches credential directory/file names a bit more loosely
// (aws-style `credentials`, `credential` dirs).
var credentialDirRe = regexp.MustCompile(`(?i)(?:^|[/\\@"\s=])(credentials?)(?:[/\\@"\s]|$)`)

// urlRe strips URLs before credential-path matching so `curl https://host/.env`
// (a download) is never mistaken for an upload of the local .env.
var urlRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^ ]*`)

// exfilVerbRe matches network-send command heads. Plain `wget URL` (download)
// is not matched; wget only counts with upload/post flags.
var exfilVerbRe = regexp.MustCompile(`(?i)^(?:curl|scp|sftp|rsync|nc|ncat|netcat)\b`)

var wgetExfilRe = regexp.MustCompile(`(?i)^wget\b.*(?:--post-file|--body-file|--upload-file|--post-data)`)

func checkExfilCredentialsSegments(segs []string) Verdict {
	// Pipelines carry content left to right (`cat .env | curl --data-binary
	// @-`), so a credential path in one segment feeding a send verb in the
	// NEXT segment is the same exfiltration as both in one segment. Non-pipe
	// compounds (`;`, `&&`) are deliberately not joined: reading .env and an
	// unrelated later curl must not block each other (documented residual:
	// multi-hop pipelines with intermediates pass).
	for i, seg := range segs {
		if !exfilVerbRe.MatchString(seg) && !wgetExfilRe.MatchString(seg) {
			continue
		}
		withPrev := seg
		if i > 0 {
			withPrev = segs[i-1] + "\n" + seg
		}
		withoutURLs := urlRe.ReplaceAllString(withPrev, " ")
		for _, loc := range credentialPathRe.FindAllStringIndex(withoutURLs, -1) {
			name := strings.TrimSpace(strings.TrimLeft(withoutURLs[loc[0]:loc[1]], "/\\@\" ="))
			// Public halves of keypairs are publishable; only the private
			// half is a credential (id_rsa.pub, and a .pub file named inside
			// the .ssh directory component).
			if strings.HasSuffix(name, ".pub") || nextComponentEndsWith(withoutURLs, loc[1], ".pub") {
				continue
			}
			return exfilVerdict(name)
		}
		for _, m := range credentialDirRe.FindAllString(withoutURLs, -1) {
			return exfilVerdict(strings.TrimSpace(strings.TrimLeft(m, "/\\@\" =")))
		}
	}
	return Verdict{}
}

// nextComponentEndsWith reports whether the path component at the match
// position (the match regex consumes its trailing separator) ends with
// suffix — e.g. `~/.ssh/id_rsa.pub`: the .ssh match is followed by the
// component `id_rsa.pub`.
func nextComponentEndsWith(s string, from int, suffix string) bool {
	rest := strings.TrimLeft(s[from:], "/\\")
	end := len(rest)
	for i, r := range rest {
		if r == '/' || r == '\\' || r == ' ' || r == '\n' {
			end = i
			break
		}
	}
	return strings.HasSuffix(rest[:end], suffix)
}

func exfilVerdict(name string) Verdict {
	return Verdict{
		Blocked: true,
		Rule:    RuleExfilCredentials,
		Reason: "blocked by sentinel rule exfil_credentials: 命令把凭据文件（" + name +
			"）发往网络属硬禁区（任何审批模式含 yolo 下都拦截）。" +
			"替代路径：需要某个配置值时指名问用户或用设置页名称引用；上传产物用不含凭据的文件。",
	}
}

// ─── system config writes ───────────────────────────────────────────────────

// Task 851 判定升级：此前按「段内出现敏感路径 + 段内出现任意写特征词或 '>'
// 字符」拦截，纯读命令文本里的 '=>' '>=' '->' 或写动词词形即误拦 grep/cat
// 等排查命令（实锤：1006 排查 847 时 grep 只读被拦；python 拼路径绕过 =
// 文本层拦截可被规避）。现按「写特征的操作数是否敏感路径」判定：
//
//   - 重定向：shellparse.RedirectTargets 在 AST 级提取写目标，目标是敏感
//     路径才拦；目标含运行时展开（> $f）不可判定 → 保守拦（防 $var 间接写）。
//   - 写动词：按段头 + 参数语义判定——sed -i/--in-place 原地写、tee/touch/
//     shred/truncate/PowerShell 写动词的位置参数、rm 族删除、cp/mv 仅目标
//     （末个非旗标参数）命中、find -delete/-exec。
//
// 已知只读命令（grep/cat/ls/stat/head/tail/find/wc/diff 等）不靠白名单放行
// ——白名单有害（find -delete 证明白名单内也有写形态），「无写特征即放行」
// 是默认，只读形态在测试矩阵逐例钉住。判定仍是纯规则（零模型，410 约束），
// 不做任何模型判断。

// headWriteVerbTextRe 保守匹配段头写动词，仅在 RedirectTargets 不可用
// （heredoc / 解析失败 / 重定向目标含展开）时使用：形态不可静态判定，段头
// 是写动词族即拦（宁滥勿漏；cp/mv 在此不分源目标，因参数不可解析）。
var headWriteVerbTextRe = regexp.MustCompile(
	`(?i)^\s*(?:sed|tee|touch|shred|truncate|set-content|add-content|out-file|` +
		`cp|copy|mv|move|move-item|rm|rmdir|unlink|del|erase|remove-item|ri)\b`)

// copyVerbRe matches cp/move spellings; for these the protected path must be
// the destination (final non-flag argument) to count as a write.
var copyVerbRe = regexp.MustCompile(`(?i)^(?:cp|copy|mv|move|move-item)\b`)

func checkWriteSystemConfigSegment(seg string) Verdict {
	// 1) 重定向：AST 级提取写目标，目标命中敏感路径才拦（目标支持 tilde
	//    形态——expandTilde 后匹配）。
	targets, redirOK := shellparse.RedirectTargets(seg)
	if !redirOK {
		// heredoc / 解析失败 / 目标含运行时展开：写目标不可知，保守回退。
		// 段以任何形态携带敏感路径（原文本或 tilde 展开后的参数）、且段头
		// 是写动词或文本含裸 '>' 才拦（heredoc 的 '<<' 不含 '>'，
		// `cat config.toml << EOF` 这类纯读形态照常放行）。
		if p := segmentProtectedPathText(seg); p != "" &&
			(headWriteVerbTextRe.MatchString(seg) || strings.Contains(seg, ">")) {
			return writeSystemConfigVerdict(p, "不可静态判定的写入形态")
		}
		return Verdict{}
	}
	for _, t := range targets {
		if p := matchProtectedPath(expandTilde(t)); p != "" {
			return writeSystemConfigVerdict(p, "重定向写入")
		}
	}
	// 2) 写动词：按段头 + 参数语义判定。
	return writeVerbVerdict(seg)
}

// segmentProtectedPathText reports the protected path a raw segment carries,
// checking the text itself and every whitespace field after tilde expansion
// (参数以 `~/...` 形态出现时整段文本不命中绝对路径，逐字段展开兜住).
func segmentProtectedPathText(seg string) string {
	if p := matchProtectedPath(seg); p != "" {
		return p
	}
	for _, f := range strings.Fields(seg) {
		if p := matchProtectedPath(expandTilde(f)); p != "" {
			return p
		}
	}
	return ""
}

// writeVerbVerdict classifies the segment head: which verbs write in place,
// which treat their arguments as write targets, and which only count when the
// protected path is the destination. Fields 不可解析时（参数含运行时展开、
// 重定向等）按空白粗分词回退——旗标词不含空格，粗分词足够旗标级判定。
func writeVerbVerdict(seg string) Verdict {
	fields, malformed := shellparse.StaticFields(seg)
	if malformed != "" {
		fields = strings.Fields(seg)
	}
	if len(fields) == 0 {
		return Verdict{}
	}
	head := strings.ToLower(shellparse.WordBase(fields[0]))
	switch {
	case head == "sed":
		// sed 只有原地写（-i/--in-place）才算写；`sed -n '1,10p' cfg` 是读。
		if sedInPlaceFlags(fields) {
			if p := fieldsProtectedPath(fields); p != "" {
				return writeSystemConfigVerdict(p, "sed 原地改写（-i/--in-place）")
			}
		}
	case head == "tee":
		// tee 的位置参数全是写入目标（-a 追加也是写）。
		if p := fieldsProtectedPath(fields); p != "" {
			return writeSystemConfigVerdict(p, "tee 写入")
		}
	case head == "touch":
		if p := fieldsProtectedPath(fields); p != "" {
			return writeSystemConfigVerdict(p, "touch 创建/更新")
		}
	case head == "shred", head == "truncate":
		if p := fieldsProtectedPath(fields); p != "" {
			return writeSystemConfigVerdict(p, "原地覆写/截断")
		}
	case head == "set-content", head == "add-content", head == "out-file":
		if p := fieldsProtectedPath(fields); p != "" {
			return writeSystemConfigVerdict(p, "PowerShell 写入")
		}
	case deleteVerbRe.MatchString(fields[0]):
		// 删除动词把祖先目录连同其中的敏感文件一起消灭，用祖先匹配。
		if p := fieldsProtectedPathOrAncestor(fields); p != "" {
			return writeSystemConfigVerdict(p, "删除")
		}
	case head == "find":
		// find 名义只读，但这些动作位写/删/执行；-delete 删目录同 rm。
		if hasFindWriteAction(fields) {
			p := fieldsProtectedPath(fields)
			if p == "" && fieldsContain(fields, "-delete") {
				p = fieldsProtectedPathOrAncestor(fields)
			}
			if p != "" {
				return writeSystemConfigVerdict(p, "find -delete/-exec 等写动作")
			}
		}
	case copyVerbRe.MatchString(fields[0]):
		return copyMoveVerdict(fields)
	}
	return Verdict{}
}

// fieldsProtectedPath reports the protected path carried by any field, tilde
// expansion included.
func fieldsProtectedPath(fields []string) string {
	for _, f := range fields {
		if p := matchProtectedPath(expandTilde(f)); p != "" {
			return p
		}
	}
	return ""
}

// fieldsProtectedPathOrAncestor additionally matches fields that name an
// ancestor directory of a protected path (`rm -rf <reasonix home>` 删树时
// 其中的敏感文件一并被删). Only deletion-shaped verbs use this — touch/tee
// 对目录不产生文件写入，祖先匹配会造成误拦。
func fieldsProtectedPathOrAncestor(fields []string) string {
	if p := fieldsProtectedPath(fields); p != "" {
		return p
	}
	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			continue
		}
		expanded := expandTilde(f)
		if expanded == "" {
			continue
		}
		for _, p := range protectedPathList() {
			if len(p) > len(expanded) && strings.HasPrefix(p, expanded) && p[len(expanded)] == '/' {
				return p
			}
		}
	}
	return ""
}

// fieldsContain reports whether the field list holds the exact word.
func fieldsContain(fields []string, word string) bool {
	for _, f := range fields {
		if f == word {
			return true
		}
	}
	return false
}

// hasFindWriteAction reports whether the find invocation carries an action
// primary that writes, deletes, or executes.
func hasFindWriteAction(fields []string) bool {
	for _, f := range fields[1:] {
		switch strings.ToLower(f) {
		case "-delete", "-exec", "-execdir", "-ok", "-okdir", "-fprint", "-fprintf", "-fls":
			return true
		}
	}
	return false
}

// copyMoveVerdict blocks cp/mv only when the protected path is the destination
// (final non-flag argument); copying FROM the config outward is a read.
func copyMoveVerdict(fields []string) Verdict {
	dest := ""
	for i := len(fields) - 1; i > 0; i-- {
		if strings.HasPrefix(fields[i], "-") {
			continue
		}
		dest = fields[i]
		break
	}
	if dest == "" {
		return Verdict{}
	}
	expanded := expandTilde(dest)
	if p := matchProtectedPath(expanded); p != "" && p == normalizePath(expanded) {
		return writeSystemConfigVerdict(p, "复制/移动覆盖")
	}
	return Verdict{}
}

// sedInPlaceFlags reports whether any flag asks sed to edit files in place
// (`-i`, `-in`, `-i.bak`, `--in-place`, `--in-place=.bak`). GNU/BSD 短旗标
// 组合里 'i' 只属 in-place，旗标含 'i' 即视为原地写（保守方向；任务 851 顺带
// 补上旧正则漏掉的 `--in-place` 长形式）。
func sedInPlaceFlags(fields []string) bool {
	for _, f := range fields[1:] {
		if !strings.HasPrefix(f, "-") || f == "-" || f == "--" {
			continue
		}
		if strings.HasPrefix(f, "--") {
			if name, _, _ := strings.Cut(strings.TrimPrefix(f, "--"), "="); strings.EqualFold(name, "in-place") {
				return true
			}
			continue
		}
		if strings.ContainsRune(strings.SplitN(strings.TrimPrefix(f, "-"), ".", 2)[0], 'i') {
			return true
		}
	}
	return false
}

// checkWriteSystemConfigPath blocks file-tool writes (or moves) whose path
// resolves to a protected system config file. Read tools are unaffected:
// write_file/edit_file/move_file are the only callers.
func checkWriteSystemConfigPath(subject string) Verdict {
	if path := matchProtectedPath(subject); path != "" {
		return writeSystemConfigVerdict(path, "文件工具写入/移动")
	}
	return Verdict{}
}

// matchProtectedPath reports which registered protected path the text names,
// comparing slash-normalized absolute paths as substrings. Both sides are
// normalized (forward slashes), so `C:\Users\...\config.toml` and its
// forward-slash spelling both match. A prefix must end at a separator or
// string end so `.../reasonix-other/config.toml` never matches the home's
// paths.
func matchProtectedPath(text string) string {
	normalized := normalizePath(text)
	if normalized == "" {
		return ""
	}
	for _, p := range protectedPathList() {
		idx := strings.Index(normalized, p)
		if idx < 0 {
			continue
		}
		// The character after the match must not continue the same path
		// name (`.../reasonix-other/config.toml` shares the home prefix but
		// is a different tree). Any separator, space, quote, or operator is
		// a boundary.
		if tail := normalized[idx+len(p):]; tail == "" || !isPathNameByte(tail[0]) {
			return p
		}
	}
	return ""
}

// isPathNameByte reports whether b can be part of a file/directory name, i.e.
// would extend the matched path rather than end it.
func isPathNameByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' ||
		b == '_' || b == '-' || b == '.' || b == '/' || b == '\\'
}

// osUserHomeDir is the tilde resolver, a package var so tests can pin it.
var osUserHomeDir = os.UserHomeDir

// expandTilde resolves a leading ~ against the USER home (what ~ means in a
// shell — not the Reasonix home) and slash-normalizes every path. Tilde forms
// return "" when the user home is unavailable; ordinary paths are returned
// normalized regardless.
func expandTilde(f string) string {
	f = strings.TrimSpace(f)
	if f == "~" || strings.HasPrefix(f, "~/") || strings.HasPrefix(f, `~\`) {
		home, err := osUserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		home = normalizePath(home)
		if f == "~" {
			return home
		}
		return normalizePath(home + "/" + f[2:])
	}
	return normalizePath(f)
}

// writeSystemConfigVerdict is the block for every write-shaped hit on a
// protected path. action names the triggering shape (重定向写入 / sed 原地改写
// / tee 写入 / …)，与判定行为对齐；文案同时明示只读访问不受本规则限制
// （task 851 用户拍板：读不需要拦截）。
func writeSystemConfigVerdict(path, action string) Verdict {
	return Verdict{
		Blocked: true,
		Rule:    RuleWriteSystemConfig,
		Reason: "blocked by sentinel rule write_system_config: " + action +
			"（" + path + "）属硬禁区——该路径是 Reasonix 系统级配置/凭据存储，" +
			"任何审批模式含 yolo 下都拦截（文件工具的人工审批门不豁免本规则；只读访问不受本规则限制）。" +
			"替代路径：告诉用户要改哪个设置，由用户在设置页或自己编辑；读取该配置用于排查可以直接进行。",
	}
}
