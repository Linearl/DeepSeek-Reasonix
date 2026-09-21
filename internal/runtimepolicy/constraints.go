package runtimepolicy

import (
	"path/filepath"
	"regexp"
	"strings"

	"reasonix/internal/shellparse"
	"reasonix/internal/taskcontract"
)

// Constraints are explicit user or host limits. They never encode task
// complexity, security keywords, or file counts.
type Constraints struct {
	ForbidMutation          bool
	ForbidTests             bool
	AllowedChecks           []string
	ForbidExternal          bool
	RequireFullVerification bool
	// AllowRebuild records that the user explicitly asked to rewrite a file
	// completely. It only ever waives the read-before-overwrite requirement for
	// a file the same instruction names; the model can never set it.
	AllowRebuild bool
	// RebuildPaths are the resolved files an AllowRebuild instruction named.
	// The waiver is a membership test over this host-recorded set, never a
	// re-parse of instruction text at write time.
	RebuildPaths     []string
	PlanModeReadOnly bool
	// PolicyFloor is the session quality floor, set from session state only —
	// never parsed from user text. It stamps receipts at write time.
	PolicyFloor taskcontract.PolicyFloor
	Notes       []string
}

// ParseConstraints accepts only explicit forbid/limit phrasing.
func ParseConstraints(instruction string) Constraints {
	var c Constraints
	lower := strings.ToLower(instruction)
	if hasGlobalMutationBan(lower) {
		c.ForbidMutation = true
		c.Notes = append(c.Notes, "user_forbid_mutation")
	}
	if matchesAny(lower, []string{
		"不要测试", "别跑测试", "不用测试", "跳过测试", "不要跑测试",
		"don't run tests", "do not run tests", "no tests", "skip tests",
		"without tests", "don't test", "do not test",
	}) {
		c.ForbidTests = true
		c.Notes = append(c.Notes, "user_forbid_tests")
	}
	if matchesAny(lower, []string{
		"完整验证", "全面验证", "闭环交付", "完整交付", "交付前检查", "验收闭环",
		"full verification", "complete verification", "verify everything",
		"closed-loop delivery", "deliver with verification",
	}) {
		c.RequireFullVerification = true
		c.Notes = append(c.Notes, "user_require_full_verification")
	}
	if matchesAny(lower, []string{
		"完全重写", "从头重写", "整个重写", "直接重写", "覆盖重写", "整个文件重写",
		"from scratch", "rewrite it completely", "rewrite the file completely",
		"overwrite it completely", "replace it entirely", "rebuild the file",
		"rewrite this file", "rewrite the whole file",
	}) {
		c.AllowRebuild = true
		c.Notes = append(c.Notes, "user_allow_rebuild")
	}
	if cmds := parseAllowedChecks(instruction); len(cmds) > 0 {
		c.AllowedChecks = cmds
		c.Notes = append(c.Notes, "user_allowed_checks")
	}
	if matchesAny(lower, []string{
		"不要 push", "不要push", "别 push", "别push", "不要推送", "不要发布",
		"don't push", "do not push", "no push", "don't publish", "do not publish",
		"no publish", "don't deploy", "do not deploy",
	}) {
		c.ForbidExternal = true
		c.Notes = append(c.Notes, "user_forbid_external")
	}
	return c
}

// hasGlobalMutationBan distinguishes a turn-wide read-only instruction from a
// scoped protection such as "do not change any config". The latter still lets
// the requested output or an unrelated implementation target be written.
func hasGlobalMutationBan(instruction string) bool {
	for _, clause := range mutationConstraintClauses(instruction) {
		clause = strings.TrimSpace(strings.TrimLeft(clause, "-*•0123456789. )\t"))
		if clause == "" {
			continue
		}
		if hasExplicitReadOnlyClause(clause) || hasGlobalNegatedMutationClause(clause) {
			return true
		}
	}
	return false
}

func mutationConstraintClauses(instruction string) []string {
	return strings.FieldsFunc(instruction, func(r rune) bool {
		switch r {
		case '\n', '\r', '.', '!', '?', ';', '。', '！', '？', '；':
			return true
		default:
			return false
		}
	})
}

