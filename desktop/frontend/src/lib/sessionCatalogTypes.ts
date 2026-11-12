import type { ProjectNode } from "./types";

export interface SessionCatalogStatus {
  state: "opening" | "ready" | "degraded" | "rebuilding" | "closed" | string;
  mode?: "disk" | "memory" | string;
  revision: number;
  indexed: number;
  total: number;
  repairPending: number;
  unindexedTargetCount?: number;
  repairActive?: number;
  repairDeferred?: number;
  repairBlocked?: number;
  nextRepairAt?: number;
  repairReason?: string;
  sourceCount?: number;
  lastRepairAt?: number;
  canRebuild?: boolean;
  lastError?: string;
  quarantinedPath?: string;
}

export interface ProjectTreeSnapshot {
  revision: number;
  projects: ProjectNode[];
  catalog: SessionCatalogStatus;
  indexed: number;
  total: number;
  indexingDone: boolean;
}

export interface ProjectTopicPageRequest {
  scope: "global" | "project" | string;
  workspaceRoot?: string;
  cursor?: string;
  limit?: number;
  query?: string;
  timeFilter?: string;
  sortMode?: "created" | "updated" | string;
}

export interface ProjectTopicPage {
  items: ProjectNode[];
  nextCursor?: string;
  revision: number;
  complete?: boolean;
  readyDirectories?: number;
  pendingDirectories?: number;
  failedDirectories?: number;
}

export interface ProjectTopicKey {
  scope: "global" | "project" | string;
  workspaceRoot?: string;
  topicId: string;
  path?: string;
  recordClassification?: boolean;
}

export interface ProjectTreeChangedV2 {
  revision: number;
  roots: string[];
  reason: string;
}

/** desktop/project_tree_reload.go ProjectTreeReloadResult 的 TS 镜像（tl1363）：
 * 手动「重载会话列表」的回执——扫除了哪些影子条目、保护了多少条、几个目录
 * 重挂对账。 */
export interface ProjectTreeReloadResult {
  scope: string;
  workspaceRoot?: string;
  removedTopicIds: string[];
  skippedPinned: number;
  skippedReferenced: number;
  reconciledDirs: number;
}

export interface ProjectRuntimeTopic {
  scope: "global" | "project" | string;
  workspaceRoot?: string;
  node: ProjectNode;
}

export interface ProjectTreeRuntimeSnapshot {
  revision: number;
  topics: ProjectRuntimeTopic[];
}

export interface SessionGroup {
  id: string;
  title: string;
  topicIds?: string[];
  /** Task 350: display-only nesting (层级≠指挥权). Optional and additive —
   * flat data has no field, and the backend caps the tree at depth 2. */
  parent?: string;
}

export interface ProjectGroupsSnapshot {
  groups: SessionGroup[];
  revision: number;
  applied: boolean;
}

export interface SessionCatalogBindings {
  GetRuntimeStateSnapshot?(): Promise<import("./runtimeStateStore").RuntimeProjection>;
  SyncRuntimeState?(): Promise<import("./runtimeStateStore").RuntimeProjection>;
  GetProjectTreeSnapshot(): Promise<ProjectTreeSnapshot>;
  GetProjectTreeRuntimeSnapshot?(): Promise<ProjectTreeRuntimeSnapshot>;
  ListProjectTopics(req: ProjectTopicPageRequest): Promise<ProjectTopicPage>;
  GetTopicSummary(key: ProjectTopicKey): Promise<ProjectNode>;
  GetSessionCatalogStatus(): Promise<SessionCatalogStatus>;
  RebuildSessionCatalog(): Promise<void>;
  /** tl1363（用户 20261102 拍板）：手动「重载会话列表」——清零绑定影子条目
   *  + 同步注册投影 + 重挂活会话 + 发 reason="metadata" v2 事件。可选：老
   *  mock/浏览器开发面没有该方法，调用点 typeof 守卫，失败照常全量重拉。 */
  ReloadProjectTreeSessions?(scope: string, workspaceRoot: string): Promise<ProjectTreeReloadResult>;
  /** tl1543：降级横幅「重新扫描」出口——走既有 reconcile 通道（Go 侧
   *  requestProjectTreeCatalogRefresh 同款原语：逐目录 requestSessionCatalogReconcile
   *  + 元数据同步 + 侧栏唯一认的树事件）。可选：浏览器开发 mock 无后端队列。 */
  RetryStartupCatalogScan?(): Promise<void>;
  /** desktop topic inventory 对账（desktop/topic_inventory.go）。可选方法：
   *  wails 生成物已含这两个绑定，TS 侧声明此前缺失，bridge.ts 的
   *  _CheckGenToApp 自检（AssertNever<Exclude<GeneratedAppKeys, keyof AppBindings>>）
   *  在重新生成 wailsjs 后会报 never 约束错误——2026-10-06 任务 539 批次补齐。 */
  GetTopicInventoryMismatches?(): Promise<TopicInventoryReconcileResult>;
  ReconcileTopicInventory?(): Promise<TopicInventoryReconcileResult>;
}

