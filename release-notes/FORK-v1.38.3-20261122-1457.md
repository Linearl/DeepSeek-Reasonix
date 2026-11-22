# 包108 release notes — v1.38.3-20261122-1457

> 基线：包107（v1.38.3-20261121-1652，构建点 a7dd9ee9b6）
> 构建点：7fa4d346c3（main-v2-stable，含 merge+fix 两 commit）
> 产物：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（74,049,547 B）
> 执行线：zcode 开发线（合并线另收 3 支：tl1735B2/tl1750/tl1748，其余 5 支 zcode 滚合）

## 一、新功能

### tl1746 任务面板一期（只读）——桌面内嵌直读 tl 数据库
- **两入口**：命令面板（Ctrl+K）「任务面板」（工具与诊断组）+ 实验室→可观测性「任务面板」开关（未稳定档，**默认关**）+子项「数据库路径」（文本框+测试连接+打开所在位置，改路径免重启生效）
- **双 tab 面板**：总览（四统计卡+状态/领域/优先级分布）+ 任务列表（搜索/状态/优先级/领域/日期范围筛选）
- **只读铁律双断言**：sqlite `mode=ro` 打开（运行时六类写语句全拒+库文件零污染）+静态源码 SQL 扫描零命中——写路径零新增，SSOT 仍为 tl.py
- **真实库对账**：1697 任务逐行吻合 tl.py（总 1697/待办 47/完成 1606/取消 44；优先级口径取 prio_mark 权威列）
- 驱动复用仓内 modernc.org/sqlite（零新增依赖）；30s 缓存+手动刷新

### tl1561 use_capability 大返回分层阈值（工具返回不再一刀切 32K）
- **三档**：关键 100K（read_file/web_fetch/use_capability/heartbeat_task_list）/常规 40K（fail-safe）/流式 8K（bash_output 增量，对齐 OpenClaw）
- 超档自动归档落盘+提示带可读路径，`read_file offset/limit` 续读逐字节拿回原文（回读一致钉）
- **result_ref 恢复承诺彻底清除**（banned 断言钉 5 处——不再提示做不到的恢复方式）；list 分页探针/子代理聚合预算钉回 32K 原口径（分页语义零改动）

## 二、可靠性/修复

### tl1748 会话版本分叉改善（可观测+注入一致性+防丢）
- **可观测**：rev_lag_takeover 打点差异数+被弃侧逐条明细（用户消息 role=USER 单标）+conflicts.jsonl 7 个 takeover_* 记账字段+超集形态显式「无损」行
- **防丢**：rewrite 前预读盘侧→被弃侧独有消息全文落 `<stem>.conflict-preserved.jsonl`（8MB 上限+截断计数诚实）——**11-21 晚 23:16 用户四点拍板消息随 rewrite 静默丢失一类事故从机制上封死**
- **注入一致性**：被弃侧真实用户消息 origin=host 中继自动转述进当前对话（不再静默缺席）；第三接管点 healDivergedSnapshotConflict 同面接线（22:59 现场 retry 路径实点）
- takeover 决策本体/版本对话框 UI 零改动

### tl1735 Block2 压缩后 system prompt 刷新链收口
- resume（重启/中断恢复）后 #Memory 区立即刷新到最新磁盘状态（rebuildHostState 在 sessionSelfHealActive gate 外接线，幂等+全 recover）
- panic 注入钉（恢复链路炸不炸 turn）+耗时探针（slog elapsed_ms）；B2-4 工具面按拍板例外收口零触碰

### golden 负载敏感预存缺陷根治（用户拍板立件）
- golden 基线钉 shell 解析 bash 形态（PinResolveShellForTest nil 短路，产品路径逐字节同）——高负载下 probeBash 3s 超时致 +439B 漂移的误报消除
- 防漂移钉双相（超时注入复现证明+pin 下逐字节合基线）

## 三、面板治理

### tl1741 实验室面板瘦身（8 项 stable 卡撤除）
- 撤：autopilot/sessionCollab/collabHub/workflowPanel/cdpDebugPort/sessionStorage 档位行/runtimeReuse/resourceGovernance 容器卡（**功能保留常开**，只撤开关面板；916 特例=storage 键与四模式机制全保留）
- fullAccess 保留为唯一 stable 主卡；自动化说明改概要卡；deprecated 9 键 WARN 承接（历史手写键可见不阻断）；**净减 1569 行，行为零变化**
- 跟测收尾：A 组 11 套断言跟改全绿+最终 harness 920 套件 15486 通过/23 套全预存零新增

### tl1728 compress 工具发现性（P1 归一化）
- capability list 收录条件 offer 型工具+三语 offer 条件说明（skip 改非条件 offer 才滤）

## 四、测试基建

### tl1750 最小档预存红批量治理（用户拍板三档之最小档）
- **12 支恒红清零**：agent loop breaker 族 8 支（shrinkLoopBreakerForTest 定式批量隔离）+boot 契约 2 支（create_goal 名单补漏）+前端 3 处 stale 死登记（发现式 runner 主入口解锁）
- **生产代码 diff=0 硬门槛守住**（9 文件全为测试+登记文件）
- 勘误：归并报告「发现式 207/207 全绿」口径与审计原始日志不符（实为 5 套件红）；新发现 6 条预存潜伏红归彻底档候拍板
- 中等档/彻底档 **blocked 候拍板**（用户令）

## 五、装机验证清单（交 reasonix 开发线）

1. **tl1746**：实验室开「任务面板」→两入口可见→面板数字与 tl.py 对账→路径子项改路径即生效→开关关=入口零痕迹
2. **tl1561**：大返回工具（web_fetch 大页面/use_capability）不再 32K 硬截断；超 100K 结果提示带归档路径且 read_file 可续读
3. **tl1748**：构造分叉接管场景→日志有 rev_lag 打点+被弃侧消息落 conflict-preserved 文件+中继行出现在对话
4. **tl1741**：实验室面板 8 卡消失/fullAccess 在/撤开关功能实际仍开（autopilot 等行为不变）
5. **tl1750**：全量测试红清单较包107 显著缩短（-12 支）
6. **tl1713 低收益门装机观察**（已拍板默认值）：复压间隔/残留率异常时回开调参

## 六、已知遗留（候拍板移交）

- 预存红中等档/彻底档（含新发现 6 条潜伏红+desktop 15 环境敏感预存红）blocked 候拍板
- memory-migration 预存红（boot_safe_mode_test.go:76）建议独立立件排查
- tl1748 中继消息 UI 呈现为 guidance 行（zcode 倾向维持现状，透明诉求）
- desktop 全测默认 10m 超时不够本机冷跑（实测 1133s，候 30m 小件）
- tl1746 二期（项目分库+工具内嵌）候拍板
- tl1735 B2-4 工具面=显式例外收口（FORK.md 裁定维持）
