package agent

import "testing"

// #39 / upstream #10023: compaction rewrites the transcript, so the per-turn
// duplicate memory must not survive it, or a legitimate re-issued todo_write
// is swallowed as a duplicate.
func TestCompactionClearsResultFingerprints(t *testing.T) {
	var s turnLoopState
	if _, seen := s.rememberFingerprint("fp", "call-1"); seen {
		t.Fatal("first fingerprint must be new")
	}
	if _, seen := s.rememberFingerprint("fp", "call-2"); !seen {
		t.Fatal("second fingerprint must be a duplicate")
	}
	s.clearResultFingerprints()
	if _, seen := s.rememberFingerprint("fp", "call-3"); seen {
		t.Fatal("fingerprint must be new again after compaction")
	}
}
