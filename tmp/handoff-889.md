# 任务 889 handoff（wt-889 / effort read timed out 复发治理·监测机制）

状态：**已完成终验**（2026-10-14）。写码+测试+工具+全量包级测试（预存红归属实证）全部落地。
分支：`wt-889`（基于 main-v2-stable @ 73ff458bd；未合并，交由用户指定会话处理）。

## Commit 列表

1. `655214c39` 889 监测切片1：effort read 分段打点补齐——breakdown 增 reconcile 子段（memo key / walk+apply，settled-skip 此前零观测）与 snapshot 子段（wait/stat/load）+ after_timeout 配对；超时 warn 携带在途 phase trail；reconcile 观测实现从 tabs.go 纯迁出（tabs.go 净减 84 行）；零行为变更，断言测试 6 个
2. `1ee04725a` 889 监测切片2：timed out 一键取数工具 `tools/effort-timeout-report`——按日/日×时段统计 + 慢读段归因 top N；解析单测 3 个
3. （终提交）本 handoff + config_snapshot.go 的 snapshotTiming 注释归位（纯注释排布修正）

## 验收对照

| 验收 | 结论 | 证据 |
|---|---|---|
| ① 监测机制：拥堵期新恶化能定位到 reconcile/snapshot 哪段哪步（打点断言测试） | ✅ | breakdown 新字段 memo_ms/reconcile_walk_ms/memo_hit/snapshot_wait_ms/snapshot_stat_ms/snapshot_load_ms/after_timeout；超时 warn 携带 `phases=`（在途阶段+各段时长）；reconcile 内部新增 phase=memo/memokey 慢日志（settled-skip 此前零观测）。测试：TestEffortReadBreakdownLogThreshold（字段断言）、TestReconcileObservedSplitsMemoKeyFromWalk、TestEffortReadMarksPhasesOnTrail（全链 marks：start/reconcile/memo/snapshot[.load]/model/done）、TestCachedConfigForRootTimedSplitsWaitStatLoad、TestEffortTimeoutWarnCarriesPhaseTrail、TestEffortPhaseTrailGenerationOwnsReplacement（×5 稳定） |
| ② timed out 聚合统计一键取数（日/时段） | ✅ | `go run ./tools/effort-timeout-report`（默认扫 MemoryUserDir/logs/desktop + 滚动文件；-since/-top/-min-ms）。真实日志实跑：10-13=7、10-14=71；10-14 分时 10时6/11时11/13时10/**16时21**/17时12；top5 慢读带段归因。单测 3 个 |
| ③ 慢因结论（IO 面定性/排除清单+最强假设） | ✅ | 见下节「慢因结论」 |
| ④ 零行为变更（纯监测+分析） | ✅ | 判定/回退/缓存契约均未动；reconcile/snapshot 为「观测旁路 + 纯迁出」；工具只读。repolint 相对 baseline：essay 持平(7633)、file-size -84、struct-state 持平(153)、无新增 findings（tabs→observed 的1条为既有注释纯迁移） |

## 慢因结论（任务书验收③：reconcile 主嫌疑路径 IO 面定性）

### 主链（代码证据 + 10-14 拥堵窗日志实证）

effort read 的 `reconcile_ms` 按序包含三段，**每段都有磁盘面**：

1. **memo key 计算（每次读都跑，含 settled-skip 命中路径）** — `tab_reconcile_memo.go:76-96`
   `canonicalTabSessionPath`（`tabs.go:9269` → `validateSessionPath` `sessions.go:753`：`os.Lstat` + `filepath.EvalSymlinks`×2）+ 最多 4 次 `configFileStampOf`（os.Stat：projects/org/session/branch-meta）；整个 keyOf 在 `a.mu.RLock` 内（tab_reconcile_memo.go:77）。**打点前该段零观测**（无任何 phase log），即任务书点名的观测缺口；本次已补（breakdown memo_ms + phase=memo/memokey 慢日志）。

