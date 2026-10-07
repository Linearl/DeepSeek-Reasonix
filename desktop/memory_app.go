package main

import (
	"time"

	"reasonix/internal/control"
	"reasonix/internal/memory"
)

// memory panel (frontend ⇄ controller)

type MemoryImport struct {
	Path       string `json:"path"`
	SourcePath string `json:"sourcePath"`
}

// MemoryDoc is one resolved instruction file with applicability metadata.
type MemoryDoc struct {
	Path       string         `json:"path"`
	Scope      string         `json:"scope"`
	Directory  string         `json:"directory,omitempty"`
	Body       string         `json:"body"`
	Imports    []MemoryImport `json:"imports"`
	Depth      int            `json:"depth"`
	Order      int            `json:"order"`
	Precedence int            `json:"precedence"`
}

type InstructionDiagnostic struct {
	Code       string `json:"code"`
	Path       string `json:"path"`
	SourcePath string `json:"sourcePath,omitempty"`
	Line       int    `json:"line,omitempty"`
	Message    string `json:"message"`
}

// MemoryFact is one saved auto-memory, surfaced read-only in the panel.
type MemoryFact struct {
	ID          string `json:"id,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Body        string `json:"body"`
	Freshness   string `json:"freshness"`
}

type MemoryConflict struct {
	Key         string `json:"key"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	GlobalID    string `json:"globalId"`
	GlobalName  string `json:"globalName"`
	Resolution  string `json:"resolution"`
}

type MemoryRecallHit struct {
	ID        string  `json:"id"`
	Revision  int     `json:"revision"`
	Name      string  `json:"name"`
	Title     string  `json:"title,omitempty"`
	Type      string  `json:"type"`
	Scope     string  `json:"scope"`
	Score     float64 `json:"score"`
	Freshness string  `json:"freshness"`
	Reason    string  `json:"reason"`
	Snippet   string  `json:"snippet"`
}

type MemoryRecallTrace struct {
	Query      string            `json:"query"`
	Hits       []MemoryRecallHit `json:"hits"`
	Omitted    int               `json:"omitted"`
	CharBudget int               `json:"charBudget"`
	UsedChars  int               `json:"usedChars"`
	Suppressed string            `json:"suppressed,omitempty"`
}

// MemoryArchive is one archived auto-memory kept only for inspection.
type MemoryArchive struct {
	ID          string `json:"id,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description"`
	Type        string `json:"type"`
	Scope       string `json:"scope"`
	Body        string `json:"body"`
	Freshness   string `json:"freshness"`
	Path        string `json:"path"`
	ArchivedAt  string `json:"archivedAt,omitempty"`
}

// MemoryScope is one writable quick-add target (scope id + the file it writes to).
type MemoryScope struct {
	Scope string `json:"scope"`
	Path  string `json:"path"`
}

// MemoryView is the whole memory panel payload: hierarchical docs, active saved
// facts, archived facts, and the writable scopes for the quick-add selector.
type MemoryView struct {
	Docs                   []MemoryDoc             `json:"docs"`
	Facts                  []MemoryFact            `json:"facts"`
	Archives               []MemoryArchive         `json:"archives"`
	Scopes                 []MemoryScope           `json:"scopes"`
	InstructionDiagnostics []InstructionDiagnostic `json:"instructionDiagnostics"`
	Conflicts              []MemoryConflict        `json:"conflicts"`
	LastRecall             MemoryRecallTrace       `json:"lastRecall"`
	StoreDir               string                  `json:"storeDir"`
	StoreGlobalDir         string                  `json:"storeGlobalDir,omitempty"`
	Available              bool                    `json:"available"`
}

// writableScopes are the quick-add targets the panel offers, broad → specific.
var writableScopes = []memory.Scope{memory.ScopeUser, memory.ScopeProject, memory.ScopeLocal}

// Memory returns the loaded memory for the panel: the REASONIX.md hierarchy,
// active/archived auto-memories, and the writable scopes. Read-only; mutations
// go through Remember / SaveDoc.
func (a *App) Memory() MemoryView {
	return a.memoryForCtrl(nil, true)
}

// MemoryForTab returns the loaded memory for a specific tab's controller,
// so the panel can show memory for any open project, not just the active tab.
// If the tab does not exist or has no controller, returns an empty view
// instead of falling back to the active tab (which would show the wrong data).
// An empty tabID is treated as "no tab specified" and falls back to the
// active tab for backward compatibility.
func (a *App) MemoryForTab(tabID string) MemoryView {
	if tabID == "" {
		return a.memoryForCtrl(nil, true)
	}
	return a.memoryForCtrl(a.ctrlByTabID(tabID), false)
}

