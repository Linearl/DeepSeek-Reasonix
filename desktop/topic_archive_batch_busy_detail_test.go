package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// tl1258: the batch failure list must carry the complete per-topic busy reason
// (holder classification, pid, acquire time, next step) instead of one bare
// "session is in use" line repeated for every topic. The holder liveness probe
// is the injected seam; these tests pin the rendering and the compatibility of
// the sessionLeaseHeldBusyError wrapper with the tl1201 sentinel classifications.

func swapDesktopProcessAliveForBusyDetail(t *testing.T, alive bool) {
	t.Helper()
	previous := agent.ProcessAlive
	agent.ProcessAlive = func(int) bool { return alive }
	t.Cleanup(func() { agent.ProcessAlive = previous })
}

func leaseBusyFixture(pid int) *agent.SessionLeaseError {
	return &agent.SessionLeaseError{
		Path: "C:\\users\\hidden\\session.jsonl",
		Info: &agent.SessionLeaseInfo{PID: pid, AcquiredAt: time.Now().Local().Truncate(time.Minute)},
	}
}

func TestBatchTopicArchiveFailureItemLeaseBusyCompleteReason(t *testing.T) {
	swapDesktopProcessAliveForBusyDetail(t, true)
	leaseErr := leaseBusyFixture(4123)
	since := leaseErr.Info.AcquiredAt.Format("15:04")

	for name, err := range map[string]error{
		"wrapped":   &sessionLeaseHeldBusyError{cause: leaseErr},
		"unwrapped": leaseErr,
	} {
		item := batchTopicArchiveFailureItem("topic_a", err)
		if !item.Forceable {
			t.Fatalf("%s: lease-busy item must stay forceable", name)
		}
		if !strings.Contains(item.Error, "pid 4123") {
			t.Fatalf("%s: busy reason must name the holder pid, got %q", name, item.Error)
		}
		if !strings.Contains(item.Error, ", since "+since) {
			t.Fatalf("%s: busy reason must name the acquire time, got %q", name, item.Error)
		}
		if !strings.Contains(item.Error, "close that window") {
			t.Fatalf("%s: busy reason must carry the close-the-window next step, got %q", name, item.Error)
		}
		if strings.Contains(item.Error, leaseErr.Path) {
			t.Fatalf("%s: busy reason must stay sanitized (no path), got %q", name, item.Error)
		}
	}
}

func TestBatchTopicArchiveFailureItemZombieCompleteReason(t *testing.T) {
	swapDesktopProcessAliveForBusyDetail(t, false)
	item := batchTopicArchiveFailureItem("topic_a", &sessionLeaseHeldBusyError{cause: leaseBusyFixture(4123)})
	if !item.Forceable {
		t.Fatal("zombie item must stay forceable")
	}
	if !strings.Contains(item.Error, "pid 4123") || !strings.Contains(item.Error, "crashed") {
		t.Fatalf("zombie reason must name the crashed holder pid, got %q", item.Error)
	}
	if !strings.Contains(item.Error, "restart Reasonix") {
		t.Fatalf("zombie reason must carry the restart next step, got %q", item.Error)
	}
	if strings.Contains(item.Error, "close that window") {
		t.Fatalf("zombie reason must not loop the user into closing a window that does not exist, got %q", item.Error)
	}
}

// The completeness half of tl1258: N failures each list their own complete
// reason — distinct holders are distinguishable, not one shared bare line.
func TestBatchTopicArchiveFailureItemsListEveryHolderDistinctly(t *testing.T) {
	swapDesktopProcessAliveForBusyDetail(t, true)
	itemA := batchTopicArchiveFailureItem("topic_a", &sessionLeaseHeldBusyError{cause: leaseBusyFixture(111)})
	itemB := batchTopicArchiveFailureItem("topic_b", &sessionLeaseHeldBusyError{cause: leaseBusyFixture(222)})
	if !strings.Contains(itemA.Error, "pid 111") {
		t.Fatalf("item A must name its own holder, got %q", itemA.Error)
	}
	if !strings.Contains(itemB.Error, "pid 222") {
		t.Fatalf("item B must name its own holder, got %q", itemB.Error)
	}
	if itemA.Error == itemB.Error {
		t.Fatal("distinct holders must not collapse into one shared bare reason")
	}
}

