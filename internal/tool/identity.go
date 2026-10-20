package tool

// Host tool identities are shared with runtime Name methods. Recognition does not
// imply registration or permission in a particular session.
const (
	HostAsk                        = "ask"
	HostCompleteSubtask            = "complete_subtask"
	HostDocs                       = "docs"
	HostExplore                    = "explore"
	HostFleet                      = "fleet"
	HostForget                     = "forget"
	HostHistory                    = "history"
	HostInstallSkill               = "install_skill"
	HostInstallSource              = "install_source"
	HostListSessions               = "list_sessions"
	HostLspDefinition              = "lsp_definition"
	HostLspDiagnostics             = "lsp_diagnostics"
	HostLspHover                   = "lsp_hover"
	HostLspReferences              = "lsp_references"
	HostMemory                     = "memory"
	HostParallelTasks              = "parallel_tasks"
	HostReadOnlySkill              = "read_only_skill"
	HostReadOnlyTask               = "read_only_task"
	HostReadSession                = "read_session"
	HostReadSkill                  = "read_skill"
	HostReadSubagentResult         = "read_subagent_result"
	HostRemember                   = "remember"
	HostResearch                   = "research"
	HostReview                     = "review"
	HostReviewReport               = "review_report"
	HostRunSkill                   = "run_skill"
	HostSecurityReview             = "security_review"
	HostSendMessage                = "send_message"
	HostSessionReadStrategyReceipt = "session_read_strategy_receipt"
	HostSessionToolResult          = "session_tool_result"
	HostSetSessionTitle            = "set_session_title"
	HostSlashCommand               = "slash_command"
	HostSubmitPlan                 = "submit_plan"
	HostTask                       = "task"
	// 任务482（fence 退役）: HostToolRecovery ("tool_recovery") is withdrawn —
	// the fence it served no longer exists. KnownToolNames must not keep
	// advertising a name with no implementation.
	HostUseCapability = "use_capability"
	HostWebSearch     = "web_search"
)

// forkCollabToolNames：fork 自研的会话级工具（session collab / channel / mailbox /
// task card / 会话元数据 / 会话提醒）。任务 1005 C4 让 sessionCollab 默认开，这批
// 工具随之默认注册；任务 1001 又新增了 session_remind（默认开）。因此两者都必须
// 列入诊断身份名单——boot 的 TestBootRegisteredToolsHaveDiagnosticIdentities
// 要求每个注册工具都可查（本批门禁报 session_remind 缺失即此处）。
var forkCollabToolNames = []string{
	"adopt_session_version",
	"channel_cancel",
	"channel_list",
	"channel_read",
	"channel_send",
	"event_wait",
	"get_message_status",
	"get_session_info",
	"get_session_status",
	"list_addressable_sessions",
	"list_session_versions",
	"mailbox",
	"peek_own_inbox",
	"query_collab_mail",
	"read_collab_status",
	"session_remind",
	"set_session_purpose",
	"talk_to_session",
	"task_card",
}

// KnownToolNames combines compile-time built-ins with host-managed identities.
// Callers must import tool/builtin to initialize the compile-time inventory.
func KnownToolNames() []string {
	names := []string{
		HostAsk,
		HostCompleteSubtask,
		HostDocs,
		HostExplore,
		HostFleet,
		HostForget,
		HostHistory,
		HostInstallSkill,
		HostInstallSource,
		HostListSessions,
		HostLspDefinition,
		HostLspDiagnostics,
		HostLspHover,
		HostLspReferences,
		HostMemory,
		HostParallelTasks,
		HostReadOnlySkill,
		HostReadOnlyTask,
		HostReadSession,
		HostReadSkill,
		HostReadSubagentResult,
		HostRemember,
		HostResearch,
		HostReview,
		HostReviewReport,
		HostRunSkill,
		HostSecurityReview,
		HostSendMessage,
		HostSessionReadStrategyReceipt,
		HostSessionToolResult,
		HostSetSessionTitle,
		HostSlashCommand,
		HostSubmitPlan,
		HostTask,
		HostUseCapability,
		HostWebSearch,
	}
	names = append(names, forkCollabToolNames...)
	for _, t := range Builtins() {
		names = append(names, t.Name())
	}
	return names
}