func (a *App) memoryForCtrl(ctrl control.SessionAPI, fallback bool) MemoryView {
	view := emptyMemoryView()
	if ctrl == nil {
		if !fallback {
			return view
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return view
		}
	}
	set := ctrl.Memory()
	if set == nil {
		return view
	}
	view.StoreDir = set.Store.Dir
	view.StoreGlobalDir = set.Store.GlobalDir
	view.Available = true
	for _, d := range set.Docs {
		imports := make([]MemoryImport, 0, len(d.Imports))
		for _, imported := range d.Imports {
			imports = append(imports, MemoryImport{Path: imported.Path, SourcePath: imported.SourcePath})
		}
		view.Docs = append(view.Docs, MemoryDoc{
			Path: d.Path, Scope: string(d.Scope), Directory: d.Directory, Body: d.Body,
			Imports: imports, Depth: d.Depth, Order: d.Order, Precedence: d.Order,
		})
	}
	for _, diagnostic := range set.InstructionDiagnostics {
		view.InstructionDiagnostics = append(view.InstructionDiagnostics, InstructionDiagnostic{
			Code: diagnostic.Code, Path: diagnostic.Path, SourcePath: diagnostic.SourcePath,
			Line: diagnostic.Line, Message: diagnostic.Message,
		})
	}
	allFacts := set.Store.ListAll()
	for _, f := range allFacts {
		view.Facts = append(view.Facts, memoryFactView(f))
	}
	for _, conflict := range memory.FindOverrides(allFacts) {
		view.Conflicts = append(view.Conflicts, MemoryConflict{
			Key: conflict.Key, ProjectID: conflict.Project.ID, ProjectName: conflict.Project.Name,
			GlobalID: conflict.Global.ID, GlobalName: conflict.Global.Name, Resolution: "project_over_global",
		})
	}
	view.LastRecall = memoryRecallTraceView(ctrl.LastMemoryRecall())
	for _, f := range set.Store.ListArchived() {
		archivedAt := ""
		if !f.ArchivedAt.IsZero() {
			archivedAt = f.ArchivedAt.Format(time.RFC3339)
		}
		view.Archives = append(view.Archives, MemoryArchive{
			ID: f.ID, Revision: f.Revision, CreatedAt: formatMemoryTime(f.CreatedAt), UpdatedAt: formatMemoryTime(f.UpdatedAt),
			Name: f.Name, Title: f.Title, Description: f.Description, Type: string(f.Type), Scope: string(f.Scope), Body: f.Body,
			Freshness: memory.FreshnessFor(f.Memory, time.Now().UTC()), Path: f.Path, ArchivedAt: archivedAt,
		})
	}
	for _, sc := range writableScopes {
		if p := set.DocPath(sc); p != "" {
			view.Scopes = append(view.Scopes, MemoryScope{Scope: string(sc), Path: p})
		}
	}
	return view
}

func formatMemoryTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func emptyMemoryView() MemoryView {
	return MemoryView{
		Docs: []MemoryDoc{}, Facts: []MemoryFact{}, Archives: []MemoryArchive{}, Scopes: []MemoryScope{},
		InstructionDiagnostics: []InstructionDiagnostic{}, Conflicts: []MemoryConflict{},
		LastRecall: MemoryRecallTrace{Hits: []MemoryRecallHit{}},
	}
}

// Remember quick-adds a one-line note to the doc-memory file for scope — the
// panel's explicit "remember" action, equivalent to typing "/remember <note>".
// An unknown scope falls back to project. Returns the file written.
func (a *App) Remember(scope, note string) (string, error) {
	return a.rememberForCtrl(nil, scope, note, true)
}

func (a *App) RememberForTab(tabID, scope, note string) (string, error) {
	if tabID == "" {
		return a.rememberForCtrl(nil, scope, note, true)
	}
	return a.rememberForCtrl(a.ctrlByTabID(tabID), scope, note, false)
}

func (a *App) rememberForCtrl(ctrl control.SessionAPI, scope, note string, fallback bool) (string, error) {
	if ctrl == nil {
		if !fallback {
			return "", nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return "", nil
		}
	}
	return ctrl.QuickAdd(parseScope(scope), note)
}

// Forget deletes a saved auto-memory by name — the panel's delete action for a
// fact the model owns. A no-op when no controller is attached.
func (a *App) Forget(name string) error {
	return a.forgetForCtrl(nil, name, true)
}

func (a *App) ForgetForTab(tabID, name string) error {
	if tabID == "" {
		return a.forgetForCtrl(nil, name, true)
	}
	return a.forgetForCtrl(a.ctrlByTabID(tabID), name, false)
}

func (a *App) forgetForCtrl(ctrl control.SessionAPI, name string, fallback bool) error {
	if ctrl == nil {
		if !fallback {
			return nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return nil
		}
	}
	return ctrl.ForgetMemory(name)
}

// RestoreArchivedMemory recovers one archived fact without replacing active
// memory. The store preserves its identity and creates a new audited revision.
func (a *App) RestoreArchivedMemory(archivePath string) (MemoryFact, error) {
	return a.restoreArchivedMemoryForCtrl(nil, archivePath, true)
}

func (a *App) RestoreArchivedMemoryForTab(tabID, archivePath string) (MemoryFact, error) {
	if tabID == "" {
		return a.restoreArchivedMemoryForCtrl(nil, archivePath, true)
	}
	return a.restoreArchivedMemoryForCtrl(a.ctrlByTabID(tabID), archivePath, false)
}

