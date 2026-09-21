package sessioncollab

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestClampHopLimitBounds: a configured ceiling is normalized, never trusted, so a
// hand-edited config always yields a usable value (task 204).
func TestClampHopLimitBounds(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, MaxHop}, {-5, MaxHop}, {1, MinHop}, {2, MinHop}, {3, 3}, {5, 5},
		{999, 999}, {1000, 1000}, {1001, MaxHopCeiling}, {99999, MaxHopCeiling},
	}
	for _, c := range cases {
		if got := ClampHopLimit(c.in); got != c.want {
			t.Errorf("ClampHopLimit(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TestDeliverHonorsConfiguredHopLimit: the default store still refuses the 6th hop with
// the historical message, and a configured ceiling moves both the boundary and the
// number reported in the error.
func TestDeliverHonorsConfiguredHopLimit(t *testing.T) {
	dir := t.TempDir()

	def := NewMailStore(filepath.Join(dir, "default"))
	if _, err := def.Deliver(MailMessage{To: "c1", Body: "x", Hop: MaxHop}); err != nil {
		t.Fatalf("hop %d must be accepted by the default store: %v", MaxHop, err)
	}
	_, err := def.Deliver(MailMessage{To: "c1", Body: "x", Hop: MaxHop + 1})
	if !errors.Is(err, ErrHopLimit) {
		t.Fatalf("hop %d must be refused, got %v", MaxHop+1, err)
	}
	if !strings.Contains(err.Error(), "(max 5)") {
		t.Fatalf("the default refusal must name the default ceiling: %v", err)
	}

	widened := NewMailStoreWithHopLimit(filepath.Join(dir, "wide"), 8)
	if _, err := widened.Deliver(MailMessage{To: "c2", Body: "x", Hop: 8}); err != nil {
		t.Fatalf("hop 8 must be accepted at ceiling 8: %v", err)
	}
	_, err = widened.Deliver(MailMessage{To: "c2", Body: "x", Hop: 9})
	if !errors.Is(err, ErrHopLimit) || !strings.Contains(err.Error(), "(max 8)") {
		t.Fatalf("ceiling 8 must refuse hop 9 and say so: %v", err)
	}

	narrowed := NewMailStoreWithHopLimit(filepath.Join(dir, "narrow"), 3)
	if _, err := narrowed.Deliver(MailMessage{To: "c3", Body: "x", Hop: 3}); err != nil {
		t.Fatalf("hop 3 must be accepted at ceiling 3: %v", err)
	}
	_, err = narrowed.Deliver(MailMessage{To: "c3", Body: "x", Hop: 4})
	if !errors.Is(err, ErrHopLimit) || !strings.Contains(err.Error(), "(max 3)") {
		t.Fatalf("ceiling 3 must refuse hop 4 and say so: %v", err)
	}
}

// TestClaimHonorsConfiguredHopLimit: the read path enforces the ceiling in force for
// messages written earlier, so lowering the limit affects newly arriving mail only.
func TestClaimHonorsConfiguredHopLimit(t *testing.T) {
	dir := t.TempDir()
	wide := NewMailStoreWithHopLimit(dir, 10)
	if _, err := wide.Deliver(MailMessage{To: "c9", Body: "x", Hop: 9}); err != nil {
		t.Fatalf("wide deliver: %v", err)
	}
	pending, refused, err := NewMailStore(dir).Claim("c9")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(pending) != 0 || len(refused) != 1 {
		t.Fatalf("expected the over-limit message to be refused, got pending=%d refused=%d", len(pending), len(refused))
	}
}
