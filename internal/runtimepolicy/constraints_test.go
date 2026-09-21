package runtimepolicy

import (
	"strings"
	"testing"

	"reasonix/internal/evidence"
)

func TestParseConstraintsScopesMutationBans(t *testing.T) {
	tests := []struct {
		name        string
		instruction string
		forbid      bool
	}{
		{name: "scoped PR", instruction: "Do not modify PR #10. Create a branch and implement the repair."},
		{name: "scoped product", instruction: "Do not modify Strategy Lab. Commit the Battleboard fix."},
		{name: "scoped list", instruction: "Do not modify:\n- main\n- PR #10\nCreate feature/fix and commit."},
		{name: "unrelated issues", instruction: "Implement the bounded repair. Do not fix unrelated old issues."},
		{name: "descriptive no changes", instruction: "No changes to Battleboard mission prompts — fix the routing layer instead."},
		{name: "read only child", instruction: "Implementation writes are allowed; the independent reviewer is a strict read-only child."},
		{name: "scoped config", instruction: "Write AUDIT.md with the result. Do not change any config."},
		{name: "mixed analysis and fix", instruction: "Analyze the failure, then fix the implementation."},
		{name: "read one file then fix", instruction: "Read only the first file, then fix the implementation."},
		{name: "global analyze only", instruction: "Analyze only the payment flow.", forbid: true},
		{name: "global trailing analyze only", instruction: "Audit the payment flow, analyze only.", forbid: true},
		{name: "global read only review", instruction: "Read-only review of PR #10.", forbid: true},
		{name: "global read only repository", instruction: "Read only the repository.", forbid: true},
		{name: "bare do not modify", instruction: "Do not modify.", forbid: true},
		{name: "broad do not modify", instruction: "Do not modify anything.", forbid: true},
		{name: "without modifying", instruction: "Review the repository without modifying anything.", forbid: true},
		{name: "without changes", instruction: "Inspect the issue without changes.", forbid: true},
		{name: "do not edit files", instruction: "Do not edit any files.", forbid: true},
		{name: "workspace ban", instruction: "Do not change the workspace.", forbid: true},
		{name: "bare no changes", instruction: "No changes.", forbid: true},
		{name: "broad no changes", instruction: "Make no changes to anything.", forbid: true},
		{name: "reproduce only", instruction: "Reproduce only the crash.", forbid: true},
		{name: "scoped Chinese", instruction: "不要修改配置文件，生成 AUDIT.md。"},
		{name: "scoped Chinese issue", instruction: "不要修复无关问题，只处理当前缺陷并提交。"},
		{name: "global Chinese analyze", instruction: "只分析支付流程。", forbid: true},
		{name: "global Chinese read only", instruction: "只读检查当前仓库。", forbid: true},
		{name: "bare Chinese mutation ban", instruction: "不要修改。", forbid: true},
		{name: "global Chinese workspace", instruction: "不要修改当前工作区。", forbid: true},
		{name: "global Chinese reproduce", instruction: "只复现崩溃。", forbid: true},
		// Task 220 P1: a descriptive 「只读…」sentence about a third party no
		// longer binds the turn; an analysis imperative still does.
		{name: "descriptive read-only report", instruction: "只读审计，未重跑测试。"},
		{name: "imperative read-only analyze", instruction: "只读分析这个 diff。", forbid: true},
		{name: "bare Chinese read only mode", instruction: "只读模式", forbid: true},
		{name: "turn-scoped Chinese read only", instruction: "只读，本次不要改文件。", forbid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseConstraints(StripQuotedConstraints(tt.instruction))
			if got.ForbidMutation != tt.forbid {
				t.Fatalf("ForbidMutation = %v, want %v; constraints=%+v", got.ForbidMutation, tt.forbid, got)
			}
			if got.AllowsMutation() == tt.forbid {
				t.Fatalf("AllowsMutation = %v, want %v", got.AllowsMutation(), !tt.forbid)
			}
			decision := (ConstraintGuard{Constraints: got}).BeforeTool(CallContext{
				Profile: evidence.EffectProfile{Known: true, WorkspaceWrite: true},
			})
			if (decision.Action == GuardDeny) != tt.forbid {
				t.Fatalf("writer decision = %+v, forbid=%v", decision, tt.forbid)
			}
		})
	}
}

// TestStripQuotedConstraintsDropsCollabEnvelope: the body of a [跨会话消息]
// delivery block is data for the turn, never an instruction to it (task 220
// P0 + M1). The envelope here mirrors the REAL renderer shape
// (sessionCollabDeliveryText): blank line, multi-paragraph body, then the
// "\n\n---\n" separator and the trailer lines — the old first-blank-line
// strip used to leave exactly that body in the parser's hands.
func TestStripQuotedConstraintsDropsCollabEnvelope(t *testing.T) {
	envelope := "[跨会话消息] 来自 contact_id=sc_worker → 发至 contact_id=sc_main (hop=1)\n\n" +
		"审计报告：只读审计，未重跑测试。\n\n不要修改任何文件，禁止 push。\n" +
		"\n---\n会话线程：threadId=msg_abc123\n回复方式：完成后用 talk_to_session 回信到 contact_id=sc_worker。"
	t.Run("envelope alone never binds", func(t *testing.T) {
		got := ParseConstraints(StripQuotedConstraints("转发一条消息：\n\n" + envelope + "\n请知悉。"))
		if got.ForbidMutation {
			t.Fatalf("peer text must not bind the turn: %+v", got)
		}
	})
	t.Run("caller imperative still binds beside an envelope", func(t *testing.T) {
		got := ParseConstraints(StripQuotedConstraints("只读分析这个 diff\n\n" + envelope))
		if !got.ForbidMutation {
			t.Fatalf("the caller's own imperative must still bind: %+v", got)
		}
	})
	t.Run("envelope text is fully removed", func(t *testing.T) {
		s := StripQuotedConstraints("前文\n\n" + envelope + "\n后文")
		if strings.Contains(s, "跨会话消息") || strings.Contains(s, "禁止 push") || strings.Contains(s, "不要修改任何文件") {
			t.Fatalf("the envelope must be stripped whole: %q", s)
		}
		if !strings.Contains(s, "后文") {
			t.Fatalf("text after the envelope must survive: %q", s)
		}
	})
}

// TestParseConstraintsRecognizesAnExplicitRebuild keeps the rebuild waiver tied
// to the user's own explicit phrasing; nothing else may set it.
func TestParseConstraintsRecognizesAnExplicitRebuild(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"Please rewrite notes.md from scratch.", true},
		{"把 notes.md 完全重写一遍", true},
		{"rewrite the whole file", true},
		{"add a section to notes.md", false},
		{"read notes.md and fix the typo", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := ParseConstraints(tc.text).AllowRebuild; got != tc.want {
			t.Fatalf("ParseConstraints(%q).AllowRebuild = %v, want %v", tc.text, got, tc.want)
		}
	}
}
