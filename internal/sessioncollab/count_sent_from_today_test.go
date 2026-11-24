package sessioncollab

import (
	"testing"
	"time"
)

// utcPlusEight is a fixed UTC+8 zone so every calendar-boundary case below is
// deterministic no matter which timezone the host or CI runs in (tl1784).
var utcPlusEight = time.FixedZone("UTC+8", 8*60*60)

// 东八区 00:30 must already belong to the new local day: the cap window opens
// at local midnight, not at Truncate(24h)'s UTC cut (which lands on 08:00 in
// +8 and left the small hours charged to yesterday).
func TestLocalDayStartUTCPlusEightSmallHours(t *testing.T) {
	now := time.Date(2026, 11, 23, 0, 30, 0, 0, utcPlusEight)
	start := localDayStart(now)
	want := time.Date(2026, 11, 23, 0, 0, 0, 0, utcPlusEight)
	if !start.Equal(want) {
		t.Fatalf("local day start at 00:30 +8 = %v, want %v", start, want)
	}
	if got := start.In(utcPlusEight).Format("2006-01-02 15:04"); got != "2026-11-23 00:00" {
		t.Fatalf("local day start wall clock = %s +8, want 2026-11-23 00:00", got)
	}
	// The regression this pins: Truncate(24h) cuts UTC days, i.e. 08:00 local —
	// if this sanity assert ever fails, Go's Truncate semantics changed.
	utcCut := now.Truncate(24 * time.Hour).In(utcPlusEight).Format("2006-01-02 15:04")
	if utcCut != "2026-11-22 08:00" {
		t.Fatalf("sanity: Truncate(24h) boundary = %s +8, want 2026-11-22 08:00", utcCut)
	}
}

// A send at 00:20 +8 counts into the new day while last night's 23:50 does
// not — the exact window CountSentFromToday hands the anti-storm cap.
func TestCountSentSinceUTCPlusEightMidnightRollover(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	cst := func(day, hh, mm int) int64 {
		return time.Date(2026, 11, day, hh, mm, 0, 0, utcPlusEight).UnixMilli()
	}
	seed := []MailMessage{
		{ID: "old", From: "sc_sender", To: "sc_peer", Body: "yesterday late", At: cst(22, 23, 50)},
		{ID: "new", From: "sc_sender", To: "sc_peer", Body: "today small hours", At: cst(23, 0, 20)},
		{ID: "other", From: "sc_other", To: "sc_peer", Body: "someone else", At: cst(23, 0, 25)},
	}
	for _, m := range seed {
		if err := appendJSONL(mail.inboxPath(m.To), m); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 23, 0, 30, 0, 0, utcPlusEight)
	if got := mail.countSentSince("sc_sender", localDayStart(now)); got != 1 {
		t.Fatalf("count at 00:30 +8 = %d, want 1 (only today's small-hours send)", got)
	}
	// Regression witness: the pre-fix UTC-cut window (= local 08:00 the day
	// before) charged both of the sender's messages to "today", which is how
	// the daily cap drifted around the boundary for 8 hours a day.
	if got := mail.countSentSince("sc_sender", now.Truncate(24*time.Hour)); got != 2 {
		t.Fatalf("count under UTC-cut window = %d, want 2 (regression witness)", got)
	}
}
