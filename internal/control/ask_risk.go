package control

import (
	"strings"

	"reasonix/internal/event"
)

// Question risk classification for unattended runs (task 49 A3).
//
// An autopilot run has nobody to answer the `ask` tool. The safe half of the
// fallback is to answer the questions the run can equally well answer itself -
// confirming what it is about to do inside the workspace, choosing between
// equivalent approaches - and record the answer so the transcript shows what was
// decided on the user's behalf.
//
// The other half must NOT be answered by the run: anything destructive, anything
// that leaves the machine (pushing, publishing, messaging), and anything that
// touches credentials. Those pause for a human, because "the agent decided for
// you" is exactly the wrong outcome there, and no prompt wording can make it right.
type askRiskClass int

const (
	// askRiskReversible: the run may answer this itself and log it.
	askRiskReversible askRiskClass = iota
	// askRiskNeedsHuman: stop and wait; the run must not decide this.
	askRiskNeedsHuman
)

// askNeedsHumanMarkers are matched case-insensitively against the question text
// and its option labels. The list is deliberately conservative: a false positive
// only costs a pause, a false negative lets an unattended run act destructively.
var askNeedsHumanMarkers = []string{
	// destructive
	"delete", "remove", "erase", "wipe", "drop ", "truncate", "overwrite",
	"force push", "reset --hard", "clean -", "rm -",
	// outward-facing
	"push", "publish", "release", "deploy", "upload", "email", "send", "post ",
	"merge", "tag",
	// credentials and money
	"credential", "token", "secret", "password", "api key", "apikey", "ssh key",
	"payment", "purchase", "billing",
	// Chinese equivalents (the fork ships zh/zh-TW; these arrive untranslated)
	"删除", "移除", "清空", "覆盖", "强制", "推送", "发布", "部署", "上传",
	"发送", "合并", "凭据", "令牌", "密钥", "密码", "支付", "购买",
}

// askRiskOfQuestion classifies one question. Text and option labels are both
// searched: a question whose body is innocuous but whose options name a deletion
// is still a deletion.
func askRiskOfQuestion(q askQuestionText) askRiskClass {
	haystack := strings.ToLower(q.Text)
	for _, option := range q.Options {
		haystack += "\n" + strings.ToLower(option)
	}
	for _, marker := range askNeedsHumanMarkers {
		if strings.Contains(haystack, marker) {
			return askRiskNeedsHuman
		}
	}
	return askRiskReversible
}

// askQuestionText is the slice of a question the classifier needs. Keeping it
// separate from event.AskQuestion keeps this file free of the event package and
// the classifier trivially testable.
type askQuestionText struct {
	Text    string
	Options []string
}

// askRiskOfQuestions classifies a whole batch: the batch needs a human if any one
// question does. A run must not answer three safe questions and quietly decide the
// dangerous fourth.
func askRiskOfQuestions(questions []askQuestionText) askRiskClass {
	for _, q := range questions {
		if askRiskOfQuestion(q) == askRiskNeedsHuman {
			return askRiskNeedsHuman
		}
	}
	return askRiskReversible
}

// autopilotNoHumanAnswer is what an unattended run receives in place of a user's
// reply, for questions the run is allowed to decide alone. It is deliberately
// explicit - the model is being told to decide for itself, not to stop - and it
// travels in Selected so the turn is not mistaken for the "no selection means skip
// and end the turn" path (#6869). Because the answer lands in the transcript, it
// doubles as the audit record of what was decided unattended.
const autopilotNoHumanAnswer = "autopilot: no human is available - decide for yourself, record the decision, and continue"

// askQuestionTexts narrows event questions to what the classifier reads.
func askQuestionTexts(questions []event.AskQuestion) []askQuestionText {
	out := make([]askQuestionText, 0, len(questions))
	for _, q := range questions {
		options := make([]string, 0, len(q.Options))
		for _, opt := range q.Options {
			options = append(options, opt.Label)
		}
		out = append(out, askQuestionText{Text: q.Prompt, Options: options})
	}
	return out
}

// autopilotAnswers builds the unattended reply: one answer per question, each
// carrying the explicit "decide for yourself" marker.
func autopilotAnswers(questions []event.AskQuestion) []event.AskAnswer {
	out := make([]event.AskAnswer, 0, len(questions))
	for _, q := range questions {
		out = append(out, event.AskAnswer{QuestionID: q.ID, Selected: []string{autopilotNoHumanAnswer}})
	}
	return out
}
