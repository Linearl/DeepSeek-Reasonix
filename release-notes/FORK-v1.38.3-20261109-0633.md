# FORK v1.38.3-20261109-0633（包 86）Release Notes

> 基线=包 85（v1.38.3-20261108-1608，构建点 2f973688a）。本包 13 支滚合+安全清零面，**主题=13 件收尾批+出包硬条件全清**。

## 🧠 防早停默认审查层（tl1460，解锁开工件）

每轮轮末审查**非 Goal 会话也生效**（用户两原则：所有对话都审查+结论可见）。判定源分层前两层零 LLM：①确定性规则（todos 未清/readiness 缺证据/plan 未执行→继续）②进度证据（有产出未清→继续；零产出无待办→空转保护停）③LLM 复核仅 Goal 模式仅规则说停时。**单向铁律**：审查层只能把「停」改「续」，永远不能把「续」改「停」；审查停止 100% 带 cause 可见（`stop_review_continued`/`stop_review_capped` 双 notice）。继续上限默认 5 轮可配（每轮显「因 X 继续 N/5」到顶转停+报告）。Goal 瘦身：继续判定下沉审查层，Goal=审查层输入（目标+验收标准）；armed⇒goal_in_pursuit 衔接 1472 契约。

## 🔁 workflow 主动暂停/恢复（tl1335）

面板暂停钮（与停止并列）+paused 徽章/续跑钮+三语：pause=节点边界优雅挂起（当前节点含 gate 修复环跑完+checkpoint 落盘后不再启新节点），cancel 立即砍语义原样保留。零风险默认路径（PauseCheck nil 既有路径逐字节相同对照钉）。

## 🔇 意见箱触达双判据防误报（feedback nudge）

T1/T2 文案三桶分类：可复用做法→自调 memory:remember 明示不提意见箱；仅 Reasonix 本身能修的缺陷→唯一邀请句；无可记跳过。门 4：本轮已成功 remember 则整触点静默。

## 🗑️ 空项目移除失效修复（tl1502）

文件夹被删后左栏项目无法移除=快照闸门对不可达会话容器报错阻塞；修=移除链前置 rootGone 检查跳过快照+Warn 留痕（摘除语义下未落盘运行态有意放弃），根存在零变化。三钉（缺失根空项目/缺失根+绑定 tab/存在路径回归）。

## 📊 信息栏两小改（tl1500）

①对话请求数/运行时间接入【设置-通用-信息栏显示项】既有管理（559 先例零新增入口，芯片可见才发 RPC 零口径分叉）；②右侧栏概览-会话指标卡片可折叠（localStorage 单键默认展开）。

## 🔧 延迟与体验修复

- **effort 切换延迟**（tl1388）：GLM 网关接入探针改 protocol-gate（声明 `reasoning_protocol="glm"` 的条目走 per-request 快路 94ms≪1s）+fallback 重建异步化（不再同步阻塞 UI）+decline 日志补 path。
- **模型名显示滞后**（tl1389 验收钉）：修法本体已在主线（tab:meta 推送），补 modelLabel 先行更新+effort 链独立两钉。

## 🔍 投递不开轮最小修+全链调研（tl1339）

最小修两刀（零行为）：①泵 paused 类 gate-held 静默停补口（WARN 四字段溯源+episode 门控+耗尽痕）②dormant probe 对 claimed 无主队首可见化。机制面（579 桥判据/807 reconciliation 扩 sweep/gate-held 升格 570）建 blocked 项候复发激活。调研报告：docs/issues/reports/问题诊断报告归档/投递不开轮根因调研-20261108.md。

## 📝 左栏时间二轮+五轮收口（tl1485c/tl1415h）

- **左栏相对时间**：behind 存量（戳落后转录 ≥1h 的 35 行）装机后首波 repair 即自愈；「1 个月」档多数为如实显示旧会话（三值一致非 bug）——复验判据=打开会话看最后一条消息实际时间。
- **打开路径**：冷机时序窗口收口（点击打开按需有界 Reconcile 2s 兜底+漏斗 authority verdict 裁决日志，首开错切当场可百分百归因）+版本面板降级主线删除守卫对齐（.trash 可恢复协议）。
- **删除护栏文案**（tl1471b）：补「本拒绝面尚无 dry-run+字面目标逐个审计」真实机制引导。

## 📐 契约与验收钉（tl1413/tl1389/tl1397 验证件）

三件均为「修复已全在基线」的验证收口：改名双入口 44 断言防回潮钉/模型名显示两钉/上下文治理迭代一对照钉（全量 vs ranges 摘要规模 3.0x）。tl1387 子代理面板全链复核 340 钉认证（装机人肉复验仍候包 86 环境）。

## 🔒 安全面（出包硬条件全清）

- **GitHub 代码扫描 28 条全部终态清零**（tl1496：26 误报+2 测试码，逐条 dismiss 带理由，0 真缺陷——承重证据=敏感源命名启发式误报+真实密钥仅进 HTTP 头负面证明）。
- **dependabot 4PR 处置**（tl1498）：PR79 codeql-action（安全类）合/PR78 go 组 9 更新合（desktop 嵌套模块 tidy 同步）/**PR77 wails 2.17.1 合**（含 GHSA-r5j2-ghh9-2jqw 安全修复，双 CLI generate 逐字节一致+wails build 冒烟 4m25s）/PR83 关（后继 PR84 npm 16 连动合，预算棘轮 672.1 随之）。
- **回内自重启排队语义**（mu-convoy2 刀④返修，用户拍板）：心跳回合内 restart_update 从拒绝改**入队+回合边界自动执行**（提示「重启已排队，回合结束后自动执行」不再误导模型失败）；前台回合 task-254 面不变。

## 预算棘轮

initial JS 672.1→672.3（贴线预抬）/largest chunk 390.7→393.3/initial raw 3286.0→3296.0（三道照实测+余量；zh/zh-TW/largest raw 未触线不动）。

## 装机与验证

- 安装器：`desktop/build/bin/reasonix-desktop-amd64-installer.exe`（v1.38.3-20261109-0633）；staging 已铺（RestartAndUpdate 可发布）。
- **重点复验**：①防早停审查层（普通会话待办未清时轮末不再静默停，续轮显「因 X 继续 N/5」，到顶带 cause 转停）②workflow 面板暂停/续跑钮 ③意见箱不再误报（过程性经验引导 remember）④左栏时间 behind 行自愈 ⑤冷机首开落副本+版本面板降级主线可删 ⑥空项目可移除 ⑦effort 切换秒级（GLM 网关条目需声明 `reasoning_protocol="glm"`）。