func TestBatchTopicArchiveFailureItemSelfHeldRuntimeReason(t *testing.T) {
	swapDesktopProcessAliveForBusyDetail(t, true)
	item := batchTopicArchiveFailureItem("topic_a", &sessionLeaseHeldBusyError{cause: leaseBusyFixture(os.Getpid())})
	if !strings.Contains(item.Error, "another runtime of this Reasonix instance") {
		t.Fatalf("self-held refusal must point at the instance's own runtime, got %q", item.Error)
	}
}

func TestBatchTopicArchiveFailureItemActiveWorkKeepsOwnMessage(t *testing.T) {
	item := batchTopicArchiveFailureItem("topic_a", errTopicHasActiveWork)
	if !item.Forceable {
		t.Fatal("active-work item must stay forceable")
	}
	if item.Error != errTopicHasActiveWork.Error() {
		t.Fatalf("active-work refusal keeps its own actionable message, got %q", item.Error)
	}
}

func TestSessionLeaseBusyDetailFallsBackToUpgradedSentinel(t *testing.T) {
	detail := sessionLeaseBusyDetail(errors.New("boom"))
	if detail != errSessionBusyElsewhere.Error() {
		t.Fatalf("no-info refusal must degrade to the sentinel text, got %q", detail)
	}
	if !strings.Contains(detail, "then retry") {
		t.Fatalf("sentinel text itself must carry the retry guidance (tl1258), got %q", detail)
	}
}

// sessionLeaseHeldBusyError keeps both compat faces: the tl1201 sentinel
// classification (errors.Is) and the original lease error (errors.As) that
// detail rendering and the friendly-error rewriter rely on.
func TestSessionLeaseBusyErrorKeepsSentinelAndHolderChain(t *testing.T) {
	swapDesktopProcessAliveForBusyDetail(t, true)
	wrapped := &sessionLeaseHeldBusyError{cause: leaseBusyFixture(4123)}
	if !errors.Is(wrapped, errSessionBusyElsewhere) {
		t.Fatal("live holder must classify as errSessionBusyElsewhere")
	}
	if !errors.Is(wrapped, agent.ErrSessionLeaseHeld) {
		t.Fatal("original ErrSessionLeaseHeld chain must stay reachable through the wrapper")
	}
	var leaseErr *agent.SessionLeaseError
	if !errors.As(wrapped, &leaseErr) || leaseErr == nil || leaseErr.Info == nil || leaseErr.Info.PID != 4123 {
		t.Fatalf("holder info must stay reachable through the wrapper, got %#v", leaseErr)
	}

	swapDesktopProcessAliveForBusyDetail(t, false)
	zombieWrapped := &sessionLeaseHeldBusyError{cause: leaseBusyFixture(4123)}
	if !errors.Is(zombieWrapped, errSessionBusyZombie) {
		t.Fatal("dead holder must classify as errSessionBusyZombie")
	}
	if errors.Is(zombieWrapped, errSessionBusyElsewhere) {
		t.Fatal("dead holder must not classify as the live-holder sentinel")
	}
}

// The detail renderer and the sentinel classifier must agree on the branch so
// a surfaced line never contradicts the machine classification.
func TestSessionLeaseBusyDetailMatchesSentinelClassification(t *testing.T) {
	for _, alive := range []bool{true, false} {
		swapDesktopProcessAliveForBusyDetail(t, alive)
		wrapped := &sessionLeaseHeldBusyError{cause: leaseBusyFixture(4123)}
		detail := sessionLeaseBusyDetail(wrapped)
		classifiedZombie := errors.Is(wrapped, errSessionBusyZombie)
		if classifiedZombie != strings.Contains(detail, "crashed") {
			t.Fatalf("alive=%v: zombie classification %v must match detail %q", alive, classifiedZombie, detail)
		}
	}
}
