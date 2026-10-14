# 892-A 片交接（P2 backend：任意嵌套分组 schema + manage_sessions 工具族）

- 分支：`wt-892a`（基于 main-v2-stable `1747f222d`）
- 范围：**只做 backend**。UI（B 片）另派，依赖本片 schema/接口落地。
- 状态：见文末「验收与残留」。

## 一、拍板记录（B 片须遵守，不要另行发明）

1. **两套分组、一个删除语义、一个校验器**
   - 会话分组（侧栏 topic 组）：`desktopGroup`，存储在 `desktop-projects.json` **并镜像到 organization sidecar**（`desktop-project-tree-organization.json` 的 `global.groups` / `projects[].groups`）。写入一律走 `updateProjectsFile` → `saveProjectsFile` → `saveProjectOrganizationFile`（host API 链路，**禁止直改文件**——三处同步坑教训）。
   - 项目分组（项目归组）：新增 `desktopProjectGroup` 实体，roster 存 `desktop-projects.json` 顶层 **`projectGroups`** 数组（"catalog"）。前端今天还在 localStorage（`projectGroups.ts`），**迁移到后端 catalog 是 B 片的活**；`desktopProject.ProjectGroup` 字符串字段语义不变（现在指向 catalog 里的组 id）。
2. **嵌套任意深度**：`maxSessionGroupDepth` 由 2（task-350 Discord 式浅层）提到 **64**（公司架构模拟够用，防病态数据的 sanity cap）。成环防护 = 显式祖先链走查（`validateGroupParentChain`），不是靠深度上限。
3. **删除级联语义（选定）**：**默认「子组升顶」**——删组时其直接子组重挂到被删组的父层（根组则升到顶层），被删组内会话只「摘组」不删除；**`cascade=true` 才连子树整删**。项目分组同语义，且指向被删组的项目 `projectGroup` 被清空（项目不删）。依据：用户决策判据 3（删保护性结构前先有替代路径）。
4. **迁移兼容**：存量扁平数组（无 parent 键）无损读入（parent 空 = 顶层）；sidecar 回写时扁平组不长出 `parent` 键（omitempty）；`projectGroups` 键缺失读为空目录。

## 二、给 B 片的接口说明

### 2.1 字段命名（三处拼法，别混）

| 语境 | 拼法 | 示例 |
|---|---|---|
| 存储 JSON（groups / projectGroups 元素） | `parent` | `{"id":"g2","title":"L2","parent":"g1"}` |
| 工具入参（manage_sessions） | `parent_id` | `{"action":"group_create","group_id":"g2","title":"L2","parent_id":"g1"}` |
| 视图/响应结构（Go struct → TS） | `parentId` | `ManageSessionsGroupView.ParentID` → `parentId` |

### 2.2 Wails 直连方法（B 片 UI 用这些，不必绕工具）

```go
// 读
func (a *App) ManageSessionsList() (ManageSessionsListResult, error)
// 会话组写（scope 默认 "global"；workspaceRoot 仅 project scope 需要）
func (a *App) CreateSessionGroup(scope, workspaceRoot, groupID, title, parentID string) (ManageSessionsGroupResult, error)
func (a *App) RenameSessionGroup(scope, workspaceRoot, groupID, title string) (ManageSessionsGroupResult, error)
func (a *App) DeleteSessionGroup(scope, workspaceRoot, groupID string, cascade, confirm bool) (ManageSessionsGroupResult, error) // confirm=false → 只返回计划
func (a *App) ReorderTopicInGroup(scope, workspaceRoot, topicID, groupID string, index int) (ManageSessionsGroupResult, error)   // index 越界自动收敛
// 项目分组写
func (a *App) CreateProjectGroup(groupID, title, parentID string) (ManageSessionsGroupResult, error)
func (a *App) RenameProjectGroup(groupID, title string) (ManageSessionsGroupResult, error)
func (a *App) DeleteProjectGroup(groupID string, cascade, confirm bool) (ManageSessionsGroupResult, error)
// 操作日志 / 回退
func (a *App) RollbackManageOperation(opID string) (ManageSessionsGroupResult, error)
// 单一 dispatch（与 agent 工具同面）；action: list/rename_session/move_session/
// reorder_session/group_create/group_rename/group_delete/rollback/op_log
func (a *App) ManageSessions(action string, args json.RawMessage) (json.RawMessage, error)
```

