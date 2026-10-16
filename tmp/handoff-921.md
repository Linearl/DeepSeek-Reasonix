# 任务 921 交接（wt-921）

- 日期：2026-10-16
- worktree/分支：`github-repo/worktrees/wt-921`（基线 `main-v2-stable @ f8a09feec`，已含 884 修复 commit `0394c9649`）
- 任务：884 遗留两项——存量超限 events 会话治理 + 折叠触发条件补强

## 一、884 交付记忆对齐

884（`wt-884/tmp/handoff-884.md`）交付要点：

1. **真根因**：`sessionDAGLogOversized` 旧口径把每个 live head 整链各自序列化后相加（Σ），
   共享前缀重复计数 → contentBytes 虚高数倍 → `content×2≥logSize` 恒成立 → 旋转永久禁死。
2. **修法**：union 去重估算（`rotatedContentBytesLocked`）+ 地板早退；只放行过去被误拒的
   旋转，fold 纪律（log≤2×content 拒绝）保留。
3. **884 残留（本单接手）**：
   - 真超限会话（live content 本身 > cap50）任何非破坏机制压不进 cap——WARN
     `fold would not shrink it` 只留置不兜底；
   - 无 save 即无旋转：`maintainDAGLog` 只在保存路径上跑，**死会话**（如 20260922-231913
     104MB，10-1 后无写入）永远不会再触发旋转；
   - 884 修的是「虚高误拒」，没修「压不进之后怎么办」——任务书①存量治理②触发补强。

## 二、现场实测甄别（2026-10-16 13:0x，本机只读）

884 修复已在跑（20261010 从 238MB 降到 100MB 实证生效），但三会话均属**真超限类**：

| 会话 | events 现状 | WARN contentBytes（union 真实口径） | jsonl | 判定 |
|---|---|---|---|---|
| 20261005-collab | 110MB | ~74MB（union 上界） | 46MB | live>cap，rotate 后 ~77MB 仍超 |
| 20261010-collab | 100MB（原 238） | ~86MB | 83MB | live>cap，rotate 后 ~86MB 仍超 |
| 20260927-collab | 98MB（WARN 实录 102.5） | 64.5MB（实录） | 57MB | live>cap，rotate 后 ~65MB 仍超 |
| 20260804-cooking | 60MB | 54.9MB（实录） | 42MB | live>cap，rotate 后 ~55MB 仍超 |
| 20260922-231913（死会话） | 104MB | — | 40MB | **无 save 无旋转**，兜底可回 cap 内 |

全局 eventsMb=1460（limit 512）。20260927 的 WARN 今天仍在刷（`content×2≥logSize` 拒绝路径）。

**甄别结论**：
- 真超限会话兜底 rotate 后 events≈union（可砍 17-37% 尾巴），仍超 cap 的部分是 live
  transcript 本体（479 治本方向：剪枝/大字段外置/转线，**需用户拍板，不入本单**）；
- 死会话类兜底后可直接回 cap 内；
- 空闲期兜底动作选型 = **强制 rotate**（`CompactSessionFile` → `SaveRewriteCompact` →
  `maintainDAGLog(mode=RewriteCompact)` 绕过 oversized 判定强制 `rotateSessionDAG`），
  非 truncate 非归档：rotate 只丢弃不可达节点（现有 CAS/原子写/lease 保障），数据
  完整性由既有机制承接；truncate 是投影语义不缩落盘（884 已证），归档改变用户可见
  会话结构，均不选。

## 三、改动设计

1. **`desktop/events_idle_sweep.go`（新增）**：空闲期兜底 sweep loop（照抄
   cold_cache_compact_loop 模式：unconditioned ticker + 每 tick 现读配置 + park/inFlight）。
   - 门：`EventsAutoRotationSnapshot` mode==auto 且 capMB>0（用户现场配置；off/manual
     尊重 333 设计不越权）；
   - 扫描：`knownSessionDirs()` 全部项目目录（三会话横跨 global-workspace 与
     cooking_action_recognition 两个 root，active-dir-only 不够）；
   - 对象：`EventsLogAboveThreshold` over-limit + 非 open/非 lease-live + 未 park；
   - 动作：`agent.CompactSessionFile`（lease 即空闲证明，复用手动 repair 入口，零新写路径）；
   - park 语义：清完记 attemptSize=after，`currentSize ≥ attemptSize×1.1`（与
     foldNoShrinkReWarnGrowth 同款 10% 重警语义）或 activityStamp 变化才重入；
   - 每 tick 处理上限（防多个 100MB replay 堆积）；freed/结果 slog Info 落地。
2. **`desktop/app_lifecycle.go`**：`startupStartBackgroundLoops` 挂 `startEventsIdleSweepLoop`。
3. **`internal/agent/session_events.go`**：fold-no-shrink WARN 的 remedy attr 补
   「idle sweep 会在空闲期自动兜底」一句（前缀 grep 契约不动）。
4. **测试**：`desktop/events_idle_sweep_test.go`——决策矩阵（门/park/re-arm/busy）+
   注入式 compact 模拟（照 runEventsRepairRounds 模式）覆盖「真超限兜底到 live 水位后
   park」「死会话回 cap 内」两形态。

## 四、验收对照

1. ✅ 存量会话 events 回 cap 内：死会话类（20260922 型）兜底后回 cap 内（单测模拟+真实
   文件副本实测）；真超限类（三会话型）兜底到 union 水位、砍 17-37% 尾巴，仍超部分
   如实申报为 live 本体（479 方向，需拍板）。
2. ✅ 全局 eventsMb 回落：兜底砍掉的尾巴计入（sum 下降，实测/模拟给数）。
3. ✅ save-path 锁：兜底在空闲期跑、不在保存路径；884 已把 Σmarshal 降为一遍 union
   （1817ms 锁等待同源减项）；当前 desktop.log 仍有 save-path lock waited（13:36
   4198ms）——该面由 885 锁有界化承接，本单互补验证=兜底不新增保存路径争用。
4. ✅ 触发补强边界单测：sweep 决策函数矩阵（auto 门/over-limit/busy/park/re-arm）。

## 五、不在本单

- 真超限会话压进 cap 的破坏性手段（历史剪枝/大字段外置/转线）——479 治本方向，需拍板；
- 陈旧并发头 retire（884 残留 #1，语义减法需拍板）；
- save-path 锁等待本体（885 承接）。
