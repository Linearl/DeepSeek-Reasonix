package zcodebridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealCLILink is the spec §三 compatibility probe against a REAL zcode
// CLI when one is installed (handshake gate + session/list + session/events).
// Read-only by design: it never injects sendText into a user's live session —
// injection semantics are covered against the mock (TestSendTextEnvelopeAndAcks).
//
// Backend selection, never a skip (workspace discipline: a silent skip is an
// untested claim):
//   - installed ZCode desktop CLI found  → real link (spec acceptance #1/#2)
//   - otherwise, or -short               → same assertions over the healthy
//     mock child, so the probe always exercises the client end to end.
//
// Environment override for other layouts: ZCODEBRIDGE_REAL_CJS=<path to
// zcode.cjs>, or ZCODEBRIDGE_REAL_COMMAND/ZCODEBRIDGE_REAL_ARGS (newline
// separated) for a CLI on PATH.
func TestRealCLILink(t *testing.T) {
	cfg, real := realCLILinkConfig(t)
	if real {
		t.Logf("real-link probe against installed zcode CLI")
	} else {
		t.Logf("no installed zcode CLI detected; probe runs against the mock child")
		cfg = helperConfig(t, mockGood)
	}
	cfg.HandshakeTimeout = 60 * time.Second

	b, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("probe: open failed (frozen face mismatch or spawn failure): %v", err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sessions, err := b.List(ctx)
	if err != nil {
		t.Fatalf("probe: session/list failed: %v", err)
	}
	t.Logf("session/list ok: %d session(s)", len(sessions))
	if len(sessions) > 0 {
		// Acceptance #2: enumerate ≥1 session and read its event increment.
		// A cold (not materialized) session answers ErrSessionNotActive — a
		// domain state, accepted as "nothing to read right now".
		_, err := b.Events(ctx, sessions[0].SessionID, 0, 5)
		switch {
		case err == nil:
			t.Logf("session/events ok on %s", sessions[0].SessionID)
		case errors.Is(err, ErrSessionNotActive):
			t.Logf("session/events: %s is cold in this app-server process (expected for idle workspaces)", sessions[0].SessionID)
		default:
			t.Fatalf("probe: session/events failed: %v", err)
		}
	}

	// The same gate again on the live connection: the upgrade-regression
	// entry point (spec §三 version-freeze rule).
	if err := b.Probe(ctx); err != nil {
		t.Fatalf("probe: second handshake failed: %v", err)
	}
}

// realCLILinkConfig finds an installed zcode CLI. Order: explicit env, the
// known ZCode desktop install layout, then "zcode" on PATH.
func realCLILinkConfig(t *testing.T) (Config, bool) {
	t.Helper()
	if p := os.Getenv("ZCODEBRIDGE_REAL_CJS"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return Config{Command: "node", Args: []string{p, "app-server", "--stdio"}}, true
		}
		t.Fatalf("ZCODEBRIDGE_REAL_CJS=%s does not exist", p)
	}
	if c := os.Getenv("ZCODEBRIDGE_REAL_COMMAND"); c != "" {
		args := []string{"app-server", "--stdio"}
		if raw := os.Getenv("ZCODEBRIDGE_REAL_ARGS"); raw != "" {
			args = splitLines(raw)
		}
		return Config{Command: c, Args: args}, true
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		p := filepath.Join(local, "Programs", "ZCode", "resources", "glm", "zcode.cjs")
		if _, err := os.Stat(p); err == nil {
			return Config{Command: "node", Args: []string{p, "app-server", "--stdio"}}, true
		}
	}
	return Config{}, false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			if line := trimSpace(s[start:i]); line != "" {
				out = append(out, line)
			}
			start = i + 1
		}
	}
	if len(out) == 0 {
		return []string{"app-server", "--stdio"}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
