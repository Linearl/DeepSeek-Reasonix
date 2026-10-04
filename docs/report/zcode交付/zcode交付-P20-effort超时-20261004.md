# zcode交付-P20：状态栏「effort read timed out (>2s); showing last known value」根因定位

- 分支：`wt-zcode-p20`（基于 main-v2-stable tip `a8b334088`）
- 日期：2026-10-04
- 结论先行：**不是配置错误，是延迟超时**。effort=max 合法（词汇表含 max，与用户核实一致）；超时提示是任务 421 止血机制的正常降级输出。**与 P18 不同锁、不同链，但同拥堵环境**——实机日志毫秒级对齐证明超时集中发生在 LoadSession 全量解码 / save-path 锁拥堵窗口内；P18 R1-R3 落地后症状频率应大幅下降，但结构性根因（每次 effort 读付一次全量 config 磁盘加载、正常 p99 1.4s 对 2s 上限只有 ~1.4x 余量）归任务 148，**本件不需要独立修复**。

## 1. 提示来源与超时机制（问题②）

- 提示文案唯一出处：`desktop/effort_fetch.go:65`（缓存命中）与 `:70`（冷缓存 default）。
- 阈值定义：`effortReadTimeout = 2 * time.Second`（`desktop/effort_fetch.go:26`，任务 421 引入，commit `2ab67be0d`，2026-10-01）。注释记载任务 421 现场遥测：正常 431-1350ms、p99 ~1.4s，双峰离群 7.6s/14.8s；2s ≈ 1.4×p99。
- 机制：`EffortForTab` 把直接读取放到独立 goroutine，2s 内未返回即降级——有缓存则回「last known value」，无缓存回 `auto`+空级别，并落 WARN 日志 + tab notice。**被放弃的读取完成后仍会写回缓存**（自愈：降级只维持到迟到的真值落地为止）。
- 降级正确性验证：
  - 代码审查：buffered channel 保证迟到 send 不泄漏 goroutine；缓存只读不写配置。
  - 测试：`desktop/effort_fetch_test.go` 三个验收测试（超时回缓存 / 冷缓存回 default / 正常读刷新缓存），本分支实测 `go test -run TestEffortForTab .` → **ok 1.876s**。
  - 实机日志（`%APPDATA%\reasonix\logs\desktop`，2026-10-04 22:40–00:03 窗口共 ~10 条）：9 条走「serving cached value」（cache_age 1m–19m53s），1 条冷缓存走「serving default」（23:32:54）——机制按设计工作。
- **用户可见影响仅为显示层**：实际生效的 effort 来自 `tab.effort`（attach 时从 provider entry 播种、每次切换重写，`desktop/app.go:978/10423/10678`）与逐请求 `provider.Request.EffortOverride`（`internal/provider/provider.go:270`），与这条展示读取完全解耦。超时**不改变任何已存配置**，下一轮 turn 深度不受影响。冷缓存降级的那一次会把控件短暂显示成 `auto`——这是「让用户怀疑 max 被重置」的来源，实际值未动，下次成功读取即恢复。

## 2. effort 读取调用链与锁关系（问题①）

```
EffortForTab(tabID)                        desktop/effort_fetch.go:44（2s 上限）
└─ go effortReadCompute
   └─ effortForTabDirect                   desktop/app.go:10463
      └─ currentProviderEntryForTab        desktop/app.go:11284
         ├─ reconcileTabWithPinnedSessionMeta   desktop/tabs.go:4345（每次读都做会话绑定对账）
         │  ├─ a.mu.RLock/RUnlock（短暂）
         │  ├─ resolveSessionBinding → agent.LoadBranchMeta ×N（小 JSON sidecar，无会话锁）
         │  └─ applySessionBindingToTab（tabs.go:4397）
         │     ├─ pinnedContextStateForSessionBinding（sidecar 文件读，无锁）
         │     ├─ a.mu.Lock()（写锁；仅绑定变更时 saveTabsLocked→tabsSaveMu+磁盘写）
         │     └─ a.mu.Unlock()
         ├─ a.mu.RLock/RUnlock（短暂）
         └─ config.LoadForRoot(workspaceRoot)   internal/config/load.go:38（每次读全量磁盘加载）
            ├─ 用户+项目 TOML 各一次 mergeRuntimeTOMLFileSnapshot
            │  └─ migrateRetiredConfigKeysFile → LockConfigFileEdits
            │     = 进程内 userEditMu + 跨进程咨询锁（5s 超时，mutate.go:81/206）
            ├─ 每 TOML 源解码 2-4 遍（校验+合并+persisted 重解码）
            └─ 全局 .env 凭据读
```

**锁面结论：effort 读取链全程不取 `lockSessionSavePath`（P18 的 save-path 锁，`internal/agent/save.go:1370`）。** 逐点排除：