func (a *App) restoreArchivedMemoryForCtrl(ctrl control.SessionAPI, archivePath string, fallback bool) (MemoryFact, error) {
	if ctrl == nil {
		if !fallback {
			return MemoryFact{}, nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return MemoryFact{}, nil
		}
	}
	restored, err := ctrl.RestoreArchivedMemory(archivePath)
	if err != nil {
		return MemoryFact{}, err
	}
	return memoryFactView(restored), nil
}

func memoryFactView(f memory.Memory) MemoryFact {
	return MemoryFact{
		ID: f.ID, Revision: f.Revision, CreatedAt: formatMemoryTime(f.CreatedAt), UpdatedAt: formatMemoryTime(f.UpdatedAt),
		Name: f.Name, Title: f.Title, Description: f.Description, Type: string(f.Type), Scope: string(f.Scope), Body: f.Body,
		Freshness: memory.FreshnessFor(f, time.Now().UTC()),
	}
}

func memoryRecallTraceView(trace memory.RecallResult) MemoryRecallTrace {
	view := MemoryRecallTrace{
		Query: trace.Query, Hits: []MemoryRecallHit{}, Omitted: trace.Omitted,
		CharBudget: trace.CharBudget, UsedChars: trace.UsedChars, Suppressed: trace.Suppressed,
	}
	for _, hit := range trace.Hits {
		view.Hits = append(view.Hits, MemoryRecallHit{
			ID: hit.Memory.ID, Revision: hit.Memory.Revision, Name: hit.Memory.Name, Title: hit.Memory.Title,
			Type: string(hit.Memory.Type), Scope: string(hit.Memory.Scope), Score: hit.Score,
			Freshness: hit.Freshness, Reason: hit.Reason, Snippet: hit.Snippet,
		})
	}
	return view
}

func (a *App) MemoryRevisions(ref string) []MemoryFact {
	return a.memoryRevisionsForCtrl(nil, ref, true)
}

func (a *App) MemoryRevisionsForTab(tabID, ref string) []MemoryFact {
	if tabID == "" {
		return a.memoryRevisionsForCtrl(nil, ref, true)
	}
	return a.memoryRevisionsForCtrl(a.ctrlByTabID(tabID), ref, false)
}

func (a *App) memoryRevisionsForCtrl(ctrl control.SessionAPI, ref string, fallback bool) []MemoryFact {
	out := []MemoryFact{}
	if ctrl == nil {
		if !fallback {
			return out
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return out
		}
	}
	for _, revision := range ctrl.MemoryRevisions(ref) {
		out = append(out, memoryFactView(revision))
	}
	return out
}

func (a *App) RestoreMemoryRevision(ref string, revision int) (MemoryFact, error) {
	return a.restoreMemoryRevisionForCtrl(nil, ref, revision, true)
}

func (a *App) RestoreMemoryRevisionForTab(tabID, ref string, revision int) (MemoryFact, error) {
	if tabID == "" {
		return a.restoreMemoryRevisionForCtrl(nil, ref, revision, true)
	}
	return a.restoreMemoryRevisionForCtrl(a.ctrlByTabID(tabID), ref, revision, false)
}

func (a *App) restoreMemoryRevisionForCtrl(ctrl control.SessionAPI, ref string, revision int, fallback bool) (MemoryFact, error) {
	if ctrl == nil {
		if !fallback {
			return MemoryFact{}, nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return MemoryFact{}, nil
		}
	}
	restored, err := ctrl.RestoreMemory(ref, revision)
	if err != nil {
		return MemoryFact{}, err
	}
	return memoryFactView(restored), nil
}

// SaveDoc overwrites a memory doc with the panel editor's contents. The controller
// validates path against the recognized memory files. Returns the file written.
func (a *App) SaveDoc(path, body string) (string, error) {
	return a.saveDocForCtrl(nil, path, body, true)
}

func (a *App) SaveDocForTab(tabID, path, body string) (string, error) {
	if tabID == "" {
		return a.saveDocForCtrl(nil, path, body, true)
	}
	return a.saveDocForCtrl(a.ctrlByTabID(tabID), path, body, false)
}

func (a *App) saveDocForCtrl(ctrl control.SessionAPI, path, body string, fallback bool) (string, error) {
	if ctrl == nil {
		if !fallback {
			return "", nil
		}
		a.mu.RLock()
		ctrl = a.activeCtrlLocked()
		a.mu.RUnlock()
		if ctrl == nil {
			return "", nil
		}
	}
	return ctrl.SaveDoc(path, body)
}

// parseScope maps a frontend scope id to a memory.Scope, defaulting to project.
func parseScope(s string) memory.Scope {
	switch memory.Scope(s) {
	case memory.ScopeUser:
		return memory.ScopeUser
	case memory.ScopeLocal:
		return memory.ScopeLocal
	default:
		return memory.ScopeProject
	}
}
