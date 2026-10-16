# handoff-685：dynamic-workflows 路线 C 编排引擎 MVP —— 声明式 DAG + 子代理节点 + 确定性门 + 独立确认 + report 落袋

- 分支：`wt-685`（worktree `github-repo/worktrees/wt-685`，基于 `main-v2-stable` @ f8a09feec）
- 任务书：tl 685（P2 批 35 专批件；**本件 schema 契约是 686 可视化面板的接口**）
- 建档：2026-10-15
- 路线定案：声明式 DAG（评估推荐档 C，用户 2026-10-09 确认），不移植 zcode TS 脚本 DSL（60-90 人日）；评估报告见 `docs/report/调研报告【非诊断】/dynamic-workflows移植工作量评估-20261009.md`

## 决策记录（供 autopilot 追认）

1. **复用优先（用户判据①③）**：子代理节点复用 `internal/agent/task*.go`（TaskTool，约 2.9k 行现成底座）；门命令复用 `internal/shellrun`（bash 工具同源前台运行器，含超时/取消/输出定界/Windows 控制台解码）；事件复用 `internal/event` 总线。全部新增代码落在**新包 `internal/workflow/`**，不改既有行为，无需实验开关（纯库层，无 boot 接线）。
2. **引擎=库层，不接工具面**：685 验收六要点全是引擎语义 + 测试；用户可见的 workflow 工具/触发接线不在本件范围（后续任务）。引擎依赖全部注入（Sink / SubagentRunner / GateRunner），测试用假件，e2e 用真 TaskTool + mock provider。
3. **顺序拓扑调度（MVP）**：扇出节点顺序执行（DAG 语义不变，只牺牲并行度）；并行 fan-out 留 Phase 2（zcode 有 fan-out 自适应，首期不做）。
4. **独立确认=节点级判定**：zcode 是逐 finding 链式确认；MVP 简化为 confirm 节点对 target 节点的 findings **整体判定**（一次重现，verdict 应用到全部 findings）。语义保真点：自查不算（confirm 是独立第二子代理节点）、失败保留标 unconfirmed。
5. **两层门语义落地**：`tier: fast|strong`；fast 可驱动 fix-loop；strong 在校验期做**支配性检查**（移除该门后任一终点不可达 → 校验失败），保证「最强层交付前必跑」是结构属性而非自觉。
6. **notCovered 严格语义**：引擎**永不自动填充** notCovered（有检查没跑 ≠ notCovered，那是 unverified）；只透传 schema 里显式声明的 `report.not_covered`。

## 设计要点（任务书口径 → 实现映射）

| 任务书要点 | 实现 |
|---|---|
| ① DAG 编排定义 YAML/JSON，schema 契约先行 | `internal/workflow/schema.go`：类型 + Parse（JSON 严格/ YAML KnownFields 严格）+ Validate（见下契约章） |
| ② 节点执行复用 task 底座 + 类型化结果 | `SubagentRunner` 接口 + `TaskSubagentRunner` 适配器（包装 agent.TaskTool / ReadOnlyTaskTool）；result 字段说明注入子代理提示（zcode §4 语义） |
| ③ 确定性门 bash+exit code 分支 | `gate.go`：字面命令 + shellrun；exit≠0 是值不是异常；stderr 带下轮反馈进 fixer；`loop.max_rounds` 上限；tier 两层门 |
| ④ 独立确认协议 | `confirm.go`：第二子代理（read-only）从证据重现；失败**保留**标 unconfirmed |
| ⑤ report 落袋 | `report.go`：findings 一确认即入袋（mutex bag）；中途失败/取消 Run 返回 (partialReport, err) 不丢 |
| ⑥ 事件流六态 | 新 `event.WorkflowNode` 种类 + payload；六态：queued/running/completed/unconfirmed/failed/gate_passed；wire 名 `workflow_node` |

参考语义源：zcode dynamic-workflows SKILL.md §4（类型化结果）/§5（world.run 门）/§10（verify-report-deliver）/§13（生命周期）——取语义不抄 TS。

## 复用衔接（任务书「派发必读」核对）

- 事件触发器引擎（tl 230 `internal/eventtrigger`）与专用等待工具（tl 228 `event_wait`）：本件**未重复造轮子**——685 MVP 不含等待节点；后续加 wait 节点时以 230 的 Trigger/checker 白名单 + 228 的挂起语义为底座（本包 GateRunner/Sink 注入缝即接点）。
- 686 面板：消费下节 schema 契约 + `workflow_node` 事件（降级路径已先行，见 wt-686/handoff-686.md；685 落地后按契约对齐真渲染）。

---

## 【Schema 契约章】（686 引用接口；版本化，破坏性变更需升 version）