func hasExplicitReadOnlyClause(clause string) bool {
	if hasMutationContinuation(clause) {
		return false
	}
	for _, phrase := range []string{
		"analyze only", "analysis only", "read-only review", "read only review",
		"reproduce only", "reproduce but don't fix", "reproduce but do not fix",
		"只分析", "仅分析", "只看不改", "复现但不修复", "只复现", "仅复现",
	} {
		if strings.Contains(clause, phrase) {
			return true
		}
	}
	trimmed := strings.TrimSpace(clause)
	if trimmed == "read-only" || strings.HasPrefix(trimmed, "read-only ") ||
		trimmed == "read only" || strings.HasPrefix(trimmed, "read only ") {
		return true
	}
	// Task 220 (P1): a bare 「只读…」prefix can no longer bind by itself — a
	// report describing a third party ("只读审计，未重跑测试") must not freeze
	// the turn. Bind only the bare imperative (只读 / 只读模式), one scoped to
	// this turn (本次/这轮), or one where an analysis verb follows directly.
	if !strings.HasPrefix(trimmed, "只读") {
		return false
	}
	if trimmed == "只读" || trimmed == "只读模式" {
		return true
	}
	if strings.Contains(trimmed, "本次") || strings.Contains(trimmed, "这轮") {
		return true
	}
	normalized := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "只读模式"), "只读"))
	for _, verb := range []string{
		"分析", "看", "检查", "审阅", "跑", "读", "浏览", "研究", "评估", "查",
		"analyze", "review", "read", "inspect", "check", "run", "verify",
	} {
		if strings.HasPrefix(normalized, verb) || strings.HasPrefix(normalized, " "+verb) {
			return true
		}
	}
	return false
}

func hasMutationContinuation(clause string) bool {
	for _, marker := range []string{" then ", " and then ", " but then ", "然后", "再", "接着"} {
		_, tail, ok := strings.Cut(clause, marker)
		if !ok {
			continue
		}
		if matchesAny(tail, []string{
			"fix", "repair", "implement", "write", "edit", "change", "modify", "create", "commit", "push",
			"修复", "实现", "编写", "写入", "编辑", "修改", "创建", "提交", "推送",
		}) {
			return true
		}
	}
	return false
}

func hasGlobalNegatedMutationClause(clause string) bool {
	if describesReadOnlyActor(clause) {
		return false
	}
	for _, phrase := range []string{
		"don't modify", "do not modify", "don't change", "do not change",
		"don't edit", "do not edit", "without modifying", "without changes",
	} {
		if tail, ok := textAfterPhrase(clause, phrase); ok && globalMutationTail(tail) {
			return true
		}
	}
	for _, phrase := range []string{"don't fix", "do not fix", "no fix"} {
		if tail, ok := textAfterPhrase(clause, phrase); ok && globalFixTail(tail) {
			return true
		}
	}
	if tail, ok := textAfterPhrase(clause, "no changes"); ok && globalNoChangesTail(tail) {
		return true
	}
	if tail, ok := textAfterPhrase(clause, "make no changes"); ok && globalNoChangesTail(tail) {
		return true
	}
	for _, phrase := range []string{"不要修改", "不要改动", "不要改", "别修改", "别改", "勿修改"} {
		if tail, ok := textAfterPhrase(clause, phrase); ok && globalChineseMutationTail(tail) {
			return true
		}
	}
	for _, phrase := range []string{"不要修复", "不要修", "别修复", "别修"} {
		if tail, ok := textAfterPhrase(clause, phrase); ok && globalChineseFixTail(tail) {
			return true
		}
	}
	return false
}

func describesReadOnlyActor(clause string) bool {
	return matchesAny(clause, []string{
		"reviewer", "sub-agent", "subagent", "child agent", "child", "planner",
		"审查者", "评审者", "子代理", "子 agent", "规划器",
	}) && matchesAny(clause, []string{"read-only", "read only", "只读"})
}

func textAfterPhrase(clause, phrase string) (string, bool) {
	_, tail, ok := strings.Cut(clause, phrase)
	return strings.TrimSpace(tail), ok
}

func globalMutationTail(tail string) bool {
	if tail == "" {
		return true
	}
	if strings.HasPrefix(tail, ":") {
		return false
	}
	return hasBroadTarget(tail)
}

