// The session recall index: the recall pool is read and tokenized once per
// memory snapshot, lazily on the first recall and cached on the Set (378B1) —
// boot and memory writes no longer pay for a recall that may never fire.
// Markdown files stay the source of truth — every write path swaps the
// snapshot through memory.Load, and the replacement snapshot builds its index
// from the latest pool on its own first recall.
package memory

import (
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"reasonix/internal/retrieval"
)

// RecallIndex is the prebuilt retrieval state for one immutable Set snapshot.
type RecallIndex struct {
	docs    []autoRecallDoc
	fielded []retrieval.FieldedDoc // V2 shadow pool, prebuilt field split
}

// RecallIndexBuildStats is the observability record of one index build
// (tl1229: 只带仪器不带优化). The tl1223 heap profile attributed 65.59MB cum
// to BuildRecallIndex and the boot prompt stage's dominant segment is the
// memory pool read behind it — this record turns each build's cost (pool size,
// token count, wall time) into greppable before-data for the 1193 family.
type RecallIndexBuildStats struct {
	Docs     int           // recall-pool entries read and tokenized
	Terms    int           // total token count across the pool
	Duration time.Duration // wall time of the whole build
	At       time.Time
}

var lastRecallIndexBuild atomic.Pointer[RecallIndexBuildStats]

// LastRecallIndexBuildStats reports the most recent index build's stats, nil
// before the first build in this process.
func LastRecallIndexBuildStats() *RecallIndexBuildStats {
	return lastRecallIndexBuild.Load()
}

// BuildRecallIndex reads and tokenizes the recall pool once. A zero store
// yields nil, which recall reports as an empty memory store. Each build
// records RecallIndexBuildStats and (for non-empty pools) emits one
// "memory: recall index built" log line — once per snapshot swap, never in a
// loop.
func BuildRecallIndex(store Store) *RecallIndex {
	started := time.Now()
	memories := recallMemories(store.ListAll())
	if len(memories) == 0 {
		lastRecallIndexBuild.Store(&RecallIndexBuildStats{At: started})
		return nil
	}
	index := &RecallIndex{}
	terms := 0
	for _, memory := range memories {
		index.fielded = append(index.fielded, retrieval.FieldedDoc{ID: memory.ID, Fields: map[string]string{
			"name": memory.Name, "title": memory.Title, "keywords": memory.Keywords,
			"subject": memory.SubjectKey, "description": memory.Description, "body": memory.Body,
		}})
		text := autoRecallSearchText(memory)
		docTerms := retrieval.Tokens(text)
		terms += len(docTerms)
		if len(docTerms) == 0 {
			continue
		}
		index.docs = append(index.docs, autoRecallDoc{
			memory: memory,
			text:   text,
			counts: retrieval.Counts(docTerms),
			length: len(docTerms),
		})
	}
	stats := &RecallIndexBuildStats{Docs: len(memories), Terms: terms, Duration: time.Since(started), At: started}
	lastRecallIndexBuild.Store(stats)
	slog.Info("memory: recall index built",
		"docs", stats.Docs, "terms", stats.Terms,
		"duration_ms", stats.Duration.Milliseconds())
	return index
}

// AutoRecall runs automatic recall against this snapshot's lazily built index —
// the per-turn path. The first call builds and caches the index (see
// Set.recallIndex); subsequent turns serve from the cache with zero disk IO.
// Semantics are identical to the package-level AutoRecall.
func (s *Set) AutoRecall(query string, opts RecallOptions) RecallResult {
	result := RecallResult{Query: strings.TrimSpace(query), CharBudget: recallCharBudget(opts.MaxChars)}
	if genericRecallQuery(result.Query) {
		result.Suppressed = "generic user turn"
		return result
	}
	if s == nil {
		result.Suppressed = "memory store is empty"
		return result
	}
	return autoRecallIndexed(s.recallIndex(), result, opts)
}
