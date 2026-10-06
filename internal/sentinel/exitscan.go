package sentinel

// Outbound secret scan (task 410, 底线二): before content leaves the machine,
// scan it for secret shapes; a hit blocks the operation. This is the mirror
// of design A's N2 (tool output redaction manages "into context", this
// manages "out to the network/repository").
//
// v1 detection forms from the task body: `*_API_KEY=` assignment lines, known
// key prefixes, `.env`-shaped lines. Detection is shape-based — a fake key in
// a commit is blocked exactly like a real one (that is the acceptance test).
//
// Surfaces and their coverage rationale live in the task 410 delivery report
// (外发面枚举清单). This file covers:
//
//	git commit   inline -m/--message text + the staged diff (git plumbing)
//	git push     outgoing commits (local branches not on any remote)
//	curl/scp/…   inline argument text (message/body/file arguments)
//	web_fetch    URL (query parameters carrying api_key/token=...)
//	web_search   query text
//
// The git plumbing runs `git -C <workspaceRoot>` with bounded output and
// fails open (scan unavailable ≠ blocked work) because the scan is an
// experimental switch; the hard-forbidden rules never fail open.

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"regexp"
	"strings"
)

const (
	gitScanMaxCommits = 50
	gitScanMaxBytes   = 1 << 20 // 1 MiB of scanned content per operation
)

// RuleExitSecretScan is the exit-scan rule id.
const RuleExitSecretScan = "exit_secret_scan"