func globalFixTail(tail string) bool {
	return tail == "" || startsWithAnyWord(tail, []string{"anything", "anything else", "any issue", "any issues"})
}

func globalNoChangesTail(tail string) bool {
	if tail == "" {
		return true
	}
	return startsWithAnyWord(tail, []string{
		"anywhere", "at all", "to anything", "to the workspace", "to the repository", "to the repo", "to the codebase",
	})
}

func hasBroadTarget(tail string) bool {
	return startsWithAnyWord(tail, []string{
		"anything", "anything else", "the workspace", "this workspace", "workspace",
		"the repository", "this repository", "repository", "the repo", "this repo", "repo",
		"the codebase", "this codebase", "codebase", "any file", "any files", "all files", "the source tree",
	})
}

func startsWithAnyWord(value string, prefixes []string) bool {
	value = strings.TrimSpace(value)
	for _, prefix := range prefixes {
		if value == prefix || strings.HasPrefix(value, prefix+" ") || strings.HasPrefix(value, prefix+",") {
			return true
		}
	}
	return false
}

func globalChineseMutationTail(tail string) bool {
	if tail == "" {
		return true
	}
	if strings.HasPrefix(tail, "：") || strings.HasPrefix(tail, ":") {
		return false
	}
	return startsWithAnyChinese(tail, []string{
		"任何内容", "任何东西", "任何文件", "所有文件", "工作区", "当前工作区",
		"仓库", "当前仓库", "代码库", "当前代码库", "源码树",
	})
}

func globalChineseFixTail(tail string) bool {
	return tail == "" || startsWithAnyChinese(tail, []string{"任何问题", "任何内容", "其他任何问题"})
}

