package sessioncollab

import (
	"sync"
	"testing"
)

// B1 acceptance: concurrent Drain calls must never double-consume. 20 messages
// drained by 10 concurrent consumers must yield exactly 20 settled, not 40.
func TestConcurrentDrainNoDoubleConsumption(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	for i := 0; i < 20; i++ {
		if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "msg"}); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	total := 0
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := mail.Drain("sc_a", func(pending, _ []MailMessage) []string {
				mu.Lock()
				total += len(pending)
				mu.Unlock()
				ids := make([]string, 0, len(pending))
				for _, m := range pending {
					ids = append(ids, m.ID)
				}
				return ids
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if total != 20 {
		t.Fatalf("concurrent drain settled %d messages, want exactly 20 (double-consumption)", total)
	}
	unread, _ := mail.InboxStatus("sc_a")
	if unread != 0 {
		t.Fatalf("unreadAfter %d, want 0", unread)
	}
}
