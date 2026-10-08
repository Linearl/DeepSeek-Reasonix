package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// Task 642: the lab mock entry must travel the exact real pipeline — the own
// collection channel first, the crash-pending queue on upload failure — with
// TestMock stamped so the receiving end can tell the drill apart from a real
// failure.

func mockCrashDetail(testMock bool) string {
	payload := map[string]any{
		"schemaVersion":   2,
		"kind":            "crash",
		"source":          "frontend.mock",
		"label":           "mock.test",
		"message":         "[MOCK TEST — not a real crash]\nSimulated crash report from the lab (task 642).",
		"errorType":       "MockCrashError",
		"errorMessage":    "Simulated crash report from the lab (task 642). This is a test event, not a real failure.",
		"stack":           "MockCrashError: simulated crash (task 642 lab entry)\n    at simulateCrash (src/lib/crashMock.ts:1:1)",
		"topFrame":        "frontend.mock",
		"fingerprintHint": "mock.lab.test",
		"buildCommit":     "dev",
		"breadcrumbs":     []any{},
		"occurredAt":      "2026-10-09T00:00:00.000Z",
	}
	if testMock {
		payload["testMock"] = true
	}
	body, _ := json.Marshal(payload)
	return string(body)
}

func TestReportMockCrashUploadsThroughRealChannel(t *testing.T) {
	oldEndpoint := crashEndpoint
	t.Cleanup(func() { crashEndpoint = oldEndpoint; removeAllPendingCrashes() })

	var got crashReport
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("body not JSON: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	crashEndpoint = srv.URL

	status, err := NewApp().ReportMockCrash("crash", mockCrashDetail(true))
	if err != nil {
		t.Fatal(err)
	}
	if status != "uploaded" {
		t.Fatalf("status = %q, want uploaded", status)
	}
	if hits.Load() != 1 {
		t.Fatalf("server hits = %d, want 1", hits.Load())
	}
	if !got.TestMock {
		t.Fatal("uploaded mock must carry testMock=true")
	}
	if got.Source != "frontend.mock" || got.Label != "mock.test" {
		t.Fatalf("mock markers lost on the wire: source=%q label=%q", got.Source, got.Label)
	}
	if _, ok := readPending(t); ok {
		t.Error("a successful upload must not queue the report")
	}
}

func TestReportMockCrashQueuesOnUploadFailure(t *testing.T) {
	oldEndpoint := crashEndpoint
	t.Cleanup(func() { crashEndpoint = oldEndpoint; removeAllPendingCrashes() })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	crashEndpoint = srv.URL

	status, err := NewApp().ReportMockCrash("crash", mockCrashDetail(true))
	if err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("status = %q, want queued", status)
	}
	r, ok := readPending(t)
	if !ok {
		t.Fatal("failed upload must land in the crash-pending queue like a native panic")
	}
	if !r.TestMock {
		t.Fatal("queued mock must keep testMock=true for the next-launch retry")
	}
}

func TestReportMockCrashForceStampsTestMock(t *testing.T) {
	oldEndpoint := crashEndpoint
	t.Cleanup(func() { crashEndpoint = oldEndpoint; removeAllPendingCrashes() })

	var got crashReport
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(buf, &got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	crashEndpoint = srv.URL

	// The lab payload lost its flag (stale frontend build); the backend must
	// stamp it anyway so an unlabeled mock can never reach the receiving end.
	if _, err := NewApp().ReportMockCrash("crash", mockCrashDetail(false)); err != nil {
		t.Fatal(err)
	}
	if !got.TestMock {
		t.Fatal("backend must force-stamp testMock on every mock report")
	}
}

func TestReportMockCrashRejectsBadInput(t *testing.T) {
	app := NewApp()
	if _, err := app.ReportMockCrash("telemetry", "x"); err == nil {
		t.Error("unknown kind should be rejected")
	}
	if _, err := app.ReportMockCrash("crash", ""); err == nil {
		t.Error("empty detail should be rejected")
	}
}

func TestCrashReportFromDetailPropagatesTestMock(t *testing.T) {
	r, err := crashReportFromDetail("crash", mockCrashDetail(true))
	if err != nil {
		t.Fatal(err)
	}
	if !r.TestMock {
		t.Fatal("schema-v2 parse must propagate testMock from the frontend payload")
	}
	r2, err := crashReportFromDetail("crash", mockCrashDetail(false))
	if err != nil {
		t.Fatal(err)
	}
	if r2.TestMock {
		t.Fatal("a payload without the flag must stay unmarked (real crash path)")
	}
}