视图结构（`desktop/manage_sessions.go`）：

```ts
interface ManageSessionsGroupView { id: string; title: string; parentId?: string; depth: number; order: number; topics: {topicId: string; title: string; order: number}[] }
interface ManageSessionsProjectGroupView { id: string; title: string; parentId?: string; depth: number; sortOrder: number }
interface ManageSessionsSessionView { topicId: string; title: string; scope: "global"|"project"; workspaceRoot?: string; groupId?: string; groupPath?: string[]; order: number }
interface ManageSessionsListResult { groups: Record<string /* "global" | "project|<root>" */, ManageSessionsGroupView[]>; projectGroups: ManageSessionsProjectGroupView[]; sessions: ManageSessionsSessionView[] }
interface ManageSessionsGroupResult { kind: "session"|"project"; scope?: string; workspaceRoot?: string; groupId: string; title?: string; parentId?: string; applied: boolean; plan?: DeleteGroupsPlan; opId?: string }
interface DeleteGroupsPlan { deleted: string[]; promoted?: string[]; unfiledTopics?: string[]; batch?: boolean }
```

### 2.3 agent 工具 schema JSON（manage_sessions，`internal/agent/manage_sessions_tool.go`）

```json
{
  "type": "object",
  "properties": {
    "action": {"type": "string", "enum": ["list", "rename_session", "move_session", "reorder_session", "group_create", "group_rename", "group_delete", "rollback", "op_log"]},
    "target": {"type": "string", "description": "Session actions: contact_id, topic_id, or exact title."},
    "title": {"type": "string"},
    "group": {"type": "string", "description": "move_session target group title; created when missing."},
    "group_id": {"type": "string", "description": "Authoritative over title when given."},
    "parent_id": {"type": "string", "description": "group_create: parent group id, same roster; empty = top. Cycles rejected."},
    "kind": {"type": "string", "enum": ["session", "project"], "description": "group_* actions: default session."},
    "index": {"type": "integer"},
    "cascade": {"type": "boolean"},
    "confirm": {"type": "boolean", "description": "group_delete executes only with true; otherwise plan-only."},
    "scope": {"type": "string"},
    "workspace_root": {"type": "string"},
    "op_id": {"type": "string"}
  },
  "required": ["action"]
}
```

接线：`boot.Options.OnManageSessions agent.ManageSessionsFunc`（nil 则不注册工具）；desktop 6 处（app_effort/app_model/app_session_delete/app_session_rebind/settings_app/tabs）已接 `a.AgentManageSessions`。

### 2.4 刷新事件（侧栏只认它）

每次写 action（含 rename/move 走的老路径）都会同步发：

```
event: "project-tree:changed-v2"
payload: ProjectTreeChangedV2 { revision: number /* catalog revision，无 catalog 时 0 */, roots: [], reason: "metadata" }
```

另经 `requestSessionCatalogMetadataSync()` 异步收敛 catalog（catalog 侧也会再发一次 reason="metadata"，重复刷新无害）。同时发旧版 `project-tree:changed`（reason="catalog-v2"）兼容老前端。**B 片侧栏刷新请监听 v2 事件的 reason="metadata"**（20261009 清理战役实证）。

### 2.5 提案制与操作日志（858 教训对齐）

