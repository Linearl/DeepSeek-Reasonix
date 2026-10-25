package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"reasonix/internal/agent"
)

// tl 1103: only the failures TrashTopicForce can actually clear (active-work
// gate, session-lease-busy family) may carry the forceable machine code; every
// other failure must pass through unmarked so the UI never steers a file lock
// or a disk-full error toward "force archive".
func TestTrashTopicMarksForceableFailures(t *testing.T) {
	forceable := []error{
		errTopicHasActiveWork,
		errSessionBusyElsewhere,
		agent.ErrSessionLeaseHeld,
		fmt.Errorf("wrapped: %w", errTopicHasActiveWork),
		// The file-lock sentinel arrives already rewritten by
		// friendlySessionFileError into errSessionBusyElsewhere.
		friendlySessionFileError(agent.ErrSessionFileLockHeld),
	}
	for _, cause := range forceable {
		marked := markTopicArchiveForceable(cause)
		if marked == nil {
			t.Fatalf("markTopicArchiveForceable(%v) = nil", cause)
		}
		if !strings.HasPrefix(marked.Error(), topicArchiveForceablePrefix) {
			t.Fatalf("markTopicArchiveForceable(%v) = %q, want prefix %q", cause, marked.Error(), topicArchiveForceablePrefix)
		}
		if !strings.Contains(marked.Error(), cause.Error()) {
			t.Fatalf("marked error %q lost the human-readable cause %q", marked.Error(), cause.Error())
		}
		if !errors.Is(marked, cause) {
			t.Fatalf("marked error must keep errors.Is(%v) (Go tests and callers rely on the chain)", cause)
		}
	}

	plain := []error{
		nil,
		errTopicArchiveBusy,
		os.ErrNotExist,
		fmt.Errorf("disk full"),
	}
	for _, cause := range plain {
		marked := markTopicArchiveForceable(cause)
		if !errors.Is(marked, cause) && !(cause == nil && marked == nil) {
			t.Fatalf("markTopicArchiveForceable(%v) = %v, want unchanged", cause, marked)
		}
		if marked != nil && strings.HasPrefix(marked.Error(), topicArchiveForceablePrefix) {
			t.Fatalf("markTopicArchiveForceable(%v) must not carry the forceable prefix, got %q", cause, marked.Error())
		}
	}
}
