<!-- 任务 38 R0 基线登记（纯登记，不改码）| 实测 2026-10-08 @ 5ffebe723 | worktree wt-38-r0-baseline -->
<!-- 用途：38 后续批（B1/R1a/R1b/C2/C3/D）每批动手前「当日重测基线」的漂移检测锚点。 -->
<!-- 纪律：本文件数字是 2026-10-08 快照，动手前必须按 §5 命令当日重测，与本表对比；行号随合并漂移，以当日实测为准。 -->

# BASELINE-38 —— App.tsx 组合边界 R0 基线

**实测环境**：worktree `wt-38-r0-baseline`，基线 commit `5ffebe723`（main tip），App.tsx 内容与方案实测点 8a47058ae 一致（两 commit 间 App.tsx 未动，行数同为 6191）。

## 1. 核心数字（2026-10-08 实测）

| 项 | 本表实测 | 方案数字（38 方案 §0 / 伞形 §1） | 差异说明 |
|---|---|---|---|
| App.tsx 行数 | **6191** | 6191 | 一致 |
| useState 声明 | **39**（wiring 审计，全读写正常） | 39 | 一致；`grep -c useState` 原始 40（含 1 行 import） |
| 前端测试文件数 | **543**（`.test.ts` 287 + `.test.tsx` 256；542 在 `src/__tests__/`，1 在 `src/components/TaskMonitorPanel.test.tsx`） | 543 | 一致。方案「约 543」口径 = 文件数，非用例数 |
| 前端测试用例数 | **PASS 行 10549**（543 suites 全执行，2026-10-08 带超时驱动实测；下界口径，见 §7） | 方案未单列 | harness 为手写断言（部分 suite 汇总式单行输出无逐条 PASS），无统一静态用例清单，10549 为可复现实测下界 |
| integrity 检查 | **727/727 全绿** | 约 727 | 一致 |
| 同名定义（伞形 A0 基线） | **4 处**：`CollabTaskCard` / `heartbeatNextRunAt` / `RuntimeState` / `VersionEntry`（748 文件 / 3203 导出名扫描） | 4 | 一致；**只许降不许涨** |
| 入口契约 | `skipped on the fork`（App.tsx 有意保持单体） | 同 | 一致 |
| `useSessionUndo.ts` 在树 | fork 停在 `27fa47f76`（245 行），落后上游一提交（上游 `2553bb31c` 增 `handleForkTurn` + `forkTurnForTab` port + `lib/forkTargets.ts`）；`lib/forkTargets.ts` fork 树**不存在** | 同 | R1a 同步范围 |
| `useInvocationMetadata` | App.tsx:1418 已消费（import 279） | 1418 | 一致，四 state 表第一项已销账 |

## 2. App.tsx 挂载的 integrity 锚位（12 条，check-fork-integrity.mjs 逐条提取）

均验证在场（integrity 727/727 即含此 12 条）。**全部落在选区/footer/dock/侧栏/Ctrl+F/版本面板/tab 关闭漏斗等区块，rewind 块与 todo 块零锚点**——R1b 换装零锚点重指。

| # | 脚本行 | feature | patterns 摘要 |
|---|---|---|---|
| 1 | 63 | task 369 选区快捷操作桥接线（App 设置回调） | `RunSelectionSideQuery(action, text, contextText)` / `setSelectionActionsEnabled` |
| 2 | 386 | 任务383 #5 classic footer icon-only + #8 automation→heartbeat 文案统一 | `sidebarCreation ? <span>{t("sidebar.trash")}` / `heartbeat.scheduler` |
| 3 | 443 | 任务245 面板键归一接线（App 侧） | `workspacePanelMemoryRoot(activeTab?.scope, activeTab?.workspaceRoot)` |
| 4 | 449 | 任务260 App dock 两 tab 接线 | `dockTabVisible("artifacts")` / `dockTabVisible("references")` / `SideFilesDockPanel` |
| 5 | 669 | 任务401 侧栏 logo 强调色 mask（App.tsx 渲染点） | 两个 `aria-label="Reasonix"` brand-logo span（workbench / 普通） |
| 6 | 700 | 任务320 左下角图标行入口 | `Mailbox size={16}` / `setCollabInboxOpen(true)` / `sidebar.collabInbox` |
| 7 | 712 | 任务320n 图标行按钮徽标接线 | `useCollabInboxUnreadCount` / `sidebar__utility-badge` |
| 8 | 850 | 任务399 Ctrl+F 快捷键注册（代码块域隔离） | `transcript.find` / `shouldIgnoreFindShortcutTarget` / `setTranscriptFindPulse` |
| 9 | 946 | 任务411 面板删除接线（App.tsx handler） | `handleDeleteVersion` / `DeleteInstalledVersion(version)` |
| 10 | 1001 | 任务440 面板接线（runtimes 过滤 + per-tab 停止） | `capsuleRuntimes={backgroundRuntimes.filter(` / `onCapsuleCancelRuntimeJob={cancelRuntimeJob}` |
| 11 | 1219 | 任务546 失效回落可见提示 | `tabBar.lastCwdFallback` |
| 12 | 1277 | 552 App 接线（关闭漏斗入栈+topic 导航重开） | `pushRecentClosedTab(...)` / `pruneRecentClosedTabs(...)` / topic kind 快照对象 |

