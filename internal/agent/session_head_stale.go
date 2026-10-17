package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"reasonix/internal/baseproc/pidalive"
	"reasonix/internal/store"
)

// 任务 945：陈旧并发头 retire（884 残留①语义减法）——保守判定后标记退役，
// 只 append retire marker 不删数据；字节由下一次 rotation 物理清除。见 handoff-945。

// staleConcurrentHeadMinAge is how long a concurrent head must go untouched
// before the idle sweep may retire it: three recentHeadWindows. Retiring is a
// semantic subtraction (the head disappears from versions), so it waits well
// past the window that merely stops mentioning the head on open.
const staleConcurrentHeadMinAge = 72 * time.Hour

// staleHeadCandidate is one live head's retirement input: the facts the
// judgment needs, snapshotted under the replay lock so the decision itself is
// a pure function.
type staleHeadCandidate struct {
	id           string
	kind         string
	writer       string
	lastActivity time.Time
}

// staleHeadWriterFact is the competing writer's registry record.
type staleHeadWriterFact struct {
	pid      int
	hostname string
}

// staleConcurrentHeadDecision reports whether one live head is a stale
// concurrent head safe to retire, and why not otherwise. Every criterion must
// hold, and any missing fact (writer unregistered, pid unknown, foreign or
// empty host) reads as "not stale": the subtraction only ever fires on
// positive evidence that the writer process is gone — pid reuse therefore
// only delays a retirement (pidalive's conservative direction), never
// retires a live writer's head.
func staleConcurrentHeadDecision(c staleHeadCandidate, selected string, selectedActivity time.Time, w *staleHeadWriterFact, pidAlive func(int) bool, selfHost string, now time.Time, minAge time.Duration) (bool, string) {
	switch {
	case c.kind != HeadKindConcurrent:
		return false, "not a concurrent head"
	case c.id == selected || c.id == SessionMainHead:
		return false, "selected head"
	case now.Sub(c.lastActivity) < minAge:
		return false, "recent activity"
	case w == nil:
		return false, "writer unregistered"
	case w.pid <= 0:
		return false, "writer pid unknown"
	case w.hostname == "" || w.hostname != selfHost:
		return false, "writer on another host"
	case pidAlive(w.pid):
		return false, "writer still alive"
	case !selectedActivity.After(c.lastActivity):
		return false, "not superseded by the selected head"
	}
	return true, "stale concurrent head"
}

// RetireStaleConcurrentHeads marks every stale concurrent head of a schema-2
// session as retired and returns the retired ids. The marker append is the
// whole mutation — no message bytes are touched, and the head's exclusive
// entries only leave the log at the next single-writer rotation (the 921 idle
// sweep's compact turns the retirement into freed bytes). Sessions that are
// schema-1, damaged, or unreadable are left alone (nil), never edited here.
// pidAlive and selfHost are injected so tests can drive the judgment; the
// production wrapper is RetireStaleConcurrentHeadsNow.
func RetireStaleConcurrentHeads(path string, now time.Time, pidAlive func(int) bool, selfHost string) ([]string, error) {
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return nil, err
	}
	if !probe.dag {
		return nil, nil
	}
	st, err := replaySessionDAG(context.Background(), store.SessionEventLog(path), defaultSessionReplayLimits)
	if err != nil {
		return nil, fmt.Errorf("retire stale concurrent heads: %w", err)
	}
	if st.damaged {
		return nil, nil
	}
	selected := st.selectedHead()
	selectedActivity := time.Time{}
	if h := st.heads[selected]; h != nil {
		selectedActivity = h.lastActivity
	}
	// Snapshot the replayed facts, then release the graph lock: RetireSessionHead
	// takes its own save-path locks and a fresh replay per marker, so no lock may
	// be held across it.
	st.mu.RLock()
	var candidates []staleHeadCandidate
	for _, id := range st.liveHeads() { // retired heads are excluded by construction
		if h := st.heads[id]; h != nil {
			candidates = append(candidates, staleHeadCandidate{id: h.id, kind: h.kind, writer: h.writer, lastActivity: h.lastActivity})
		}
	}
	writerFacts := make(map[string]*staleHeadWriterFact, len(st.writers))
	for id, w := range st.writers {
		writerFacts[id] = &staleHeadWriterFact{pid: w.pid, hostname: w.hostname}
	}
	st.mu.RUnlock()

	var retired []string
	for _, c := range candidates {
		ok, reason := staleConcurrentHeadDecision(c, selected, selectedActivity, writerFacts[c.writer], pidAlive, selfHost, now, staleConcurrentHeadMinAge)
		if !ok {
			slog.Debug("session: concurrent head kept", "path", path, "head", c.id, "reason", reason)
			continue
		}
		if err := RetireSessionHead(path, c.id); err != nil {
			return retired, fmt.Errorf("retire stale head %s: %w", c.id, err)
		}
		retired = append(retired, c.id)
		slog.Info("session: retired stale concurrent head",
			"path", path, "head", c.id, "writer", c.writer,
			"lastActivity", c.lastActivity.UTC().Format(time.RFC3339))
	}
	return retired, nil
}

// RetireStaleConcurrentHeadsNow is the production wrapper with live process
// facts. A hostname lookup failure blanks selfHost, which the judgment reads
// as "cannot attribute the writer to this machine" and keeps every head.
func RetireStaleConcurrentHeadsNow(path string, now time.Time) ([]string, error) {
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	return RetireStaleConcurrentHeads(path, now, pidalive.Alive, host)
}
