package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// fakeHeartbeatManager records the last mutating call and replays canned
// results, so tool-layer tests exercise only decoding, seam wiring, and
// error pass-through.
type fakeHeartbeatManager struct {
	list     HeartbeatListView
	upsert   HeartbeatUpsertRequest
	upsertRe HeartbeatUpsertResult
	upsertEr error
	setReq   HeartbeatSetEnabledRequest
	setRe    HeartbeatSetEnabledResult
	setEr    error
}

func (f *fakeHeartbeatManager) ListTasks() (HeartbeatListView, error) { return f.list, nil }

func (f *fakeHeartbeatManager) UpsertTask(req HeartbeatUpsertRequest) (HeartbeatUpsertResult, error) {
	f.upsert = req
	return f.upsertRe, f.upsertEr
}

func (f *fakeHeartbeatManager) SetEnabled(req HeartbeatSetEnabledRequest) (HeartbeatSetEnabledResult, error) {
	f.setReq = req
	return f.setRe, f.setEr
}

func withFakeManager(t *testing.T) *fakeHeartbeatManager {
	t.Helper()
	fake := &fakeHeartbeatManager{}
	SetHeartbeatManager(fake)
	t.Cleanup(func() { SetHeartbeatManager(nil) })
	return fake
}

func TestHeartbeatToolsFailWithoutEngine(t *testing.T) {
	SetHeartbeatManager(nil)
	ctx := context.Background()
	for _, tool := range []struct {
		name string
		exec func(context.Context, json.RawMessage) (string, error)
	}{
		{"heartbeat_task_list", heartbeatTaskList{}.Execute},
		{"heartbeat_task_upsert", heartbeatTaskUpsert{}.Execute},
		{"heartbeat_task_enable", heartbeatTaskEnable{}.Execute},
	} {
		out, err := tool.exec(ctx, json.RawMessage(`{}`))
		if err == nil {
			t.Fatalf("%s must fail without an injected engine", tool.name)
		}
		if !strings.Contains(err.Error(), "desktop") {
			t.Fatalf("%s error should explain the missing desktop engine, got: %v", tool.name, err)
		}
		if out != "" {
			t.Fatalf("%s must not return output on failure", tool.name)
		}
	}
}

func TestHeartbeatUpsertRejectsUnknownFields(t *testing.T) {
	withFakeManager(t)
	_, err := heartbeatTaskUpsert{}.Execute(context.Background(), json.RawMessage(`{"expected_revision":1,"intervalx":"30m"}`))
	if err == nil || !strings.Contains(err.Error(), "intervalx") || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field must be named explicitly, got %v", err)
	}
}

func TestHeartbeatUpsertRejectsEngineOwnedFields(t *testing.T) {
	withFakeManager(t)
	_, err := heartbeatTaskUpsert{}.Execute(context.Background(), json.RawMessage(`{"expected_revision":1,"topicId":"t1"}`))
	if err == nil || !strings.Contains(err.Error(), "topicId") || !strings.Contains(err.Error(), "engine-owned") {
		t.Fatalf("engine-owned field must be rejected by name, got %v", err)
	}
}

func TestHeartbeatUpsertRequiresExpectedRevision(t *testing.T) {
	withFakeManager(t)
	_, err := heartbeatTaskUpsert{}.Execute(context.Background(), json.RawMessage(`{"title":"t"}`))
	if err == nil || !strings.Contains(err.Error(), "expected_revision is required") {
		t.Fatalf("missing expected_revision must be a hard error, got %v", err)
	}
}

