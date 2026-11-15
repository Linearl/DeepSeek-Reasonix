package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// tl1613 pins the task-family description guidance: the label is the user's
// only window onto a dispatch they cannot see the prompt of, so the describe
// must keep the "user reads this label without seeing the prompt" intent and
// stay optional (soft guidance, never a required field).
func TestTl1613TaskDescriptionGuidance(t *testing.T) {
	cases := []struct {
		name   string
		schema string
	}{
		{name: "task", schema: string((&TaskTool{}).Schema())},
		{name: "task read-only", schema: string((&ReadOnlyTaskTool{}).Schema())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var doc struct {
				Properties map[string]struct {
					Description string `json:"description"`
				} `json:"properties"`
				Required []string `json:"required"`
			}
			if err := json.Unmarshal([]byte(tc.schema), &doc); err != nil {
				t.Fatalf("%s schema is not valid JSON: %v", tc.name, err)
			}
			got, ok := doc.Properties["description"]
			if !ok {
				t.Fatalf("%s description property missing", tc.name)
			}
			if !strings.Contains(got.Description, "without seeing the prompt") {
				t.Fatalf("%s description guidance missing design intent:\n%s", tc.name, got.Description)
			}
			for _, req := range doc.Required {
				if req == "description" {
					t.Fatalf("%s description must stay optional, got required=%v", tc.name, doc.Required)
				}
			}
		})
	}
}
