package main

// Task 846: Wails bindings for the change-data-home lab feature. The wizard
// lives in the lab tab behind experimental_home_pointer; these methods are the
// whole write surface (查看路径 / 迁移 / 恢复默认). Storage settings page stays
// read-only by design — these bindings are named HomePointer*/HomeMigration*
// and are NOT referenced from StorageSettingsPage.

import (
	"errors"
	"fmt"
	"strings"

	"reasonix/internal/config"
	"reasonix/internal/homemigrate"
)

// HomePointerStatusView is the lab card's boot state.
type HomePointerStatusView struct {
	// ExperimentalEnabled mirrors desktop.experimental_home_pointer.
	ExperimentalEnabled bool `json:"experimentalEnabled"`
	// PointerPath is where the pointer file would live (OS-default home).
	PointerPath string `json:"pointerPath"`
	// ActiveHome is the home the process is actually using right now.
	ActiveHome string `json:"activeHome"`
	// DefaultHome is the OS-implied home (the reset target).
	DefaultHome string `json:"defaultHome"`
	// PointerActive is true when the active home came from the pointer file.
	PointerActive bool `json:"pointerActive"`
	// PointerError carries the fallback reason when a pointer file exists
	// but could not be honored (risk 2 diagnostics).
	PointerError string `json:"pointerError"`
	// MigrationLockActive reports a live migration mutex from any process.
	MigrationLockActive bool `json:"migrationLockActive"`
}

// HomeMigrationPreviewView is the wizard's step-2 payload: validation result,
// advisory warnings, and the six risks to acknowledge.
type HomeMigrationPreviewView struct {
	Valid    bool                  `json:"valid"`
	Source   string                `json:"source"`
	Target   string                `json:"target"`
	Warnings []string              `json:"warnings"`
	Error    string                `json:"error"`
	Risks    []homemigrate.Risk    `json:"risks"`
}

// HomeMigrationResultView is Execute's terminal outcome.
type HomeMigrationResultView struct {
	Success bool   `json:"success"`
	Phase   string `json:"phase"`
	Detail  string `json:"detail"`
}

// HomePointerStatus assembles the lab card state. Read-only: safe without the
// experiment switch (the frontend hides the card anyway).
func (a *App) HomePointerStatus() HomePointerStatusView {
	enabled := false
	if cfg, _, err := a.loadDesktopUserConfigForView(); err == nil && cfg != nil {
		enabled = cfg.Desktop.ExperimentalHomePointer
	}
	view := HomePointerStatusView{
		ExperimentalEnabled: enabled,
		PointerPath:         config.DefaultHomePointerPath(),
		ActiveHome:          config.ReasonixHomeDir(),
		DefaultHome:         config.OsDefaultHomeDir(),
		MigrationLockActive: homemigrate.MigrationInProgress(),
	}
	pointer, err := config.ReadHomePointerFile(config.DefaultHomePointerPath())
	switch {
	case err != nil:
		view.PointerError = err.Error()
	case pointer != nil:
		view.PointerActive = !strings.EqualFold(view.ActiveHome, view.DefaultHome)
	}
	return view
}

// HomeMigrationPreview validates a target directory and returns the risk
// list. No locks taken, no writes — safe to call freely from the wizard.
func (a *App) HomeMigrationPreview(target string) HomeMigrationPreviewView {
	view := HomeMigrationPreviewView{
		Source: config.ReasonixHomeDir(),
		Risks:  homemigrate.AllRisks(),
	}
	warnings, err := homemigrate.ValidatePlan(view.Source, target)
	if err != nil {
		view.Error = err.Error()
		return view
	}
	view.Valid = true
	view.Target = target
	view.Warnings = warnings
	return view
}

// HomeMigrationExecute runs the transactional migration. Gates:
// experimental switch on (write path), every risk acknowledged, migration
// mutex free. Any failure leaves the pointer untouched (rollback).
func (a *App) HomeMigrationExecute(target string, acknowledged []string) (HomeMigrationResultView, error) {
	if err := a.requireHomePointerExperiment("migrate the data home"); err != nil {
		return HomeMigrationResultView{}, err
	}
	source := config.ReasonixHomeDir()
	exec := homemigrate.NewExecutor()
	phase, detail := exec.Execute(homemigrate.Plan{Source: source, Target: target}, acknowledged, nil)
	result := HomeMigrationResultView{
		Success: phase == homemigrate.PhaseDone,
		Phase:   string(phase),
		Detail:  detail,
	}
	if !result.Success {
		// The transaction aborted by design (pointer untouched). Surface the
		// reason as data; a Go error would render as a generic binding error.
		return result, nil
	}
	return result, nil
}

// HomeMigrationResetToDefault removes the pointer file (data is never copied
// back). Same switch gate as Execute.
func (a *App) HomeMigrationResetToDefault() error {
	if err := a.requireHomePointerExperiment("reset the data home pointer"); err != nil {
		return err
	}
	return homemigrate.NewExecutor().ResetToDefault()
}

func (a *App) requireHomePointerExperiment(action string) error {
	cfg, _, err := a.loadDesktopUserConfigForView()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg == nil || !cfg.Desktop.ExperimentalHomePointer {
		return fmt.Errorf("%s requires the 实验室 switch experimental_home_pointer (task 846); it is currently off", action)
	}
	if homemigrate.MigrationInProgress() {
		return errors.New("another home migration is already in progress (risk: concurrent instance)")
	}
	return nil
}