2. **memo 未命中 → walk** — `resolveSessionBinding`（binding_resolve_cache.go:207）
   - `knownSessionDirs`（tabs.go:9403）：`loadProjectsFile`（tabs.go:6300，读 desktop-projects.json+org sidecar，legacy inline org 还会写回）+ 每项目 `os.Stat(dir)`（dirs=15）；
   - 每目录 `validateSessionPath`（Lstat+EvalSymlinks×2）+ `LoadBranchMeta`（branch.go:223 纯 ReadFileUTF8，Windows 撕裂读另有 20/50/100ms backoff 重试）；
   - **缓存失效条件包含 session 文件 mtime 本身**（bindingResolveLookup 逐个重验 probed 指纹、tabReconcileMemoKey 含 sessionStamp）→ **活跃会话每次保存都 bust 两层缓存**，拥堵窗=高频保存窗 → 常态 miss、常态全 walk。
   - 日志实证（10-14 当日 382 条 resolve slow、窗口 100 条）：dirs_ms 合计 147s（单条最高 14.4s）、**scan_ms 合计 474s（单条最高 31.9s）**、meta_ms≈0。
   - `applySessionBindingToTab`（tabs.go:5096）：admission 门、`registerProjectRoot→addProject→updateProjectsFile`（desktopProjectsFileMu+磁盘读写）、`loadTopicTitleSource`（磁盘读）、**a.mu.Lock publish 段（从 Lock 前计时）**。
   - 日志实证：apply slow 1110 条/日（窗口 644），**publish_ms 最高 100507ms、>1s 共 270 条**；reconcile slow 1629 条/日 **全部 phase=path**；窗口内多条 publish_ms 90-100s 的 apply **同一秒齐落**（排队 convoy 形态）。

3. **P1 主嫌疑：App.mu 持锁段内的磁盘 IO → 锁排队放大**
   - `applySessionBindingToTab` 在 **a.mu 写锁内 3 次调用 canonicalTabSessionPath**（tabs.go:5041/5056/5070，每次=Lstat+EvalSymlinks）；
   - `tabReconcileMemoKeyOf` 在 **a.mu 读锁内**调 canonicalTabSessionPath（tab_reconcile_memo.go:77-86）；
   - 机制：拥堵/AV → 路径解析变慢 → 读锁持有变长 → 写者（apply publish）排队；Go RWMutex 写者优先 → 后续读者（effort read 的 tabByID/RLock、其他 apply）一并被挡 → publish_ms 90-100s convoy → reconcile 段雪崩（133s 样本：total=133427 **reconcile=133135**；261s 样本：reconcile=191828+snapshot=69485）。
   - 同窗佐证：`canonical path resolution timed out budget=250ms`（internal/sandbox/canonical.go:228）成串出现——磁盘/路径解析确实慢到预算级；窗口 breakdown 中 reconcile_ms 占 total 绝大多数（2409/2593、3800/3800、5328/5419）→ snapshot 非主因。

### 排除清单（有证据不支持）

- **session save-path 锁（883/885 域）不是 effort read 的直接阻塞源**：代码走查 effort 路径全链不取 `sessionSaveLocks`（reconcile→LoadBranchMeta 纯读；apply→saveTabsWrite 走 tabsSaveMu/tabsSaveQueue）。窗口内 save-path lock waited 41 条（最高 9.5s）与 timed out 同窗出现 = **同域并发症状**（同一磁盘拥堵 + 同一 session 高频保存），非锁依赖。
- 883 canonical probe（site=save-lock-key，≥5ms 才打点）窗口内 **0 条** → save 锁键漏斗本身当时不是主要耗时。
- resolve 的 meta 段（LoadBranchMeta）窗口内 ≈0，排除 sidecar 读。
- snapshot 非主因（见上）；config 包加键变重是 snapshot 侧次要放大器（下次用 snapshot_load_ms 直接验证）。

### 最强假设（待新打点复核，未终局）

