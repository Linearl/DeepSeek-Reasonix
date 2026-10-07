package tool

// 任务 426（工具安全元数据 + 权限判定单点收敛）——本文件是内置工具安全属性的
// 唯一事实源：并发分组（agent.execute_batch 的 partitionToolCalls）、权限判定
// （permission.IsFileMutationTool 及其 "Edit" 规则别名）、UI 展示（event.Tool
// 的 ReadOnly/RiskLevel/Destructive 字段）三处消费者都只从这里读。
//
// 上游核查结论（2026-10-08，fork 铁律「架构型取上游」）：上游 v1.38.3..v1.39.7
// 之间 internal/permission/ 与 internal/tool/ 元数据层只有零散 bug fix 和
// Harness 重构（#10209/#10241），没有声明式安全元数据或判定单点的同构实现；
// EffectHint 在两端形态一致，也不存在 ConcurrentSafe/RiskLevel 概念。因此本件
// 定为自研（设计借鉴 zcode 的 types.ts/scheduler.ts/permission-flow 单点形态，
// 按 Reasonix 自身的契约测试惯例落地）。
//
// 声明形态：按工具名的纯数据表（builtinSafety）。选表而不选工具结构体方法，
// 是因为字符串侧消费者（权限规则匹配、sentinel 底线、持久化规则）在没有工具
// 实例、也没有链接 builtin 注册包的上下文里也要能回答（permission 包只依赖
// internal/tool，不会链入 builtin 的 init 注册）。单一事实源由契约测试
// （safety_contract_test.go）保证：每个注册的内置工具必须有一行登记，登记的
// ReadOnly 必须与 Tool.ReadOnly() 一致（无漂移）；新增工具漏登记即测试红。
// 表外的动态工具（MCP、能力代理）由 SafetyOf 按 ReadOnly()/MCPDestructiveHint
// 推导，推导口径与收敛前的兜底行为逐位一致（只读即可并发、未知即串行）。
//
// 等价性纪律：FileMutation/ConcurrentSafe=false/Destructive 的登记集合与收敛
// 前的三份散落清单（permission.IsFileMutationTool、parallelisableCall 的
// barrier 名单、kill/restart 家族的危险面）逐一等价，由契约测试逐条钉死。

// RiskLevel 是工具的静态风险档位，供 UI 展示与后续门禁使用。三档从低到高：
// low（纯观测）、medium（改变会话/工作区状态）、high（进程/应用生命周期或
// 不可逆操作）。
type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

// SafetySpec 是一个工具的静态安全元数据。全部字段都是「这个工具型别」的静态
// 属性；按调用参数变化的动态效应分类走 EffectHintProvider / BatchClassifier，
// 不进本结构。
type SafetySpec struct {
	// ReadOnly 与 Tool.ReadOnly() 必须一致，由契约测试钉死。
	ReadOnly bool
	// Destructive 表示工具会终止/重启宿主进程或以任意命令面执行（bash、
	// kill_shell、restart_update、restart_and_update）。破坏性工具在任何
	// 并发分组里独占。
	Destructive bool
	// ConcurrentSafe 表示只读且可与其他只读调用并行扇出。false 不代表工具
	// 有写副作用（complete_step 只写台账），只代表它必须独占串行批：证据
	// 台账回执要求按 provider 顺序落账。
	ConcurrentSafe bool
	// FileMutation 表示工具会变更工作区文件。权限层用它决定 "Edit" 规则的
	// 覆盖面与 "always allow" 的记忆范围；sentinel 底线用它决定路径审计。
	FileMutation bool
	// RiskLevel 是静态风险档位（UI 展示消费）。
	RiskLevel RiskLevel
}

