package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"

	"reasonix/internal/sessioncollab"
)

// M1: the 167×235 interop must survive a concurrent Drain consumer.
// Pins: 20 messages + concurrent agent drain + peer drain = exactly 20.
// Block2 M-a note: the host pump's real path (runCollabDelivery, two-phase
// Claim→Ack) never coexists with drain_inbox — the exclusion is enforced by
// construction: ONE boot snapshot decides BOTH the registration
// (internal/boot/collab_drain_gate.go publish site) and the pump skip
// (desktop collabBackgroundDelivery read site, minor-1 audit-2; unreachability
// proof pinned in desktop/session_collab_gate_test.go). This test therefore
// models the pump side as a second Drain consumer — NOT as an exemption, but
// to pin the same-lock Claim+Ack exactly-once contract the store itself must
// hold for whichever single consumer is active.
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
		mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_creator", To: "sc_me", Body: "msg", Delivery: "steer", ReplyTo: "sc_creator"})
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
			mail.Drain(context.Background(), "sc_me", func(pending, _ []sessioncollab.MailMessage) []string {
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