- `agent.LoadBranchMeta`（branch.go:223）直接 `ReadFileUTF8` 读 sidecar，不加会话锁；
- 会撞 save-path 锁的话题标题路径 `topicTitleFromSession → LoadSessionUserMessages`（tabs.go:5151→5155）只被开新 tab 的 `topicTitleFallbackForOpen`（tabs.go:2404）调用，**不在 effort 链上**；
- `loadPinnedContextState`、`knownSessionDirs`、`loadProjectsFile` 均为无锁小文件读；
- effort 链上唯一的进程内长临界区风险是 `applySessionBindingToTab` 在 `a.mu` 写锁内做 `saveTabsLocked` 磁盘写（仅绑定变更时触发，稳态不触发）；
- 次级串行点：`LoadForRoot` 的迁移步骤每次都过 `userEditMu` + 跨进程咨询锁（读路径付写锁成本），但正常持有为毫秒级，非 2s 量级来源。

## 3. 与 P18 的关系（问题③）：不同锁，同拥堵环境

**同机同时刻实机证据（`logs/desktop/desktop.log.*`，2026-10-04 晚）：**

| 时刻 | P18 侧信号 | effort 侧信号 |
|---|---|---|
| 22:40:41.590-.610 | `image reference unresolved` 连发（= LoadSession 全量解码进行中的标记行，DAG 报告 §82） | — |
| **22:40:41.739 / .993** | （同一解码持续中） | **effort read timed out ×2**（同 tab，间隔 254ms） |
| 23:05:06 | save-path lock waited **74499ms** | 23:06:47 effort timed out |
| 23:46–23:49 | save-path lock waited 23s/30s/40s/**69.5s**/35.7s 连发 | 23:48:50 effort timed out（cache_age 1m1s） |
| 全窗 | save-path lock waited 1.4s–74.5s 多次 | effort timed out ~10 次 / 90 分钟 |

**判定：**
1. **不是同一把锁、不是排队关系**——effort 读从不取 `lockSessionSavePath`，P18 的 19-40s 锁等待不会直接挡住 effort 读（第 2 节锁面逐点排除）。
2. **是同一拥堵环境的受害者**——LoadSession 全量解码（589MiB 级、每分钟 ~2 次、25-30s）与保存拥堵期间，进程处于高分配/GC 风暴 + 磁盘读压力下；effort 读本身基线 431-1350ms（多 TOML 解析 + 绑定对账的多次小文件 I/O），对 2s 上限只有 ~1.4-4.6x 余量，遇资源争抢即越线。上面毫秒级同窗是直接证据。
3. **P18 R1-R3 落地后：症状频率应大幅下降（大概率趋零），但非结构性消解。** R1（load 接缓存）+ R2（消除解码循环）直接移除本机最大的解码/GC/磁盘压力源；R3（保存解耦）进一步降低磁盘竞争。但只要任何外部停顿（Defender 扫描、索引服务）落在一次 effort 读上，~1.4s 的 p99 对 2s 上限的余量仍会被打穿——结构性修复是**任务 148**（per-request effort + 移除每读 config 全量加载），421 注释已明确归属。**因此本件不重复修，只交付结论与验证步骤。**

## 4. 修复建议

- **P20 不出代码修复**（根因归属：频率治理 → P18 R1-R3；结构治理 → 任务 148；421 止血机制已按设计工作且测试全绿）。
- 可选低优先建议（留给任务 148 一并做，不在本件动）：`currentProviderEntryForTab` 的展示读可考虑 `LoadForRootReadOnly`（免迁移写锁转门），但 TOML 解码才是耗时大头，收益有限；更大的收益在 148 的「按标签缓存 provider entry + 变更失效」。
- 提示文案无需改：`showing last known value` 语义准确；唯一易误导的是冷缓存降级显示 `auto`（用户会误以为 max 丢了），若要改进属产品文案层，非本件范围。

## 5. 验证步骤（P18 出包后复测判据）

1. 复现环境：打开含 589MiB 级会话的 global workspace（`c--users-...-global-workspace` 项目），多 tab 常驻 + turn 运行中切 tab。
2. P18 生效判据（P18 自有验收）：`save-path lock waited ≥19s` 归零/大降；SetActiveTab 等锁 p50 <1s。
3. 本件症状判据：`grep "effort read timed out" %APPDATA%\reasonix\logs\desktop\desktop.log*` —— P18 落地后同强度使用场景下**新增条目应趋零**；若仍有零星条目，看 `cache_age` 确认降级自愈（迟到真值已写回），并归入任务 148 余量问题。
4. 回归确认：`cd desktop && go test -run TestEffortForTab .` 全绿（本分支已验：ok 1.876s）。

## 附：关键文件

| 文件 | 作用 |
|---|---|
| `desktop/effort_fetch.go` | 2s 上限 + 缓存降级（任务 421，commit `2ab67be0d`） |
| `desktop/effort_fetch_test.go` | 降级机制 3 个验收测试 |
| `desktop/app.go:10463,11284` | `effortForTabDirect` / `currentProviderEntryForTab`（每读全量 config 加载 + 绑定对账） |
| `desktop/tabs.go:4345,4397,4624` | 会话绑定对账链（全部无会话锁） |
| `internal/config/load.go:38,942,1100` | `LoadForRoot` 与迁移路径（`userEditMu`+咨询锁转门） |
| `internal/agent/save.go:1370,1558` | `lockSessionSavePath` / `LoadSession`（P18 的锁，effort 链不取） |
| 实机日志 | `%APPDATA%\reasonix\logs\desktop\desktop.log.*`（22:40:41 毫秒级对齐样本） |
