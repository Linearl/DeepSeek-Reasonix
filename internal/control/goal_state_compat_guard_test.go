package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// tl1317: an extra goal-state field whose value is not valid JSON used to be
// merged verbatim into the RawMessage map, failing the whole marshal and
// killing the sidecar save. The merge must drop the bad field and keep every
// valid one.
func TestMarshalGoalStateDropsInvalidExtraField(t *testing.T) {
	state := goalState{Goal: "g", Status: "running"}
	raw, err := marshalGoalState(state, map[string]json.RawMessage{
		"futurePolicy": json.RawMessage(`{"mode":"adaptive"}`),
		"broken":       json.RawMessage(`{{"inner":1}}`),
	})
	if err != nil {
		t.Fatalf("marshal with a damaged extra field: %v", err)
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(raw, &merged); err != nil {
		t.Fatalf("merged state is not valid JSON: %v", err)
	}
	if _, present := merged["broken"]; present {
		t.Fatal("invalid extra field was merged")
	}
	if string(merged["futurePolicy"]) != `{"mode":"adaptive"}` {
		t.Fatalf("valid extra field lost: %s", merged["futurePolicy"])
	}
	if string(merged["goal"]) != `"g"` {
		t.Fatalf("known field lost: %s", merged["goal"])
	}
}

// tl1317: the sidecar write must fail fast on bytes that are not valid JSON
// instead of persisting them.
func TestWriteGoalStateDataFailsFastOnInvalidJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "goal-state.json")
	if err := writeGoalStateData(path, []byte(`{{"inner":1}}`)); err == nil {
		t.Fatal("invalid JSON was written to the goal sidecar")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("invalid JSON reached the disk: %v", err)
	}
	if err := writeGoalStateData(path, []byte(`{"goal":"g"}`)); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != `{"goal":"g"}` {
		t.Fatalf("valid sidecar write = %s, %v", data, err)
	}
}
