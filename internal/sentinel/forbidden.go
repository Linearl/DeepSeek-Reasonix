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
func checkForcePushSegment(seg string) Verdict {
	fields, malformed := shellparse.StaticFields(seg)
	if malformed != "" {
		if isGitPushHead(seg) && forceFlagRe.MatchString(seg) {
			return forcePushVerdict("unknown")
		}
		return Verdict{}
	}
	if len(fields) < 3 || !strings.EqualFold(fields[0], "git") || !strings.EqualFold(fields[1], "push") {
		return Verdict{}
	}
	if !forceFlagRe.MatchString(seg) {
		return Verdict{}
	}
	protected := protectedBranchList()
	sawRefspec := false
	seenRemote := false
	for _, f := range fields[2:] {
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

// systemConfigWriteRe matches in-place write verbs and redirects on the
// segment text; the protected-path substring check pins the target, so a
// redirect to an ordinary file never trips the rule.
var systemConfigWriteRe = regexp.MustCompile(`(?i)(?:^|\s)(sed\s+-\w*i|tee|rm|shred|truncate|set-content|add-content|out-file)(?:\s|$)|>{1,2}`)

// copyVerbRe matches cp/move spellings; for these the protected path must be
// the destination (final non-flag argument).
var copyVerbRe = regexp.MustCompile(`(?i)^(?:cp|copy|mv|move|move-item)\b`)

func checkWriteSystemConfigSegment(seg string) Verdict {
	if path := matchProtectedPath(seg); path != "" && systemConfigWriteRe.MatchString(seg) {
		return writeSystemConfigVerdict(path)
	}
	// cp/mv: only when the protected path is the destination argument.
	fields, malformed := shellparse.StaticFields(seg)
	if malformed == "" && len(fields) > 2 && copyVerbRe.MatchString(fields[0]) {
		last := fields[len(fields)-1]
		if p := matchProtectedPath(expandTilde(last)); p != "" && p == normalizePath(expandTilde(last)) {
			return writeSystemConfigVerdict(p)
		}
	}
	return Verdict{}
}

// checkWriteSystemConfigPath blocks file-tool writes (or moves) whose path
// resolves to a protected system config file. Read tools are unaffected:
// write_file/edit_file/move_file are the only callers.
func checkWriteSystemConfigPath(subject string) Verdict {
	if path := matchProtectedPath(subject); path != "" {
		return writeSystemConfigVerdict(path)
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

func writeSystemConfigVerdict(path string) Verdict {
	return Verdict{
		Blocked: true,
		Rule:    RuleWriteSystemConfig,
		Reason: "blocked by sentinel rule write_system_config: " + path +
			" 是 Reasonix 系统级配置/凭据存储，模型写入属硬禁区（任何审批模式含 yolo 下都拦截；文件工具的人工审批门不豁免本规则）。" +
			"替代路径：告诉用户要改哪个设置，由用户在设置页或自己编辑；配置问题可先读出来汇报。",
	}
}
