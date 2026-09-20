package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestCreateCollabSessionToolBatchAndShape guards the schema contract behind
// tasks 162/166/167: the batch form is what makes one call create N sessions,
// the two shapes never mix silently, and the host receives normalised items.
func TestCreateCollabSessionToolBatchAndShape(t *testing.T) {
	var got CreateCollabSessionRequest
	create := func(req CreateCollabSessionRequest) (CreateCollabSessionResult, error) {
		got = req
		return CreateCollabSessionResult{
			Created: []CreateCollabSessionItemResult{{Title: "a", ContactID: "sc_a", TopicID: "topic_a"}},
			TopicID: "topic_a", ContactID: "sc_a",
		}, nil
	}
	tool := NewCreateCollabSessionTool(`C:\caller`, create)
	exec := func(args string) (string, error) {
		return tool.Execute(context.Background(), json.RawMessage(args))
	}

	// Batch form: one call, top-level group as the default, per-item override,
	// per-item model.
	out, err := exec(`{"group":"g1","sessions":[{"title":"a","purpose":"pa"},{"title":"b","purpose":"pb","group":"g2","model":"prov/chat"}]}`)
	if err != nil {
		t.Fatalf("batch call failed: %v", err)
	}
	if len(got.Sessions) != 2 {
		t.Fatalf("host received %d items, want 2", len(got.Sessions))
	}
	items := got.ItemList()
	if items[0].Group != "g1" || items[1].Group != "g2" {
		t.Fatalf("group inheritance wrong: %+v", items)
	}
	if items[1].Model != "prov/chat" || items[0].Model != "" {
		t.Fatalf("model inheritance wrong: %+v", items)
	}
	if !strings.Contains(out, "sc_a") {
		t.Fatalf("result must carry the contact id: %s", out)
	}

	// Both shapes at once is a refusal, not a guess.
	if _, err := exec(`{"title":"a","purpose":"p","group":"g","sessions":[{"title":"b","purpose":"p"}]}`); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("mixed shapes must be refused, got %v", err)
	}
	// Neither shape: refuse with an actionable message.
	if _, err := exec(`{"group":"g"}`); err == nil || !strings.Contains(err.Error(), "title and purpose are required") {
		t.Fatalf("empty call must be refused, got %v", err)
	}
	// Batch limits are explicit, never a silent truncation.
	big := `{"group":"g","sessions":[` + strings.TrimSuffix(strings.Repeat(`{"title":"t","purpose":"p"},`, MaxCreateCollabSessions+1), ",") + `]}`
	if _, err := exec(big); err == nil || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("over-limit batch must be refused, got %v", err)
	}
	// `delivery` only means something with a message, and only the two modes.
	if _, err := exec(`{"title":"a","purpose":"p","group":"g","delivery":"steer"}`); err == nil || !strings.Contains(err.Error(), "only meaningful with message") {
		t.Fatalf("delivery without message must be refused, got %v", err)
	}
	if _, err := exec(`{"title":"a","purpose":"p","group":"g","message":"hi","delivery":"interrupt"}`); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unknown delivery must be refused, got %v", err)
	}
	// First message + delivery reach the host verbatim.
	if _, err := exec(`{"title":"a","purpose":"p","group":"g","message":"start","delivery":"followup"}`); err != nil {
		t.Fatalf("single form with message failed: %v", err)
	}
	if got.Message != "start" || got.Delivery != "followup" {
		t.Fatalf("host request lost the first message: %+v", got)
	}
	if items := got.ItemList(); len(items) != 1 || items[0].Title != "a" {
		t.Fatalf("single form must normalise to one item: %+v", items)
	}
}

// TestCreateCollabSessionToolSchemaExposesEveryField keeps the schema and the
// host request in step: a field the model cannot pass is a field the host never
// sees, and the three tasks each added one.
func TestCreateCollabSessionToolSchemaExposesEveryField(t *testing.T) {
	tool := NewCreateCollabSessionTool("", func(CreateCollabSessionRequest) (CreateCollabSessionResult, error) {
		return CreateCollabSessionResult{}, nil
	})
	schema := string(tool.Schema())
	for _, want := range []string{`"sessions"`, `"model"`, `"message"`, `"delivery"`, `"group_id"`, `"project"`} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema is missing %s: %s", want, schema)
		}
	}
	desc := tool.Description()
	for _, want := range []string{fmt.Sprint(MaxCreateCollabSessions), "provider/model", "steer", "followup", "does NOT roll back"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description is missing %q: %s", want, desc)
		}
	}
	if tool.ReadOnly() {
		t.Fatal("create_collab_session writes sessions, so ReadOnly must stay false")
	}
}
