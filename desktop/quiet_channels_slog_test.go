package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// captureQuietChannelSlog redirects the default logger for one test (these
// tests are not parallel) so a fixed silent-channel line can be asserted by
// its exact wording - the grep text the acceptance criterion names.
func captureQuietChannelSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

// Task 304 channel 1 (lease): the lease-held rejection used to wrap-return to
// the UI only, leaving desktop.log at zero hits for the incident's key
// hypothesis. Both legs must log with the wording the reader greps.
func TestLeaseHeldRejectionLogs(t *testing.T) {
	buf := captureQuietChannelSlog(t)

	wrapped := userFacingSessionLeaseError("model", agent.ErrSessionLeaseHeld)
	if wrapped == nil {
		t.Fatal("userFacingSessionLeaseError = nil, want the UI wrapper")
	}
	if out := buf.String(); !strings.Contains(out, "session lease held; refusing session access") || !strings.Contains(out, "setting=model") {
		t.Fatalf("lease rejection log missing/incorrect: %q", out)
	}

	// Leg 2: the contention retry gave up on a foreign holder.
	buf.Reset()
	var zero struct{}
	got, err := withSessionLeaseContentionRetry(func() (struct{}, error) {
		return zero, agent.ErrSessionLeaseHeld
	})
	if err == nil || got != zero {
		t.Fatalf("contention retry = (%v, %v), want the held error surfaced", got, err)
	}
	if out := buf.String(); !strings.Contains(out, "session lease still held after contention retries") {
		t.Fatalf("contention-giveup log missing: %q", out)
	}
}

// Task 304 channel 2 (single-instance lock): yielding to the existing process
// used to run with a zero-slog function body; the yield line must ship before
// the window is shown. The zero-value App is safe (showMainWindowFrom returns
// on a nil ctx), so this pins the log line itself.
func TestSecondInstanceLaunchLogsYield(t *testing.T) {
	buf := captureQuietChannelSlog(t)
	(&App{}).secondInstanceLaunch()
	if out := buf.String(); !strings.Contains(out, "second instance launched while a previous desktop is still running") {
		t.Fatalf("second-instance yield log missing: %q", out)
	}
}
