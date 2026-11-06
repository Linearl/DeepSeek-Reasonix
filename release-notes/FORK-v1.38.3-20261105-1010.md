# Release Notes — FORK v1.38.3-20261105-1010（包 74）

> 基线：上一时间戳包 v1.38.3-20261104-1515（包 73）。本包 = 昨夜清零批八件 + 前任续做收尾。
> 主线 tip：64cdc5fc2（含 2ae4a0345 棘轮抬线）。

## 缺陷修复

- **归档被拒 lease/meta 全族根治**（tl1383+续做，用户反复报「无法归档」终解）：meta 错代自愈（ReconcileBranchMetaIdentity——转正落位即改写身份+账位对齐事件日志末条；存量已坏的连撞两次同冲突自动触发对账，会话自动恢复可写）+ unknown holder 三连拒 reclaim 自愈 + errSessionBusyUnidentifiedHolder 哨兵五面接线（FE 三语「lease 残留（无活跃 holder）」，不再误导「another window」）+ force 补扫僵尸条目直通（tl1363 族防御）+ legacy_ 双注册同查定论（无第三处遗漏）
- **强制归档死循环根治**（tl1392，包 73 装机实测打回重做）：进程内孤儿 lease 清扫原语（活 lease 注册表+双半清除，可用身份一票否决真占用零触碰）+ force 路径接入 Sweep + 文案三修（restart 仅磁盘残留形态；force 后拒绝改「附 desktop.log 反馈」且不标 forceable）+ boot 重 acquire 诊断日志（acp session/load+tab bind 两点可 grep）
- **steer 降级缺口**（tl1393）：向运行中会话发消息裸报错根治——notActive 拒（「is not the active turn」）纳入共享谓词 + steerForTab/performEnqueue/degrade 三处接线（拒→落 follow-up queue→气泡+desktop.log 对账→不 throw）；Go 格式串计数钉防文案漂移
- **模型切换显示滞后**（tl1389 修法 2）：切模型后输入框模型名 <300ms 更新（后端双路径推 tab:meta 事件）；重建期显示「正在应用模型…」；重建提速（RuntimeReload 复用装配砍 skills 段 44ms→0ms + 三次 LoadForRoot 合一）；观测补盲（model switch 计数单源迁 build end）
- **侧栏影子条目+刷新出口**（tl1363）：后台归档后的零绑定注册残留不再永驻侧栏；项目/Global 右键新增「重载会话列表」（复制路径上方），一键清残留+重挂活会话；legacy/topic 双注册归并
- **子代理面板两层修复**（tl1387）：右侧「子」tab 此前不显示运行中子代理——面板补第二数据源（jobs 注册表+foreground 注册表，对齐 zcode 形态），同步派+后台跑的子代理都可见；provider/模型名行内渲染删除（改 hover 恒可用三级链：live>最近已知>创建时）
- **中断渲染哨兵**（tl1347 二轮）：两条中断投影路径实测正确+回归钉防再犯（Task 1047 同机制回归史）
- **todo_write 引导与容错**（tl1354）：plan_id 破坏性语义进 schema+误传回执当场给恢复步骤（实测完整找回）；顺序错位自动摆正（三形态，伪造洞钉死不开放）；NormalizeSerialTodoInput 统一入口防 continuity 死锁
- **host 协议消息窄渲染**（tl1350）：四类系统提示收成一行「⚙ host context」折叠条，三语+45 断言

## 新特性/增强

- **装机后 UI 验证自动化**（tl1382）：`verify_pkg_ui.py` 三态（run/plan/probe）——读 handoff 验证点表自动生成断言映射，CDP 逐项断言+restore 闭环+危险动作 denylist；包 71 spec 首例+使用文档。解锁 GC 五件装机验证收口路径
- **CTXAgent 压缩三增量**（tl1378，双实验开关默认关）：curate_context 细粒度档（单条工具结果缩成一行注记，2min 独立限速）/review hint 提示层（每 25 批次或 +20k 提醒复查，只提示不代劳）/compress 描述补 hand-off 引导

## 预存红消解（续）

- `TestConcurrentLoadDuringAppendsStaysConsistent` 负载敏感红（读者 30ms 节流防饿死，核心一致性断言不动）`919a086db`
- capabilities-panel 源码钉过期（tl1361 顺手修）`12657e3af`

## 门禁

构建 EXIT=0（三跑；首跑 E1=zh locale 119.9 触 119.8 线 0.1→棘轮抬线 120.8/122.9 `2ae4a0345`；二跑 E1=eslint shim 缺失 junction 事故后遗症，pnpm rebuild 恢复）。vet=0 / 双模块 build=0 / 各件定向+包级回归全绿。
预存欠账单列：agent 包全量慢态机器贴 600s 默认超时悬崖（候跑包脚本显式 -timeout 或降载）/billing TestAggregateMixedCurrencies 偶发（连跑 5 绿）/queryCoalesce 跨测试缓存泄漏（候立项）/tl1240 mock 毫秒碰撞（候小件）/tl1377 P1 两件系统性 UI 问题（候修）。
