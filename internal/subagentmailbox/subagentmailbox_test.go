package subagentmailbox

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestHub(t *testing.T) *Hub {
	t.Helper()
	return &Hub{Dir: t.TempDir()}
}

func TestValidRef(t *testing.T) {
	valid := []string{
		"sa_20261008_120000_000000000_ab12cd34",
		"sa_x",
	}
	invalid := []string{
		"",
		"  ",
		"sa_../escape",
		"sa_a/b",
		"sa_a\\b",
		"sa_a b",
		"other_20261008",
		"sa_" + strings.Repeat("x", 200),
	}
	for _, ref := range valid {
		if !ValidRef(ref) {
			t.Fatalf("ValidRef(%q) = false, want true", ref)
		}
	}
	for _, ref := range invalid {
		if ValidRef(ref) {
			t.Fatalf("ValidRef(%q) = true, want false", ref)
		}
	}
}

func TestAppendPersistsBeforeDelivery(t *testing.T) {
	hub := newTestHub(t)
	mb := hub.MailboxFor("sa_20261008_120000_000000000_deadbeef")
	if mb == nil {
		t.Fatal("MailboxFor returned nil for a valid ref")
	}
	entry, err := mb.Append(FromUser, "check tests", "Run the full suite before finishing.")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if entry.ID == "" || !strings.HasPrefix(entry.ID, "msg_") {
		t.Fatalf("Append assigned bad ID %q", entry.ID)
	}
	// The message must be durable on disk the moment Append returns —
	// before any consumer ever looks at it.
	data, err := os.ReadFile(filepath.Join(mb.Dir(), entry.ID+pendingSuffix))
	if err != nil {
		t.Fatalf("message file not durable after Append: %v", err)
	}
	if !strings.Contains(string(data), "Run the full suite before finishing.") {
		t.Fatalf("persisted body missing text: %s", data)
	}
	if got := mb.PendingCount(); got != 1 {
		t.Fatalf("PendingCount = %d, want 1", got)
	}
	if _, err := mb.Append(FromUser, "", "  "); err == nil {
		t.Fatal("Append with empty text succeeded, want error")
	}
}

func TestAppendRefusesInvalidRef(t *testing.T) {
	hub := newTestHub(t)
	if mb := hub.MailboxFor("sa_../escape"); mb != nil {
		t.Fatal("MailboxFor accepted a traversal-shaped ref")
	}
	if _, err := hub.Deliver("../escape", FromUser, "", "hi"); err == nil {
		t.Fatal("Deliver with a traversal ref succeeded, want error")
	}
}

func TestLoaderReadsThenMarksDelivered(t *testing.T) {
	hub := newTestHub(t)
	ref := "sa_20261008_120000_000000000_deadbeef"
	mb := hub.MailboxFor(ref)
	entry, err := mb.Append(FromParent, "", "guidance text")
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	load := mb.Loader(entry.ID)
	text, err := load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if text != "guidance text" {
		t.Fatalf("load text = %q", text)
	}
	if got := mb.PendingCount(); got != 0 {
		t.Fatalf("PendingCount after consume = %d, want 0", got)
	}
	if _, err := os.Stat(filepath.Join(mb.Dir(), entry.ID+deliveredSuffix)); err != nil {
		t.Fatalf("delivered copy missing: %v", err)
	}
	// A second consume of the same ID finds no pending file: read fails and
	// the file must not resurrect.
	if _, err := load(); err == nil {
		t.Fatal("second load of a delivered message succeeded, want error")
	}
}

func TestMarkDeliveredIdempotent(t *testing.T) {
	hub := newTestHub(t)
	mb := hub.MailboxFor("sa_20261008_120000_000000000_deadbeef")
	if err := mb.MarkDelivered("msg_missing"); err != nil {
		t.Fatalf("MarkDelivered of a missing message returned error: %v", err)
	}
}

