package main

// Task 1259 layer 2 (治面): before a version switch executes, put the user in
// charge of the other running instances of this app.
//
// A switch only moves current.json; every already-running instance keeps its
// own (now old) tree, and the 2026-10-29 incident showed exactly what those
// become: ghost-prone processes that pruning must then protect and that
// elevated instances no ordinary-privilege path can kill. So the switch flow
// scans first, tells the user "N instances will become old versions", and —
// only on confirmation — asks each one to close gracefully. The close is a
// window-close request, never a force kill: instances that refuse (elevated
// ones, or instances whose close behavior hides to tray) are reported for
// manual handling. Declining keeps them running and proceeds with the
// informed switch; layer 1 now guards their trees from pruning either way.

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"reasonix/internal/installlayout"
)

// scanOtherVersionInstances returns the live instances of this app that the
// switch affects: processes executing from version trees of this install,
// excluding this process (and, when this process runs from a version tree,
// that tree's processes — they are already old by definition and reaping
// them adds nothing to the switch).
func scanOtherVersionInstances(installRoot, target string) []installlayout.OccupiedProcess {
	return filterOtherInstances(installlayout.ProbeVersionOccupancy(installRoot, ""), os.Getpid(), versionSwitchRunningVersion(), target)
}

// filterOtherInstances is the pure selection rule over one probe result,
// ordered oldest version first so closes proceed back-to-front.
func filterOtherInstances(procs []installlayout.OccupiedProcess, self int, running, target string) []installlayout.OccupiedProcess {
	others := make([]installlayout.OccupiedProcess, 0, len(procs))
	for _, p := range procs {
		if p.PID == self {
			continue
		}
		if running != "" && strings.EqualFold(p.Version, running) {
			continue
		}
		if strings.EqualFold(p.Version, target) {
			continue // the target version stays current after the switch
		}
		others = append(others, p)
	}
	sort.Slice(others, func(i, j int) bool { return others[i].Version < others[j].Version })
	return others
}

// occupiedProcessDescriptions renders instances for log fields.
func occupiedProcessDescriptions(procs []installlayout.OccupiedProcess) []string {
	out := make([]string, 0, len(procs))
	for _, p := range procs {
		out = append(out, fmt.Sprintf("%s pid=%d", p.Version, p.PID))
	}
	return out
}

// switchInstancesPromptMessage builds the confirmation body (pure, so tests
// can assert exactly what the user is told).
func switchInstancesPromptMessage(others []installlayout.OccupiedProcess, target string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d other Reasonix instance(s) are still running:\n\n", len(others))
	for _, p := range others {
		fmt.Fprintf(&b, "  version %s (pid %d)\n", p.Version, p.PID)
	}
	fmt.Fprintf(&b, "\nSwitching to %s leaves them running an old version. Close them now (graceful close, no force kill)?", target)
	b.WriteString("\n\nInstances that cannot be closed (elevated ones, or those running in the tray) are reported for manual close.")
	return b.String()
}

// confirmCloseOtherInstances asks the user via the native dialog. false =
// keep the instances running (the switch still proceeds, informed).
func confirmCloseOtherInstances(a *App, others []installlayout.OccupiedProcess, target string) bool {
	if a == nil || a.ctx == nil {
		slog.Warn("restart: cannot confirm instance close without a window context; keeping instances running")
		return false
	}
	confirm := "Close them and switch"
	cancel := "Switch anyway, leave them running"
	result, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         "Old instances still running",
		Message:       switchInstancesPromptMessage(others, target),
		Buttons:       []string{confirm, cancel},
		DefaultButton: cancel,
		CancelButton:  cancel,
	})
	if err != nil {
		slog.Warn("restart: instance-close dialog failed; keeping instances running", "err", err)
		return false
	}
	return result == confirm
}

// unclosedInstancesMessage builds the manual-close notice body (pure, tested).
func unclosedInstancesMessage(survivors []installlayout.OccupiedProcess) string {
	var b strings.Builder
	b.WriteString("These instance(s) did not close gracefully — close them manually (tray menu Quit, or as administrator):\n\n")
	for _, p := range survivors {
		fmt.Fprintf(&b, "  version %s (pid %d)\n", p.Version, p.PID)
	}
	b.WriteString("\nThe switch proceeds; their version trees are kept from pruning while they run.")
	return b.String()
}

// informUnclosedInstances surfaces the manual-close notice after the graceful
// attempts. Log-only without a window context (the warning is already in the
// log via the caller).
func (a *App) informUnclosedInstances(survivors []installlayout.OccupiedProcess) {
	if a == nil || a.ctx == nil {
		return
	}
	_, _ = runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:    runtime.WarningDialog,
		Title:   "Some instances could not be closed",
		Message: unclosedInstancesMessage(survivors),
		Buttons: []string{"OK"},
	})
}