### 1. 编排定义（YAML 或 JSON；JSON 首字符 `{` 走严格 JSON，否则严格 YAML KnownFields——未知字段报错）

```yaml
version: 1                       # 必填，当前仅 1
name: string                     # 必填，工作流名（进事件 payload）
description: string              # 可选

nodes:                           # 必填，至少 1 个
  - id: string                   # 必填，唯一，^[a-z0-9][a-z0-9_-]*$
    type: subagent | gate | phase | join | confirm   # 必填

    # —— type: subagent ——
    prompt: string               # 必填（支持 {{node_id}} / {{node_id.field}} 插值，被引节点必须是祖先）
    description: string          # 可选，短标签（进派发行显示）
    result:                      # 可选，类型化结果 schema（字段说明注入子代理提示）
      - name: string             #   字段名
        type: string             #   string|number|boolean|object|array
        description: string      #   字段说明——子代理读到的提示（zcode §4 的最廉价质量杠杆）
    findings_field: string       # 可选：result 中承载 findings 数组的字段名（数组项键：where/what/evidence/severity；status 由引擎指派）
    tools: [string]              # 可选，工具白名单
    model / effort: string       # 可选
    max_steps: int               # 可选

    # —— type: gate（确定性门，world.run 等价）——
    command: string              # 必填，字面命令（**永不插值**——命令集在确认时定死，round 差异走 fixer 提示）
    timeout: string              # 可选，Go duration；默认 300s（zcode 默认）
    tier: fast | strong          # 可选，默认 fast；strong 交付前必跑（结构支配性校验）
    loop:                        # 可选，「修到测试过」门控循环
      fixer: node-id             #   必填，subagent 节点（该节点禁止有边——只由门在循环内执行）
      max_rounds: int            #   必填，≥1，轮次上限（无界循环=zcode 反模式，必须有帽）

    # —— type: phase（阶段分组标记）——
    label: string                # 必填，阶段名（686 分组着色）

    # —— type: join（汇聚）——
    # 无专有字段；结果=上游各节点结果的 JSON 映射（供下游插值）

    # —— type: confirm（独立确认）——
    target: node-id              # 必填，指向 subagent 节点（引擎加隐式依赖：target 完成后才跑）
    prompt: string               # 可选，额外确认指令

edges:                           # 可选
  - from: node-id                # 必填
    to: node-id                  # 必填
    when: always | pass | fail   # 可选，默认 always；pass/fail 仅允许 from=gate
    label: string                # 可选（数据流标注，686 显示）

report:                          # 可选
  conclusion: string             # 可选，模板（支持 {{node_id}} / {{node_id.field}}；缺省=引擎合成摘要）
  not_covered: [string]          # 可选，**仅显式声明**；引擎永不自动填充（严格语义）
```

### 2. 校验规则（Parse 后 Validate 逐条；任一违反 = 拒绝运行）

1. id 唯一、匹配 `^[a-z0-9][a-z0-9_-]*$`；type 合法；type 专有字段按上表必填（subagent 须有 prompt；gate 须有 command；phase 须有 label；confirm 须有 target 且指向 subagent 节点）。
2. 边端点必须存在；`when: pass|fail` 的 from 必须是 gate；未知字段（严格模式）报错。
3. **无环**（DAG）；fixer 节点必须零边（仅由门循环执行，不进常规调度）。
4. 插值引用（prompt/conclusion 里的 `{{node_id}}` / `{{node_id.field}}`）：node_id 必须存在；**prompt 里被引节点必须是当前节点祖先**（依赖闭包内，防引用未跑节点）；`.field` 若声明了 result schema 则必须在其中。
5. **strong 门支配性**：对每个 `tier: strong` 门 G 与每个终点节点 S（无出边，S≠G）：移除 G 后 S 必须从源不可达（否则校验失败）——「最强层交付前必跑」是图结构属性。
6. loop.fixer 指向的节点不得再被普通边引用（或被引用即报错）。
7. confirm.target 必须是 subagent 节点；confirm 与 target 之间的数据依赖由引擎隐式保证（无需显式边）。

### 3. 节点执行语义

