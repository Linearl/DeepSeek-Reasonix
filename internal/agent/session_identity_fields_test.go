package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// Task 348 — session identity & duties structured fields.
//
// Acceptance being pinned here (tasklist 348 正文):
//  1. after `identity`/`duties` are passed, list_addressable_sessions shows them;
//  2. a purpose-only call is byte-for-byte what it was before (zero migration);
//  3. the type enum maps one-to-one onto the three-layer architecture roles —
//     exactly five values, a sixth is rejected at the write boundary.

// newIdentityFixture wires both tools against one temp session and returns
// the tool instances plus the session path and its directory config.
func newIdentityFixture(t *testing.T) (SessionCollabConfig, string) {
	t.Helper()
	dir := t.TempDir()
	self := filepath.Join(dir, "self.jsonl")
	writeEmpty(t, self)
	cfg := SessionCollabConfig{
		Enabled:            true,
		SessionDir:         dir,
		WorkspaceRoot:      dir,
		ResolveSessionPath: func() string { return self },
	}
	return cfg, self
}

// findOwnRow picks this fixture's row out of the directory page by its
// contact_id — the scan also walks the machine's real session dirs, so counts
// and positions are never ours to assert.
func findOwnRow(t *testing.T, listOut, contactID string) (raw string, found bool) {
	t.Helper()
	var payload struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(listOut), &payload); err != nil {
		t.Fatalf("directory page must be JSON: %v\n%s", err, listOut)
	}
	for _, row := range payload.Sessions {
		var id struct {
			ContactID string `json:"contactId"`
		}
		if err := json.Unmarshal(row, &id); err != nil {
			t.Fatalf("row must be JSON: %v", err)
		}
		if id.ContactID == contactID {
			return string(row), true
		}
	}
	return "", false
}

func TestSetSessionPurposeStructuredFieldsRoundTrip(t *testing.T) {
	cfg, self := newIdentityFixture(t)
	ctx := context.Background()
	purposeTool := NewSetSessionPurposeTool(cfg)
	listTool := NewListAddressableSessionsTool(cfg)

	// 1. purpose-only call first: the directory must be shaped exactly like the
	// pre-348 world — no structured keys anywhere in the payload.
	if _, err := purposeTool.Execute(ctx, json.RawMessage(`{"purpose":"coordination hub"}`)); err != nil {
		t.Fatalf("purpose-only call: %v", err)
	}
	meta, found, err := LoadBranchMeta(self)
	if err != nil || !found {
		t.Fatalf("branch meta after purpose-only call: found=%v err=%v", found, err)
	}
	out, err := listTool.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	row, ok := findOwnRow(t, out, meta.ContactID)
	if !ok {
		t.Fatalf("fixture session missing from directory: %s", out)
	}
	for _, key := range []string{"identityType", "identityDomain", "duties"} {
		if strings.Contains(row, `"`+key+`"`) {
			t.Fatalf("a purpose-only row must not carry %q (zero migration): %s", key, row)
		}
	}

	// 2. structured call: Chinese alias 主对话 must STORE as the canonical
	// main — aliases widen input, never the value set.
	if _, err := purposeTool.Execute(ctx, json.RawMessage(
		`{"purpose":"coordination hub","identity":{"type":"主对话","domain":"协作"},"duties":["派单","收货"]}`)); err != nil {
		t.Fatalf("structured call: %v", err)
	}
	meta, found, err = LoadBranchMeta(self)
	if err != nil || !found {
		t.Fatalf("branch meta after structured call: found=%v err=%v", found, err)
	}
	if meta.IdentityType != sessioncollab.IdentityMain {
		t.Fatalf("alias 主对话 must normalize to %q, got %q", sessioncollab.IdentityMain, meta.IdentityType)
	}
	if meta.IdentityDomain != "协作" {
		t.Fatalf("domain not stored: %q", meta.IdentityDomain)
	}
	if len(meta.Duties) != 2 || meta.Duties[0] != "派单" || meta.Duties[1] != "收货" {
		t.Fatalf("duties not stored: %#v", meta.Duties)
	}

	// 3. list_addressable_sessions carries the fields out.
	out, err = listTool.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	row, ok = findOwnRow(t, out, meta.ContactID)
	if !ok {
		t.Fatalf("fixture session missing from directory: %s", out)
	}
	var decoded struct {
		IdentityType   string   `json:"identityType"`
		IdentityDomain string   `json:"identityDomain"`
		Duties         []string `json:"duties"`
	}
	if err := json.Unmarshal([]byte(row), &decoded); err != nil {
		t.Fatalf("row: %v", err)
	}
	if decoded.IdentityType != sessioncollab.IdentityMain ||
		decoded.IdentityDomain != "协作" ||
		len(decoded.Duties) != 2 {
		t.Fatalf("structured fields did not ride out of the directory: %s", row)
	}

	// 4. presence-based update: a later purpose-only call must KEEP the
	// structured fields (old callers cannot erase what they cannot see).
	if _, err := purposeTool.Execute(ctx, json.RawMessage(`{"purpose":"still coordinating"}`)); err != nil {
		t.Fatal(err)
	}
	meta, _, _ = LoadBranchMeta(self)
	if meta.Purpose != "still coordinating" || meta.IdentityType != sessioncollab.IdentityMain ||
		meta.IdentityDomain != "协作" || len(meta.Duties) != 2 {
		t.Fatalf("purpose-only overwrite leaked into the structured fields: %+v", meta)
	}

	// 5. explicit [] clears the list; identity REPLACE drops the omitted block.
	if _, err := purposeTool.Execute(ctx, json.RawMessage(
		`{"purpose":"still coordinating","duties":[],"identity":{"domain":"调研"}}`)); err != nil {
		t.Fatal(err)
	}
	meta, _, _ = LoadBranchMeta(self)
	if len(meta.Duties) != 0 {
		t.Fatalf("explicit [] must clear duties: %#v", meta.Duties)
	}
	if meta.IdentityDomain != "调研" {
		t.Fatalf("identity replace must take the new domain: %q", meta.IdentityDomain)
	}
	if meta.IdentityType != "" {
		t.Fatalf("identity replace must replace the WHOLE block (type absent → cleared), got %q", meta.IdentityType)
	}
}

