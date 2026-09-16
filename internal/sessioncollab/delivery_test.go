package sessioncollab

import "testing"

// Task 143: delivery defaults to followup (conservative) and rejects garbage
// instead of silently treating every value as followup.
func TestValidateDelivery(t *testing.T) {
	cases := []struct {
		in      string
		want    Delivery
		wantErr bool
	}{
		{"", DeliveryFollowup, false},
		{"followup", DeliveryFollowup, false},
		{"FOLLOWUP", DeliveryFollowup, false},
		{" steer ", DeliverySteer, false},
		{"steer", DeliverySteer, false},
		{"interrupt", "", true},
	}
	for _, c := range cases {
		got, err := ValidateDelivery(c.in)
		if c.wantErr {
			if err == nil {
				t.Fatalf("ValidateDelivery(%q) want error", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("ValidateDelivery(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

// Deliver must normalize the delivery field so the stored record is explicit
// rather than relying on every reader to default an empty value.
func TestDeliverNormalizesDelivery(t *testing.T) {
	mail := NewMailStore(t.TempDir())
	msg, err := mail.Deliver(MailMessage{To: "sc_a", Body: "x"})
	if err != nil || msg.Delivery != string(DeliveryFollowup) {
		t.Fatalf("empty delivery must normalize to followup: %+v %v", msg, err)
	}
	steer, err := mail.Deliver(MailMessage{To: "sc_a", Body: "y", Delivery: "steer"})
	if err != nil || steer.Delivery != string(DeliverySteer) {
		t.Fatalf("steer delivery must be preserved: %+v %v", steer, err)
	}
	if _, err := mail.Deliver(MailMessage{To: "sc_a", Body: "z", Delivery: "interrupt"}); err == nil {
		t.Fatal("unknown delivery must be rejected")
	}
}
