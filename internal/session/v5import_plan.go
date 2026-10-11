package session

// Task 773: dry-run planning for the v5 import. The plan is the first of the
// four safety boundaries (766 ⑦): it walks the whole source store, folds every
// transcript, and reports per-session size, generation, target and failure
// reasons BEFORE anything is written. `sessions import-v5` without --apply
// stops here.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// V5ImportFailure is one source entry that could not even be opened.
type V5ImportFailure struct {
	SessionID string `json:"sessionId"`
	Reason    string `json:"reason"`
}

// V5PlanEntry is one session's route through the import.
type V5PlanEntry struct {
	SessionID       string         `json:"sessionId"`
	Status          string         `json:"status"` // import | skip | fail
	Reason          string         `json:"reason,omitempty"`
	WorkspaceRoot   string         `json:"workspaceRoot,omitempty"`
	RegistryStatus  string         `json:"registryStatus,omitempty"` // archived | reserved | deleted | purged | ""
	StorageRevision int            `json:"storageRevision"`
	LogBytes        int64          `json:"logBytes"`
	TotalBytes      int64          `json:"totalBytes"`
	MessageCount    int            `json:"messageCount"`
	Title           string         `json:"title,omitempty"`
	ModelRef        string         `json:"modelRef,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	TargetPath      string         `json:"targetPath,omitempty"`
	HeaderMissing   bool           `json:"headerMissing,omitempty"`
	AuxFiles        []string       `json:"auxFiles,omitempty"`
	Origin          string         `json:"origin,omitempty"`
	CWD             string         `json:"cwd,omitempty"`
	KindCounts      map[string]int `json:"kindCounts,omitempty"`
}

// V5Plan is the full dry-run result.
type V5Plan struct {
	SourceRoot    string            `json:"sourceRoot"`
	RegistryFile  string            `json:"registryFile"`
	RegistryFound bool              `json:"registryFound"`
	Entries       []V5PlanEntry     `json:"entries"`
	Failures      []V5ImportFailure `json:"failures"`
	ImportCount   int               `json:"importCount"`
	SkipCount     int               `json:"skipCount"`
	FailCount     int               `json:"failCount"`
	TotalLogBytes int64             `json:"totalLogBytes"`
	GeneratedAt   time.Time         `json:"generatedAt"`
}

// V5ImportOptions bounds one plan/run. ResolveTarget maps a registry workspace
// root to the legacy session directory that should receive its sessions
// (empty root → the global session dir). Keeping resolution at the caller
// keeps this package free of config wiring.
type V5ImportOptions struct {
	SourceRoot string
	// ResolveTarget returns the target legacy session directory for a
	// workspace root ("" = global). Files are only ever added, never replaced.
	ResolveTarget func(workspaceRoot string) (string, error)
}

// PlanV5Import walks the source store and folds every transcript. It never
// writes: source opens are read-only, transcripts decode in memory.
func PlanV5Import(ctx context.Context, opts V5ImportOptions) (*V5Plan, error) {
	store, err := OpenV5Store(opts.SourceRoot)
	if err != nil {
		return nil, err
	}
	treeRoot := filepath.Dir(filepath.Dir(store.Root))
	state, regErr := ReadV5WorkspaceState(treeRoot)
	plan := &V5Plan{
		SourceRoot:   store.Root,
		RegistryFile: filepath.Join(treeRoot, RegistryPath()),
		GeneratedAt:  time.Now().UTC(),
	}
	if regErr != nil {
		// A damaged registry must not silently reroute every session to the
		// default target: report it, keep going with no owners, and let the
		// operator decide (the plan lists every target path).
		plan.Failures = append(plan.Failures, V5ImportFailure{SessionID: "(registry)", Reason: regErr.Error()})
	} else {
		plan.RegistryFound = state.Version > 0 || len(state.Workspaces) > 0
	}
	sessions, openFailures, err := store.Sessions()
	if err != nil {
		return nil, err
	}
	plan.Failures = append(plan.Failures, openFailures...)
	for _, session := range sessions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := planEntryFor(session, state, opts)
		if entry.Status == "fail" {
			plan.FailCount++
		} else if entry.Status == "skip" {
			plan.SkipCount++
		} else {
			plan.ImportCount++
			plan.TotalLogBytes += entry.LogBytes
		}
		plan.Entries = append(plan.Entries, entry)
	}
	plan.FailCount += len(openFailures)
	if regErr != nil {
		plan.FailCount++
	}
	sort.Slice(plan.Entries, func(i, j int) bool { return plan.Entries[i].SessionID < plan.Entries[j].SessionID })
	return plan, nil
}

// planEntryFor classifies one session and, for imports, folds its transcript
// so the preview shows real message counts and surfaces decode failures
// before any target write (upstream studio's per-item import errors, #12538).
func planEntryFor(session *V5Session, state *V5WorkspaceState, opts V5ImportOptions) V5PlanEntry {
	entry := V5PlanEntry{
		SessionID:       session.ID,
		Status:          "import",
		StorageRevision: session.Manifest.StorageRevision,
		LogBytes:        session.LogSize,
		CreatedAt:       session.Manifest.CreatedAt,
		UpdatedAt:       session.UpdatedAt(),
		HeaderMissing:   session.HeaderMissing,
		AuxFiles:        session.AuxFiles,
		Origin:          session.Header.Origin,
		CWD:             session.Header.CWD,
	}
	root, known, status := state.Owner(session.ID)
	entry.WorkspaceRoot = root
	entry.RegistryStatus = status
	switch status {
	case "reserved":
		entry.Status = "skip"
		entry.Reason = "registry holds an interrupted create reservation; nothing to import"
		return entry
	case "deleted", "purged":
		entry.Status = "skip"
		entry.Reason = "registry lifecycle is " + status
		return entry
	case "archived":
		// Archived is display state the legacy store cannot express; the
		// conversation still imports and the loss is recorded in the report.
		entry.Reason = "archived in registry; imports as a visible session"
	}
	targetDir, err := opts.ResolveTarget(root)
	if err != nil {
		entry.Status = "fail"
		entry.Reason = "resolve target: " + err.Error()
		return entry
	}
	entry.TargetPath = filepath.Join(targetDir, session.ID+".jsonl")
	if _, err := os.Stat(entry.TargetPath); err == nil {
		entry.Status = "skip"
		entry.Reason = "target already exists (previous import or name collision)"
		return entry
	}
	transcript, err := session.Transcript(context.Background())
	if err != nil {
		entry.Status = "fail"
		entry.Reason = err.Error()
		return entry
	}
	entry.MessageCount = len(transcript.Messages)
	entry.Title = transcript.Title
	entry.ModelRef = transcript.ModelRef
	entry.KindCounts = transcript.KindCounts
	entry.TotalBytes = session.LogSize
	if !known && entry.Reason == "" {
		entry.Reason = "not listed in registry; imports to the default target"
	}
	return entry
}

// Summarize renders the one-paragraph dry-run preview line the CLI prints.
func (p *V5Plan) Summarize() string {
	var b strings.Builder
	fmt.Fprintf(&b, "source: %s\n", p.SourceRoot)
	fmt.Fprintf(&b, "registry: %s (found=%v)\n", p.RegistryFile, p.RegistryFound)
	fmt.Fprintf(&b, "sessions: %d to import, %d skipped, %d failed; %.1f MiB of framed logs to import\n",
		p.ImportCount, p.SkipCount, p.FailCount, float64(p.TotalLogBytes)/(1024*1024))
	return b.String()
}
