package control

import "testing"

// Task 109 B8: a soft verb only pauses when the same text names an outward
// target, so local phrasing stops stopping unattended runs while real
// publish/merge-into-remote cases still do.
func TestAskRiskSoftMarkersNeedAnOutwardTarget(t *testing.T) {
	cases := []struct {
		name string
		text string
		want askRiskClass
	}{
		{"merge findings into a local report", "Should I merge the findings into the report?", askRiskReversible},
		{"tag a local build", "Should I tag this build as scratch?", askRiskReversible},
		{"send the summary to the transcript", "Should I send the summary to the transcript?", askRiskReversible},
		{"merge a PR into main-v2", "Should I merge the PR into main-v2?", askRiskNeedsHuman},
		{"push to origin", "Push the branch to origin?", askRiskNeedsHuman},
		{"publish always pauses", "Publish the release now?", askRiskNeedsHuman},
		{"delete always pauses", "Delete the scratch directory?", askRiskNeedsHuman},
		{"chinese soft with an outward target", "是否合并到远端分支？", askRiskNeedsHuman},
		{"chinese soft without a target", "要不要合并这两个函数？", askRiskReversible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := askRiskOfQuestion(askQuestionText{Text: tc.text}); got != tc.want {
				t.Fatalf("risk = %v, want %v", got, tc.want)
			}
		})
	}
}

// The approval path also sees the tool name and its arguments, so a soft verb in
// the subject is judged together with the command about to run.
func TestAskRiskOfApprovalUsesToolAndArgs(t *testing.T) {
	if got := askRiskOfApproval("bash", "push the branch", "", []byte(`{"command":"git push origin main"}`)); got != askRiskNeedsHuman {
		t.Fatalf("push to origin = %v, want needs-human", got)
	}
	if got := askRiskOfApproval("write_file", "merge the two helpers", "", []byte(`{"path":"pkg/util.go"}`)); got != askRiskReversible {
		t.Fatalf("local merge = %v, want reversible", got)
	}
}
