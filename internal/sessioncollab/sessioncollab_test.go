package sessioncollab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCardCreateUpdateList(t *testing.T) {
	root := t.TempDir()
	store := NewCardStore(root)
	c, err := store.Create(Card{Title: "ship collab base", Body: "141+145"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Status != StatusPending {
		t.Fatalf("create: %+v", c)
	}
	c2, err := store.Update(c.ID, func(card *Card) error {
		card.Status = StatusRunning
		card.Assignee = "sc_expert"
		card.Nodes = append(card.Nodes, CardNode{ContactID: "sc_expert", Role: "expert", At: 1})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if c2.Status != StatusRunning || len(c2.Nodes) != 1 {
		t.Fatalf("update: %+v", c2)
	}
	list, err := store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	// Atomic overwrite: concurrent create of second card must not clobber first.
	if _, err := store.Create(Card{Title: "second"}); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(c.ID)
	if err != nil || got.Title != "ship collab base" {
		t.Fatalf("get after second create: %v %v", got, err)
	}
}

func TestMailDeliverHopAndInbox(t *testing.T) {
	root := t.TempDir()
	mail := NewMailStore(root)
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "hello", Hop: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "too deep", Hop: MaxHop + 1}); err == nil {
		t.Fatal("expected hop limit error")
	}
	box, err := mail.Inbox("sc_a")
	if err != nil || len(box) != 1 || box[0].Delivery != "followup" {
		t.Fatalf("inbox: %v %v", box, err)
	}
	if _, err := mail.Deliver(MailMessage{To: "", Body: "x"}); err == nil {
		t.Fatal("empty target must fail")
	}
}

func TestResolveContact(t *testing.T) {
	ids := []Identity{{ContactID: "sc_x", Purpose: "expert"}, {ContactID: "sc_y"}}
	id, ok := ResolveContact(ids, "sc_x")
	if !ok || id.Purpose != "expert" {
		t.Fatalf("resolve: %+v %v", id, ok)
	}
	if _, ok := ResolveContact(ids, "nope"); ok {
		t.Fatal("missing contact should not resolve")
	}
}

func TestScanDirSkipsNonJsonl(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Sessions without contact_id are skipped by the loader contract.
	got := ScanDir(dir, func(string) (string, string, string, string, bool) {
		return "", "", "", "", false
	})
	if len(got) != 0 {
		t.Fatalf("expected empty scan, got %v", got)
	}
}
