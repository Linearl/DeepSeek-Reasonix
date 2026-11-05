package main

import (
	"fmt"
	"strings"

	"reasonix/internal/agentimport"
	"reasonix/internal/config"
)

// AgentImportSourceView reports one external tool's detection state for the
// import panel (tl1422). An undetected tool still appears so the UI can say so.
type AgentImportSourceView struct {
	Tool     string            `json:"tool"`
	Detected bool              `json:"detected"`
	Paths    map[string]string `json:"paths,omitempty"` // kind → discovered path
	Note     string            `json:"note,omitempty"`
	Items    int               `json:"items"`
}

// AgentImportItemView is one importable item with its converted content
// preview and the conflict state of Reasonix's target storage.
type AgentImportItemView struct {
	ID          string `json:"id"`
	Tool        string `json:"tool"`
	Kind        string `json:"kind"` // "command" | "skill" | "mcp"
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SourcePath  string `json:"sourcePath,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Conflict    bool   `json:"conflict"`
	// NativelyActive marks items Reasonix already discovers in the home
	// convention dirs (~/.claude …); importing duplicates them into the
	// Reasonix home dirs.
	NativelyActive bool     `json:"nativelyActive"`
	Recommended    bool     `json:"recommended"`
	Notes          []string `json:"notes,omitempty"`
}

// AgentImportScanView is the import panel's data.
type AgentImportScanView struct {
	Sources []AgentImportSourceView `json:"sources"`
	Items   []AgentImportItemView   `json:"items"`
}

// AgentImportRequestView selects one scanned item plus its conflict handling.
type AgentImportRequestView struct {
	ID     string `json:"id"`
	Policy string `json:"policy"` // "skip" | "overwrite" | "rename"; default skip
}

// AgentImportOutcomeView is one receipt line.
type AgentImportOutcomeView struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Kind   string `json:"kind"`
	Name   string `json:"name"`
	Status string `json:"status"` // "imported" | "renamed" | "skipped" | "failed"
	Detail string `json:"detail,omitempty"`
}

// AgentImportReceiptView is the import summary: success/skip/fail counts plus
// the per-item detail the UI can expand on failures.
type AgentImportReceiptView struct {
	Imported int                      `json:"imported"`
	Skipped  int                      `json:"skipped"`
	Failed   int                      `json:"failed"`
	Items    []AgentImportOutcomeView `json:"items"`
	// Refreshed reports which live surfaces were reloaded after the import
	// (skills rebuild + command rescan). False entries mean the session was
	// busy; the files are on disk and load on the next rebuild.
	SkillsRefreshed  bool `json:"skillsRefreshed"`
	CommandsReloaded bool `json:"commandsReloaded"`
}

// agentImportScanner builds the read-only scanner over the real home and
// Reasonix target dirs, with current MCP names for conflict detection.
func agentImportScanner() agentimport.Scanner {
	return agentimport.Scanner{
		ExistingMCPNames: agentimport.LoadExistingMCPNames(),
	}
}

// AgentImportScan scans the installed external AI coding tools (pure read —
// never writes to the external tools' files).
func (a *App) AgentImportScan() AgentImportScanView {
	scanner := agentImportScanner()
	result := scanner.Scan()
	view := AgentImportScanView{}
	for _, src := range result.Sources {
		paths := make(map[string]string, len(src.Paths))
		for kind, path := range src.Paths {
			paths[string(kind)] = path
		}
		view.Sources = append(view.Sources, AgentImportSourceView{
			Tool:     string(src.Tool),
			Detected: src.Detected,
			Paths:    paths,
			Note:     src.Note,
			Items:    src.Items,
		})
	}
	for _, cand := range result.Candidates {
		view.Items = append(view.Items, AgentImportItemView{
			ID:             cand.ID,
			Tool:           string(cand.Tool),
			Kind:           string(cand.Kind),
			Name:           cand.Name,
			Description:    cand.Description,
			SourcePath:     cand.SourcePath,
			Preview:        cand.Preview,
			Conflict:       cand.Conflict,
			NativelyActive: cand.NativelyActive,
			Recommended:    cand.Recommended,
			Notes:          cand.Notes,
		})
	}
	return view
}

// agentImportMCPSink persists each imported MCP entry. With a live session it
// goes through the controller (persist + connect, the cc-switch import path);
// without one it writes the user config directly so the entries load next
// session.
func (a *App) agentImportMCPSink() agentimport.MCPSink {
	return func(entry config.PluginEntry) error {
		_, ctrl := a.activeTabAndCtrl()
		if ctrl == nil {
			_, _, _, err := config.ImportCCSwitchMCPEntries([]config.PluginEntry{entry})
			return err
		}
		_, _, _, _, _, _, err := ctrl.ImportMCPEntries([]config.PluginEntry{entry})
		return err
	}
}

// AgentImportImport lands the selected items into Reasonix storage, honouring
// per-item conflict policies, then refreshes the live surfaces best-effort.
func (a *App) AgentImportImport(requests []AgentImportRequestView) (AgentImportReceiptView, error) {
	if len(requests) == 0 {
		return AgentImportReceiptView{}, fmt.Errorf("nothing selected to import")
	}
	importer := agentimport.Importer{
		Scanner: agentImportScanner(),
		MCPSink: a.agentImportMCPSink(),
	}
	parsed := make([]agentimport.ImportRequest, 0, len(requests))
	for _, req := range requests {
		parsed = append(parsed, agentimport.ImportRequest{
			ID:     req.ID,
			Policy: agentimport.ConflictPolicy(strings.TrimSpace(req.Policy)),
		})
	}
	receipt, err := importer.Import(parsed)
	if err != nil {
		return AgentImportReceiptView{}, err
	}
	view := AgentImportReceiptView{
		Imported: receipt.Imported,
		Skipped:  receipt.Skipped,
		Failed:   receipt.Failed,
	}
	for _, item := range receipt.Items {
		view.Items = append(view.Items, AgentImportOutcomeView{
			ID:     item.ID,
			Tool:   string(item.Tool),
			Kind:   string(item.Kind),
			Name:   item.Name,
			Status: item.Status,
			Detail: item.Detail,
		})
	}
	if receipt.Imported > 0 || receipt.Failed > 0 {
		// Best-effort live refresh: a busy session simply picks the imported
		// files up on its next rebuild.
		if err := a.RefreshSkills(); err == nil {
			view.SkillsRefreshed = true
		}
		if err := a.ReloadCommands(); err == nil {
			view.CommandsReloaded = true
		}
	}
	return view, nil
}
