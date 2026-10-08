package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/config"
)

// crash_analysis.go is task 617 route B: "one-click analyze" from the crash /
// performance prompt. It reuses the existing session surface — rotate the
// active tab to a fresh session, switch it to YOLO, and submit an initial
// instruction that carries the sanitized diagnostic payload and asks the agent
// to run the gh-issue-submit skill and report the issue link back into the
// session. No new network endpoint is introduced; the only side effects are a
// local gh auth probe and the user-approved session itself.
//
// Route A (copy a paste-ready issue skeleton) stays the zero-dependency
// fallback whenever any prerequisite here fails.

const crashAnalysisRepo = "Linearl/DeepSeek-Reasonix"

type CrashAnalysisAvailabilityReport struct {
	// SourceReady: a local reasonix fork checkout was detected. Root-cause
	// analysis needs the source; diagnostics alone only carry symptoms.
	SourceReady bool   `json:"sourceReady"`
	SourceDir   string `json:"sourceDir,omitempty"`
	// GhAuthenticated: `gh auth status` (with token env vars stripped, matching
	// the libs-side keyring convention) reports a usable GitHub identity.
	GhAuthenticated bool   `json:"ghAuthenticated"`
	GhCheckDetail   string `json:"ghCheckDetail,omitempty"`
	// WorkspaceReady: the active tab has a live session to host the analysis.
	WorkspaceReady bool `json:"workspaceReady"`
	Ready          bool `json:"ready"`
}

// CrashAnalysisAvailability runs the three route-B prerequisite checks without
// starting anything. The frontend gates the one-click flow on this report and
// shows one distinct notice per failed check (missing source / spend warning /
// gh auth).
func (a *App) CrashAnalysisAvailability() CrashAnalysisAvailabilityReport {
	dir := detectCrashAnalysisSourceDir()
	ghOK, ghDetail := ghAuthenticated()
	report := CrashAnalysisAvailabilityReport{
		SourceReady:     dir != "",
		SourceDir:       dir,
		GhAuthenticated: ghOK,
		GhCheckDetail:   ghDetail,
	}
	a.mu.RLock()
	tab := a.activeTabLocked()
	live := tab != nil && !a.tabIsReadOnly(tab) && tab.Ctrl != nil
	a.mu.RUnlock()
	report.WorkspaceReady = live
	report.Ready = report.SourceReady && report.GhAuthenticated && report.WorkspaceReady
	return report
}

// StartCrashAnalysis performs route B end to end and returns a short summary of
// what was started. Every prerequisite is re-checked here so the binding stays
// safe even if the frontend's earlier availability probe went stale.
func (a *App) StartCrashAnalysis(kind, detail string) (string, error) {
	dir := detectCrashAnalysisSourceDir()
	if dir == "" {
		return "", fmt.Errorf("no local reasonix source detected — root-cause analysis needs the fork checkout; use the Copy button to report manually")
	}
	if ok, ghDetail := ghAuthenticated(); !ok {
		hint := strings.TrimSpace(ghDetail)
		if hint == "" {
			hint = "gh auth status failed"
		}
		return "", fmt.Errorf("GitHub CLI is not authenticated (%s) — run gh auth login first, or use the Copy button to report manually", hint)
	}

	r, err := crashReportFromDetail(kind, detail)
	if err != nil {
		return "", err
	}
	if err := ensureCrashIdentity(&r); err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}

	// Rotate the active tab to a fresh session, force YOLO so the analysis run
	// is not interrupted by approval prompts, then submit the instruction.
	if err := a.NewSessionForTab(""); err != nil {
		return "", fmt.Errorf("could not start a fresh session for the analysis (%v) — use the Copy button to report manually", err)
	}
	a.SetModeForTab("", "yolo")
	instruction := buildCrashAnalysisInstruction(dir, string(payload))
	if err := a.SubmitToTab("", instruction); err != nil {
		return "", fmt.Errorf("could not submit the analysis instruction (%v) — use the Copy button to report manually", err)
	}
	return fmt.Sprintf("YOLO analysis session started; it will analyze the diagnostic against %s and submit an issue to %s via gh-issue-submit.", dir, crashAnalysisRepo), nil
}

func buildCrashAnalysisInstruction(sourceDir, payload string) string {
	var b strings.Builder
	b.WriteString("请分析以下 Reasonix 桌面端诊断报告，定位根因并提交 issue。\n\n")
	b.WriteString("要求：\n")
	b.WriteString(fmt.Sprintf("1. 调用 gh-issue-submit 技能，分析该诊断的根因，提交 issue 到 %s，附复现线索与 file:line。\n", crashAnalysisRepo))
	b.WriteString(fmt.Sprintf("2. 本地 fork 源码位于 %s ，优先在其中定位相关代码路径，给出具体文件与行号。\n", sourceDir))
	b.WriteString("3. issue 提交成功后，把 issue 链接发送到本会话；若提交失败，把根因分析与建议的 issue 标题/正文发送到本会话。\n\n")
	b.WriteString("诊断 payload（已脱敏）：\n```json\n")
	b.WriteString(payload)
	b.WriteString("\n```\n")
	return b.String()
}

// detectCrashAnalysisSourceDir probes the known fork checkout locations and
// returns the first one that actually looks like the reasonix source tree.
// Detection failure is the normal "source not downloaded" case: route B stops
// and the UI points at route A.
func detectCrashAnalysisSourceDir() string {
	candidates := []string{
		// The workspace layout on this machine: the global workspace (sibling of
		// the app's own user dir) hosts local repo checkouts under github-repo/.
		filepath.Join(config.MemoryUserDir(), "global-workspace", "github-repo", "reasonix"),
	}
	for _, dir := range candidates {
		if isReasonixSourceDir(dir) {
			return dir
		}
	}
	return ""
}

func isReasonixSourceDir(dir string) bool {
	goModPath := filepath.Join(dir, "go.mod")
	body, err := os.ReadFile(goModPath)
	if err != nil || !bytes.Contains(body, []byte("module reasonix")) {
		return false
	}
	// The diagnostics under analysis live in the desktop surface; require its
	// source so "some other repo named reasonix" does not pass.
	_, err = os.Stat(filepath.Join(dir, "desktop", "crash_app.go"))
	return err == nil
}

// ghAuthenticated probes the GitHub CLI the same way the analysis run will use
// it: token env vars stripped so gh resolves the keyring-stored identity (the
// libs-side `env -u GITHUB_TOKEN gh ...` convention) rather than an exported
// variable the session would not see.
func ghAuthenticated() (bool, string) {
	gh, err := exec.LookPath("gh")
	if err != nil {
		return false, "gh CLI not found on PATH"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gh, "auth", "status")
	cmd.Env = filterEnv(os.Environ(), "GITHUB_TOKEN", "GH_TOKEN")
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if ctx.Err() != nil {
			detail = "gh auth status timed out"
		}
		return false, detail
	}
	return true, ""
}

func filterEnv(environ []string, names ...string) []string {
	drop := make(map[string]bool, len(names))
	for _, name := range names {
		drop[name] = true
	}
	kept := make([]string, 0, len(environ))
	for _, entry := range environ {
		if key, _, found := strings.Cut(entry, "="); found && drop[key] {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}
