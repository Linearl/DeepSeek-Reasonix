package control

import (
	"errors"
	"strings"
	"time"

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
// After DefaultAutopilotAskWait with no human, the run ends as a terminal
// failure instead of hanging forever (task 109 B4).
type askRiskClass int

const (
	// askRiskReversible: the run may answer this itself and log it.
	askRiskReversible askRiskClass = iota
	// askRiskNeedsHuman: stop and wait; the run must not decide this.
	askRiskNeedsHuman
)

// DefaultAutopilotAskWait is how long an unattended run waits for a human on a
// high-risk ask before ending the run. Long enough for a nearby human to notice
// a phone notification; short enough that a long overnight run fails closed
// rather than looking hung (task 109 B4).
const DefaultAutopilotAskWait = 10 * time.Minute

// ErrAutopilotAskUnanswered is returned when a high-risk ask sat unanswered
// past the unattended wait. The Goal FSM maps it to a terminal Blocked/Failed
// so the safety valve shows up as a real stop, not an invisible hang.
var ErrAutopilotAskUnanswered = errors.New("autopilot: a high-risk question was left unanswered; the unattended run stopped instead of deciding for the user")

// askHardNeedsHumanMarkers pause an unattended run on their own: destructive
// actions, actions that unambiguously leave the machine (publishing, deploying,
// mailing), and anything touching credentials or money. A false positive here
// costs a pause, a false negative lets an unattended run act destructively, so
// these stay unconditional.
var askHardNeedsHumanMarkers = []string{
	// destructive
	"delete", "remove", "erase", "wipe", "drop ", "truncate", "overwrite",
	"force push", "reset --hard", "clean -", "rm -",
	// outward-facing, unambiguously
	"publish", "release", "deploy", "upload", "email",
	// credentials and money
	"credential", "token", "secret", "password", "api key", "apikey", "ssh key",
	"payment", "purchase", "billing",
	// Chinese equivalents (the fork ships zh/zh-TW; these arrive untranslated)
	"删除", "移除", "清空", "覆盖", "强制", "推送", "发布", "部署", "上传",
	"发送", "凭据", "令牌", "密钥", "密码", "支付", "购买",
}

// askSoftNeedsHumanMarkers are words that are only risky when they aim at
// something outside the workspace: "merge the findings into the report" is local
// work, "merge the PR into main-v2" is not. They pause only when the same text
// also names an outward target (task 109 B8).
var askSoftNeedsHumanMarkers = []string{
	"push", "merge", "tag", "send", "post ", "合并", "标签",
}

// askOutwardContextMarkers name a target outside the workspace - a remote, a
// registry, a deployed environment, or a person. They are what turns a soft
// marker into a real outward-facing action.
var askOutwardContextMarkers = []string{
	"remote", "origin", "github", "gitlab", "pull request", " pr #", " pr ", "upstream",
	"main-v2", "http://", "https://", "npm", "pypi", "registry", "docker",
	"生产", "线上", "服务器", "服务端", "邮件", "客户", "远端", "远程", "线上环境",
}

// askRiskOfText classifies one haystack: hard markers decide alone, soft ones
// need an outward target in the same text.
func askRiskOfText(haystack string) askRiskClass {
	for _, marker := range askHardNeedsHumanMarkers {
		if strings.Contains(haystack, marker) {
			return askRiskNeedsHuman
		}
	}
	soft := false
	for _, marker := range askSoftNeedsHumanMarkers {
		if strings.Contains(haystack, marker) {
			soft = true
			break
		}
	}
	if !soft {
		return askRiskReversible
	}
	for _, marker := range askOutwardContextMarkers {
		if strings.Contains(haystack, marker) {
			return askRiskNeedsHuman
		}
	}
	return askRiskReversible
}

// askRiskOfQuestion classifies one question. Text and option labels are both
// searched: a question whose body is innocuous but whose options name a deletion
// is still a deletion.
func askRiskOfQuestion(q askQuestionText) askRiskClass {
	haystack := strings.ToLower(q.Text)
	for _, option := range q.Options {
		haystack += "\n" + strings.ToLower(option)
	}
	return askRiskOfText(haystack)
}

// askRiskOfApproval classifies an approval request, where the tool name and its
// arguments count as evidence too: a soft verb is far more likely to be
// outward-facing when the tool is bash and the args name a remote (task 109 B8).
func askRiskOfApproval(tool, subject, reason string, args []byte) askRiskClass {
	return askRiskOfText(strings.ToLower(strings.Join([]string{tool, subject, reason, string(args)}, "\n")))
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
