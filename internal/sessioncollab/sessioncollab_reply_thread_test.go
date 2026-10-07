package sessioncollab

import (
	"context"
	"errors"
	"testing"
)

// threeStateStore seeds one mailbox per scenario: "peer" (任务 194 有效态),
// "self" (自引用，invalid) and "third" (串线).
func threeStateStore(t *testing.T) (*MailStore, MailMessage, MailMessage, MailMessage) {
	t.Helper()
	store := NewMailStore(t.TempDir())

	// The inbound message A is answering: sent by peer, sitting in A's mailbox.
	inbound, err := store.Deliver(context.Background(), MailMessage{To: "A", From: "peer", Body: "ping", Hop: 0})
	if err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	// The id of a message A itself sent: it lives in the peer's mailbox, not A's.
	ownOutbound, err := store.Deliver(context.Background(), MailMessage{To: "peer", From: "A", Body: "hello", Hop: 0})
	if err != nil {
		t.Fatalf("seed outbound: %v", err)
	}
	// An unrelated conversation with a third session, also in A's mailbox.
	thirdParty, err := store.Deliver(context.Background(), MailMessage{To: "A", From: "third", Body: "other thread", Hop: 0})
	if err != nil {
		t.Fatalf("seed third party: %v", err)
	}
	return store, inbound, ownOutbound, thirdParty
}

// TestResolveReplyParentValid: answering the message you received resolves to it.
func TestResolveReplyParentValid(t *testing.T) {
	store, inbound, _, _ := threeStateStore(t)
	parent, isReply, err := store.ResolveReplyParent(MailMessage{From: "A", To: "peer", ThreadID: inbound.ID})
	if err != nil {
		t.Fatalf("a reply to an inbound message must resolve: %v", err)
	}
	if !isReply {
		t.Fatal("a message with a thread_id is a reply")
	}
	if parent.ID != inbound.ID || parent.Hop != inbound.Hop {
		t.Fatalf("resolved parent = %+v, want id=%s hop=%d", parent, inbound.ID, inbound.Hop)
	}
}

// TestResolveReplyParentRejectsOwnOutboundId: the id a sender typed for its own
// outbound message is not in its mailbox, so the reply must fail at call time
// (task 194: this used to be queued, written to the peer, then dropped).
func TestResolveReplyParentRejectsOwnOutboundId(t *testing.T) {
	store, _, ownOutbound, _ := threeStateStore(t)
	_, isReply, err := store.ResolveReplyParent(MailMessage{From: "A", To: "peer", ThreadID: ownOutbound.ID})
	if !isReply {
		t.Fatal("the failure must be reported as a bad reply, not as a new chain")
	}
	if !errors.Is(err, ErrReplyThreadUnknown) {
		t.Fatalf("want ErrReplyThreadUnknown, got %v", err)
	}
	if err == nil {
		t.Fatal("expected an error")
	}
}

// TestResolveReplyParentRejectsCrossWiredThread: the thread exists in the sender's
// mailbox but was opened by someone else than the peer being answered (156.B).
func TestResolveReplyParentRejectsCrossWiredThread(t *testing.T) {
	store, _, _, thirdParty := threeStateStore(t)
	_, _, err := store.ResolveReplyParent(MailMessage{From: "A", To: "peer", ThreadID: thirdParty.ID})
	if !errors.Is(err, ErrReplyThreadCrossWired) {
		t.Fatalf("want ErrReplyThreadCrossWired, got %v", err)
	}
}

// TestResolveReplyParentNewChain: a message without a thread_id (or naming itself)
// is a new chain and must not be blocked.
func TestResolveReplyParentNewChain(t *testing.T) {
	store, inbound, _, _ := threeStateStore(t)
	// No thread_id at all: a new chain.
	if _, isReply, err := store.ResolveReplyParent(MailMessage{From: "A", To: "peer"}); err != nil || isReply {
		t.Fatalf("no thread_id must be a new chain (isReply=%v err=%v)", isReply, err)
	}
	// A thread_id that names nothing is an invalid reference, not a new chain: the
	// sender believes it is answering something, so it has to be told otherwise.
	if _, isReply, err := store.ResolveReplyParent(MailMessage{From: "A", To: "peer", ThreadID: "msg_missing"}); !isReply || !errors.Is(err, ErrReplyThreadUnknown) {
		t.Fatalf("unknown thread_id must be refused (isReply=%v err=%v)", isReply, err)
	}
	// A reply that names its own id is a new chain too (the id is assigned on write).
	_, isReply, err := store.ResolveReplyParent(MailMessage{ID: inbound.ID, From: "A", To: "peer", ThreadID: inbound.ID})
	if err != nil || isReply {
		t.Fatalf("self-named thread must be a new chain (isReply=%v err=%v)", isReply, err)
	}
}
