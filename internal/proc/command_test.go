package proc

import (
	"context"
	"os/exec"
	"testing"
)

func TestCommandConstructorsPreserveArguments(t *testing.T) {
	background := Command("reasonix-helper", "--probe", "a b")
	if len(background.Args) != 3 || background.Args[1] != "--probe" || background.Args[2] != "a b" {
		t.Fatalf("background args = %#v", background.Args)
	}
	visible := VisibleCommandContext(context.Background(), "reasonix-ui", "--open")
	if len(visible.Args) != 2 || visible.Args[1] != "--open" {
		t.Fatalf("visible args = %#v", visible.Args)
	}
}

// TestCommandConstructorsDoNotInterpretShellMetacharacters pins the task-415
// code-scanning command-injection triage: the proc wrappers hand argv arrays
// straight to exec and never route through a shell, so a metacharacter
// payload must survive verbatim as a single argument instead of being split
// or expanded. Callers that need a shell name the shell explicitly
// (bash/sh/cmd with -c) behind the tool approval flow.
func TestCommandConstructorsDoNotInterpretShellMetacharacters(t *testing.T) {
	payload := "out.txt; rm -rf / && echo pwned `id` $(id) | tee /etc/passwd"
	cases := map[string]*exec.Cmd{
		"Command":           Command("git", "log", payload),
		"CommandContext":    CommandContext(context.Background(), "git", "log", payload),
		"VisibleCommand":    VisibleCommand("git", "log", payload),
		"VisibleCommandCtx": VisibleCommandContext(context.Background(), "git", "log", payload),
	}
	for name, cmd := range cases {
		if len(cmd.Args) != 3 || cmd.Args[0] != "git" || cmd.Args[1] != "log" {
			t.Fatalf("%s rewrote argv: %#v", name, cmd.Args)
		}
		if cmd.Args[2] != payload {
			t.Fatalf("%s split or rewrote the payload: %#v", name, cmd.Args)
		}
	}
}
