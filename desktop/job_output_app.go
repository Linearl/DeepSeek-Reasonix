package main

import (
	"fmt"
	"strings"
)

// tl1479 — the capsule's bash output detail reads its data here. The agent's
// tail path (jobs.Manager.OutputForSession, bash_output) is incremental and
// consuming; the UI gets a separate non-consuming snapshot instead so the two
// readers never fight over the same cursor.

// JobOutputView is one background job's recent output for the capsule's
// output detail. Text is the bounded tail snapshot the jobs manager serves
// (64KiB, server-side) — the frontend never re-ships a multi-megabyte
// artifact. Found is false when the id is unknown (the job ended and left the
// manager, or the tab has no controller).
type JobOutputView struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	Found  bool   `json:"found"`
}

// JobOutputForTab reads one background job's recent output without relying on
// whatever tab happens to be active when the asynchronous frontend call
// completes (same tab-routing contract as CancelJobForTab). Read-only and
// non-consuming: bash_output's incremental cursor is untouched.
func (a *App) JobOutputForTab(tabID, jobID string) (JobOutputView, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return JobOutputView{}, fmt.Errorf("job id is required")
	}
	if tabID != "" && a.isRemoteTab(tabID) {
		return JobOutputView{}, fmt.Errorf("background job output is unavailable for remote tabs")
	}
	ctrl := a.ctrlForRuntimeTabID(tabID)
	if ctrl == nil {
		return JobOutputView{Found: false}, nil
	}
	reader, ok := ctrl.(interface {
		JobOutput(string) (string, string, bool)
	})
	if !ok {
		return JobOutputView{}, fmt.Errorf("background job output is unavailable")
	}
	text, status, found := reader.JobOutput(jobID)
	return JobOutputView{Text: text, Status: status, Found: found}, nil
}
