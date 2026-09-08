package serve

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

type sessionListEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
	Turns      int    `json:"turns,omitempty"`
	Current    bool   `json:"current,omitempty"`
	Running    bool   `json:"running,omitempty"`
	TakenOver  bool   `json:"takenOver,omitempty"`
	MtimeMilli int64  `json:"mtimeMilli"`
	// HeldBy reports lease ownership for clients: "me" (this serve
	// runtime holds it), "other" (another runtime, e.g. the desktop app,
	// holds it — write attempts will be rejected), or "" (free).
	HeldBy string `json:"heldBy,omitempty"`
}

// sessions lists saved sessions with event-log-aware titles and turn counts.
// Preview reads (which parse the transcript tail) run on a bounded worker
// pool so a directory with many/large sessions responds in ~max(per-session)
// instead of ~sum(per-session); titles never block on LLM generation
// (sessionTitleNonBlocking: cached/preview title now, LLM title next request).
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	ctrl := s.ctl()
	dir := ctrl.SessionDir()
	if dir == "" {
		writeJSON(w, []any{})
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	current := agent.CanonicalSessionPath(ctrl.SessionPath())
	running := map[string]bool{}
	s.detachedMu.Lock()
	for path, detached := range s.detached {
		running[filepath.Clean(path)] = controllerHasActiveRuntimeWork(detached.ctrl)
	}
	s.detachedMu.Unlock()

	type workItem struct {
		name  string
		path  string
		modNs int64
	}
	// Cheap state-derived fields stay on this goroutine; the preview read
	// (whole-file parse on cache miss) is the expensive part that parallelizes.
	rows := make([]sessionListEntry, 0, len(entries))
	jobs := make([]workItem, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !store.IsSessionTranscriptName(entry.Name()) {
			continue
		}
		path := agent.CanonicalSessionPath(filepath.Join(dir, entry.Name()))
		if agent.IsCleanupPending(path) {
			continue
		}
		mtime := agent.SessionContentModTime(path)
		cleanPath := agent.CanonicalSessionPath(path)
		row := sessionListEntry{
			Name:       strings.TrimSuffix(entry.Name(), ".jsonl"),
			Path:       path,
			Current:    cleanPath == current,
			Running:    running[cleanPath],
			TakenOver:  s.sessionMirrored(cleanPath) || leaseHeldByForeignRuntime(cleanPath),
			MtimeMilli: mtime.UnixMilli(),
		}
		if row.Current {
			row.Running = controllerHasActiveRuntimeWork(ctrl) && !row.TakenOver
		}
		if agent.SessionLeaseHeldByCurrentRuntime(path) {
			row.HeldBy = "me"
		} else if agent.SessionLeaseHeldByOtherRuntime(path) {
			row.HeldBy = "other"
		}
		rows = append(rows, row)
		jobs = append(jobs, workItem{name: entry.Name(), path: path, modNs: mtime.UnixNano()})
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range jobs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			first, turns, cached := agent.SessionPreviewCached(jobs[i].path)
			if !cached {
				first, turns = agent.SessionPreview(jobs[i].path)
			}
			if turns > 0 {
				rows[i].Turns = turns
				rows[i].Title = s.sessionTitleNonBlocking(r.Context(), jobs[i].name, first, jobs[i].modNs)
			}
		}(i)
	}
	wg.Wait()
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	writeJSON(w, rows)
}
