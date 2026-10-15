# 902 验证计划：权限 preset 关闭软件/切页后静默变「工作区内修改」（data-loss 级）

- 任务：902（P1 desktop，批 33 验证先行件）
- 上游：issue 10335 / PR 10825（同款缺陷，desktop 侧）
- 分支：wt-902（基于 main-v2-stable）
- 日期：2026-10-15

## 缺陷描述（上游）

关闭软件或切换页面后，权限 preset 被静默改回「工作区内修改」（workspace-edit），用户此前选择的更严格/更宽松 preset 丢失——data-loss 级别（权限静默放宽可能放行写操作）。

## fork 现状（结构不同构）

- fork **无** `desktop_persistence_state.go` / `SetPermissionPresetForTab` 符号（上游持久化路径不存在同名实现）。
- fork 有同族逻辑：`app_modes.go:157-161` 的 autoapprove→Yolo 重算逻辑。
- **是否同病未知**——本任务只做验证，先复现再定修法；复现不了出报告销项，不硬造修复。

## 验证计划（三步）

### 第一步：读码层定性
1. 读 `app_modes.go:157-161`（autoapprove→Yolo 重算）逐行定性：状态重建时是否会覆盖用户已设的权限 preset。
2. 追权限 preset 持久化链：preset 从哪读、写到哪、重启/切页（会话/tab 切换）后从哪恢复；确认恢复路径上是否存在「重算覆盖用户值」的写入。
3. 产出：证据链（文件:行号 + 调用链），定性同病 / 不同病 / 无法判定。

### 第二步：复现层测试
1. 单元测试模拟：「设权限 preset → 重启等价（状态序列化往返 / 状态重建）→ 查 preset」。
2. 无头环境做不了的真实 UI 操作（关软件、切页），用状态重建路径的代码级等价测试代替。
3. 判定标准（可证伪）：
   - **复现**：重建后 preset ≠ 用户所设值（被改回 workspace-edit 或被重算覆盖）。
   - **不复现**：重建后 preset == 用户所设值，且读码确认无旁路写入。

### 第三步：二选一结论
- **复现** → 转修复件：对照上游 PR 10825 的 persistence+fence 修法，出修复 + 测试，中文 commit「902 」开头。
- **不复现** → 出报告销项：报告落 `docs/report/zcode交付/`，附测试输出/断言日志作为实证，不做无缺陷的硬造修复。

## 验收标准

- 复现/不复现二选一**有实证**：测试输出、断言日志或代码级证据链（文件:行号），不许口头结论。
- 若不复现：报告路径 `docs/report/zcode交付/`，内容含读码证据链 + 测试输出。
- status 净、显式路径提交、中文 message「902 」开头、禁 stash/merge/rebase。

## 记录

- [x] 第一步：worktree wt-902 创建，本计划落盘
- [x] 第一步：读码定性（app_modes.go + preset 持久化链）
- [x] 第二步：复现测试（desktop/preset_persistence_repro_902_test.go，7 组全通过，commit c4858a503）
- [x] 第三步：二选一结论 = **不复现**，出报告销项

## 最终结论（2026-10-15）

**不复现**。七组状态重建往返测试全部保值：

1. 重启等价（desktop-tabs.json entry 直读恢复）——yolo/auto/ask 三档均保值（T1/T1b）；
2. 切页等价（BranchMeta sidecar 切走写/回读）——保值（T2）；
3. 打开会话等价（applyTabSessionProfile 读 session 自己 sidecar）——保值（T3）；
4. 前端水合后回推（SetComposerProfileForTab 幂等）——保值（T4）。

`app_modes.go:157-161` 重算只在显式 SetModeForTab 触发，重启/切页恢复路径均不经过（读码证据链见报告）。

**残留风险（销项但记录）**：
- R1：SetComposerProfileForTab 无会话围栏（T5 实证可被未水合默认值打掉 yolo 并持久化）；前端有 controllerReady/pending/reconcile 三重护栏，真实窗口需前端测试证伪，本次不做。
- R2：SetModeForTab yolo→ask 联动降档会持久化（T6）；显式调用才触发，若未来出现「切页后自动推 SetMode」的前端改动即成真缺陷。

报告：`docs/report/zcode交付/zcode交付-902-权限preset静默回退复现验证-20261015.md`（工作区）。

## 读码层结论（2026-10-15）

### 上游 #10335 病灶（PR #10825 描述）

- 权限 preset 属于**会话**，但值存**tab surface**；`OpenSession` 把出面 surface 的 preset 套到被开会话；New Chat surface 强制 workspace-write。
- renderer 侧 composer profile 表按 tab 键控，导航后的 profile push 把上一个会话的 preset 推给新会话。
- 修法：per-SessionID 记录（`session_preset_store.go`）+ 会话围栏（`SetPermissionPresetForTab(expectedSessionID)`）+ 新绑定会话不吃 surface 值。

### fork 无同名结构，但有同族三条链（逐条定性）

fork 无 `SetPermissionPresetForTab` / `desktop-session-presets.json`；权限轴是 `toolApprovalMode`（ask/auto/yolo），持久化两处：

1. **`desktop-tabs.json` 的 `ToolApprovalMode` 列**（`persistedDesktopTabEntry` → `persistedToolApprovalMode`：ask 存空串，auto/yolo 存值）——重启恢复入口 `restorePersistedTabs`（app_tabs.go:146-149）直接取 entry 值，**不吃 surface、不吃 sidecar**。
2. **session BranchMeta sidecar 的 `ToolApprovalMode`**（`saveTabSessionMeta` → `saveTabSessionMetaSnapshot`）——切页 rebind 前写旧 session（app_session_rebind.go:118/187），打开新 tab 时 `applyTabSessionProfile(loadTabSessionProfile(path))`（tabs.go:2538）从 sidecar 恢复——**per-session 语义，与上游修后方向一致**。
3. **前端启动回推**（App.tsx:2166-2181）：controllerReady 后 `SetComposerProfileForTab(activeTabId, collab, toolApprovalMode, goal)`——后端 `SetComposerProfileForTab`（app_modes.go:261）**直接覆盖 tab.toolApprovalMode 并 saveTabsLocked 持久化，无会话围栏**。若回推发生在 tabMetas/meta 水合之前（默认 `defaultComposerProfile.toolApprovalMode="ask"`），即上游 renderer 泄漏同族；水合若再读被覆盖后的 meta，则锁死为 ask（data-loss）。

### app_modes.go:157-161 重算逻辑定性

`SetModeForTab` 内：新 mode 带 autoapprove → yolo；否则**当前 approval 是 yolo → 强制降 ask**。这是显式模式切换时的联动降档（非静默），只有 `SetModeForTab` 被调用才触发——启动回推走 `SetComposerProfileForTab` 不经过它。**是否构成「关闭/切页后变默认」需测试判定**：关键是（a）重启往返 entry 值是否保值；（b）`SetComposerProfileForTab` 无围栏覆盖是否可被默认值打回并持久化。

## 全包回归核对（补录）

`go test -timeout 30m ./desktop` 全包 10 个 FAIL 逐个在基线 main-v2-stable（6280772ea）复跑：9 个基线同样红（预存），1 个（TestSetActiveRemoteTabProceedsWhenCurrentSessionCannotPersist）高负载偶发、两边单跑均过。**wt-902 无回归**。
