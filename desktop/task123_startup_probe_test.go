package main

// Task 123 startup first-load probe — a deliberately manual measurement tool,
// not part of the normal suite: it reads real session files from disk (the
// user's own history) and reports stage timings for the newest-history-page
// read that startup pays for.
//
// Why it is checked in: the fix for task 123 (bounded tail overlay instead of a
// full DAG replay) is only verifiable against real large sessions, and the
// numbers below are the acceptance evidence for "same 36.9 MiB session,
// startup:total ≤1000 ms". Re-run it whenever the cold/live history read path
// changes:
//
//	TASK123_PROBE_DIR="<sessions dir>" \
//	TASK123_PROBE_SESSIONS="20260804-063617.999197700-deepseek-deepseek-v4-flash" \
//	go test ./ -run TestTask123StartupProbe -v -timeout 900s
//
// Optional: TASK123_PROBE_EVENTS=1 also times the full event-log load (heavy:
// hundreds of MiB of JSON, minutes of CPU). Without TASK123_PROBE_DIR the probe
// skips, because it must never touch session files nobody asked about.

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/provider"
	"reasonix/internal/store"
)

func TestTask123StartupProbe(t *testing.T) {
	base := os.Getenv("TASK123_PROBE_DIR")
	if strings.TrimSpace(base) == "" {
		t.Skip("TASK123_PROBE_DIR not set; this probe reads real sessions and only runs on demand")
	}
	for _, name := range strings.Split(os.Getenv("TASK123_PROBE_SESSIONS"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		t.Run(name, func(t *testing.T) { task123ProbeStartupHistory(t, base, filepath.Join(base, name+".jsonl")) })
	}
}

func task123ProbeStep(t *testing.T, label string, f func()) time.Duration {
	start := time.Now()
	f()
	elapsed := time.Since(start)
	t.Logf("    %-38s %9.1f ms", label, float64(elapsed.Microseconds())/1000)
	return elapsed
}

func task123ProbeStartupHistory(t *testing.T, sessionDir, sessionPath string) {
	withEvents := os.Getenv("TASK123_PROBE_EVENTS") == "1"
	req := HistorySliceRequest{Turns: 60, Entries: 120, Bytes: 512 << 10}

	var info os.FileInfo
	task123ProbeStep(t, "os.Stat", func() { info, _ = os.Stat(sessionPath) })
	if info == nil {
		t.Fatalf("stat %s: not found", sessionPath)
	}
	var looksEvent bool
	task123ProbeStep(t, "historySessionLooksEventFormat", func() { looksEvent = historySessionLooksEventFormat(sessionPath) })
	t.Logf("    => transcript=%.1f MiB eventFormat=%v", float64(info.Size())/(1<<20), looksEvent)

	var identity agent.PersistedState
	var identityKnown bool
	task123ProbeStep(t, "agent.SessionContentIdentity", func() {
		identity, identityKnown, _ = agent.SessionContentIdentity(sessionPath)
	})
	t.Logf("    => identity ok=%v revision=%d revKnown=%v digest=%s", identityKnown, identity.Revision, identity.RevisionKnown, task123ShortBytes(identity.Digest))

	indexPath := store.SessionDisplayIndex(sessionPath)
	var idx *agent.SessionDisplayIndex
	task123ProbeStep(t, "agent.LoadSessionDisplayIndex", func() { idx, _ = agent.LoadSessionDisplayIndex(indexPath) })

	var valid, tsValid bool
	task123ProbeStep(t, "agent.ValidateSessionDisplayIndex", func() {
		if idx != nil {
			valid = agent.ValidateSessionDisplayIndex(idx, identity.Revision, identity.RevisionKnown, identity.Digest, info.Size())
		}
	})
	task123ProbeStep(t, "historyIndexTimestampValid", func() {
		tsValid = historyIndexTimestampValid(indexPath, sessionPath, info, idx, true)
	})
	if idx != nil {
		t.Logf("    => index msgs=%d turns=%d size=%d valid=%v tsValid=%v sizeMatch=%v",
			idx.MessageCount, idx.AuthoredTurns, idx.TranscriptSize, valid, tsValid, idx.TranscriptSize == info.Size())
	} else {
		t.Logf("    => index missing/corrupt")
	}

	resolver := sessionDisplayResolver(sessionDir, sessionPath)
	var plannerTurns []plannerDisplayTurn
	task123ProbeStep(t, "sessionDisplayResolver+plannerTurns", func() {
		plannerTurns = sessionPlannerDisplayTurns(sessionDir, sessionPath)
	})
	t.Logf("    => plannerTurns=%d", len(plannerTurns))

	if idx != nil && valid && tsValid && idx.TranscriptSize == info.Size() {
		app := &App{}
		src := coldHistorySliceSource(sessionPath, idx)
		var cold HistorySlice
		var pageErr error
		coldElapsed := task123ProbeStep(t, "pageHistorySliceSource(index) [cold, task 123]", func() {
			cold, pageErr = app.pageHistorySliceSource(src, req, resolver, plannerTurns, nil, sessionPath)
		})
		if pageErr != nil {
			t.Logf("    => page error: %v", pageErr)
		}
		stamped, userRows := task123CountStamped(cold)
		t.Logf("    => cold page entries=%d turns=%d-%d/%d inlineBytes~%d ms=%d",
			len(cold.Entries), cold.StartTurn, cold.EndTurn, cold.TotalTurns, task123InlineBytes(cold), coldElapsed.Milliseconds())
		t.Logf("    => persisted-time overlay stamped %d/%d user rows", stamped, userRows)
		hits, misses := historyTimeOverlayStats()
		t.Logf("    => overlay tail cache hits=%d misses=%d", hits, misses)

		var warm HistorySlice
		warmElapsed := task123ProbeStep(t, "pageHistorySliceSource(index) [warm]", func() {
			warm, _ = app.pageHistorySliceSource(src, req, resolver, plannerTurns, nil, sessionPath)
		})
		warmStamped, warmUsers := task123CountStamped(warm)
		t.Logf("    => warm page entries=%d stamped=%d/%d ms=%d", len(warm.Entries), warmStamped, warmUsers, warmElapsed.Milliseconds())

		// The retreat path: with historyTimeOverlayFullLookup the same page pays
		// the full DAG replay again — the "before" number for the task-123
		// comparison (4.0 s of the measured 5.0 s startup on a 146 MiB log).
		historyTimeOverlayFullLookup = true
		var full HistorySlice
		fullElapsed := task123ProbeStep(t, "pageHistorySliceSource(index) [full overlay retreat]", func() {
			full, _ = app.pageHistorySliceSource(src, req, resolver, plannerTurns, nil, sessionPath)
		})
		historyTimeOverlayFullLookup = false
		fullStamped, fullUsers := task123CountStamped(full)
		t.Logf("    => full-overlay page entries=%d stamped=%d/%d ms=%d", len(full.Entries), fullStamped, fullUsers, fullElapsed.Milliseconds())
	}

	// Tail-read cost curve: the overlay budget trades scanned bytes for timestamp
	// coverage, so these numbers decide the shipped default.
	var tail8, tail32, tail64 int
	var tail8Done, tail32Done, tail64Done bool
	task123ProbeStep(t, "agent.LoadSessionUserTimesTail 8 MiB", func() {
		times, done := agent.LoadSessionUserTimesTail(sessionPath, 8<<20)
		tail8, tail8Done = len(times), done
	})
	task123ProbeStep(t, "agent.LoadSessionUserTimesTail 32 MiB", func() {
		times, done := agent.LoadSessionUserTimesTail(sessionPath, 32<<20)
		tail32, tail32Done = len(times), done
	})
	task123ProbeStep(t, "agent.LoadSessionUserTimesTail 64 MiB", func() {
		times, done := agent.LoadSessionUserTimesTail(sessionPath, 64<<20)
		tail64, tail64Done = len(times), done
	})
	t.Logf("    => tail ids 8MiB=%d(complete=%v) 32MiB=%d(%v) 64MiB=%d(%v)",
		tail8, tail8Done, tail32, tail32Done, tail64, tail64Done)

	// What the cold path falls back to when the index does not validate.
	var scanned *agent.SessionDisplayIndex
	task123ProbeStep(t, "agent.ScanSessionDisplayIndex (fallback)", func() { scanned, _ = agent.ScanSessionDisplayIndex(sessionPath) })
	if scanned != nil {
		t.Logf("    => scanned msgs=%d turns=%d digest=%s", scanned.MessageCount, scanned.AuthoredTurns, task123ShortString(scanned.ContentDigest))
		var slice HistorySlice
		task123ProbeStep(t, "pageHistorySliceSource(scan) [fallback]", func() {
			slice, _ = (&App{}).pageHistorySliceSource(coldHistorySliceSource(sessionPath, scanned), req, resolver, plannerTurns, nil, sessionPath)
		})
		t.Logf("    => scan page entries=%d turns=%d-%d/%d", len(slice.Entries), slice.StartTurn, slice.EndTurn, slice.TotalTurns)
	}

	if withEvents {
		eventLog := store.SessionEventLog(sessionPath)
		if fi, err := os.Stat(eventLog); err == nil {
			t.Logf("    => events log=%.1f MiB", float64(fi.Size())/(1<<20))
		}
		var msgs []provider.Message
		var state agent.PersistedState
		task123ProbeStep(t, "agent.LoadSessionDisplayMessages(events)", func() {
			msgs, state, _, _ = agent.LoadSessionDisplayMessages(sessionPath)
		})
		t.Logf("    => loaded msgs=%d revision=%d revKnown=%v", len(msgs), state.Revision, state.RevisionKnown)
	}
}

// task123CountStamped reports how many user rows of the page carry a persisted
// time — the overlay's actual effect, as opposed to its cost.
func task123CountStamped(slice HistorySlice) (stamped, userRows int) {
	for _, entry := range slice.Entries {
		if entry.Message.Role != string(provider.RoleUser) {
			continue
		}
		userRows++
		if entry.Message.CreatedAt > 0 {
			stamped++
		}
	}
	return stamped, userRows
}

func task123ShortBytes(d [32]byte) string {
	return hex.EncodeToString(d[:])[:12]
}

func task123ShortString(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}

func task123InlineBytes(s HistorySlice) int {
	total := 0
	for _, e := range s.Entries {
		total += e.inlineBytes()
	}
	return total
}
