package sessioncollab

import "testing"

// Task 309: the mailbox default channel is steer — immediate injection with
// automatic follow-up degradation — while an explicit followup keeps the old
// queued semantics and unknown values are still refused.
func TestValidateDeliveryDefaultsSteer(t *testing.T) {
	cases := []struct {
		in   string
		want Delivery
	}{
		{"", DeliverySteer},
		{"steer", DeliverySteer},
		{"STEER", DeliverySteer},
		{"followup", DeliveryFollowup},
		{" followup ", DeliveryFollowup},
	}
	for _, tc := range cases {
		got, err := ValidateDelivery(tc.in)
		if err != nil {
			t.Fatalf("ValidateDelivery(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ValidateDelivery(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	if _, err := ValidateDelivery("bogus"); err == nil {
		t.Fatal("ValidateDelivery(bogus) accepted an unknown channel")
	}
}

// Task 309: read receipts are best-effort — a missing mailbox or missing
// addresses degrades to a no-op instead of failing the consume path.
func TestSendReadReceiptBestEffortNoop(t *testing.T) {
	if err := SendReadReceipt("", "sc_sender", "sc_recipient", "msg_1"); err != nil {
		t.Fatalf("empty mailDir must no-op, got %v", err)
	}
	if err := SendReadReceipt(t.TempDir(), "", "sc_recipient", "msg_1"); err != nil {
		t.Fatalf("empty sender must no-op, got %v", err)
	}
	if err := SendReadReceipt(t.TempDir(), "sc_sender", "", "msg_1"); err != nil {
		t.Fatalf("empty recipient must no-op, got %v", err)
	}
}
