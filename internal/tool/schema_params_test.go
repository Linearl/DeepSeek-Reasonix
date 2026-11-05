package tool

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSchemaParamsDerivesRowsFromSchema(t *testing.T) {
	raw := json.RawMessage(`{
		"type":"object",
		"properties":{
			"scope":{"type":"string","enum":["","global","project"],"description":"Task scope."},
			"expected_revision":{"type":"integer","description":"CAS guard."},
			"enabled":{"type":"boolean"}
		},
		"required":["expected_revision"]
	}`)
	rows := SchemaParams(raw)
	want := []SchemaParam{
		{Name: "enabled", Type: "boolean", Required: false},
		{Name: "expected_revision", Type: "integer", Required: true, Description: "CAS guard."},
		{Name: "scope", Type: "string", Required: false, Enum: []any{"", "global", "project"}, Description: "Task scope."},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("SchemaParams = %+v, want %+v", rows, want)
	}
}

func TestSchemaParamsNilWithoutProperties(t *testing.T) {
	for name, raw := range map[string]json.RawMessage{
		"empty object":  json.RawMessage(`{}`),
		"type only":     json.RawMessage(`{"type":"object"}`),
		"no properties": json.RawMessage(`{"type":"object","required":["a"]}`),
		"invalid JSON":  json.RawMessage(`{"type":`),
		"array root":    json.RawMessage(`[]`),
	} {
		if rows := SchemaParams(raw); rows != nil {
			t.Fatalf("%s: SchemaParams = %+v, want nil", name, rows)
		}
	}
}

func TestSchemaParamsStableOrder(t *testing.T) {
	raw := json.RawMessage(`{"properties":{"zeta":{"type":"string"},"alpha":{"type":"string"},"mid":{"type":"string"}}}`)
	first := SchemaParams(raw)
	second := SchemaParams(raw)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated derivation differs: %+v vs %+v", first, second)
	}
	if len(first) != 3 || first[0].Name != "alpha" || first[2].Name != "zeta" {
		t.Fatalf("rows not name-ordered: %+v", first)
	}
}
