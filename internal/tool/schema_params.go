package tool

import (
	"encoding/json"
	"sort"
)

// SchemaParam is one per-parameter row derived from a tool's registered JSON
// Schema (tl1438). It exists so inspect consumers (model and desktop) can
// render a structured parameter table without re-parsing the raw schema —
// the schema itself stays the single source of truth; this is a pure
// derivation, never a second hand-written definition.
type SchemaParam struct {
	Name        string   `json:"name"`
	Type        string   `json:"type,omitempty"`
	Required    bool     `json:"required"`
	Enum        []any    `json:"enum,omitempty"`
	Description string   `json:"description,omitempty"`
}

// SchemaParams derives per-parameter rows (name/type/required/enum/description)
// from the tool's registered JSON Schema. Rows are ordered by parameter name so
// repeated derivations of one schema are byte-stable. It returns nil when the
// schema declares no properties or is not a parsable object — callers must
// surface that as "no parameter schema available" rather than inventing
// placeholder data (tl1438: no fabricated schemas).
//
// Only top-level "type" and "enum" are echoed. Conditional shapes (anyOf /
// oneOf / patternProperties) are deliberately not flattened: echoing what the
// engine definition actually declares is the contract; guessing would fork the
// schema semantics that argument validation enforces.
func SchemaParams(raw json.RawMessage) []SchemaParam {
	var doc struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Enum        []any  `json:"enum"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Properties) == 0 {
		return nil
	}
	required := make(map[string]bool, len(doc.Required))
	for _, name := range doc.Required {
		required[name] = true
	}
	names := make([]string, 0, len(doc.Properties))
	for name := range doc.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]SchemaParam, 0, len(names))
	for _, name := range names {
		property := doc.Properties[name]
		rows = append(rows, SchemaParam{
			Name:        name,
			Type:        property.Type,
			Required:    required[name],
			Enum:        property.Enum,
			Description: property.Description,
		})
	}
	return rows
}
