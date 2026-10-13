package homemigrate

import (
	"fmt"
	"sort"
)

// RiskID identifies one of the six migration risks the wizard must show and
// the user must acknowledge before Execute runs. The ids are contract: they
// cross the Wails binding to the frontend, which renders one checkbox per id
// (frontend test asserts all six render; Execute refuses partial acks).
type RiskID string

const (
	// RiskDualHomeSplit: after the switch, data written by processes that
	// never re-read the pointer (already-running instances, external tools
	// launched with an explicit REASONIX_HOME) lands in the OLD home while
	// new processes use the new one. The old home is intentionally kept, so
	// the split is visible and reversible rather than silently merged.
	RiskDualHomeSplit RiskID = "dual-home-split"
	// RiskPointerCorruptFallback: a corrupt/deleted pointer file makes the
	// next launch silently fall back to the OS-default home (by design, so a
	// bad pointer cannot brick the app); new-looking data then "disappears"
	// from the default view until the pointer is repaired.
	RiskPointerCorruptFallback RiskID = "pointer-corrupt-fallback"
	// RiskInterruptedMigration: a killed process mid-copy leaves a partial
	// target directory; the pointer never switched, so the fix is simply to
	// re-run the migration — but the user must not hand-edit the partial
	// copy meanwhile.
	RiskInterruptedMigration RiskID = "interrupted-migration"
	// RiskConcurrentInstance: while the migration mutex is live, second
	// Reasonix processes (desktop/CLI/serve) refuse to start, and a running
	// instance must not be used for new turns until the migration finishes.
	RiskConcurrentInstance RiskID = "concurrent-instance"
	// RiskOnedriveLock: a target inside a OneDrive sync root invites sync
	// file locks and partial-upload windows over the live home; strongly
	// discouraged, allowed only with explicit acknowledgement.
	RiskOnedriveLock RiskID = "onedrive-lock"
	// RiskCredentialEnv: the credential .env is copied with freshly redone
	// access permissions; the ORIGINAL .env stays in the old home after the
	// migration — the user should clean up the old home once the new one is
	// confirmed working.
	RiskCredentialEnv RiskID = "credential-env"
)

// Risk is one wizard-displayed risk row. Severity gates the UI emphasis.
type Risk struct {
	ID       RiskID `json:"id"`
	Severity string `json:"severity"` // "warn" | "error"
	// DetailKey is the frontend locale key holding the user-facing text
	// (settings.homePointer.risk.<id>) — Go ships ids, locales ship words,
	// so the three languages stay in sync without Go-side copies.
	DetailKey string `json:"detailKey"`
}

// AllRisks returns the six task-846 risks in stable display order.
func AllRisks() []Risk {
	return []Risk{
		{ID: RiskDualHomeSplit, Severity: "warn", DetailKey: "settings.homePointer.risk.dualHomeSplit"},
		{ID: RiskPointerCorruptFallback, Severity: "warn", DetailKey: "settings.homePointer.risk.pointerCorruptFallback"},
		{ID: RiskInterruptedMigration, Severity: "warn", DetailKey: "settings.homePointer.risk.interruptedMigration"},
		{ID: RiskConcurrentInstance, Severity: "error", DetailKey: "settings.homePointer.risk.concurrentInstance"},
		{ID: RiskOnedriveLock, Severity: "error", DetailKey: "settings.homePointer.risk.oneDriveLock"},
		{ID: RiskCredentialEnv, Severity: "warn", DetailKey: "settings.homePointer.risk.credentialEnv"},
	}
}

// RequireAllRisksAcknowledged is the Execute gate: every risk id must be in
// the acknowledged set, no extras tolerated (a stale id means the frontend
// and backend drifted — refuse and say so).
func RequireAllRisksAcknowledged(acknowledged []string) error {
	required := map[RiskID]bool{}
	for _, risk := range AllRisks() {
		required[risk.ID] = false
	}
	seen := map[string]bool{}
	for _, id := range acknowledged {
		trimmed := RiskID(id)
		if _, known := required[trimmed]; !known {
			return fmt.Errorf("%w: unknown risk id %q (backend/frontend drift)", ErrRisksNotAcknowledged, id)
		}
		if seen[id] {
			return fmt.Errorf("%w: risk %q acknowledged twice", ErrRisksNotAcknowledged, id)
		}
		seen[id] = true
		required[trimmed] = true
	}
	var missing []string
	for id, ok := range required {
		if !ok {
			missing = append(missing, string(id))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: missing %v", ErrRisksNotAcknowledged, missing)
	}
	return nil
}
