package boot

import (
	"strings"
	"testing"

	"reasonix/internal/extension"
)

// Task 623 regression guard: doctor text must keep a separator between boolean
// field names and their values ("clean=true", never "cleantrue"). A settings
// walkthrough suspected glued forms on the diagnostics page; the current
// renderer is correct, and these tests pin the format so a future edit cannot
// silently drop the separators.
func TestRenderRuntimeDoctorTextBooleanSeparators(t *testing.T) {
	t.Parallel()
	report := RuntimeDoctorReport{
		Recoverability: extension.Recoverability{Clean: true, HasIrreversible: false},
		Resume:         extension.ResumeDecision{AllowResume: true, CleanRollback: true},
	}
	text := RenderRuntimeDoctorText(report)
	for _, want := range []string{
		"recoverability: clean=true irreversible=false",
		"resume: allow=true cleanRollback=true",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor text missing separated boolean form %q:\n%s", want, text)
		}
	}
	for _, glued := range []string{"cleantrue", "irreversiblefalse", "allowtrue", "cleanrollbacktrue"} {
		if strings.Contains(text, glued) {
			t.Fatalf("doctor text renders glued boolean %q (separator lost):\n%s", glued, text)
		}
	}
}

// The false branch must keep the same "key=value" shape.
func TestRenderRuntimeDoctorTextBooleanSeparatorsFalseBranch(t *testing.T) {
	t.Parallel()
	report := RuntimeDoctorReport{
		Recoverability: extension.Recoverability{Clean: false, HasIrreversible: true},
		Resume:         extension.ResumeDecision{AllowResume: false, CleanRollback: false},
	}
	text := RenderRuntimeDoctorText(report)
	for _, want := range []string{
		"recoverability: clean=false irreversible=true",
		"resume: allow=false cleanRollback=false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor text missing separated boolean form %q:\n%s", want, text)
		}
	}
}
