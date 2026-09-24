# FORK v1.38.3-20260925-0252

> 基线：`v1.38.3-20260924-2113`（只写本版新增；历史沿 [`FORK-v1.38.3.md`](FORK-v1.38.3.md) 链式回溯）

## 本版新增（批七第一波四件 283/240/275/303，全部审计 PASS + wt-merge 专门会话合并）

### 1. 任务 283 —— autopilot / 无人族交付检查不再阻塞（F1 头件）
「交付检查尚未完成」卡停根因：gate 判定只认 autopilot，auto / yolo / 工具豁免（无人子会话）全族不在无人名单 → 首个 final 即停。修复：turn 级 unattended 快照（autopilot ∪ 工具豁免）+ 暂停只对人在场 + advisory 扩全无人族（≤2 次界语义保留）+ 放行审计记录（ReadinessAdvisory + gap marker + 会话 notice）。人在场行为逐字不变。合入 `6aec78473`。

### 2. 任务 240 —— 软预算误判与放行死路四缺陷全修
① 任务分类动态化：跨 turn ledger reset 失忆 + 只查 Write 漏 Mutation 双根因修，会话级「写过即永不再套 read-only 提示」；② 放行通道：提示改 `call tool:extend_research_budget`（通路实测可达），失败报错给可行动指引（tool:/具体 id/mcp-tool 形式），工具关时降级不指向死路；③ 失败轮次不计入收敛轮（有效轮 = rounds − failed）；④ 口径统一：软预算=单 turn 收敛闸，提示自我声明「非会话级上限（本会话无固定总限额）」。合入 `26b268910`。

### 3. 任务 275 —— events 日志瘦身：自动闸根因修复 + 手动 compact CLI
① 根因：quiet period 拒绝因 `lastActivity` 恒刷新**永不可清** → 活跃会话每次 save 必拒、轮转永久 defer（且原为 Info 级静默）——删除该自锁条件（file lock 序列化 + generation CAS 已覆盖 race 保护，lease/handoff 拒保留），deferred 升 Warn 可 grep；② 手动入口：`reasonix session compact <path>`（lease=idle 证明，生产 SaveRewriteCompact 路径+原子回滚，输出前后字节）。合入 `cb9efb6b9`。

### 4. 任务 303 —— 三联修复：turn 死锁自愈 + 摘要语义分离 + 缓存读数
① 「出错后无法对话仅压缩可解锁」：中断态 45s 超时自愈（作废残尸 turn + generation 护栏拒迟到 finish + 复位 + 会话 notice），只认中断态不误杀长工具轮；② 「压缩成功界面报失败」：折叠与摘要结果语义分离（折叠保留、摘要流错误单独提示+2 次退避重试）；③ 摘要 688K 全量 prefill：compaction 三阶段日志带 cache_hit/cache_miss/request_count 直接读数。四项 slog 打点（死锁/流错误/压缩阶段/提交拦截）全落。合入 `e78043247`。

### 5. 过程与基建
- wt-merge 专门合并会话首跑（流程 5.5 严格执行：三轮合并+21 文件对账+分组验证）
- 批七验证清单 `tasks/批七-装机验证清单-20260925.md`（🤖自验/👤人工分列）
- 新立任务 308（内存归因，调研交付：replaySessionDAG 478MB/61.66% 主因）

## 本版未含（第 0 步合入扫描判定）
- 308 优化落地件（310 DAG 流式化/311 GOMEMLIMIT）——等 275 落地后新 pprof 再定方案
- 批七余项：F2(286/242/196/192/268/182/303 已交外) F4(225/231) F5(274/284) F3(157/163) E(243/244) D(236) 架构(290) —— 后续批次
- 299 观察期至 10-01（watching）

## 升级提醒
从 `v1.38.3-20260924-2113` 升级：数据无迁移；回滚=重启并更新切回 2113。装机验证照 `tasks/批七-装机验证清单-20260925.md`。
