package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/heartbeatcfg"
)

func writeTempHeartbeatConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "heartbeat-tasks.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// 验收 ①：干净配置 exit 0 且报 OK；事故原样配置（createdAt 字符串）exit 1
// 且 Human-readable 输出点名 tasks[0].createdAt 与行号。
func TestRunHeartbeatValidateVerdicts(t *testing.T) {
	clean := writeTempHeartbeatConfig(t, `{"schemaVersion":4,"revision":1,"tasks":[{"id":"a","title":"T","prompt":"P","interval":"5m","enabled":true}]}`)
	var out string
	var rc int
	out = captureStdout(t, func() {
		rc = runHeartbeatValidate([]string{"--file", clean})
	})
	if rc != verdictExitConfirmed {
		t.Fatalf("clean exit=%d want 0, out=%q", rc, out)
	}
	if !strings.Contains(out, "OK") {
		t.Fatalf("clean output=%q want OK", out)
	}

	broken := writeTempHeartbeatConfig(t, "{\n  \"tasks\": [\n    {\"id\": \"a\", \"interval\": \"5m\", \"createdAt\": \"yesterday\"}\n  ]\n}")
	out = captureStdout(t, func() {
		rc = runHeartbeatValidate([]string{"--file", broken})
	})
	if rc != verdictExitRefuted {
		t.Fatalf("broken exit=%d want 1, out=%q", rc, out)
	}
	if !strings.Contains(out, "tasks[0].createdAt") || !strings.Contains(out, "(line 3)") {
		t.Fatalf("broken output=%q want per-field line with line number", out)
	}
}

// 验收 ④：三类错误在同一份配置里各自显式报出——类型错、重复 id、坏 interval。
func TestRunHeartbeatValidateReportsAllThreeClasses(t *testing.T) {
	data := `{
  "schemaVersion": 4,
  "tasks": [
    {"id": "dup", "interval": "5m", "createdAt": "oops"},
    {"id": "dup", "interval": "nope"},
    {"id": "x", "interval": "0 99 * * *"}
  ]
}`
	path := writeTempHeartbeatConfig(t, data)
	var out string
	var rc int
	out = captureStdout(t, func() {
		rc = runHeartbeatValidate([]string{"--file", path})
	})
	if rc != verdictExitRefuted {
		t.Fatalf("exit=%d want 1", rc)
	}
	for _, want := range []string{
		"[unmarshal] tasks[0].createdAt",
		"[duplicate-id] tasks[1].id",
		"[interval] tasks[1].interval",
		"[interval] tasks[2].interval",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

// --json：结构化输出可反序列化，kind 字段可机读（脚本/agent 消费用）。
func TestRunHeartbeatValidateJSONOutput(t *testing.T) {
	path := writeTempHeartbeatConfig(t, `{"schemaVersion":4,"tasks":[{"id":"a","interval":"bogus"},{"id":"a","interval":"5m"}]}`)
	var out string
	var rc int
	out = captureStdout(t, func() {
		rc = runHeartbeatValidate([]string{"--file", path, "--json"})
	})
	if rc != verdictExitRefuted {
		t.Fatalf("exit=%d want 1", rc)
	}
	var problems []heartbeatcfg.Problem
	if err := json.Unmarshal([]byte(out), &problems); err != nil {
		t.Fatalf("json output not decodable: %v\n%s", err, out)
	}
	if len(problems) != 2 {
		t.Fatalf("problems=%d want 2 (interval + duplicate): %+v", len(problems), problems)
	}
}

// 文件不存在：usage 级退出码 2，错误写 stderr 而不是当配置问题报。
func TestRunHeartbeatValidateMissingFile(t *testing.T) {
	rc := runHeartbeatValidate([]string{"--file", filepath.Join(t.TempDir(), "absent.json")})
	if rc != verdictExitUsage {
		t.Fatalf("exit=%d want 2", rc)
	}
}

// 未知子命令：usage + exit 2。
func TestHeartbeatCommandUnknownSubcommand(t *testing.T) {
	if rc := heartbeatCommand([]string{"frobnicate"}); rc != verdictExitUsage {
		t.Fatalf("exit=%d want 2", rc)
	}
	if rc := heartbeatCommand(nil); rc != verdictExitUsage {
		t.Fatalf("no-args exit=%d want 2", rc)
	}
}