- **调度**：顺序拓扑；零入边节点先跑（fixer 除外）；confirm 节点隐式等待 target。
- **边激活**：gate 出边按 exit code 激活（0→pass/always，≠0→fail/always）；未激活边的后代=跳过（保持 queued 灰，不发终态事件）。
- **subagent 节点**：prompt = 节点 prompt（插值后）+ 类型化结果提示（若声明 result）；最终答复若声明 result 则解析 JSON（整体→首尾大括号截取→失败即节点失败）；声明 findings_field 且字段为数组 → findings 提取。
- **gate 节点**：跑字面命令（shellrun，含超时）。exit 0 → `gate_passed`，激活 pass/always 边，命令记入 report.verified（`<command> (exit 0)`）。exit ≠0 → **值不是异常**：若有 loop 且轮次未尽 → 以**合并输出尾部（含 stderr）为反馈**跑 fixer 子代理 → 重跑门（Round 递增，事件带 round）；轮次尽或无 loop → 节点 `failed`，激活 fail/always 边；**无 fail 边承接 → 工作流失败**。超时/启动失败 = 世界未应答 → 工作流失败（不是分支值）。
- **join 节点**：全部激活入边满足后完成；结果 = `{node_id: result}` 映射。
- **confirm 节点**：以 target 的证据（result + prompt）构造 read-only 第二子代理提示「仅凭证据重现，回答 JSON {confirmed, reason}」；confirmed=true → target 的 findings 标 `verified`（入袋）；false/解析失败/子代理失败 → findings **保留**标 `unconfirmed` 入袋（怀疑仍是怀疑，给人看）；confirm 成功本身记入 report.verified。
- **失败语义**：subagent 节点失败 → 工作流失败（fail-fast）；已入袋 findings 不丢。ctx 取消同理。

### 4. WorkflowReport（Run 返回值；中途失败也返回已入袋部分）

```go
type WorkflowReport struct {
    Conclusion string     // report.conclusion 模板插值 或 引擎合成摘要
    Findings   []Finding  // 入袋顺序；{Where, What, Evidence, Status(verified|unconfirmed), Severity}
    Verified   []string   // 实际裁决结果的命令/确认：通过的门命令、成功的独立确认
    NotCovered []string   // 仅 schema 显式声明（严格语义）
}
// Run(ctx, wf) (*WorkflowReport, error) —— error != nil 时 report 仍非 nil（落袋保证）
```

### 5. 事件契约（`internal/event` 新 Kind：`WorkflowNode`；wire 名 **`workflow_node`**）

- 发射点：Run 开始（全部调度节点各发一条 queued）/ 节点开始（running，gate 带 Round）/ 终态（completed | gate_passed | failed | unconfirmed；confirm 失败时对 **target 节点**补发 unconfirmed 状态变迁）。
- **六态**（686 状态色）：`queued` 排队灰 / `running` 运行中 / `completed` 完成绿 / `unconfirmed` 黄 / `failed` 红 / `gate_passed` 门通过。
- payload（eventwire 透传为 `workflow` 对象，camelCase）：

```json
{
  "runId": "wf-...",          // 引擎生成
  "workflow": "name",
  "nodeId": "hunt-a",
  "nodeType": "subagent",     // subagent|gate|phase|join|confirm
  "state": "running",         // queued|running|completed|unconfirmed|failed|gate_passed
  "phase": "label",           // 当前 phase 节点 label（无则空）
  "round": 2,                 // gate 循环轮次（非 gate/首轮外=0）
  "detail": "exit 1; ..."     // 短诊断（错误摘要/exit code），外部化标记随 wire
}
```

- 桌面契约同步点：`desktop/frontend/src/lib/types.ts` EventKind 增 `"workflow_node"` + `WireWorkflowNode` payload 接口；wire 契约测试（TestKindNamesComplete / TestDesktopWireEventKindTypeCoversSharedKinds）钉住。

### 6. 示例编排（`internal/workflow/testdata/`，e2e 用）

扇出（hunt-a/hunt-b subagent）→ join（汇聚）→ confirm（确认 hunt-a findings）→ gate（fast 门控循环：unit 测试失败→fixer 修→重跑，max_rounds=3）→ strong gate（交付前必跑）→ phase 标记 → 完成。

## 验收映射（六要点逐条）

1. schema 契约文档（本章）+ 解析校验单测（含全部 7 条校验规则的正反例）
2. 子代理节点执行端到端（真 TaskTool + mock provider，含类型化结果解析）
3. 确定性门分支 + 循环上限测试（exit 0/≠0 分支、stderr 反馈进 fixer、max_rounds 截断、超时失败）
4. 独立确认 unconfirmed 语义测试（重现成功→verified；重现失败/确认子代理失败→findings 保留标 unconfirmed）
5. report 落袋中途失败不丢测试（node2 失败/ctx 取消 → Run 返回 partial report 含 node1 findings）
6. 事件六态发总线测试（收集 Sink 断言六态序列与 payload 字段）

## 进度日志

- 2026-10-15：建档；worktree wt-685 建立（base f8a09feec）；调研 task 底座/internal event/shellrun/eventtrigger/SKILL.md 语义源完毕；schema 契约成章。
