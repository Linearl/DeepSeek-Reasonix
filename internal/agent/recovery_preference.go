package agent

import (
	"fmt"
	"sort"
)

// SetRecoveryPreferred records exactly one explicit preferred lineage member.
// Clearing old choices first can only fall back to an unresolved group; an
// interrupted update can never leave two canonical choices.
func SetRecoveryPreferred(paths []string, chosenPath string) error {
	chosenPath = canonicalSessionSavePath(chosenPath)
	if chosenPath == "" {
		return fmt.Errorf("empty preferred recovery path")
	}
	unique := map[string]struct{}{}
	for _, path := range paths {
		if path = canonicalSessionSavePath(path); path != "" {
			unique[path] = struct{}{}
		}
	}
	if _, ok := unique[chosenPath]; !ok {
		return fmt.Errorf("preferred recovery path is outside the lineage")
	}
	ordered, err := validatedRecoveryPreferenceMembers(unique)
	if err != nil {
		// Fail-soft (#9927 family): a lineage accumulated by repeated
		// recovery forks (e.g. after forced kills left the transcript tail
		// damaged, #9890) can trip the strict member checks — multiple
		// normal members or unreadable sidecars. Refusing here crashes the
		// version chooser and leaves the user stuck; proceeding with the
		// plain member list still records an explicit preference, which is
		// strictly better than an unhandled rejection.
		ordered = make([]string, 0, len(unique))
		for path := range unique {
			ordered = append(ordered, path)
		}
		sort.Strings(ordered)
	}
	for _, path := range ordered {
		if err := UpdateBranchMeta(path, false, clearRecoveryPreference); err != nil {
			return err
		}
	}
	chosen, err := LoadSession(chosenPath)
	if err != nil || chosen == nil {
		// Fail-soft: an unreadable transcript still gets its preference
		// flag recorded (without a digest) instead of failing the whole
		// choice operation (#9927).
		return UpdateBranchMeta(chosenPath, false, func(meta *BranchMeta) error {
			meta.RecoveryPreferred = true
			return nil
		})
	}
	if chosen.normalizedDirty || chosen.eventLogDamaged {
		// Damaged tail (#9890): record the preference without a digest —
		// digest mismatch is a soft signal for later reconciliation, not a
		// reason to reject the user's explicit choice.
		return UpdateBranchMeta(chosenPath, false, func(meta *BranchMeta) error {
			meta.RecoveryPreferred = true
			return nil
		})
	}
	digest, err := digestSessionMessages(chosen.Snapshot())
	if err != nil {
		return err
	}
	return UpdateBranchMeta(chosenPath, false, func(meta *BranchMeta) error {
		meta.RecoveryPreferred = true
		meta.RecoveryPreferredDigest = digestString(digest)
		return nil
	})
}

func validatedRecoveryPreferenceMembers(unique map[string]struct{}) ([]string, error) {
	ordered := make([]string, 0, len(unique))
	recoveredMembers, normalMembers := 0, 0
	for path := range unique {
		meta, ok, err := LoadBranchMeta(path)
		if err != nil || !ok {
			return nil, fmt.Errorf("invalid recovery lineage member")
		}
		if meta.Recovered {
			recoveredMembers++
		} else {
			normalMembers++
		}
		ordered = append(ordered, path)
	}
	if recoveredMembers == 0 || normalMembers > 1 {
		return nil, fmt.Errorf("invalid recovery lineage members")
	}
	sort.Strings(ordered)
	return ordered, nil
}

func clearRecoveryPreference(meta *BranchMeta) error {
	meta.RecoveryPreferred = false
	meta.RecoveryPreferredDigest = ""
	return nil
}