func startsWithAnyChinese(value string, prefixes []string) bool {
	value = strings.TrimSpace(value)
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

// StripQuotedConstraints removes fenced and quoted spans so cited phrases
// cannot bind the host, and [跨会话消息] delivery blocks (task 220): the body
// of a peer's message is DATA for this turn, not an instruction to it — a
// report that merely said 「只读审计」 once froze every write tool for a
// whole turn, and letting peer text reach the parser widened the
// prompt-injection surface for free.
func StripQuotedConstraints(raw string) string {
	s := stripCollabEnvelope(raw)
	s = stripFences(s)
	s = stripInlineCode(s)
	s = stripQuoted(s, '"', '"')
	s = stripQuoted(s, '“', '”')
	s = stripQuoted(s, '「', '」')
	return strings.TrimSpace(s)
}

// stripCollabEnvelope removes cross-session delivery blocks (task 220 M1).
// A block starts at the [跨会话消息] marker. The real renderer
// (sessionCollabDeliveryText) puts the body AFTER a blank line and closes it
// with a "\n\n---\n" separator followed by the identifying trailer lines
// (会话线程 / 关联任务卡片 / 回复方式 / 单向通知) — so the block is stripped
// structurally: to the end of the trailer line behind the separator. Stopping
// at the first blank line used to leave the body in place, where a
// 「不要修改X」 could still freeze the turn. A block that lost its separator
// (truncation, manual paste) strips to the next marker or the end of input:
// the body is data, and over-stripping costs less than a frozen turn.
func stripCollabEnvelope(s string) string {
	const marker = "[跨会话消息]"
	for {
		i := strings.Index(s, marker)
		if i < 0 {
			return s
		}
		rest := s[i+len(marker):]
		end := len(s)
		if j := strings.Index(rest, "\n\n---\n"); j >= 0 {
			// Real renderer shape: strip through the WHOLE trailer — the
			// identifying lines stack (thread id, reply instructions, the
			// require-reply banner), so the cut lands after the LAST one.
			trailer := rest[j:]
			cut := 0
			for _, tail := range []string{"会话线程：threadId=", "关联任务卡片：", "回复方式：", "这是一条单向通知：", "⚠ 发件人要求回信"} {
				if k := strings.Index(trailer, tail); k >= 0 {
					endOfLine := k + len(tail)
					if lineEnd := strings.Index(trailer[k:], "\n"); lineEnd >= 0 {
						endOfLine = k + lineEnd + 1
					}
					if endOfLine > cut {
						cut = endOfLine
					}
				}
			}
			if cut == 0 {
				cut = len(trailer)
			}
			end = i + len(marker) + j + cut
		} else if next := strings.Index(rest, marker); next >= 0 {
			end = i + len(marker) + next
		}
		s = s[:i] + s[end:]
	}
}

// rebuildPathPattern extracts candidate file tokens from one instruction clause.
var rebuildPathPattern = regexp.MustCompile("`[^`]+`|\"[^\"]+\"|'[^']+'|[A-Za-z0-9_./\\\\:-]+")

// ParseRebuildPaths resolves the files an instruction names in a clause that
// itself grants AllowRebuild. Callers record the result once per turn and
// authorize a rebuild by membership, so model-authored text can never grant the
// waiver at write time.
func ParseRebuildPaths(instruction, baseDir string) []string {
	var paths []string
	for _, clause := range strings.FieldsFunc(instruction, func(r rune) bool {
		return strings.ContainsRune("\n;；。!?！？", r)
	}) {
		if !ParseConstraints(clause).AllowRebuild {
			continue
		}
		lower := strings.ToLower(clause)
		if matchesAny(lower, []string{"不要", "别", "not ", "don't", "禁止"}) {
			continue
		}
		for _, token := range rebuildPathPattern.FindAllString(clause, -1) {
			token = strings.Trim(token, "`\"'")
			if token == "" {
				continue
			}
			if !filepath.IsAbs(token) {
				token = filepath.Join(baseDir, token)
			}
			paths = append(paths, filepath.Clean(token))
		}
	}
	return paths
}

func (c Constraints) AllowsMutation() bool {
	return !c.ForbidMutation && !c.PlanModeReadOnly
}

func (c Constraints) AllowsTests() bool { return !c.ForbidTests }

func (c Constraints) AllowsExternal() bool { return !c.ForbidExternal }

func (c Constraints) AllowsCommand(command string) bool {
	if !c.AllowsTests() {
		return false
	}
	command = strings.TrimSpace(command)
	if command == "" || len(c.AllowedChecks) == 0 {
		return true
	}
	for _, allowed := range c.AllowedChecks {
		if strings.EqualFold(strings.TrimSpace(allowed), command) {
			return true
		}
	}
	commandFields, malformed := shellparse.StaticFields(command)
	if malformed != "" || len(commandFields) == 0 {
		return false
	}
	for _, allowed := range c.AllowedChecks {
		allowedFields, malformed := shellparse.StaticFields(strings.TrimSpace(allowed))
		if malformed == "" && len(allowedFields) > 0 && hasFieldPrefix(commandFields, allowedFields) {
			return true
		}
	}
	return false
}

func parseAllowedChecks(instruction string) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)只跑\s+([^\n,，;；]+)`),
		regexp.MustCompile(`(?i)只运行\s+([^\n,，;；]+)`),
		regexp.MustCompile(`(?i)only\s+run\s+([^\n,;]+)`),
		regexp.MustCompile(`(?i)just\s+run\s+([^\n,;]+)`),
	}
	var out []string
	for _, re := range patterns {
		m := re.FindStringSubmatch(instruction)
		if len(m) < 2 {
			continue
		}
		cmd := strings.Trim(strings.TrimSpace(m[1]), "\"'`。.")
		if cmd != "" {
			out = append(out, cmd)
		}
	}
	return out
}

func matchesAny(lower string, needles []string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(lower, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func hasFieldPrefix(fields, prefix []string) bool {
	if len(prefix) > len(fields) {
		return false
	}
	for i := range prefix {
		if !strings.EqualFold(fields[i], prefix[i]) {
			return false
		}
	}
	return true
}

func stripFences(s string) string {
	var b strings.Builder
	inFence := false
	for line := range strings.SplitSeq(s, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			inFence = !inFence
			continue
		}
		if !inFence {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func stripInlineCode(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == '`' {
			in = !in
			continue
		}
		if !in {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func stripQuoted(s string, open, close rune) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if !in && r == open {
			in = true
			continue
		}
		if in && r == close {
			in = false
			continue
		}
		if !in {
			b.WriteRune(r)
		}
	}
	return b.String()
}