func TestHeartbeatUpsertDecodesPatchAndProvided(t *testing.T) {
	fake := withFakeManager(t)
	fake.upsertRe = HeartbeatUpsertResult{TaskID: "abc", Created: true, Revision: 7, IntervalKind: "duration", ParsedInterval: "30m0s"}
	out, err := heartbeatTaskUpsert{}.Execute(context.Background(), json.RawMessage(`{
		"expected_revision": 3,
		"id": "abc",
		"title": "nightly",
		"enabled": false,
		"notifyChannels": true,
		"workspaceRoot": ""
	}`))
	if err != nil {
		t.Fatal(err)
	}
	req := fake.upsert
	if req.ExpectedRevision != 3 {
		t.Fatalf("expected_revision not forwarded: %+v", req)
	}
	patch := req.Patch
	if patch.ID != "abc" || patch.Title != "nightly" {
		t.Fatalf("string fields not forwarded: %+v", patch)
	}
	if patch.Enabled == nil || *patch.Enabled {
		t.Fatalf("enabled=false not decoded: %+v", patch.Enabled)
	}
	if patch.NotifyChannels == nil || !*patch.NotifyChannels {
		t.Fatalf("notifyChannels=true not decoded: %+v", patch.NotifyChannels)
	}
	if !patch.Provided["workspaceRoot"] {
		t.Fatalf("explicit empty string must count as provided (clearing): %v", patch.Provided)
	}
	if patch.Provided["prompt"] {
		t.Fatalf("absent key must not be provided: %v", patch.Provided)
	}
	var result HeartbeatUpsertResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.TaskID != "abc" || !result.Created || result.Revision != 7 {
		t.Fatalf("receipt not echoed: %s", out)
	}
}

func TestHeartbeatUpsertRejectsWrongArgTypes(t *testing.T) {
	withFakeManager(t)
	_, err1 := heartbeatTaskUpsert{}.Execute(context.Background(), json.RawMessage(`{"expected_revision":1,"enabled":"yes"}`))
	if err1 == nil || !strings.Contains(err1.Error(), "enabled") {
		t.Fatalf("non-boolean enabled must be rejected with the field named, got %v", err1)
	}
	_, err2 := heartbeatTaskUpsert{}.Execute(context.Background(), json.RawMessage(`{"expected_revision":"one"}`))
	if err2 == nil || !strings.Contains(err2.Error(), "expected_revision") {
		t.Fatalf("non-integer expected_revision must be rejected, got %v", err2)
	}
}

func TestHeartbeatEnableDecodesAndForwards(t *testing.T) {
	fake := withFakeManager(t)
	fake.setRe = HeartbeatSetEnabledResult{TaskID: "t1", Enabled: false, Revision: 9}
	out, err := heartbeatTaskEnable{}.Execute(context.Background(), json.RawMessage(`{"id":"t1","enabled":false,"expected_revision":8}`))
	if err != nil {
		t.Fatal(err)
	}
	if fake.setReq.ID != "t1" || fake.setReq.Enabled || fake.setReq.ExpectedRevision != 8 {
		t.Fatalf("set-enabled request not forwarded: %+v", fake.setReq)
	}
	var result HeartbeatSetEnabledResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.TaskID != "t1" || result.Enabled || result.Revision != 9 {
		t.Fatalf("receipt not echoed: %s", out)
	}
}

func TestHeartbeatEnableRequiresIDAndEnabled(t *testing.T) {
	withFakeManager(t)
	_, err1 := heartbeatTaskEnable{}.Execute(context.Background(), json.RawMessage(`{"enabled":true,"expected_revision":1}`))
	if err1 == nil || !strings.Contains(err1.Error(), "id is required") {
		t.Fatalf("missing id must be rejected, got %v", err1)
	}
	_, err2 := heartbeatTaskEnable{}.Execute(context.Background(), json.RawMessage(`{"id":"t1","expected_revision":1}`))
	if err2 == nil || !strings.Contains(err2.Error(), "enabled is required") {
		t.Fatalf("missing enabled must be rejected, got %v", err2)
	}
}

func TestHeartbeatManagerErrorsPassThrough(t *testing.T) {
	fake := withFakeManager(t)
	fake.upsertEr = errors.New("heartbeat config changed concurrently (expected revision 3 stale; current revision is 4)")
	_, err := heartbeatTaskUpsert{}.Execute(context.Background(), json.RawMessage(`{"expected_revision":3,"title":"t"}`))
	if err == nil || !strings.Contains(err.Error(), "current revision is 4") {
		t.Fatalf("manager conflict error must pass through verbatim, got %v", err)
	}
}