/** desktop/topic_inventory.go TopicInventoryMismatch 的 TS 镜像 */
export interface TopicInventoryMismatch {
  kind: string;
  scope: string;
  workspaceRoot?: string;
  topicId?: string;
  tabId?: string;
  sessionPath?: string;
  detail: string;
  handling: string;
}

/** desktop/topic_inventory.go TopicInventoryReconcileResult 的 TS 镜像 */
export interface TopicInventoryReconcileResult {
  reconciledAt: number;
  scannedTabs: number;
  scannedTopics: number;
  sessionCatalogOpen: boolean;
  indexWriteFailures: number;
  mismatches: TopicInventoryMismatch[];
}

export interface ProjectTreeOrganizationBindings {
  ReorderTopics(scope: string, workspaceRoot: string, orderedTopicIDs: string[]): Promise<void>;
  ListProjectGroups(scope: string, workspaceRoot: string): Promise<SessionGroup[]>;
  SaveSessionGroups(scope: string, workspaceRoot: string, groups: SessionGroup[]): Promise<void>;
  GetProjectGroups?(scope: string, workspaceRoot: string): Promise<ProjectGroupsSnapshot>;
  SaveSessionGroupsVersioned?(
    scope: string,
    workspaceRoot: string,
    expectedRevision: number,
    groups: SessionGroup[],
  ): Promise<ProjectGroupsSnapshot>;
  /** Task 19 / 144: file a session into a collaboration group, creating it when
   * the id is unknown. groupID is authoritative when given; groupTitle is only
   * used to create a new group, so a renamed group stays reachable by id. */
  AddTopicToGroup?(
    scope: string,
    workspaceRoot: string,
    topicID: string,
    groupID: string,
    groupTitle: string,
  ): Promise<void>;
  /** Task 170: move a session between groups (remove from every other group,
   * then file into the target). Optional like AddTopicToGroup; the drift check
   * in bridge.ts requires the key because wails generates the binding for
   * App.MoveTopicToGroup. */
  MoveTopicToGroup?(
    scope: string,
    workspaceRoot: string,
    topicID: string,
    groupID: string,
    groupTitle: string,
  ): Promise<void>;
  // ── Task 892-A: manage_sessions 直连面（handoff-892a §2.2）────────────────
  // 全部可选：老 mock/浏览器开发面没有这些方法，调用点 typeof 守卫 + 既有
  // CAS/localStorage 回退；wails 运行时注入的 window.go.main.App 在 A 片
  // 编译后具备。字段拼法（handoff §2.1）：工具入参 parent_id / 视图 parentId /
  // 存储 parent —— 这里的签名是直连面（parentId）。
  /** list 动作的宿主面：全部 roster + 项目分组 catalog + 会话归属。 */
  ManageSessionsList?(): Promise<ManageSessionsListResult>;
  /** group_create（kind=session）：scope 缺省 "global"；workspaceRoot 仅 project
   * 作用域需要。parentID 空 = 顶层；成环/超深由后端校验拒绝。 */
  CreateSessionGroup?(
    scope: string,
    workspaceRoot: string,
    groupID: string,
    title: string,
    parentID: string,
  ): Promise<ManageSessionsGroupResult>;
  RenameSessionGroup?(
    scope: string,
    workspaceRoot: string,
    groupID: string,
    title: string,
  ): Promise<ManageSessionsGroupResult>;
  /** group_delete 提案制：confirm=false 只返回 plan（零写入），confirm=true 才执行。 */
  DeleteSessionGroup?(
    scope: string,
    workspaceRoot: string,
    groupID: string,
    cascade: boolean,
    confirm: boolean,
  ): Promise<ManageSessionsGroupResult>;
  /** reorder_session：index 越界自动收敛，stale drag 不会失败。 */
  ReorderTopicInGroup?(
    scope: string,
    workspaceRoot: string,
    topicID: string,
    groupID: string,
    index: number,
  ): Promise<ManageSessionsGroupResult>;
  /** group_create（kind=project）：roster 是 desktop-projects.json 顶层 catalog。 */
  CreateProjectGroup?(groupID: string, title: string, parentID: string): Promise<ManageSessionsGroupResult>;
  RenameProjectGroup?(groupID: string, title: string): Promise<ManageSessionsGroupResult>;
  DeleteProjectGroup?(groupID: string, cascade: boolean, confirm: boolean): Promise<ManageSessionsGroupResult>;
  /** 操作回退：恢复 opID 那笔的 before 快照；无写互锁，尽快回退最新一笔。 */
  RollbackManageOperation?(opID: string): Promise<ManageSessionsGroupResult>;
  /** 单一 dispatch（与 agent 工具同面）：9 action 见 handoff §2.3 schema。 */
  ManageSessions?(action: string, args: string): Promise<string>;
  /** tl1560 返修②（用户裁决项目间互迁）：把一个分组（组壳 + 全部成员对话）
   * 在分区之间整体迁移——源 = (scope, sourceRoot)（global 区 root 传空），
   * 目标 = targetWorkspaceRoot（空 = 迁回 global）。后端一次原子事务（会话
   * 文件搬家 + 组切换 + 两侧 GroupsRevision 递增 + topic 注册搬家），失败
   * 整体回滚。可选：老 mock/浏览器开发面没有该方法，菜单入口 typeof 守卫
   * 隐藏。 */
  MigrateSessionGroup?(scope: string, sourceRoot: string, groupID: string, targetWorkspaceRoot: string): Promise<SessionGroupMigrationResult>;
}

