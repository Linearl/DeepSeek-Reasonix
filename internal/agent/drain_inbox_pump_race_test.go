package agent

import (
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"reasonix/internal/sessioncollab"
)

// M1: the 167×235 interop must survive a concurrent Drain consumer.
// Pins: 20 messages + concurrent agent drain + peer drain = exactly 20.
// Block2 M-a note: the host pump's real path (runCollabDelivery, two-phase
// Claim→Ack) never coexists with drain_inbox — registration is mutually
// exclusive with the experimental_collab_background_delivery switch (see
// internal/boot/collab_drain_gate.go). The pump side is therefore modelled as
// a second Drain consumer, which pins the same-lock Claim+Ack exactly-once
// contract this test exists for.
func TestDrainInboxInteropWithHostPumpRace(t *testing.T) {
	dir := t.TempDir()
	mailDir := filepath.Join(t.TempDir(), "mail")
	mePath := drainFixture(t, dir, "me", "Me", "sc_me")

	cfg := SessionCollabConfig{
		Enabled: true, SessionDir: dir, WorkspaceRoot: dir,
		MailDir: mailDir, CurrentSessionPath: mePath, CurrentContactID: "sc_me",
	}
	mail := sessioncollab.NewMailStore(mailDir)
	for i := 0; i < 20; i++ {
		mail.Deliver(sessioncollab.MailMessage{From: "sc_creator", To: "sc_me", Body: "msg", Delivery: "steer", ReplyTo: "sc_creator"})
	}

	var mu sync.Mutex
	agentCount, pumpCount := 0, 0
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := NewDrainInboxTool(cfg).Execute(nil, []byte(`{}`))
			if err != nil {
				t.Error(err)
				return
			}
			var p drainPayload
			if err := json.Unmarshal([]byte(out), &p); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			agentCount += p.Took
			mu.Unlock()
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mail.Drain("sc_me", func(pending, _ []sessioncollab.MailMessage) []string {
				mu.Lock()
				pumpCount += len(pending)
				mu.Unlock()
				ids := make([]string, 0, len(pending))
				for _, m := range pending {
					ids = append(ids, m.ID)
				}
				return ids
			})
		}()
	}

	wg.Wait()
	total := agentCount + pumpCount
	if total != 20 {
		t.Fatalf("agent(%d)+pump(%d)=%d, want exactly 20", agentCount, pumpCount, total)
	}
	if unread, _ := mail.InboxStatus("sc_me"); unread != 0 {
		t.Fatalf("unreadAfter %d, want 0", unread)
	}
}
