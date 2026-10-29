// The session recall index: the recall pool is read and tokenized once per
// memory snapshot, lazily on the first recall and cached on the Set (378B1) —
// boot and memory writes no longer pay for a recall that may never fire.
// tl1193 adds a process-resident layer: the built index is shared across
// snapshots behind a stat fingerprint, so the "one build per snapshot swap"
// above degrades to "one build per actual pool change" — a memory write that
// leaves the pool's files untouched (and every non-writing snapshot reload,
// e.g. a compaction reload) reuses the resident index instead of re-reading
// and re-tokenizing the pool. Markdown files stay the source of truth: the
// fingerprint covers each fact file's name, size, and mtime, so any write
// path (save, restore, archive, raw edit) that changes content changes the
// fingerprint and the next recall rebuilds.
package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
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

// recallCacheEntry is one resident index plus the stat fingerprint it was
// built from. RecallIndex is immutable after build (every consumer —
// autoRecallIndexed, shadowRankV2 — only reads), so sharing it across
// snapshots is safe.
type recallCacheEntry struct {
	fingerprint string
	index       *RecallIndex
}

// recallCache holds the process-resident recall index. A nil index (empty
// pool) caches fine: an unchanged empty pool stays an empty index.
var recallCache atomic.Pointer[recallCacheEntry]

// fingerprint hashes each fact file's name, size, and mtime across both store
// scopes. Stat-only — no file content is read — so validating the cache costs
// a directory listing, not a pool reload. Contents that change without
// touching size AND mtime are outside this guard's model (the write paths all
// go through atomic replace, which always bumps mtime); a zero Store yields ""
// and the cache is skipped both ways.
func (s Store) fingerprint() string {
	if s.Dir == "" && s.GlobalDir == "" {
		return ""
	}
	h := sha256.New()
	for _, dir := range s.dirs() {
		if dir == "" {
			continue
		}
		fmt.Fprintf(h, "dir\x00%s\x00", dir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || e.Name() == indexFile || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			fmt.Fprintf(h, "%s|%d|%d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// recallIndexFor returns the resident index when the store's stat fingerprint
// is unchanged, building and adopting it otherwise. Racing first recalls both
// build and the last store wins — the build is idempotent, so the race is
// benign.
func recallIndexFor(store Store) *RecallIndex {
	fp := store.fingerprint()
	if fp != "" {
		if c := recallCache.Load(); c != nil && c.fingerprint == fp {
			return c.index
		}
	}
	index := BuildRecallIndex(store)
	if fp != "" {
		recallCache.Store(&recallCacheEntry{fingerprint: fp, index: index})
	}
	return index
}

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
