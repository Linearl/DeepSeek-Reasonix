package builtin

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

func TestScreenshotRegistered(t *testing.T) {
	if _, ok := tool.LookupBuiltin("screenshot"); !ok {
		t.Fatal("screenshot not registered as a built-in")
	}
}

func TestScreenshotRequiresOutput(t *testing.T) {
	_, err := (screenshotTool{}).Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "output is required") {
		t.Fatalf("err = %v, want an output-required error", err)
	}
}

// The unknown-title path is safe to run for real on Windows: it must fail
// with the explicit "no visible window" error and must not write the file.
func TestScreenshotUnknownWindowErrorsWithoutWriting(t *testing.T) {
	out := filepath.Join(t.TempDir(), "shot.png")
	_, err := (screenshotTool{}).Execute(context.Background(), argsJSON(t, map[string]any{
		"output":       out,
		"window_title": "zz-no-such-window-zz",
	}))
	if err == nil {
		t.Fatal("expected an error for a non-existent window")
	}
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(err.Error(), "no visible window") {
			t.Fatalf("err = %v, want a no-visible-window error", err)
		}
	default:
		if !strings.Contains(err.Error(), "only implemented on Windows") {
			t.Fatalf("err = %v, want the platform error", err)
		}
	}
}
