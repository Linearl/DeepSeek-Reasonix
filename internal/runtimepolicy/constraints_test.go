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
		{name: "read only child description no longer binds", instruction: "Implementation writes are allowed; the independent reviewer is a strict read-only child."},
		{name: "scoped config", instruction: "Write AUDIT.md with the result. Do not change any config."},
		{name: "mixed analysis and fix", instruction: "Analyze the failure, then fix the implementation."},
		{name: "read one file then fix", instruction: "Read only the first file, then fix the implementation."},
		// tl1621: read-only / analyze-only wording never binds the turn any
		// more — clause-level matching kept mis-freezing text that merely
		// described or restricted scope (tl220 patched it twice and the
		// heartbeat incident still got through), and peer products set
		// read-only-ness from configuration before the turn, never from text.
		{name: "analyze only no longer binds", instruction: "Analyze only the payment flow."},
		{name: "trailing analyze only no longer binds", instruction: "Audit the payment flow, analyze only."},
		{name: "read-only review no longer binds", instruction: "Read-only review of PR #10."},
		{name: "read only repository no longer binds", instruction: "Read only the repository."},
		{name: "bare do not modify", instruction: "Do not modify.", forbid: true},
		{name: "broad do not modify", instruction: "Do not modify anything.", forbid: true},
		{name: "without modifying", instruction: "Review the repository without modifying anything.", forbid: true},
		{name: "without changes", instruction: "Inspect the issue without changes.", forbid: true},
		{name: "do not edit files", instruction: "Do not edit any files.", forbid: true},
		{name: "workspace ban", instruction: "Do not change the workspace.", forbid: true},
		{name: "bare no changes", instruction: "No changes.", forbid: true},
		{name: "broad no changes", instruction: "Make no changes to anything.", forbid: true},
		{name: "reproduce only no longer binds", instruction: "Reproduce only the crash."},
		{name: "scoped Chinese", instruction: "不要修改配置文件，生成 AUDIT.md。"},
		{name: "scoped Chinese issue", instruction: "不要修复无关问题，只处理当前缺陷并提交。"},
		{name: "Chinese analyze only no longer binds", instruction: "只分析支付流程。"},
		{name: "Chinese read only no longer binds", instruction: "只读检查当前仓库。"},
		{name: "bare Chinese mutation ban", instruction: "不要修改。", forbid: true},
		{name: "global Chinese workspace", instruction: "不要修改当前工作区。", forbid: true},
		{name: "Chinese reproduce only no longer binds", instruction: "只复现崩溃。"},
		// tl1621: bare, imperative, turn-scoped and third-party-descriptive
		// read-only wording alike no longer bind. The pinned input is the
		// heartbeat incident verbatim — 「只读；暂不实施」 in a YOLO audit task
		// whose own brief required spawning sessions and writing reports.
		{name: "descriptive read-only report no longer binds", instruction: "只读审计，未重跑测试。"},
		{name: "imperative read-only analyze no longer binds", instruction: "只读分析这个 diff。"},
		{name: "bare Chinese read only mode no longer binds", instruction: "只读模式"},
		{name: "bare Chinese read only clause no longer binds", instruction: "只读；暂不实施"},
		{name: "turn-scoped Chinese read only no longer binds", instruction: "只读，本次不要改文件。"},
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
	// tl1621: the caller's own wording is checked with a negated mutation
	// ("不要修改当前工作区" — absent from the envelope) because read-only
	// wording no longer binds. Envelope-stripping completeness itself is
	// pinned by the sub-test below, which asserts the envelope's mutation
	// phrasing is gone whole.
	t.Run("caller mutation ban still binds beside an envelope", func(t *testing.T) {
		got := ParseConstraints(StripQuotedConstraints("不要修改当前工作区\n\n" + envelope))
		if !got.ForbidMutation {
			t.Fatalf("the caller's own mutation ban must still bind: %+v", got)
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

// TestStripQuotedConstraintsDropsChannelEnvelope (tl1566): the channel
// subtype tag [跨会话消息·群聊] must strip exactly like the plain marker —
// the envelope here mirrors the REAL channel renderer shape (head tag,
// 投递编号 trailer, channel_send reply line, the effect tail note). A
// brand-new tag outside the [跨会话消息 family would have silently exempted
// group copies from task-220 stripping: pasted peer text could freeze the
// turn again.
func TestStripQuotedConstraintsDropsChannelEnvelope(t *testing.T) {
	envelope := "[跨会话消息·群聊] 群聊：调研组，来自 contact_id=human:local → 发至 contact_id=sc_main\n\n" +
		"审计报告：只读审计，未重跑测试。\n\n不要修改任何文件，禁止 push。\n" +
		"\n---\n投递编号：chf_abc123\n回复方式：完成后用 channel_send 工具把回信发到群聊 调研组（回信落在该频道历史里，成员可见；不要用 talk_to_session 回到发件人个人对话——那是私聊通道，群聊里看不到）。如需确保发件人收到投递，可在回信开头 @ 其 contact_id（群聊按 @ 路由投递）。\n" +
		"效果要求：完成工作后请把简短结论发回群聊；内容较多时写成文档、将文档路径或附件引用发到群聊，不要只把结论留在本会话里。"
	t.Run("channel envelope alone never binds", func(t *testing.T) {
		got := ParseConstraints(StripQuotedConstraints("转发一条群聊消息：\n\n" + envelope + "\n请知悉。"))
		if got.ForbidMutation {
			t.Fatalf("peer text must not bind the turn: %+v", got)
		}
	})
	t.Run("channel envelope text is fully removed", func(t *testing.T) {
		s := StripQuotedConstraints("前文\n\n" + envelope + "\n后文")
		for _, leftover := range []string{"跨会话消息", "禁止 push", "不要修改任何文件", "效果要求", "channel_send"} {
			if strings.Contains(s, leftover) {
				t.Fatalf("the channel envelope must be stripped whole, %q survived: %q", leftover, s)
			}
		}
		if !strings.Contains(s, "后文") {
			t.Fatalf("text after the envelope must survive: %q", s)
		}
	})
}