拥堵（AV/磁盘风暴 + session 高频保存持续 bust 两层缓存）→ reconcile 三个磁盘面同步变慢 + **App.mu 持锁段内路径解析变慢造成读写锁 convoy** → 2s cap 打穿；拥堵缓解即回落（10-09~10-14 逐日 1/2/1/4/7/33 的时段相关性吻合）。

### 下次爆表取证路径（监测机制落地后）

1. `go run ./tools/effort-timeout-report` → 日/日×时段分布 + 慢读 top N 段归因（一键）。
2. 爆表时段 grep `effort read breakdown` → memo_ms vs walk、snapshot wait/stat/load、after_timeout 配对。
3. 超时 warn 行 `phases=` → 卡在哪步、各步多久（在途，不等读完成）。
4. `session reconcile slow phase=memo|memokey|path|resolve|apply` + resolve/apply slow 行 → reconcile 内部步与 publish 排队时长。

## 测试与预存红申报（终跑已完成）

- 新增/改动测试：6（889 打点断言）+ 3（工具解析）×5 轮稳定；定向回归子集（Effort/ReconcileMemo/BindingResolve/Snapshot/ContextUsage/SwitchTab/SessionBinding…）**全绿**（59s）。
- **desktop 包全量终跑**（-timeout 1800s，1094.9s 完成，日志 `global-workspace/tmp/wt889-final-desktop.log`）：7 例 FAIL——`TestPumpDerivedHopExhaustionIsAHopRefusal`、`TestHopExhaustedIsReportedAndSettled`、`TestSettingsExposesEffectiveSandboxWriteRoots`、`TestCatalogTopicTitleLocalizesAtSidebarBoundary`、`TestCatalogManualDefaultTitleIsNotLocalized`、`TestBuildTabControllerBlocksWhenSessionLeaseHeld`、`TestStartTopicActivationFailureDetachesPreviousAndReattaches`。**归属已实证**：后 5 例在**纯净基线 commit 73ff458bd 临时 worktree 单跑同样恒红**（临时 worktree 已删），前 2 例为基线已知红 → 7 例全部预存红，与 889 无交集；889 相关测试与回归子集全绿。
- **root 模块全量**（`wt889-final-root.log`）：`ok reasonix/tools/effort-timeout-report 2.420s`（本次唯一新增 root 包）；12 个 FAIL 包（acp/agent/boot/cli/config/control/proc/provider/sandbox/sessioncatalog/store/testutil）均为本次零触碰包（root 模块改动仅 tools/effort-timeout-report 新目录，desktop 为独立模块），预存红/重负载套件红。
- `-race` 不可用：本机无 gcc（CGO_ENABLED 需 C 编译器）——环境限制，非本次引入；phase trail 全部状态在 effortPhase.mu 串行下。
- repolint（工具闸门）：**基线即红**（main 仓同 commit 输出同 9 项超顶），本次改动后总量相对 baseline essay 持平(7633) / file-size -84 / struct-state 持平(153)，findings 差集仅 1 条纯迁移（tabs→tab_reconcile_observed.go）。
- 本机计时注记：Windows 单调钟粒度 ~ms（back-to-back 2000/2000 读数相等，81% 亚 ms 跨度读 0），亚 ms 健康段显示 0ms 属正常；打点对拥堵量级（百 ms–百 s）无影响。

## 残留风险

1. phase trail 按 tabID 注册、同 tab 并发读互相顶替（代际检查保证不误删/不误标，但交错 marks 属可接受诊断噪声）；无 trail 的非 EffortForTab 读不打点。
2. breakdown 子段在读完成时才落盘；**永不完成的读**只有超时行 `phases=` 可归因（设计如此，已覆盖）。
3. 工具解析的是已落盘字段：新字段（memo_ms 等）要等含 889 的包部署后才非 0；旧日志行缺字段按 0 解析（兼容已验证）。
4. 慢因主嫌疑（App.mu 锁内路径解析）尚未修——889 定位为纯监测；修复需另立项（候选：把锁内 canonicalTabSessionPath 移出临界区，或给路径解析加预算，涉判定路径变更，须按「可用性机制删前有替代」原则另行拍板）。