## 3. rewind 块行号标定（R1b 换装的删除/重指面）

| 块 | 行号（本表实测） | 方案数字 | 说明 |
|---|---|---|---|
| state 块（3 state + ref + setter） | **3405-3434** | 3405 起 | `rewindSignal`(3405) / `rewindStatesByTab`(3411) / `rewindStatesByTabRef`(3412-3413) / `rewindCommittingByTab`(3414) / 派生(3415-3416) / `setRewindStateForTab`(3418) / `setRewindCommittingForTab`(3427) |
| `handleSessionRevertCommitted` | **3436-3447** | 3436-3447 | 一致 |
| `hydratePlaceholderActive`（hook 入参供体，**保留**） | 3449 | 3449 | 一致 |
| `commitThenSend` 守卫（改写点，**保留**） | 3704-3706 | 3706 | 新轮次作废 undo 槽 |
| `handleMessageAction` | **3730-3836** | 3730-3836 | 一致（107 行） |
| `handleEditPrompt` | **3838-3866** | 3838-3866 | 一致（29 行） |
| 横幅条件块 | **5375-5401** | — | `{!runtimeTransitioning && rewindState && (` → `)}` |
| 横幅内联 onUndo | **5381-5398** | 5382-5395 | 差 2 行：方案取内联主体，本表含 `onUndo: () => {` 起止行；换装时整段替换为 `handleUndoRewind`，以当日 sed 实测为准 |
| rewind prop 触点（消费点，重指不删） | 5273 / 5276 / 5277 / 5283 / 5286 / 5558 / 5626 / 5628 / 5897 / 5959 | 同 | `onRewind` / `rewindDisabled` / `running` 合成 / `rewindSignal` / `hasOlderHistory` / `onSessionRevertCommitted`(5897) |
| useController 供体解构 | 652-655 | 652-653 | `rewindForTab` / `rewindForTabDetailed` / `undoRewindForTab` |
| `RewindUndoState` type import | 169 | 169 | 换装后删除 |
| rewind 全文件引用 | `grep -c rewind`（区分大小写）= 41 行级命中；`-i` 口径 67 | 41 | 一致 |

## 4. todo 块行号标定（38 定「dismissedTodoKeys 不搬」，本标定供 D 阶段组件化参考）

| 块 | 行号（本表实测） | 说明 |
|---|---|---|
| imports | 38（`ListTodo`）/ 74（`TodoPanel`）/ 117（`parseTodos`）/ 119-129（`todoVisibility`）/ 274（`todoDismissalStorage`） | — |
| 实验开关 state | 771-773（`todoSidebarEnabled`，Task 259 boot 快照） | useState 计数内 |
| 设置接线 | 1261 / 1280-1282 / 1322（日志行） | — |
| dock 门控 | 1493-1501（`effectiveRightDockMode` / `dockTabVisible`） | — |
| 数据推导块 | **1968-2035**（注释 1969 起；`todoEntry` 1979 / `todos` 1990 / `todoArchive` 1998 / `dismissedTodoKeys` state 2011 / `todoKey`~`showTodos` 2012-2024 / `dismissTodos` 2025-2035） | 方案写「1975-2040 约 66 行」；本表按注释起行到 `dismissTodos` 收行实测为 1968-2035（68 行），口径差 7 行（方案含尾随空行/下一注释），换装/组件化时以当日实测为准 |
| 内联 TodoPanel 渲染 | **5364-5374**（`!todoSidebarEnabled` 分支） | — |
| dock todos tab + dock TodoPanel | **5741-5752 / 5803-5821** | — |
| `DismissTodoBatchForTab` 直调 | 2034 | 不搬实证一：直调桥而非权威协议 |

## 5. 当日重测命令（每批动手前照抄执行，对比本表）

