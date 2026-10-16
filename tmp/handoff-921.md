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

## 四、验收对照（2026-10-16 实测收口）

1. ✅ 存量会话 events 回 cap 内（真实文件副本实测，原件未动）：
   - 死会话 20260922-231913（103.5MB events / 39.2MB live）：兜底生产动作
     CompactSessionFile → **45.6MB，回 cap(50MB) 内**，freed 57.9MB；
   - 真超限 20260927-020815（98.3MB / 56.7MB live）：→ **63.5MB**，freed 34.9MB
     尾巴，仍超 cap 的 13.5MB 是 live 本体（479 治本方向，需拍板，如实申报）；
   - 死会话形态另有单测 `TestCompactSessionFileClearsUnreachableRowsUnderCap`
     （retire 并发头 → 12MB 夹具压回 live 水位、字节物理清除、转写完整性）。
2. ✅ 全局 eventsMb 回落：上述两会话合计 freed 92.8MB（1460 → ~1367 若在原件上
   执行）；三会话+20261005 型真超限合计可砍 ~120-150MB。live 本体部分不计入
   （不可非破坏回收）。
3. ✅ save-path 锁：兜底在空闲期跑（lease 空闲证明），不进保存路径；实测副本
   compact 的 lock_wait_ms=0、全程 5.4-5.7s；1817ms 锁等待的 Σmarshal 减项已由
   884 落地，剩余 save-path lock waited（13:36 4198ms）由 885 锁有界化承接。
4. ✅ 触发补强边界单测：`desktop/events_idle_sweep_test.go` 7 测——配置门
   （off/manual/auto 无 cap 全关）、决策矩阵（门/超限/busy/park 重入线 10%）、
   死会话回 cap + park 防重清、真超限 park、预算大者优先、忙/失败重试、
   under-limit 静默。

## 五、不在本单（残留申报）

- 真超限会话压进 cap 的破坏性手段（历史剪枝/大字段外置/转线）——479 治本方向，需拍板；
  本件后它们的稳态是「live 水位 + park」，不再无限增长尾巴，但 cap 之内装不下 live
  本体这件事没有任何非破坏机制能解决。
- 陈旧并发头 retire（884 残留 #1，语义减法需拍板）——sweep 只清已 retire 头的字节，
  不替用户做 retire 决定。
- save-path 锁等待本体（885 承接）。
- sweep 覆盖 auto+cap 模式（用户现场配置）；manual 模式的存量清理仍走存储面板手动
  入口（333 设计，不越权）。

## 六、commit 列表

1. `639d6fc93` 交接文档落盘
2. `07fca68ac` events_idle_sweep 循环 + lifecycle 挂载 + WARN remedy 补强（7 测）
3. `13e0f815a` 验收①端到端单测（死会话形态）