/** tl1560：desktop/session_group_migrate.go SessionGroupMigrationResult 的
 * TS 镜像——迁移回执（只在整体成功时返回）。skippedTopics = 显式留在源
 * 分区的成员（迷路/cleanup-pending/转录缺失，返修①：落盘名册与实际搬迁
 * 清单一致）。 */
export interface SessionGroupMigrationResult {
  groupId: string;
  groupTitle: string;
  targetWorkspaceRoot: string;
  migratedTopics: string[];
  movedSessions: number;
  skippedTopics?: string[];
}

// ── Task 892-A 视图（Go struct → TS，handoff §2.2）──────────────────────────

export interface ManageSessionsTopicView {
  topicId: string;
  title: string;
  order: number;
}

export interface ManageSessionsGroupView {
  id: string;
  title: string;
  parentId?: string;
  depth: number;
  order: number;
  topics: ManageSessionsTopicView[];
}

export interface ManageSessionsProjectGroupView {
  id: string;
  title: string;
  parentId?: string;
  depth: number;
  sortOrder: number;
}

export interface ManageSessionsSessionView {
  topicId: string;
  title: string;
  scope: "global" | "project" | string;
  workspaceRoot?: string;
  groupId?: string;
  groupPath?: string[];
  order: number;
}

/** ManageSessionsList 的 groups 键：`"global"` 或 `"project|<root>"`。 */
export interface ManageSessionsListResult {
  groups: Record<string, ManageSessionsGroupView[]>;
  projectGroups: ManageSessionsProjectGroupView[];
  sessions: ManageSessionsSessionView[];
}

/** group_delete 的提案：deleted 必有；promoted=升顶子组；batch=批量阈值(≥10)。 */
export interface DeleteGroupsPlan {
  deleted: string[];
  promoted?: string[];
  unfiledTopics?: string[];
  batch?: boolean;
}

export interface ManageSessionsGroupResult {
  kind: "session" | "project" | string;
  scope?: string;
  workspaceRoot?: string;
  groupId: string;
  title?: string;
  parentId?: string;
  applied: boolean;
  plan?: DeleteGroupsPlan;
  opId?: string;
}

// SessionReference is a session selected via @ past:chats for context injection.
export interface SessionReference {
  path: string;
  title: string;
  preview?: string;
  turns?: number;
  turnsState?: "unknown" | "valid" | "corrupt" | string;
  createdAt?: number;
  lastActivityAt?: number;
}
