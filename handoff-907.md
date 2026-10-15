# handoff-907 —— final-readiness 失败后侧栏永远 running、无完成提示（上游 8859 / PR11085）wt-907

- 日期：2026-10-15
- worktree：`github-repo/worktrees/wt-907`（分支 `wt-907`，基 `main-v2-stable` @ `6280772ea`，status 净）
- 语义权威：`docs/report/调研报告【非诊断】/上游修复类issue同款筛查-1.38.3后-20261014.md`（8859 条目 + 立项表 #11 P2）+ 上游 PR11085 diff（`gh pr diff 11085`，esengine/DeepSeek-Reasonix）
- 纪律：切片 commit、显式路径 add、禁 add -A/commit -a、禁 stash/merge/rebase、status 净；中文 message「907 」开头；源码改动走 Edit/Write

## 缺陷机理（上游 8859 三症状）

回合以 final-readiness 检查失败结束（交付检查未完成）时：

1. **无完成提示音**：turn_done 带 `err`（FinalReadinessError），而通知链只在 `!e.err` 时播成功音——`err` 分支直接静音，没有任何路径覆盖「停在等用户」的结局。
2. **侧栏永远 running**（上游根因 `session_catalog_runtime.go:230-231`：`running := status != ""`——任何非空 status 都渲染为 running，awaiting_delivery 也不例外）。上游 PR 正文说明该半在 1.x 已不复现（running 改由 controller active work 推导，awaiting_delivery 渲染独立徽标），**本件修复本体只有提示音**；侧栏半按验收①用测试坐实 fork 已回 idle。
3. 交付检查交互卡（#8851）——fork 已有（reducer turn_done final_readiness → delivery notice + continue_delivery）。

上游 PR11085 修法：`runtimeNotifications.ts` 加 `PAUSED_TURN_OUTCOMES = {final_readiness, recovery_paused, incomplete_read, completion_uncertain}`，turn_done 读 `outcome`：四类「等用户」结局播 attention chime（Settings 的「AI 提问」音），干净结束播 success，普通失败保持静音。

## fork 对接面（与上游结构差异，勿盲搬）

| 上游 | fork（wt-907 实测） |
|---|---|
| `lib/runtimeNotifications.ts`（createRuntimeNotifications owner） | **无此文件**；chime 决策内联在两处 onEvent：`App.tsx:1038-1040`（活树，`main.tsx → App`）与 `app-runtime/useRuntimeEventHandlers.ts:86`（app-runtime 分解树，合同上未挂载但测试读其源码、与活树双轨同步） |
| `app-runtime/useRuntimeNotifications.ts`（hook 转发 outcome） | 无此文件；fork 的 onEvent 直接收完整 `WireEvent`（types.ts:450 已声明 `outcome` union 含 final_readiness 等七值），**无需扩类型** |
| attention/success 音 | fork `lib/sound.ts` 已有 `playAttentionChime`/`playSuccessChime`（各受 localStorage 偏好门控，默认 off），复用不新建 |
| 侧栏 running | fork 已按 controller active work 推导：`catalogStateStatus`（desktop/runtime_state.go:153）schema1 路径返回 `runtimeDisplayStatus(...), state.ActiveWork()`——idle 控制器 + activity=awaiting_delivery → **running=false、status=awaiting_delivery**（scratch 实测）；前端 `projectTreeTopic.ts:383` dot 谓词不含 awaiting_delivery，`topicStatusLabel` 有独立「交付检查」文案。legacy/schema0/nil-ctrl 分支仍 `status != ""` 判 running，但活控制器恒走 schema1，不构成复现面 |

## 移植面（切片）

1. **切片 1：共享判定 + 两树接线（~20 行）**
   - `lib/sound.ts`：加 `PAUSED_TURN_OUTCOMES` Set + 纯函数 `turnDoneChimeChoice(err?: string, outcome?: string): "success" | "attention" | "none"`（语义=上游 diff：paused 结局→attention；无 err→success；有 err 且非 paused→none），带上游同款注释。
   - `App.tsx` onEvent turn_done 分支：`if (!e.err) playSuccessChime()` 改为按 choice 分派（attention→playAttentionChime，success→playSuccessChime）。
   - `useRuntimeEventHandlers.ts` 同步改（双轨一致，防 app-runtime 树复活时语义漂移）。
2. **切片 2：测试钉（新文件 `src/__tests__/turn-done-paused-chime.test.ts` + Go 侧栏测试）**
   - 前端：仿上游 runtime-turn-done-chime.test.ts——localStorage + FakeAudioContext 计数，四类 paused outcome 各播 attention（1）且不播 success（0）；干净 turn_done 播 success；普通 err 双静音；`turnDoneChimeChoice` 判定矩阵直测。
   - 侧栏回 idle（验收①）：Go 测试构造「final_readiness 结局 → tab.ActivityStatus=awaiting_delivery + 控制器 idle」→ `catalogControllerStatus` 返回 running=false；`catalogStateStatus` schema1 同断言。若 FAIL 则转修复件（当前 scratch 预判 PASS，属回归锁）。
3. **切片 3：回归**——`sound.test.ts`、`delivery-parked-turn-done.test.ts`、`delivery-continue-commands.test.tsx`、`controller-notices-heads.test.ts`、`project-tree-runtime.test.ts`、`app-chrome-tabs.test.ts` + `tsc --noEmit` + desktop 包 `go test ./desktop/ -run 'Catalog|TopicActivity|RuntimeState'`。