// builtinSafety 是内置工具安全元数据的唯一登记表（按规范名）。运行期注册的
// 工具（ui_interact、restart_update）也在此登记，使字符串侧消费者无需链接
// 注册点即可查询。
var builtinSafety = map[string]SafetySpec{
	// 纯观测（只读 + 可并行扇出）。
	"claim_check":            {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"code_index":             {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"extend_research_budget": {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"submit_feedback":        {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"glob":                   {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"grep":                   {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"heartbeat_task_list":    {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"inspect_worktree_merge": {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"ls":                     {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"prepare_worktree_merge": {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"read_file":              {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"todo_read":              {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"update_goal":            {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"view_image":             {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	"web_fetch":              {ReadOnly: true, ConcurrentSafe: true, RiskLevel: RiskLow},
	// 证据台账/顺序敏感：只读但必须独占串行批（原 parallelisableCall
	// 硬编码 barrier 名单的元数据化）。
	"bash_output":   {ReadOnly: true, ConcurrentSafe: false, RiskLevel: RiskLow},
	"complete_step": {ReadOnly: true, ConcurrentSafe: false, RiskLevel: RiskLow},
	"compress":      {ReadOnly: true, ConcurrentSafe: false, RiskLevel: RiskLow},
	"todo_write":    {ReadOnly: true, ConcurrentSafe: false, RiskLevel: RiskLow},
	"wait":          {ReadOnly: true, ConcurrentSafe: false, RiskLevel: RiskLow},
	// 工作区文件变更（原 permission.IsFileMutationTool 七个名字的元数据化）。
	"delete_range":  {ReadOnly: false, ConcurrentSafe: false, FileMutation: true, RiskLevel: RiskMedium},
	"delete_symbol": {ReadOnly: false, ConcurrentSafe: false, FileMutation: true, RiskLevel: RiskMedium},
	"edit_file":     {ReadOnly: false, ConcurrentSafe: false, FileMutation: true, RiskLevel: RiskMedium},
	"move_file":     {ReadOnly: false, ConcurrentSafe: false, FileMutation: true, RiskLevel: RiskMedium},
	"multi_edit":    {ReadOnly: false, ConcurrentSafe: false, FileMutation: true, RiskLevel: RiskMedium},
	"notebook_edit": {ReadOnly: false, ConcurrentSafe: false, FileMutation: true, RiskLevel: RiskMedium},
	"write_file":    {ReadOnly: false, ConcurrentSafe: false, FileMutation: true, RiskLevel: RiskMedium},
	// 普通写入者/宿主操作（串行，非文件变更面）。
	"create_worktree":                {ReadOnly: false, ConcurrentSafe: false, RiskLevel: RiskMedium},
	"heartbeat_task_enable":          {ReadOnly: false, ConcurrentSafe: false, RiskLevel: RiskMedium},
	"heartbeat_task_upsert":          {ReadOnly: false, ConcurrentSafe: false, RiskLevel: RiskMedium},
	"merge_worktree_back":            {ReadOnly: false, ConcurrentSafe: false, RiskLevel: RiskMedium},
	"open_isolated_worktree_project": {ReadOnly: false, ConcurrentSafe: false, RiskLevel: RiskMedium},
	"screenshot":                     {ReadOnly: false, ConcurrentSafe: false, RiskLevel: RiskMedium},
	"ui_interact":                    {ReadOnly: false, ConcurrentSafe: false, RiskLevel: RiskMedium},
	// 进程/应用生命周期或任意命令面：破坏性，任何分组独占。
	"bash":               {ReadOnly: false, ConcurrentSafe: false, Destructive: true, RiskLevel: RiskHigh},
	"kill_shell":         {ReadOnly: false, ConcurrentSafe: false, Destructive: true, RiskLevel: RiskHigh},
	"restart_and_update": {ReadOnly: false, ConcurrentSafe: false, Destructive: true, RiskLevel: RiskHigh},
	"restart_update":     {ReadOnly: false, ConcurrentSafe: false, Destructive: true, RiskLevel: RiskHigh},
}

// SafetyOfName 按规范名读取登记表。未知名返回零值（各布尔位 false、RiskLevel
// 为空串），与「未知工具按最保守处理」的既有口径一致：并发分组串行、权限层
// 不认领文件变更面。
func SafetyOfName(name string) SafetySpec {
	return builtinSafety[name]
}

// SafetyOf 是实例侧的唯一读取点：先按工具规范名查登记表（覆盖全部内置工具，
// 含运行期注册者）；表外动态工具按既有接口保守推导——只读性取 ReadOnly()，
// 破坏性取 MCPDestructiveHint，可并发等价于只读（与收敛前 partitionToolCalls
// 的兜底 return target.ReadOnly() 逐位一致），文件变更一律 false（动态工具
// 不许静默认领）。推导出的 RiskLevel 只反映这两项输入。
func SafetyOf(t Tool) SafetySpec {
	if spec, ok := builtinSafety[t.Name()]; ok {
		return spec
	}
	spec := SafetySpec{ConcurrentSafe: t.ReadOnly(), RiskLevel: RiskMedium}
	if t.ReadOnly() {
		spec.ReadOnly = true
		spec.RiskLevel = RiskLow
	}
	if d, ok := t.(interface{ MCPDestructiveHint() bool }); ok && d.MCPDestructiveHint() {
		spec.Destructive = true
		spec.RiskLevel = RiskHigh
	}
	return spec
}
