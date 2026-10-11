package boot

import (
	"context"
	"testing"

	"reasonix/internal/baseproc"
	"reasonix/internal/config"
	"reasonix/internal/tool"
)

// TestStartBaseClientAlwaysInline pins the 任务785 retirement guarantee: the
// experimental_base_process switch is gone, so startBaseClient must NEVER
// build a manager or spawn a subprocess — every build gets the bare inline
// client (R4: zero side effects, the pre-S1 behaviour byte for byte).
func TestStartBaseClientAlwaysInline(t *testing.T) {
	client := startBaseClient(context.Background(), &config.Config{}, tool.NewRegistry())
	if client.Mode() != baseproc.ModeInline {
		t.Fatalf("mode = %q, want inline (base subprocess is retired, 任务785)", client.Mode())
	}
}
