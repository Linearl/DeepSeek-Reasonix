package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// ── task 663 gap ④: pending-crash analysis entry ─────────────────────────────

// The queued Go panic of the previous run must map into the schema-2 payload
// StartCrashAnalysis parses structurally, newest first.
func TestPendingCrashAnalysisPayloadsNewestFirstSchema2(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	writePendingCrash("site-old", "boom-old", []byte("stack-old"))
	writePendingCrash("site-new", "boom-new", []byte("stack-new"))

	payloads := pendingCrashAnalysisPayloads(pendingCrashPaths())
	if len(payloads) != 2 {
		t.Fatalf("payloads = %d, want 2", len(payloads))
	}
	var newest frontendCrashPayload
	if err := json.Unmarshal([]byte(payloads[0]), &newest); err != nil {
		t.Fatalf("newest payload not valid JSON: %v", err)
	}
	if newest.SchemaVersion != 2 {
		t.Errorf("newest schemaVersion = %d, want 2", newest.SchemaVersion)
	}
	if newest.Kind != "crash" || newest.Source != "go" {
		t.Errorf("newest kind/source = %q/%q, want crash/go", newest.Kind, newest.Source)
	}
	if !strings.Contains(newest.Message, "site-new") {
		t.Errorf("newest payload must carry the newest panic site, got: %s", newest.Message)
	}
	var oldest frontendCrashPayload
	if err := json.Unmarshal([]byte(payloads[1]), &oldest); err != nil {
		t.Fatalf("older payload not valid JSON: %v", err)
	}
	if !strings.Contains(oldest.Message, "site-old") {
		t.Errorf("older payload must carry the older panic site, got: %s", oldest.Message)
	}
	// The payload must survive the exact parser the analysis entry uses.
	r, err := crashReportFromDetail("crash", payloads[0])
	if err != nil {
		t.Fatalf("crashReportFromDetail rejected the pending payload: %v", err)
	}
	if r.Kind != "crash" || r.Source != "go" {
		t.Errorf("parsed kind/source = %q/%q, want crash/go", r.Kind, r.Source)
	}
}

// Startup/webview diagnostic writers queue schema-3 reports; the analysis
// snapshot must accept anything the flush would ship (≤ currentCrashSchema).
func TestPendingCrashAnalysisPayloadsAcceptsCurrentSchemaVariants(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	report := baseCrashReport("crash")
	report.SchemaVersion = currentCrashSchema
	report.Source = "startup"
	report.Message = "startup diagnostic panic"
	if !writePendingReport(report, true) {
		t.Fatal("writePendingReport failed")
	}

	payloads := pendingCrashAnalysisPayloads(pendingCrashPaths())
	if len(payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(payloads))
	}
	var payload frontendCrashPayload
	if err := json.Unmarshal([]byte(payloads[0]), &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload.SchemaVersion != 2 {
		t.Errorf("payload schemaVersion = %d, want 2 (analysis-facing schema)", payload.SchemaVersion)
	}
	if payload.Source != "startup" {
		t.Errorf("payload source = %q, want startup preserved", payload.Source)
	}
	if !contains(payload.Message, "startup diagnostic panic") {
		t.Errorf("payload message must carry the report, got: %s", payload.Message)
	}
}

// Gap ④'s point: the analysis entry must survive the flush — the flush ships
// (telemetry on, this test) or drops (telemetry off) the queue files either
// way, and the boot snapshot taken before it is the only copy the entry has.
func TestPendingCrashSnapshotSurvivesFlush(t *testing.T) {
	removeAllPendingCrashes()
	oldVersion, oldEndpoint := version, crashEndpoint
	t.Cleanup(func() {
		version, crashEndpoint = oldVersion, oldEndpoint
		removeAllPendingCrashes()
	})
	version = "v9.9.9"

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	crashEndpoint = srv.URL

	writePendingCrash("snapshot-survivor", "boom", []byte("stack"))

	app := NewApp()
	app.snapshotPendingCrashForAnalysis()
	app.flushPendingCrash()
	if hits.Load() != 1 {
		t.Fatalf("flush should have shipped the report, hits = %d", hits.Load())
	}
	if remaining := len(pendingCrashPaths()); remaining != 0 {
		t.Fatalf("flush should have cleared the queue, %d files left", remaining)
	}

	snap := app.PendingCrashSnapshot()
	if len(snap.Reports) != 1 {
		t.Fatalf("snapshot reports = %d, want 1 (the entry must survive the flush)", len(snap.Reports))
	}
	var payload frontendCrashPayload
	if err := json.Unmarshal([]byte(snap.Reports[0]), &payload); err != nil {
		t.Fatalf("surviving snapshot payload not valid JSON: %v", err)
	}
	if !strings.Contains(payload.Message, "snapshot-survivor") {
		t.Errorf("surviving payload must carry the panic site, got: %s", payload.Message)
	}
	if _, err := crashReportFromDetail("crash", snap.Reports[0]); err != nil {
		t.Fatalf("surviving payload must be analyzable: %v", err)
	}
}

// A process that never snapshotted (report written after boot, dev flow) falls
// back to reading the live queue.
func TestPendingCrashSnapshotLiveFallback(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	writePendingCrash("live-fallback", "boom", []byte("stack"))
	snap := NewApp().PendingCrashSnapshot()
	if snap.Count != 1 {
		t.Fatalf("count = %d, want 1", snap.Count)
	}
	if len(snap.Reports) != 1 {
		t.Fatalf("reports = %d, want 1", len(snap.Reports))
	}
}