## PR11085 语义对照（验收 ④）

- **outcome 集合对照**：上游 `PAUSED_TURN_OUTCOMES = {final_readiness, recovery_paused, incomplete_read, completion_uncertain}`——fork `sound.ts` 逐字同集（测试断言 size=4 + 逐值判定）。
- **chime 分派对照**：上游 `if (paused.has(outcome)) attention; else if (!err) success`（err 且非 paused → 静音）——fork `turnDoneChimeChoice` 同序同判：paused→attention 优先，err→none，否则 success。
- **偏好门控对照**：上游「Both sounds stay off unless the user enabled them」——fork `playAttentionChime`/`playSuccessChime` 各受 localStorage 偏好门控（默认 off），测试钉「attention 偏好 off 时 paused 结局不发声」。
- **载体差异**：上游修在 `runtimeNotifications.ts`（owner）+ `useRuntimeNotifications.ts`（hook 透传 outcome）；fork 无这两个文件，判定收敛进 `lib/sound.ts`，接线落在两棵既有树——`App.tsx` onEvent（活树，main.tsx 唯一挂载）与 `app-runtime/useRuntimeEventHandlers.ts`（分解树，双轨防漂移）。fork 的 `WireEvent.outcome` 类型本就含七值 union，无需像上游 hook 那样扩 `& { outcome?: string }`。
- **测试对照**：上游 `runtime-turn-done-chime.test.ts`（驱动 createRuntimeNotifications + FakeAudioContext 数 contexts）——fork 对应 `turn-done-paused-chime.test.ts`（18 断言：判定矩阵 + 播放分派 + 偏好门控 + 两树接线源码合同）。
- **侧栏半对照**：上游 PR 正文「running 半在 1.x 已不复现，running 来自 controller active work，awaiting_delivery 渲染独立徽标」——fork 实测同语义：`catalogStateStatus` schema1 路径返回 `state.ActiveWork()`（idle 控制器 + awaiting_delivery → running=false），前端 `projectTreeStatusShowsDot` 不含 awaiting_delivery、`topicStatusLabel` 有独立「交付检查」文案。故 PR11085 代码改动只有提示音（上游 diff 三文件全是 chime 面）；侧栏半以 Go 回归锁（`final_readiness_sidebar_idle_907_test.go`）坐实。残留差异：fork `catalogStateStatus` 的 schema0/nil-ctrl legacy 分支仍 `status != ""` 判 running，但活控制器恒走 schema1，不构成复现面（记录在案，不动）。

## Commits

| commit | 内容 |
|---|---|
| `26b3e11fa` | handoff 计划落盘 |
| `3020a7563` | 切片1：sound.ts PAUSED_TURN_OUTCOMES+turnDoneChimeChoice；App.tsx/useRuntimeEventHandlers turn_done 分派接线 |
| `a2c2bc9e5` | 切片2：turn-done-paused-chime.test.ts（18 断言）+ final_readiness_sidebar_idle_907_test.go（2 测试） |

## 验收结果（2026-10-15/16）

1. **侧栏回 idle**：`TestFinalReadinessTurnEndsSidebarIdleAwaitingDelivery`（真实控制器跑完一轮 + sink 注入 final_readiness TurnDone → ActivityStatus=awaiting_delivery → 投影 running=false + status 徽标）与 `TestCatalogStateStatusIdleReadinessNotRunning`（schema1 idle+awaiting_delivery → running=false）双绿。
2. **提示音**：`turn-done-paused-chime.test.ts` 18/18 PASS——四类 paused 结局 attention 响、success 不误响、干净结束 success、普通失败双静音、attention 偏好 off 静音、两树接线合同。
3. **既有族回归**：sound(36)/controller-notices-heads/final-readiness-locale/project-tree-runtime(91)/app-chrome-tabs(113)/delivery-continue-commands 全绿；`tsc --noEmit` exit 0；bundle 棘轮 8 项 PASS（initial JS 576.4/576.8 KiB）。desktop 定向 go test：RuntimeState/TopicActivity/Readiness 全绿。
4. **PR11085 语义对照**：见上节，已保留。

### 预存红申报（非本件回归，基线 main-v2-stable@6280772ea 复现同 FAIL）

- `delivery-parked-turn-done.test.ts` 3 FAIL——基线同 3 FAIL（BASELINE-38.md 亦记录该 suite 3 红）；本件未触 useController/reducer。
- `TestCatalogTopicTitleLocalizesAtSidebarBoundary` / `TestCatalogManualDefaultTitleIsNotLocalized` FAIL——基线同 FAIL。
- `tsc-test-gate tsconfig.test.json` exit 1（基线 49 / wt 53 条基线外诊断）——差值 4 条全在 `composer-goal-toggle.test.tsx:2314`（该文件两仓逐字节一致；wailsjs 生成物仅基线仓存在且被 gitignore，属环境差），本件新增测试文件 0 诊断。

## 残留

- `catalogStateStatus` schema0/nil-ctrl legacy 分支仍 `status != ""` 判 running（上游旧缺陷形态）；活控制器恒走 schema1 无复现面，未动。
- 偏好默认 off：用户未开「AI 提问」音时 paused 结局依旧静音——与上游「Both sounds stay off unless enabled」一致，属设计而非缺陷。