```bash
cd desktop/frontend
wc -l src/App.tsx                                                # 基线 6191
node scripts/check-app-state-wiring.mjs                          # 基线 39（只降不涨；R1b 后应 36）
grep -c "useState" src/App.tsx                                   # 原始 40（含 import）
find src -name "*.test.ts" | wc -l                               # 基线 287
find src -name "*.test.tsx" | wc -l                              # 基线 256
node scripts/check-duplicate-definitions.mjs                     # 基线 4（只降不涨）
node scripts/check-app-entry-contract.mjs                        # skipped on the fork
node scripts/check-app-layers.mjs                                # 绿
cd ../.. && node scripts/check-fork-integrity.mjs                # 基线 727/727
grep -c 'file: "desktop/frontend/src/App.tsx"' scripts/check-fork-integrity.mjs  # 基线 12
grep -n 'file: "desktop/frontend/src/App.tsx"' scripts/check-fork-integrity.mjs  # 锚位行号随脚本漂移
# rewind/todo 块行号：
grep -nE "handleMessageAction = |handleEditPrompt = |handleSessionRevertCommitted = " desktop/frontend/src/App.tsx
# 全量测试：发现式 495 suites + 专属脚本 47 suites + components 1 = 543 文件。
# ⚠️ run-tests.mjs 原样跑会在 checkpoint-turn-transcript 卡死（见 §7）；重测用 45s 超时包裹逐 suite 执行，
#    结果与 §7 登记的红集/挂起集比对（不新增、不扩大即通过），不要假设全绿。
```

## 6. 与伞形 A0 的合并口径说明

伞形 A0 = ①审计基线固化（本表 §1 即其今日版）+ ②新增 `check-fork-module-boundary.mjs` 守卫并入 `check:app-layers`。**②属改码（新增脚本），不在本批「纯登记不改码」边界内**，留待 A0 正式批；本批仅落地①的登记面。伞形基线 37/4 中「37」为 10-07 旧值，今日实测 39（期间 merge 引入），以本表为准。

## 7. 全量跑测实况与基线红集（R1b 验证层的基准修正，本批最重要的新增发现）

543 suites 于 2026-10-08 以带超时驱动（每 suite 45s，镜像 run-tests.mjs 的 stub 逻辑）全部执行完毕：**PASS 行 10549、FAIL 68（18 suites）、超时挂起 7（断言全过后事件循环不退出）**。经抽样归因（bundle-contract 单独复跑 + 主仓库 8a47058ae 复跑同样红），**判定为 fork 树预存真红，非本批引入（本批零代码改动）、非本地环境差异**：

- **红因实证**（bundle-contract.test.ts）：断言 `.sidebar--workbench .sidebar__utility-row` 为 `repeat(3, …)`，而 styles.css:34265 实为 `repeat(4, …)`（任务 320 UI 规格 20261002 改 4 列，测试未同步）——确定性失败，与运行环境无关。
- **对方案 §3.4 的修正**：R1b 验证层「`pnpm test` 全量必绿」在基线即不成立。**R1b 及后续批的全量验证口径改为：红集与本表登记一致（不新增、不扩大），重跑仅比对 fail 集与挂起集**；红集/挂起的处置（修测试或修码）不在 38 边界内，登记待主对话裁决。

**18 个红 suites（68 FAIL）**：bundle-contract(1) / composer-goal-toggle(6) / composer-run-strip(4) / composer-session-draft(2) / context-window-ring(4) / delivery-parked-turn-done(3) / provider-editor-model-picker(2) / remote-project-tree(19) / search-footnotes-render(6) / session-experience(1) / settings-refresh-snapshot(1) / subagent-progress-card(3) / tab-switch-hydration(6) / tab-switch-session-rebind(2) / task258-message-presentation(1) / task562-experiment-tiers(3) / topic-activation(3) / use-controller-identity-retry(1)。

**7 个挂起 suites**（PASS 全打完、进程不退出，45s 强杀；疑 jsdom 定时器/句柄泄漏，逐 suite 归因不在本批）：checkpoint-turn-transcript / completion-summary-ui / transcript-collapse-all / transcript-fold-preference / transcript-materialization / transcript-process-fold / turn-result-notice-collapse。

**口径与可复现性**：① PASS 行按「行首 PASS」计数，汇总式输出的 suite（如 active-tab-mirror 仅打一行 summary）内部断言未计入，故 10549 为下界；② 驱动为临时脚本未入仓库（本批纯登记不改码），重测可按 §5 命令 + 45s 超时包裹复现；③ 用 `run-tests.mjs` 原样跑会在第 33 个 suite（checkpoint-turn-transcript）永久卡死，原样全量跑在基线不可行——这本身即基线事实。
