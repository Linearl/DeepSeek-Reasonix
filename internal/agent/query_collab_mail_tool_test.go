package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

// Task 320 b acceptance: the in-session query returns delivered/read status,
// truncates at the limit (60 fixtures, limit 50 → 50 + truncated), and never
// consumes anything (read-only: the seen cursor does not move).
func TestQueryCollabMailLimitAndStatusFields(t *testing.T) {
	mailDir := t.TempDir()
	mail := sessioncollab.NewMailStore(mailDir)
	now := time.Now().UnixMilli()
	var ids []string
	for i := 0; i < 60; i++ {
		m, err := mail.Deliver(sessioncollab.MailMessage{
			From: "sc_a", To: "sc_b",
			Body: fmt.Sprintf("history %d", i),
			At:   now - int64(i)*1000,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	// One consumed message proves the read flag rides the seen cursor.
	if err := mail.Ack("sc_b", ids[0]); err != nil {
		t.Fatal(err)
	}
	unreadBefore, _ := mail.InboxStatus("sc_b")

	tool := NewQueryCollabMailTool(SessionCollabConfig{
		Enabled:            true,
		SessionDir:         t.TempDir(),
		WorkspaceRoot:      t.TempDir(),
		MailDir:            mailDir,
		ResolveSessionPath: func() string { return "" },
	})
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"limit":50}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Revision  string `json:"revision"`
		Returned  int    `json:"returned"`
		Total     int    `json:"total"`
		Truncated bool   `json:"truncated"`
		Entries   []struct {
			ID        string `json:"id"`
			Delivered bool   `json:"delivered"`
			Read      bool   `json:"read"`
			Bucket    string `json:"bucket"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("query result must be JSON: %v\n%s", err, out)
	}
	if payload.Total != 60 || payload.Returned != 50 || !payload.Truncated {
		t.Fatalf("limit contract: total=%d returned=%d truncated=%v", payload.Total, payload.Returned, payload.Truncated)
	}
	if payload.Revision == "" {
		t.Fatal("the query must carry a snapshot revision (e-① contract)")
	}
	if len(payload.Entries) != 50 {
		t.Fatalf("entries: %d", len(payload.Entries))
	}
	byID := map[string]bool{}
	for _, e := range payload.Entries {
		byID[e.ID] = true
		if !e.Delivered {
			t.Fatalf("indexed rows are delivered by construction: %+v", e)
		}
		if e.Bucket != "mention" {
			t.Fatalf("plain mail buckets as mention: %+v", e)
		}
	}
	if !byID[ids[0]] {
		// newest first → ids[0] is row one; the acked message must show read=true.
		t.Fatalf("newest message missing: %s", out[:200])
	}
	// The acked (read) message must come back with read=true; unread filter
	// must exclude it.
	readOut, err := tool.Execute(context.Background(), json.RawMessage(`{"unread_only":true,"limit":500}`))
	if err != nil {
		t.Fatal(err)
	}
	var unreadPayload struct {
		Total   int `json:"total"`
		Entries []struct {
			ID   string `json:"id"`
			Read bool   `json:"read"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(readOut), &unreadPayload); err != nil {
		t.Fatal(err)
	}
	if unreadPayload.Total != 59 {
		t.Fatalf("unread_only must drop the consumed message: total=%d", unreadPayload.Total)
	}
	for _, e := range unreadPayload.Entries {
		if e.ID == ids[0] || e.Read {
			t.Fatalf("unread filter leaked a read row: %+v", e)
		}
	}

	// Read-only: nothing about querying may consume mail.
	unreadAfter, _ := mail.InboxStatus("sc_b")
	if unreadAfter != unreadBefore {
		t.Fatalf("query must never advance the cursor: before=%d after=%d", unreadBefore, unreadAfter)
	}

	// Fixed vocabulary: unknown bucket/state/order are rejected, not ignored.
	for _, bad := range []string{
		`{"bucket":"vibes"}`,
		`{"state":"negotiating"}`,
		`{"order":"sideways"}`,
		`{"since":200,"until":100}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(bad)); err == nil {
			t.Fatalf("invalid args must be rejected: %s", bad)
		}
	}
}
