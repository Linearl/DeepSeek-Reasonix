package main

import "fmt"

// crash_mock.go is task 642: the lab dev-debug "simulate crash" entry. The user
// proposal behind 617/618 was that without a real crash there is no way to see
// the whole reporting chain work. This entry builds a synthetic report on the
// frontend (no process actually crashes), then runs it through the exact real
// pipeline so every leg is exercised:
//
//  1. crashReportFromDetail — the same parse/sanitize/shape the real reports
//     take on the receiving side of the binding;
//  2. postCrashReport — the own collection channel (crashEndpoint);
//  3. on upload failure, writePendingReport — the crash-pending queue a native
//     panic uses, so the next launch retries it via flushPendingCrash.
//
// Every synthetic report is stamped TestMock so the receiving end (the
// crash-report worker) can tell it apart from a real failure: the flag survives
// into storage, pins severity to "low", and namespaces the fingerprint so a
// mock can never inflate a real crash group.

// ReportMockCrash runs one simulated report through the real pipeline. The
// returned status is "uploaded" (own channel accepted it) or "queued" (upload
// failed — e.g. the known upstream outage — and the report landed in the local
// crash-pending queue for the next-launch retry, exactly like a native panic).
func (a *App) ReportMockCrash(kind, detail string) (string, error) {
	r, err := crashReportFromDetail(kind, detail)
	if err != nil {
		return "", err
	}
	// Belt and suspenders: the lab payload already carries testMock, but a
	// stale lab build must never be able to send an unlabeled mock.
	r.TestMock = true
	if r.Source == "" {
		r.Source = "frontend.mock"
	}
	c, err := httpClient()
	if err != nil {
		return "", err
	}
	if err := ensureCrashIdentity(&r); err != nil {
		return "", err
	}
	if err := postCrashReport(a.reqCtx(), c, crashEndpoint, r); err != nil {
		if !writePendingReport(r, true) {
			return "", fmt.Errorf("upload failed (%v) and the local pending queue refused the report", err)
		}
		return "queued", nil
	}
	return "uploaded", nil
}
