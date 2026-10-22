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

// staleHeadChainLen is the candidate head's exclusive-chain length metric used
// by the conservative branch below.
type staleHeadChainLen func(headID string) int

// staleConcurrentHeadDecision reports whether one live head is a stale
// concurrent head safe to retire, and why not otherwise. Every criterion must
// hold. A missing writer fact — including the "writer pid unknown" form that
// rotations of old logs produce when an identity-less writer entry is
// re-emitted (pid/hostname are blanked on replay: task 1075 现场 5/7 头属于
// 此形态, 修复前永久判 "not stale") — falls back to the conservative chain
// equivalence check: the head's chain must be a prefix of the selected head's
// chain, which means it contributes no exclusive message bytes and retiring
// it cannot lose anything. Anything else keeps the head. pid reuse therefore
// only delays a retirement (pidalive's conservative direction), never
// retires a live writer's head or an exclusive chain.
func staleConcurrentHeadDecision(c staleHeadCandidate, selected string, selectedActivity time.Time, w *staleHeadWriterFact, pidAlive func(int) bool, selfHost string, now time.Time, minAge time.Duration, selectedChain, candidateChain staleHeadChainLen) (bool, string) {
	switch {
	case c.kind != HeadKindConcurrent:
		return false, "not a concurrent head"
	case c.id == selected || c.id == SessionMainHead:
		return false, "selected head"
	case now.Sub(c.lastActivity) < minAge:
		return false, "recent activity"
	case !selectedActivity.After(c.lastActivity):
		// Both the pid-backed path and the conservative fallback below need
		// the selected line to have superseded the candidate.
		return false, "not superseded by the selected head"
	}
	// pid-backed path (task 945 原判据): positive evidence the writer is gone.
	if w != nil && w.pid > 0 {
		switch {
		case w.hostname == "" || w.hostname != selfHost:
			return false, "writer on another host"
		case pidAlive(w.pid):
			return false, "writer still alive"
		}
		return true, "stale concurrent head"
	}
	// Conservative fallback (task 1075): no pid evidence, so only a chain the
	// selected head provably contains may be retired. A concurrent head's
	// chain is its fork-point prefix plus its own appends; when its LENGTH is
	// strictly shorter than the selected head's it holds no message the
	// selected chain lacks (any exclusive message would make it at least as
	// long as the fork point plus one). Length equality does NOT prove
	// containment — two heads forked at the same point and grown to the same
	// length hold different exclusive bytes — so those stay (the conservative
	// side: over-keeping costs bytes, over-retiring loses data).
	if candidateChain == nil || selectedChain == nil {
		return false, "writer unregistered"
	}
	cLen, sLen := candidateChain(c.id), selectedChain(selected)
	if cLen <= 0 {
		return false, "writer unregistered"
	}
	if cLen >= sLen {
		return false, "chain not strictly shorter than selected (exclusive bytes possible)"
	}
	return true, "chain is a prefix of the selected head"
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
	// Conservative fallback needs chain lengths; snapshot the ids before
	// releasing the lock (chainIDs reads the shared graph).
	selectedChainIDs := st.chainIDs(selected)
	chainLens := make(map[string]int, len(st.headOrder))
	for _, id := range st.headOrder {
		if h := st.heads[id]; h != nil {
			chainLens[id] = len(st.chainIDs(id))
		}
	}
	st.mu.RUnlock()

	var retired []string
	for _, c := range candidates {
		ok, reason := staleConcurrentHeadDecision(c, selected, selectedActivity, writerFacts[c.writer], pidAlive, selfHost, now, staleConcurrentHeadMinAge,
			func(string) int { return len(selectedChainIDs) },
			func(headID string) int { return chainLens[headID] })
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