func TestListSkipsDeliveredAndSortsOldestFirst(t *testing.T) {
	hub := newTestHub(t)
	mb := hub.MailboxFor("sa_20261008_120000_000000000_deadbeef")
	first, _ := mb.Append(FromUser, "", "first")
	second, _ := mb.Append(FromUser, "", "second")
	if err := mb.MarkDelivered(first.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	entries, err := mb.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != second.ID {
		t.Fatalf("List = %v, want only %q", entries, second.ID)
	}
}

func TestRegistryPublishLookupUnpublish(t *testing.T) {
	reg := NewRegistry()
	ref := "sa_20261008_120000_000000000_deadbeef"
	if _, ok := reg.Lookup(ref); ok {
		t.Fatal("Lookup found a handle before Publish")
	}
	accepted := false
	steer := func(itemID string, load func() (string, error)) bool { accepted = true; return true }
	unpublish := reg.Publish(ref, steer)
	if _, ok := reg.Lookup(ref); !ok {
		t.Fatal("Lookup missed a published handle")
	}
	unpublish()
	if _, ok := reg.Lookup(ref); ok {
		t.Fatal("Lookup found a handle after Unpublish")
	}
	if accepted {
		t.Fatal("steer called without Lookup")
	}
	// Empty refs and nil steer never register.
	if un := reg.Publish("", steer); un == nil {
		t.Fatal("Publish with empty ref returned nil closer")
	} else {
		un()
	}
	if un := reg.Publish(ref, nil); un == nil {
		t.Fatal("Publish with nil steer returned nil closer")
	} else {
		un()
	}
	if _, ok := reg.Lookup(ref); ok {
		t.Fatal("no-op publishes registered a handle")
	}
}

func TestRegistryRepublishKeepsLatestHandle(t *testing.T) {
	reg := NewRegistry()
	ref := "sa_20261008_120000_000000000_deadbeef"
	firstCalled, secondCalled := false, false
	first := func(itemID string, load func() (string, error)) bool { firstCalled = true; return true }
	second := func(itemID string, load func() (string, error)) bool { secondCalled = true; return true }
	unpublishFirst := reg.Publish(ref, first)
	reg.Publish(ref, second)
	unpublishFirst() // stale closer must not tear down the newer registration
	got, ok := reg.Lookup(ref)
	if !ok {
		t.Fatal("stale unpublish removed the newer handle")
	}
	got("x", nil)
	if !secondCalled || firstCalled {
		t.Fatalf("Lookup resolved the wrong handle (first=%v second=%v)", firstCalled, secondCalled)
	}
}

func TestRegistryConcurrentPublishUnpublish(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			un := reg.Publish("sa_20261008_120000_000000000_deadbeef", func(itemID string, load func() (string, error)) bool { return true })
			reg.Lookup("sa_20261008_120000_000000000_deadbeef")
			un()
		}()
	}
	wg.Wait()
	if _, ok := reg.Lookup("sa_20261008_120000_000000000_deadbeef"); ok {
		t.Fatal("handle survived every unpublish")
	}
}

