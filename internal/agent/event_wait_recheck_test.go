package agent

import "testing"

// Task 244 B3 (experimental_event_wait_recheck): the return-time recheck is a
// pure seam so the judged-vs-returned window race is testable without waiting
// on a live session state machine.

func TestEventWaitRecheckValueStates(t *testing.T) {
	// Off (default): nil — the return shape stays byte-identical.
	if got := eventWaitRecheckValue(false, func() bool { return true }); got != nil {
		t.Fatalf("off state must return nil, got %v", *got)
	}
	// On: the second verdict rides along, whatever it is.
	if got := eventWaitRecheckValue(true, func() bool { return true }); got == nil || !*got {
		t.Fatal("on state must surface the re-evaluated verdict (true)")
	}
	if got := eventWaitRecheckValue(true, func() bool { return false }); got == nil || *got {
		t.Fatal("on state must surface the re-evaluated verdict (false)")
	}
	// On with no checker: nil, never a guessed verdict.
	if got := eventWaitRecheckValue(true, nil); got != nil {
		t.Fatalf("nil checker must stay nil, got %v", *got)
	}
}

// The disagreement case — the judged tick said satisfied but the return-time
// recheck says otherwise — is the whole point of the field: it must be
// representable, so a caller can see the race instead of swallowing it.
func TestEventWaitRecheckValueExposesDisagreement(t *testing.T) {
	judged := true
	recheck := eventWaitRecheckValue(true, func() bool { return !judged })
	if recheck == nil {
		t.Fatal("disagreement must be representable")
	}
	if *recheck == judged {
		t.Fatal("recheck must carry the second verdict, not echo the first")
	}
}
