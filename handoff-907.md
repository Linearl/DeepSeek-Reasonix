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

## PR11085 语义对照（验收 ④，交付时保留在本文件）

（切片完成后回填：outcome 集合对照 / chime 分派对照 / 失败静音边界对照 / 侧栏半上游「不复现」声明与 fork schema1 实测对照）

## 计划（切片）

1. handoff 落盘（本文件）→ commit。
2. 切片 1：sound.ts 判定 + 两树接线 → tsc → commit「907 切片1：…」。
3. 切片 2：前端 chime 测试 + Go 侧栏 idle 测试 → 定向跑绿 → commit「907 切片2：…」。
4. 回归：前端 delivery/notice 族 + tsc + desktop 定向 go test（预存红单独申报）。
5. 回填本文件：commits 表 + 验收结果 + PR11085 对照 + 残留。

## 验收

1. 构造 readiness 失败 → 侧栏回 idle（Go 测试：awaiting_delivery + idle ctrl → running=false）。
2. 可见通知/提示音：paused 结局播 attention chime（复用 sound.ts 通道，偏好门控不变）；交付卡既有 delivery-parked 测试覆盖可见通知。
3. 既有 delivery/notice 族测试全绿。
4. PR11085 语义对照说明在本文件。
