package sentinel

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSecretShapeDetection pins the v1 detection forms from the task body:
// *_API_KEY= assignments, known key prefixes, .env-shaped lines.
func TestSecretShapeDetection(t *testing.T) {
	hits := []string{
		"sk-proj-abc123defGHIjklMNOpq",
		"sk-abcdef1234567890abcdef12",
		"ghp_Abc123Def456Ghi789Jkl012MnO",
		"github_pat_ABCDEF1234567890abcdef1234567890abCD",
		"xoxb-123456789012-1234567890123-abcdefabcdef",
		"AKIAIOSFODNN7EXAMPLE",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJVadQssw5c",
		`MY_API_KEY=abcdefghijklmnop`,
		`openai_api_key: "abcdef1234567890"`,
		`AUTH_TOKEN=Abcdef123456789`,
		"OPENAI_API_KEY=sk1234567890123",
		"DB_PASSWORD=supersecretvalue123",
	}
	for _, s := range hits {
		if shape := scanText(s); shape == "" {
			t.Errorf("want hit, got clean: %q", s)
		}
	}

	clean := []string{
		"fix: add GITHUB_TOKEN support to the importer",
		"API_KEY=${VAULT_KEY}",      // indirection, not a value
		"API_KEY=",                  // empty value
		"password: hunter2",         // too short
		"DOC: describes API_KEY semantics without a value",
		"const maxRetries = 12",
		"docs/api-keys.md explains rotation",
		"",
	}
	for _, s := range clean {
		if shape := scanText(s); shape != "" {
			t.Errorf("want clean, got hit (%s): %q", shape, s)
		}
	}
}

// TestURLSecretParams covers the query-parameter surface of web_fetch.
func TestURLSecretParams(t *testing.T) {
	hits := []string{
		"https://api.example.com/v1/data?api_key=abcdef123456",
		"https://host.example/path?token=AbcDef123456",
		"https://host.example/path?access_token=abcdef123456789",
	}
	for _, s := range hits {
		if scanURLs(s) == "" {
			t.Errorf("want URL param hit: %q", s)
		}
	}
	clean := []string{
		"https://api.example.com/v1/users?page=2",
		"https://host.example/path?q=api+key+rotation",
	}
	for _, s := range clean {
		if got := scanURLs(s); got != "" {
			t.Errorf("want clean URL, got %s: %q", got, s)
		}
	}
}

// TestExitScanLegalOps is the false-positive regression set for the exit
// scan: ordinary outbound work must pass untouched.
func TestExitScanLegalOps(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetExitScan(true)
		SetHardForbidden(false) // isolate the exit scan under test
		legal := []string{
			`git commit -m "fix: handle empty config"`,
			`git commit -m "docs: describe token rotation"`,
			`git push origin feature-x`,
			`git push`,
			`curl https://api.example.com/v1 -d '{"name":"x"}'`,
			`curl -H "Accept: application/json" https://api.example.com/items`,
			`gh pr create --title "add feature" --body "implements the export"`,
			`npm publish`,
			`{"url": "https://api.example.com/v1/users?page=2"}`,
			`{"query": "reasonix sentinel 设计"}`,
		}
		for _, cmd := range legal {
			if strings.HasPrefix(cmd, "{") {
				expectAllowed(t, "web_fetch", json.RawMessage(cmd))
				continue
			}
			tool, args := bashCall(t, cmd)
			expectAllowed(t, tool, args)
		}
	})
}

// gitRepo builds a throwaway git repository with one commit on main and
// returns its path.
func gitRepo(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git 二进制不可用，出口扫描的仓库实跑测试无法运行: %v", err)
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(out.String()))
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Sentinel Test")
	run("commit", "--allow-empty", "-m", "init")
	return dir
}

func writeRepoFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestExitScanBlocksFakeKeyCommit is the acceptance probe: a commit carrying
// a fake key is caught before it can leave the machine — both at commit time
// (staged diff) and at push time (outgoing commits). The staging happens as
// its own allowed call (`git add` is not outbound), mirroring the real
// sequence; the blocked commit call itself must not execute.
func TestExitScanBlocksFakeKeyCommit(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetExitScan(true)
		dir := gitRepo(t)

		writeRepoFile(t, dir, "config.json", `{"provider":"example","api_key":"abcdef1234567890"}`)
		// The `git add` call ran in a previous, allowed tool call:
		gitRun(t, dir, "add", "config.json")

		// Commit time: the staged diff contains the key.
		tool, args := bashCall(t, `git commit -m "add config"`)
		if v := CheckToolCall(tool, args, dir); !v.Blocked || v.Rule != RuleExitSecretScan {
			t.Fatalf("commit with fake key must be blocked by %s, got %+v", RuleExitSecretScan, v)
		}
		// The probe must not have executed: a blocked pre-check means the
		// commit never ran — the staged change is still there.
		out := gitCapture(t, dir, "diff", "--cached", "--name-only")
		if !strings.Contains(out, "config.json") {
			t.Fatalf("blocked commit must not have run; staged files: %q", out)
		}

		// Simulate the commit having slipped through earlier (e.g. via an
		// editor message): push time must still catch the outgoing commit.
		gitRun(t, dir, "commit", "--allow-empty", "-m", "add config api_key=abcdef1234567890")
		tool, args = bashCall(t, `git push origin main`)
		if v := CheckToolCall(tool, args, dir); !v.Blocked || v.Rule != RuleExitSecretScan {
			t.Fatalf("push with fake-key outgoing commit must be blocked, got %+v", v)
		}
	})
}

// TestExitScanAllowsCleanCommit proves zero false positives on ordinary work
// in the same test-repo setting.
func TestExitScanAllowsCleanCommit(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetExitScan(true)
		dir := gitRepo(t)

		writeRepoFile(t, dir, "README.md", "# hello\n\nNormal content about token rotation.\n")
		gitRun(t, dir, "add", "README.md")
		tool, args := bashCall(t, `git commit -m "docs: readme"`)
		if v := CheckToolCall(tool, args, dir); v.Blocked {
			t.Fatalf("clean commit must pass, got blocked by %s: %s", v.Rule, v.Reason)
		}
		tool, args = bashCall(t, `git push origin main`)
		if v := CheckToolCall(tool, args, dir); v.Blocked {
			t.Fatalf("clean push must pass, got blocked by %s: %s", v.Rule, v.Reason)
		}
	})
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(out.String()))
	}
}

func gitCapture(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, strings.TrimSpace(out.String()))
	}
	return out.String()
}

// TestAuditRecordWritten verifies the JSONL interception trail (who/when/
// which rule) and that the recorded subject is redacted.
func TestAuditRecordWritten(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetHardForbidden(true)
		audit := filepath.Join(t.TempDir(), "sentinel", "intercepts.jsonl")
		SetAuditPath(audit)

		tool, args := bashCall(t, "git push --force origin main")
		expectBlocked(t, tool, args, RuleForcePushProtected)

		data, err := os.ReadFile(audit)
		if err != nil {
			t.Fatalf("audit file must exist after a block: %v", err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) != 1 {
			t.Fatalf("want 1 audit line, got %d", len(lines))
		}
		var rec InterceptRecord
		if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
			t.Fatalf("audit line must be JSON: %v", err)
		}
		if rec.Rule != RuleForcePushProtected || rec.Tool != "bash" {
			t.Errorf("rule/tool = %s/%s, want %s/bash", rec.Rule, rec.Tool, RuleForcePushProtected)
		}
		if rec.Time == "" || rec.WorkspaceRoot != "" {
			t.Errorf("record missing time or has unexpected root: %+v", rec)
		}
		if !strings.Contains(rec.Subject, "git push") {
			t.Errorf("subject should carry the command text: %+v", rec)
		}

		// An empty audit path disables the file but blocks still work.
		SetAuditPath("")
		expectBlocked(t, tool, args, RuleForcePushProtected)
	})
}

// TestExitscanDisabledByDefaultDoubleCheck keeps the documented posture
// explicit: turning the scan off restores ordinary outbound flow even for
// key-bearing (fake) content.
func TestExitscanDisabledByDefaultDoubleCheck(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetExitScan(false)
		tool, args := bashCall(t, fmt.Sprintf("git commit -m %q", "key=abcdef1234567890"))
		expectAllowed(t, tool, args)
	})
}
