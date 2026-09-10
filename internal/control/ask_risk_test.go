package control

import "testing"

// TestAskRiskNeedsHumanForDestructiveAndOutwardFacing pins the conservative half of
// the classifier (task 49 A3). Each case is something an unattended run must not
// decide on its own: a false positive only costs a pause, while a false negative is
// a run that deleted, pushed, or spent with nobody watching.
func TestAskRiskNeedsHumanForDestructiveAndOutwardFacing(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		options []string
	}{
		{"delete files", "Delete these 12 files?", nil},
		{"force push", "The branch has diverged. What should I do?", []string{"force push", "rebase"}},
		{"publish", "Publish the release now?", nil},
		{"credentials", "Which API key should I use?", nil},
		{"rm flag", "Run this?", []string{"rm -rf build/"}},
		{"chinese delete", "要删除这些文件吗？", nil},
		{"chinese push", "是否推送到远端？", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := askRiskOfQuestion(askQuestionText{Text: tc.text, Options: tc.options})
			if got != askRiskNeedsHuman {
				t.Fatalf("risk = %v, want askRiskNeedsHuman", got)
			}
		})
	}
}

// TestAskRiskReversibleForWorkspaceChoices is the other half: questions the run may
// answer itself and log, because they are choices inside the workspace rather than
// commitments about the world.
func TestAskRiskReversibleForWorkspaceChoices(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		options []string
	}{
		{"approach", "How should I structure the parser?", nil},
		{"naming", "Which name do you prefer?", []string{"Config", "Settings"}},
		{"scope", "Should I refactor the helper too?", nil},
		{"chinese approach", "用哪种实现方式？", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := askRiskOfQuestion(askQuestionText{Text: tc.text, Options: tc.options})
			if got != askRiskReversible {
				t.Fatalf("risk = %v, want askRiskReversible", got)
			}
		})
	}
}

// TestAskRiskBatchNeedsHumanIfAnyQuestionDoes guards the batch rule: one dangerous
// question must not ride in on the back of three safe ones.
func TestAskRiskBatchNeedsHumanIfAnyQuestionDoes(t *testing.T) {
	safe := []askQuestionText{
		{Text: "Which layout?"},
		{Text: "Should I add tests?"},
	}
	if got := askRiskOfQuestions(safe); got != askRiskReversible {
		t.Fatalf("all-safe batch = %v, want reversible", got)
	}
	mixed := append(append([]askQuestionText{}, safe...), askQuestionText{Text: "Push to origin?"})
	if got := askRiskOfQuestions(mixed); got != askRiskNeedsHuman {
		t.Fatalf("mixed batch = %v, want needs-human", got)
	}
}