// The enum is a closed five-value set: anything outside it fails the call and
// writes nothing. This is the「无第七种私造值」half of the acceptance.
func TestSetSessionDutyRejectsUnknownIdentityType(t *testing.T) {
	cfg, self := newIdentityFixture(t)
	ctx := context.Background()
	purposeTool := NewSetSessionPurposeTool(cfg)

	if _, err := purposeTool.Execute(ctx, json.RawMessage(`{"purpose":"groundwork"}`)); err != nil {
		t.Fatal(err)
	}
	before, found, err := LoadBranchMeta(self)
	if err != nil || !found {
		t.Fatalf("meta: %v", err)
	}

	_, err = purposeTool.Execute(ctx, json.RawMessage(
		`{"purpose":"groundwork","identity":{"type":"vibes","domain":"x"}}`))
	if err == nil {
		t.Fatal("a sixth identity value must be rejected, not stored")
	}
	if !strings.Contains(err.Error(), "human|main|sub|heartbeat|system") {
		t.Fatalf("the rejection must teach the five allowed values, got: %v", err)
	}

	after, found, err := LoadBranchMeta(self)
	if err != nil || !found {
		t.Fatalf("meta after rejected call: %v", err)
	}
	if after.IdentityType != before.IdentityType || after.IdentityDomain != before.IdentityDomain ||
		after.Purpose != before.Purpose {
		t.Fatalf("a rejected identity must write nothing: before=%+v after=%+v", before, after)
	}
}

// Exactly five canonical values, and every accepted input spelling folds into
// one of them — aliases may not invent a sixth.
func TestIdentityTypeEnumIsClosedFive(t *testing.T) {
	types := sessioncollab.IdentityTypes()
	if len(types) != 5 {
		t.Fatalf("the three-layer architecture admits exactly 5 identity types, got %d: %v", len(types), types)
	}
	want := map[string]bool{
		"human": true, "main": true, "sub": true, "heartbeat": true, "system": true,
	}
	for _, typ := range types {
		if !want[typ] {
			t.Fatalf("unexpected canonical identity type %q", typ)
		}
		delete(want, typ)
	}
	if len(want) != 0 {
		t.Fatalf("canonical set missing: %v", want)
	}

	// Accepted inputs all fold into the canonical five.
	for _, input := range []string{"人", "human", "HUMAN", "主对话", "main", "子对话", "sub", "heartbeat", "系统", "system", "", "  main  "} {
		if _, err := sessioncollab.NormalizeIdentityType(input); err != nil {
			t.Fatalf("input %q must be accepted: %v", input, err)
		}
	}
	// And nothing else is.
	for _, input := range []string{"vibes", "assistant", "worker", "主对话1", "human2", "人机"} {
		if _, err := sessioncollab.NormalizeIdentityType(input); err == nil {
			t.Fatalf("input %q must be rejected — the enum is closed", input)
		}
	}
}
