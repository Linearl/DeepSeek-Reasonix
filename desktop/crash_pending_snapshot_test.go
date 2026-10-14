package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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

	payloads := pendingCrashAnalysisPayloads(pendingCrashPaths(), false)
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
// Issue #45 (task 878): the payload keeps the SOURCE identity — schemaVersion,
// version/eventId/dedupKey, analysisReplay marker — and the exact parser the
// analysis entry uses must hand back that identity with nothing left for
// ensureCrashIdentity to regenerate. (The pre-878 behavior this test pinned —
// schema hard-coded down to 2 and identity dropped — is exactly the forgery
// the issue describes.)
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
	// writePendingReport fills the identity before the file lands; read the
	// stored report back so the expectations reference the source truth.
	body, err := os.ReadFile(pendingCrashPaths()[0])
	if err != nil {
		t.Fatal(err)
	}
	var stored crashReport
	if err := json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}

	payloads := pendingCrashAnalysisPayloads(pendingCrashPaths(), false)
	if len(payloads) != 1 {
		t.Fatalf("payloads = %d, want 1", len(payloads))
	}
	var payload frontendCrashPayload
	if err := json.Unmarshal([]byte(payloads[0]), &payload); err != nil {
		t.Fatalf("payload not valid JSON: %v", err)
	}
	if payload.SchemaVersion != currentCrashSchema {
		t.Errorf("payload schemaVersion = %d, want %d (source schema preserved)", payload.SchemaVersion, currentCrashSchema)
	}
	if payload.Source != "startup" {
		t.Errorf("payload source = %q, want startup preserved", payload.Source)
	}
	if !contains(payload.Message, "startup diagnostic panic") {
		t.Errorf("payload message must carry the report, got: %s", payload.Message)
	}
	if payload.Version != stored.Version || payload.EventID != stored.EventID || payload.DedupKey != stored.DedupKey {
		t.Errorf("payload lost the source identity: version %q/%q eventId %q/%q dedupKey %q/%q",
			payload.Version, stored.Version, payload.EventID, stored.EventID, payload.DedupKey, stored.DedupKey)
	}
	if !payload.AnalysisReplay || payload.AnalysisOf != stored.EventID {
		t.Errorf("payload replay marker = %v/%q, want true/%q", payload.AnalysisReplay, payload.AnalysisOf, stored.EventID)
	}

	// Round trip through the exact parser the analysis entry uses.
	r, err := crashReportFromDetail("crash", payloads[0])
	if err != nil {
		t.Fatalf("crashReportFromDetail rejected the pending payload: %v", err)
	}
	if r.Version != stored.Version || r.EventID != stored.EventID || r.DedupKey != stored.DedupKey || r.SchemaVersion != currentCrashSchema {
		t.Fatalf("parsed identity drifted from the source: version %q/%q eventId %q/%q dedupKey %q/%q schema %d/%d",
			r.Version, stored.Version, r.EventID, stored.EventID, r.DedupKey, stored.DedupKey, r.SchemaVersion, currentCrashSchema)
	}
	if !r.AnalysisReplay || r.AnalysisOf != stored.EventID {
		t.Fatalf("parsed replay marker = %v/%q, want true/%q", r.AnalysisReplay, r.AnalysisOf, stored.EventID)
	}
	before := r
	if err := ensureCrashIdentity(&r); err != nil {
		t.Fatalf("ensureCrashIdentity: %v", err)
	}
	if r.EventID != before.EventID || r.DedupKey != before.DedupKey || r.Version != before.Version {
		t.Fatalf("ensureCrashIdentity re-stamped a replay payload: %+v", r)
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

// ── task 804: degraded/performance events must not reach the startup banner ──

// A WebView2 frame/GPU/utility subprocess failure (kind=performance with
// web-runtime context) stays out of the prompt surface but remains on the
// diagnostics surface (promptOnly=false) and on disk (flush untouched).
func TestPendingCrashPromptSkipsPerformanceDegraded(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	degraded := baseCrashReport("performance")
	degraded.SchemaVersion = currentCrashSchema
	degraded.Source = "web.runtime.native"
	degraded.Label = "windows.webview2.process_failed"
	degraded.WebRuntime = &webRuntimeDiagnostic{
		Engine:         "webview2",
		Kind:           "utility_process_exited",
		Reason:         "terminated",
		RuntimeVersion: "1.0",
		GPUMode:        "enabled",
		Recovery:       "not_applicable",
	}
	if !writePendingReport(degraded, true) {
		t.Fatal("writePendingReport failed")
	}

	if got := pendingCrashAnalysisPayloads(pendingCrashPaths(), true); len(got) != 0 {
		t.Fatalf("prompt surface = %d payloads, want 0 (degraded must not banner)", len(got))
	}
	if got := pendingCrashAnalysisPayloads(pendingCrashPaths(), false); len(got) != 1 {
		t.Fatalf("diagnostics surface = %d payloads, want 1 (queue stays queryable)", len(got))
	}
	if remaining := len(pendingCrashPaths()); remaining != 1 {
		t.Fatalf("queue files = %d, want 1 (flush/diagnostics untouched)", remaining)
	}
}

// kind=performance WITHOUT web-runtime context is not the WebView2 degraded
// family — unknown producers keep surfacing (fail-open, gate stays narrow).
func TestPendingCrashPromptKeepsPerformanceWithoutWebRuntime(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	bare := baseCrashReport("performance")
	bare.SchemaVersion = currentCrashSchema
	bare.Source = "go"
	if !writePendingReport(bare, true) {
		t.Fatal("writePendingReport failed")
	}

	if got := pendingCrashAnalysisPayloads(pendingCrashPaths(), true); len(got) != 1 {
		t.Fatalf("prompt surface = %d payloads, want 1 (non-webview2 performance stays)", len(got))
	}
}

// The real fatal path (kind=crash, browser process exited) still banners.
func TestPendingCrashPromptKeepsCrashKind(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	fatal := baseCrashReport("crash")
	fatal.SchemaVersion = currentCrashSchema
	fatal.Source = "web.runtime.native"
	fatal.WebRuntime = &webRuntimeDiagnostic{
		Engine:         "webview2",
		Kind:           "browser_process_exited",
		Reason:         "terminated",
		RuntimeVersion: "1.0",
		GPUMode:        "enabled",
		Recovery:       "not_applicable",
	}
	if !writePendingReport(fatal, true) {
		t.Fatal("writePendingReport failed")
	}

	if got := pendingCrashAnalysisPayloads(pendingCrashPaths(), true); len(got) != 1 {
		t.Fatalf("prompt surface = %d payloads, want 1 (kind=crash still banners)", len(got))
	}
}

// End to end: the boot snapshot (fresh watermark path) filters degraded
// events exactly like the fallback does.
func TestPendingCrashSnapshotFiltersDegradedForPrompt(t *testing.T) {
	removeAllPendingCrashes()
	t.Cleanup(removeAllPendingCrashes)

	degraded := baseCrashReport("performance")
	degraded.SchemaVersion = currentCrashSchema
	degraded.Source = "web.runtime.native"
	degraded.WebRuntime = &webRuntimeDiagnostic{
		Engine:         "webview2",
		Kind:           "gpu_process_exited",
		Reason:         "terminated",
		RuntimeVersion: "1.0",
		GPUMode:        "enabled",
		Recovery:       "not_applicable",
	}
	if !writePendingReport(degraded, true) {
		t.Fatal("writePendingReport failed")
	}

	app := NewApp()
	app.snapshotPendingCrashForAnalysis()
	snap := app.PendingCrashSnapshot()
	if snap.Count != 0 {
		t.Fatalf("snapshot count = %d, want 0 (degraded must not banner on boot)", snap.Count)
	}
}