func TestDeliverThreeStates(t *testing.T) {
	hub := newTestHub(t)
	ref := "sa_20261008_120000_000000000_deadbeef"

	// parked: no running handle.
	receipt, err := hub.Deliver(ref, FromUser, "summary", "parked body")
	if err != nil {
		t.Fatalf("Deliver (parked): %v", err)
	}
	if receipt.Disposition != DispositionParked {
		t.Fatalf("disposition = %q, want parked", receipt.Disposition)
	}
	if hub.PendingCount(ref) != 1 {
		t.Fatalf("parked message not pending")
	}

	// steered: live handle accepts; loader marks delivered at consume.
	consumed := ""
	steer := func(itemID string, load func() (string, error)) bool {
		text, err := load()
		if err != nil {
			t.Fatalf("consume load: %v", err)
		}
		consumed = text
		return true
	}
	un := GlobalRegistry.Publish(ref, steer)
	receipt, err = hub.Deliver(ref, FromUser, "", "steered body")
	if err != nil {
		t.Fatalf("Deliver (steered): %v", err)
	}
	if receipt.Disposition != DispositionSteered {
		t.Fatalf("disposition = %q, want steered", receipt.Disposition)
	}
	if consumed != "steered body" {
		t.Fatalf("consume text = %q", consumed)
	}
	if hub.PendingCount(ref) != 1 { // the parked message from before is still pending
		t.Fatalf("pending = %d, want 1", hub.PendingCount(ref))
	}
	un()

	// queued: handle live but admission refused (terminal race).
	un = GlobalRegistry.Publish(ref, func(itemID string, load func() (string, error)) bool { return false })
	receipt, err = hub.Deliver(ref, FromUser, "", "queued body")
	if err != nil {
		t.Fatalf("Deliver (queued): %v", err)
	}
	if receipt.Disposition != DispositionQueued {
		t.Fatalf("disposition = %q, want queued", receipt.Disposition)
	}
	un()
	if got := hub.PendingCount(ref); got != 2 {
		t.Fatalf("pending after queued = %d, want 2 (queued message must stay on disk)", got)
	}
}

func TestDeliverNilHubAndNilMailbox(t *testing.T) {
	var hub *Hub
	if mb := hub.MailboxFor("sa_20261008_120000_000000000_deadbeef"); mb != nil {
		t.Fatal("nil hub returned a mailbox")
	}
	if got := hub.PendingCount("sa_20261008_120000_000000000_deadbeef"); got != 0 {
		t.Fatalf("nil hub PendingCount = %d", got)
	}
	if err := hub.RemoveAllMailbox("sa_20261008_120000_000000000_deadbeef"); err != nil {
		t.Fatalf("nil hub RemoveAllMailbox: %v", err)
	}
	if got := hub.DrainForContinue("sa_20261008_120000_000000000_deadbeef"); got != "" {
		t.Fatalf("nil hub DrainForContinue = %q", got)
	}
	if receipt, err := hub.Deliver("sa_20261008_120000_000000000_deadbeef", FromUser, "", "x"); err == nil || receipt.Disposition != DispositionNotFound {
		t.Fatalf("nil hub Deliver = %+v, %v; want not_found + error", receipt, err)
	}
}

func TestDrainForContinueDeliversAndMarksDelivered(t *testing.T) {
	hub := newTestHub(t)
	ref := "sa_20261008_120000_000000000_deadbeef"
	if _, err := hub.Deliver(ref, FromUser, "late message", "Do X instead."); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	block := hub.DrainForContinue(ref)
	if !strings.Contains(block, "<pending-mail") || !strings.Contains(block, "Do X instead.") || !strings.Contains(block, "late message") {
		t.Fatalf("drain block missing parts:\n%s", block)
	}
	if got := hub.PendingCount(ref); got != 0 {
		t.Fatalf("pending after drain = %d, want 0", got)
	}
	// Second drain is empty: messages are delivered exactly once.
	if again := hub.DrainForContinue(ref); again != "" {
		t.Fatalf("second drain = %q, want empty", again)
	}
	// Fresh dispatch (empty ref) never drains.
	if got := hub.DrainForContinue(""); got != "" {
		t.Fatalf("drain with empty ref = %q", got)
	}
}

func TestRemoveAllMailbox(t *testing.T) {
	hub := newTestHub(t)
	ref := "sa_20261008_120000_000000000_deadbeef"
	if _, err := hub.Deliver(ref, FromUser, "", "body"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}
	if err := hub.RemoveAllMailbox(ref); err != nil {
		t.Fatalf("RemoveAllMailbox: %v", err)
	}
	if _, err := os.Stat(hub.MailboxFor(ref).Dir()); !os.IsNotExist(err) {
		t.Fatalf("mailbox dir still present after RemoveAll: %v", err)
	}
	if hub.PendingCount(ref) != 0 {
		t.Fatal("pending count nonzero after RemoveAll")
	}
}
