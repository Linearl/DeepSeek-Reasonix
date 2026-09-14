package provider

import (
	"encoding/json"
	"testing"
)

func TestToolCallRecordPersistsStableIdentityAndRecoveryStates(t *testing.T) {
	record := ToolCallRecord{
		Identity: ActionIdentity{SessionID: "s", TurnID: "t", AttemptID: "a", CallID: "c", CanonicalTool: "write_file", ArgumentDigest: "sha", ResourceScope: "workspace:/tmp/x"},
		State:    ToolRunStarted, ReadOnly: false, IdempotencyKey: "idem", StartedAt: 10,
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	var got ToolCallRecord
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.Identity != record.Identity || got.State != ToolRunStarted || got.IdempotencyKey != "idem" {
		t.Fatalf("record round trip = %+v", got)
	}
}