// Secret shapes, v1. Ordered by specificity; the first hit names the shape.
var secretShapes = []struct {
	name string
	re   *regexp.Regexp
}{
	// Known provider key prefixes (same families as internal/secrets masks).
	{"OpenAI 风格 key（sk-/rk- 前缀）", regexp.MustCompile(`\b(?:sk|rk)-(?:proj-)?[A-Za-z0-9_-]{12,}\b`)},
	{"GitHub token（ghp_/gho_/ghu_/ghs_/ghr_/github_pat_ 前缀）", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9_]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)},
	{"Slack token（xox?- 前缀）", regexp.MustCompile(`\bxox(?:[abprs])-?[A-Za-z0-9-]{16,}\b`)},
	{"AWS access key（AKIA/ASIA 前缀）", regexp.MustCompile(`\bA(?:KIA|SIA)[0-9A-Z]{16}\b`)},
	{"JWT（三段式）", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`)},
	// Credential-named assignments with a value: *_API_KEY=..., SECRET=...
	// A value needs 12+ secret-ish characters so docs prose never matches.
	{"凭据赋值行（NAME=<值>）", regexp.MustCompile(`(?i)\b[A-Za-z0-9_-]*(?:API[_-]?KEY|ACCESS[_-]?KEY|SECRET|PRIVATE[_-]?KEY|AUTH[_-]?TOKEN|PASSWORD|PASSWD)[A-Za-z0-9_-]*['"]?\s*[:=]\s*['"]?[A-Za-z0-9+/_=-]{12,}`)},
	// .env-shaped line: CREDENTIAL_NAME=value where the name smells like a
	// credential (same name families as the assignment form).
	{".env 特征行", regexp.MustCompile(`(?im)^[A-Z][A-Z0-9_]*(?:API_KEY|SECRET|TOKEN|PASSWORD|PASSWD|PRIVATE_KEY)[A-Z0-9_]*=[^\s]{8,}$`)},
}

// URL query parameters that carry credentials.
var urlSecretParamRe = regexp.MustCompile(`(?i)[?&](?:api[_-]?key|access[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password|key)=([^&\s]{6,})`)

// outboundVerbs are bash command heads whose arguments are leaving (or about
// to be committed locally on the way out) and therefore get the inline scan.
var outboundVerbs = regexp.MustCompile(`(?i)^(?:git\s+commit|git\s+push|curl|scp|sftp|rsync|nc|ncat|netcat|twine|gh|npm\s+publish|pnpm\s+publish|yarn\s+publish)\b`)

func checkOutbound(toolName string, args json.RawMessage, subject, workspaceRoot string) Verdict {
	switch toolName {
	case "bash":
		return checkOutboundBash(subject, workspaceRoot)
	case "web_fetch", "web_search":
		return scanVerdict("URL/查询", subject)
	default:
		return Verdict{}
	}
}

func checkOutboundBash(cmd, workspaceRoot string) Verdict {
	for _, seg := range bashSegments(cmd) {
		if !outboundVerbs.MatchString(seg) {
			continue
		}
		// 1. Inline content: the command line itself (commit messages, curl
		// data bodies, gh pr/release text). URLs are stripped first — a
		// downloaded URL's path is not outbound content; its query params
		// are checked separately.
		if v := scanVerdict("命令参数", stripURLs(seg)); v.Blocked {
			return v
		}
		// 2. Git-borne content via deterministic plumbing. Fail-open.
		switch {
		case isGitCommit(seg):
			if v := scanVerdict("staged diff", gitContent(workspaceRoot,
				"diff", "--cached", "--unified=0")); v.Blocked {
				return v
			}
		case isGitPush(seg):
			if v := scanVerdict("待推送提交", gitContent(workspaceRoot,
				"log", "-p", "--format=medium", "--max-count=50", "--branches", "--not", "--remotes")); v.Blocked {
				return v
			}
		}
	}
	return Verdict{}
}

func isGitCommit(seg string) bool {
	f := strings.Fields(seg)
	return len(f) >= 2 && strings.EqualFold(f[0], "git") && strings.EqualFold(f[1], "commit")
}

func isGitPush(seg string) bool {
	f := strings.Fields(seg)
	return len(f) >= 2 && strings.EqualFold(f[0], "git") && strings.EqualFold(f[1], "push")
}

// gitContent runs one read-only git plumbing command in dir and returns its
// bounded output. Failures return "" — the scan then simply does not fire
// (fail-open), which the config comment documents.
func gitContent(dir string, gitArgs ...string) string {
	if dir == "" {
		return ""
	}
	git, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	cmd := exec.Command(git, append([]string{"-C", dir}, gitArgs...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil // plumbing noise is irrelevant; failure = skip scan
	if err := cmd.Run(); err != nil {
		return ""
	}
	b := out.Bytes()
	if len(b) > gitScanMaxBytes {
		b = b[:gitScanMaxBytes]
	}
	return string(b)
}

// scanText returns the first secret shape found in s, "" when clean.
func scanText(s string) string {
	if s == "" {
		return ""
	}
	for _, shape := range secretShapes {
		if shape.re.MatchString(s) {
			return shape.name
		}
	}
	return ""
}

// stripURLs removes URL substrings so downloads (`curl https://host/.env`)
// are not scanned as if their URL were outbound message content. URLs are
// separately checked for credential query parameters.
var urlStripRe = urlRe

func stripURLs(s string) string { return urlStripRe.ReplaceAllString(s, " ") }

func scanVerdict(what, content string) Verdict {
	shape := scanText(content)
	if shape == "" {
		if scanURLs(content) == "" {
			return Verdict{}
		}
		shape = "URL 查询参数携带凭据（key/token=...）"
	}
	return Verdict{
		Blocked: true,
		Rule:    RuleExitSecretScan,
		Reason: "blocked by sentinel rule exit_secret_scan: " + what +
			" 含 secret 形态（" + shape + "），已阻断本次外发操作（出口 secret 扫描，实验开关）。" +
			"替代路径：把值从外发内容移除后重试；确需传输凭据由用户人工执行。",
	}
}

// scanURLs checks URL query parameters for credential-carrying values.
func scanURLs(s string) string {
	if s == "" {
		return ""
	}
	if urlSecretParamRe.MatchString(s) {
		return "URL 查询参数携带凭据（key/token=...）"
	}
	return ""
}
