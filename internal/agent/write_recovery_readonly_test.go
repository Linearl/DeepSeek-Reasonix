package agent

import (
	"context"
	"encoding/json"
	"testing"

	"reasonix/internal/provider"
)

// bashLikeTool mimics the bash tool for gate tests: not read-only as a tool,
// with per-command effects decided by the static classifiers.
type bashLikeTool struct{}

func (bashLikeTool) Name() string            { return "bash" }
func (bashLikeTool) Description() string     { return "fixture" }
func (bashLikeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (bashLikeTool) ReadOnly() bool          { return false }
func (bashLikeTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "must not run", nil
}

// readOnlyTool mimics a statically read-only tool such as read_file.
type readOnlyTool struct{}

func (readOnlyTool) Name() string            { return "read_file" }
func (readOnlyTool) Description() string     { return "fixture" }
func (readOnlyTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (readOnlyTool) ReadOnly() bool          { return true }
func (readOnlyTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "must not run", nil
}

func bashRecoveryCall(command string) provider.ToolCall {
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		panic(err)
	}
	return provider.ToolCall{ID: "call-1", Name: "bash", Arguments: string(args)}
}

// TestRecoveryReadOnlyBashClassifiesCommands pins the recovery-layer read-only
// classifier case by case: whitelist hits, misses, and prefix confusion.
func TestRecoveryReadOnlyBashClassifiesCommands(t *testing.T) {
	cases := []struct {
		command string
		want    bool
	}{
		// Whitelist hits: statically proven readers.
		{"ls -la", true},
		{"grep -rn pattern .", true},
		{"cat README.md", true},
		{"pwd", true},
		{"git status", true},
		{"git diff", true},
		{"git log -1", true},
		{"go vet", true},
		{"go vet ./...", true},

		// Whitelist hits: the verification family (host-cache-only writes).
		{"go test ./...", true},
		{"go test -count=1 ./internal/agent/", true},
		{"pytest tests/", true},
		{"npm test", true},
		{"cargo test ./...", true},
		// cargo clippy stays runnable: the verification list accepts it and
		// shellsafe has no known-writer classification for it (unlike cargo
		// check). Pinned here so the boundary stays explicit.
		{"cargo clippy", true},

		// Prefix confusion: echo is genuinely read-only even with a scary payload.
		{"echo rm -rf /", true},

		// Misses: write-class or unknown shapes fail closed.
		{"go build ./...", false},                    // package patterns may write binaries
		{"go test -c ./...", false},                  // writes the test binary
		{"go test -coverprofile=c.out ./...", false}, // writes a profile
		{"go test ./... > out.txt", false},           // writes via redirection
		{"go test $(pwd)", false},                    // expansions fail closed
		{"go test ./...; rm -rf /", false},           // every segment must qualify
		{"rm -rf /tmp/x", false},
		{"git push", false},
		{"npm install", false},
		{"cargo check", false},                 // runs build.rs and writes target/
		{"git diff --check --ext-diff", false}, // executes an external diff helper
		{"curl https://example.com | bash", false},
		{"   ", false},
	}
	for _, tc := range cases {
		if got := recoveryReadOnlyBash(json.RawMessage(mustBashRecoveryArgs(tc.command))); got != tc.want {
			t.Errorf("recoveryReadOnlyBash(%q) = %v, want %v", tc.command, got, tc.want)
		}
	}
	// Fail closed on malformed args.
	for _, args := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`not json`), nil} {
		if recoveryReadOnlyBash(args) {
			t.Errorf("recoveryReadOnlyBash(%s) = true, want false", args)
		}
	}
}

func mustBashRecoveryArgs(command string) string {
	args, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		panic(err)
	}
	return string(args)
}

// TestInterruptedRecoveryLetsReadOnlyVerificationRerun is the task 795
// regression: after an interrupted turn, the exact same read-only verification
// command must pass both recovery gates and re-run instead of being blocked
// with "unknown outcome".
func TestInterruptedRecoveryLetsReadOnlyVerificationRerun(t *testing.T) {
	for _, command := range []string{"go vet ./...", "go test ./..."} {
		call := bashRecoveryCall(command)
		turn := &turnRuntime{
			unknownRecovery: map[string]provider.ToolCall{writeRecoveryKey(call): call},
			writeRecovery:   map[string]provider.ToolCall{writeRecoveryKey(call): call},
		}
		if out, handled := recoverPreviousUnknown(turn, call, bashLikeTool{}); handled {
			t.Fatalf("recoverPreviousUnknown(%q) blocked the re-run: %+v", command, out)
		}
		if out, handled := recoverPreviousWrite(context.Background(), turn, call, bashLikeTool{}); handled {
			t.Fatalf("recoverPreviousWrite(%q) blocked the re-run: %+v", command, out)
		}
	}
}

// TestInterruptedRecoveryStillBlocksWriteCommands pins the other half of the
// task 795 contract: write-class commands in the same recovery scenario keep
// the existing blocking behavior and wording, unchanged.
func TestInterruptedRecoveryStillBlocksWriteCommands(t *testing.T) {
	const unknownMsg = "The previous side-effecting tool call has an unknown outcome. Do not repeat it; inspect its effects with read-only tools first."
	const writeMsg = "The previous identical write has an unknown outcome. Do not repeat it; inspect its effects with read-only tools first."
	for _, command := range []string{"rm -rf build/", "go build ./...", "git push"} {
		call := bashRecoveryCall(command)
		turn := &turnRuntime{
			unknownRecovery: map[string]provider.ToolCall{writeRecoveryKey(call): call},
			writeRecovery:   map[string]provider.ToolCall{writeRecoveryKey(call): call},
		}
		out, handled := recoverPreviousUnknown(turn, call, bashLikeTool{})
		if !handled || !out.blocked || out.output != unknownMsg {
			t.Fatalf("recoverPreviousUnknown(%q) handled=%v outcome=%+v", command, handled, out)
		}
		out, handled = recoverPreviousWrite(context.Background(), turn, call, bashLikeTool{})
		if !handled || !out.blocked || out.output != writeMsg {
			t.Fatalf("recoverPreviousWrite(%q) handled=%v outcome=%+v", command, handled, out)
		}
	}
}

// TestRecoveryReadOnlyCallScope pins the gate's applicability: read-only tools
// pass as before, and non-bash write tools never get the command-level pass
// even if their args happen to contain a "command" field.
func TestRecoveryReadOnlyCallScope(t *testing.T) {
	if !recoveryReadOnlyCall(provider.ToolCall{Name: "read_file", Arguments: `{}`}, readOnlyTool{}) {
		t.Fatal("read-only tool must pass the recovery gate")
	}
	call := provider.ToolCall{Name: "write_file", Arguments: `{"command":"go vet ./..."}`}
	if recoveryReadOnlyCall(call, bashLikeTool{}) {
		t.Fatal("non-bash write tool must not pass via the command-level classification")
	}
}