- `group_delete`（两套分组同）**无 confirm 强制提案**：返回 `plan{deleted,promoted,unfiledTopics,batch}` 零写入；`confirm=true` 才执行。`batch = deleted+unfiledTopics ≥ 10`（`manageSessionsBatchThreshold`），UI 见 batch 应展开全量清单再确认。
- 轻操作（rename/reorder/create/move 单实体）自动执行，无需 confirm。
- 日志：`%APPDATA%/reasonix/manage-sessions-operations.jsonl`，每行 `{opId,at,action,kind,scope,workspaceRoot,subject,plan,before,after,applied,error}`，`before/after` 是整册 roster 快照。`rollback(opId)` 恢复 before 快照；**无与更新写操作的互锁**（只靠版本化保存校验），应尽快回退最新一笔。回退自身也留日志。

## 三、B 片须自行处理的前端残留

1. `sessionGroupTree.ts` / `ProjectTree.tsx` / `project-tree-group-hierarchy.test.tsx` 里 task-350 的**深度 2 假设**（子组不得有子组）已失效——后端现在接受任意深度（≤64），递归渲染缩进、拖拽多层级、级联删除确认弹窗全在 B 片。
2. 项目分组的 localStorage（`projectGroups.ts`：`projectTree:groups` 等 3 键）→ 后端 catalog 迁移：读时若 catalog 为空可一次性导入，之后以后端为准。
3. 既有 `rename_session` / `move_topic_to_group` 两个老工具**保留未动**（兼容），manage_sessions 是并行新面；后续 GC 语义对齐另行。

## 四、验收与残留

### 提交（分支 wt-892a，未 push，未合并）

| sha | 内容 |
|---|---|
| `981bbd740` | 切片1·schema 递归化 + 级联删除助手 + 项目分组 catalog schema |
| `c380cb021` | 切片2·manage_sessions 宿主半（9 action + 提案制 + 日志 + v2 事件）+ 验收测试 |
| `9b1c24743` | 切片3·agent 工具 + boot 接线 + desktop 6 处 wiring |

### 测试结果

- 本片新测全绿：`TestManageSessions*`（desktop，11 个）+ `TestManageSessionsTool*`（agent，3 个）+ `TestSessionGroupHierarchyValidation` / `TestProjectGroupCatalogValidation`（重写后的深度契约）。
- 深度契约旧测试已按新语义重写：depth-3 从「拒绝」改「接受」，新增三节点环 / 根下环 / 恰好 64 层通过 / 65 层拒绝四例。
- `internal/boot` 全包绿；`internal/agent` 全包除 2 个**预存红**（干净基线 main-v2-stable `1747f222d` 复现同样失败，与本片无关）：`TestToolCheckpointReloadRefreshesProjection`、`TestInterruptedRecoveryKeepsRedactedFailureSummary`。
- `desktop` 全包（`-timeout 30m`，1266s 跑完）：8 个 FAIL，**7 个在干净基线复现同样失败**（hop/lease×2/sandbox-roots/locale×2/session_collab），第 8 个 `TestSetActiveRemoteTabProceedsWhenCurrentSessionCannotPersist` 是 TempDir 清理竞态（`directory is not empty`），单跑在基线与本分支均 PASS。定向族（organization/collab/project-tree/本片 manage_sessions）基线与本分支均绿。

### 残留风险（B 片/后续须知）

1. **plan 是提案时点快照**：group_delete 从 confirm 到执行之间 roster 若被并发修改，执行按新 roster 重新计算（语义参数不变，成员可能出入）——UI 应在 confirm 前刷新提案。
2. **rollback 无写互锁**：只靠版本化保存校验；连续多笔写之后回退旧笔会重放历史。建议 UI 只对最新一笔提供一键回退。
3. 既有 `rename_session` / `move_topic_to_group` 工具与新面并存（未删，兼容期）；两处 rename/move 语义重复但实现同一宿主路径，行为一致。
4. 前端深度 2 假设（`sessionGroupTree.ts` / hierarchy 测试）与项目分组 localStorage 迁移都在 B 片范围（见第三节）。
5. GC 语义对齐、UI 菜单/拖拽多层级/级联确认弹窗：不在 892A 范围。
