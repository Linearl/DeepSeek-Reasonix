package builtin

import (
	"encoding/json"
	"strings"
	"testing"
)

// tl1613 pins the optional `description` argument on the high-frequency tools
// (bash is the must-do first member; grep/glob/read_file/edit_file/
// write_file/multi_edit extend it per the task body). The UI renders this
// field as the collapsed card's human title while the raw command/pattern/
// diff stays in the expandable body, so every guidance string must carry the
// design intent: the user reads the description without seeing the raw input.
// The schemas are hand-written JSON strings — the parse step is the ratchet
// that keeps an escaping slip from shipping a broken contract.
func TestTl1613DescriptionFieldContract(t *testing.T) {
	cases := []struct {
		name     string
		schema   string
		contains string // distinctive per-tool guidance marker
	}{
		{
			name:     "bash",
			schema:   string(bash{}.Schema()),
			contains: "often without seeing the command",
		},
		{
			name:     "grep",
			schema:   string(grepTool{}.Schema()),
			contains: "often without seeing the pattern",
		},
		{
			name:     "glob",
			schema:   string(globTool{}.Schema()),
			contains: "often without seeing the pattern",
		},
		{
			name:     "read_file",
			schema:   string(readFile{}.Schema()),
			contains: "next to the path already shown",
		},
		{
			name:     "edit_file",
			schema:   string(editFile{}.Schema()),
			contains: "often without seeing the diff",
		},
		{
			name:     "write_file",
			schema:   string(writeFile{}.Schema()),
			contains: "often without seeing the body",
		},
		{
			name:     "multi_edit",
			schema:   string(multiEdit{}.Schema()),
			contains: "often without seeing the diff",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			}
			if err := json.Unmarshal([]byte(tc.schema), &doc); err != nil {
				t.Fatalf("schema is not valid JSON: %v\n%s", err, tc.schema)
			}
			raw, ok := doc.Properties["description"]
			if !ok {
				t.Fatalf("description property missing from schema:\n%s", tc.schema)
			}
			var field struct {
				Type        string `json:"type"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(raw, &field); err != nil {
				t.Fatalf("description property is not valid JSON: %v", err)
			}
			if field.Type != "string" {
				t.Fatalf("description type = %q, want string", field.Type)
			}
			if !strings.Contains(field.Description, tc.contains) {
				t.Fatalf("description guidance missing design intent %q:\n%s", tc.contains, field.Description)
			}
			for _, req := range doc.Required {
				if req == "description" {
					t.Fatalf("description must stay optional (never required), got required=%v", doc.Required)
				}
			}
		})
	}
}

// tl1613: the bash guidance must keep the zcode prompt's positive/negative
// examples and brevity budget — they are what make the field fill rates
// usable (short honest titles instead of echo'd command text).
func TestTl1613BashDescriptionGuidanceShape(t *testing.T) {
	var doc struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(bash{}.Schema(), &doc); err != nil {
		t.Fatalf("bash schema is not valid JSON: %v", err)
	}
	got := doc.Properties["description"].Description
	for _, marker := range []string{
		"active voice",
		"the user's language",
		"complex",
		"5-10 words",
		"List files in current directory",
		"Find and delete all .tmp files recursively",
	} {
		if !strings.Contains(got, marker) {
			t.Fatalf("bash description guidance missing %q:\n%s", marker, got)
		}
	}
	if !strings.Contains(got, "echo the command's text, its flags") {
		t.Fatalf("bash description guidance missing no-echo rule:\n%s", got)
	}
}
