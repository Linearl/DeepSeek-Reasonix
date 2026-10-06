package main

// Task 360: the frontend jank monitor (crash.ts promptPerformanceReport: long
// task / js heap / event loop lag) had no local persistence — breadcrumbs lived
// in a 30-slot in-memory ring and the frame samples only reached the dialog
// payload, so a jank the user dismissed left nothing on disk to diagnose later.
// This is the sink: one JSON line per event under logs/perf/, next to the
// backend perf samples so both timelines can be correlated by day.

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	jankFilePrefix = "jank-"
	jankFileSuffix = ".jsonl"
	// One record is a performance snapshot, not a heap dump: anything beyond
	// this is a bug in the caller, not a bigger window into the jank.
	jankMaxRecordBytes = 64 << 10
	// Same ceiling philosophy as the perf samples (perfMonitorMaxFileBytes):
	// one rotation backup keeps the ceiling honest without losing the tail.
	jankMaxFileBytes = 20 << 20
)

func jankDayPath(now time.Time) string {
	return filepath.Join(perfMonitorDir(), jankFilePrefix+now.Format("20060102")+jankFileSuffix)
}

// scrubJankStrings rewrites every string value in the decoded record with the
// same scrubbers the crash upload path applies (crash_app.go scrubSensitiveText),
// so the local dump keeps the dialog's privacy promise ("paths and secrets are
// removed") without depending on the frontend to pre-clean. Breadcrumbs carry
// raw console.error/bridge text and are the realistic carrier of user paths or
// tokens; map keys are the record's own field names from crash.ts and stay
// untouched. Same false-positive budget as the upload path (e.g. a 32+ hex id
// becomes [redacted-hex]).
func scrubJankStrings(value any) any {
	switch v := value.(type) {
	case string:
		return scrubSensitiveText(v)
	case map[string]any:
		for key, item := range v {
			v[key] = scrubJankStrings(item)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = scrubJankStrings(item)
		}
		return v
	default:
		return value
	}
}

// ReportJankRecord appends one frontend jank event to logs/perf/jank-YYYYMMDD.jsonl.
// The record arrives as a JSON object string built by crash.ts (reason + label +
// performance snapshot + sampled long-task frames + recent breadcrumbs); this
// side validates the envelope, scrubs paths/secrets with the upload path's
// scrubbers, stamps server-side arrival time (same clock as the perf samples),
// caps the size, and appends. Fire-and-forget from the frontend: a failing
// diagnostic must never surface as user-visible errors, so failures only log.
func (a *App) ReportJankRecord(record string) {
	record = strings.TrimSpace(record)
	if record == "" {
		return
	}
	now := time.Now()
	var payload map[string]any
	if err := json.Unmarshal([]byte(record), &payload); err != nil {
		// Not a valid object: keep the rejection itself observable (304 纪律 —
		// a silent drop here would read as "no jank happened").
		slog.Warn("desktop: jank record rejected", "reason", "invalid json", "bytes", len(record))
		return
	}
	scrubJankStrings(payload)
	payload["ts"] = now.UTC().Format(time.RFC3339Nano)
	compact, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("desktop: jank record rejected", "reason", "unserializable", "err", err)
		return
	}
	if len(compact) > jankMaxRecordBytes {
		shrunken := map[string]any{
			"ts":        payload["ts"],
			"truncated": true,
			"bytes":     len(compact),
		}
		if reason, ok := payload["reason"].(string); ok {
			shrunken["reason"] = reason
		}
		if label, ok := payload["label"].(string); ok {
			shrunken["label"] = label
		}
		compact, _ = json.Marshal(shrunken)
	}
	path := jankDayPath(now)
	if err := appendJankLine(path, compact); err != nil {
		slog.Warn("desktop: jank record write failed", "path", path, "err", err)
		return
	}
	slog.Info("desktop: jank record", "path", path, "reason", fmt.Sprint(payload["reason"]))
}

func appendJankLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > jankMaxFileBytes {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}
