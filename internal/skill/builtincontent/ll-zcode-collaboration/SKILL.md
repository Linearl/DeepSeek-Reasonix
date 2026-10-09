---
name: ll-zcode-collaboration
description: Reasonix↔zcode 协同开发全流程流水线——用户定目标后 Reasonix 写 handoff、双方经心跳+留言板通信（doc-mailbox）、zcode 并行子代理逐件开发并逐件交付、Reasonix 逐件审计（不攒批）、合并/构建/重启更新/验证/报告/清理全由 Reasonix 自治完成。当用户要求"和 zcode 协同开发""把目标派给 zcode""zcode 流水线开发""夜间批""协同开发这个目标"时使用。
runas: inline
---

# ll-zcode-collaboration：Reasonix↔zcode 协同开发流水线

> **来源**：2026-10-01 用户确立（夜间批十五件实战后固化）。分工铁律：**zcode=开发产能（并行子代理），Reasonix=协调+质量门（审计）+集成（合并/构建/重启/验证/报告/清理）**。

## 职责边界（不可越）

| 方 | 做 | 不做 |
|---|---|---|
| **Reasonix（主对话）** | handoff 文档、派单、逐件审计、合并、构建、重启更新（restart_update）、验证、批次报告、清理构建缓存与过时 worktree、tasklist 回写、push | 不替 zcode 写代码；不攒批审计 |
| **zcode** | 并行子代理逐件开发、worktree 隔离、分支交付、自跑测试/integrity、DONE 留言 | 不合并、不出包、不 push、不碰 tasklist 他人条目、不自我审计 |

## 七阶段流水线

### 阶段 0：初始化（已有则跳过）
- 调用 **doc-mailbox** 技能：建 `docs/collab/zcode/`（README+from-main+to-main）+心跳接线（5min+留言板事件驱动）
- 确认 zcode 侧可达（留言板首条 INFO+用户转交或直派）

### 阶段 1：目标 → handoff
- 用户目标 → Reasonix 写**自包含 handoff 文档** `docs/handoff/zcode-<主题>-<YYYYMMDD>.md`：任务分解（按域分组/依赖序）、每件验收标准（可证伪）、工作方式（worktree 隔离+junction node_modules+显式路径提交+预算棘轮 gzip+1.0/raw+10 one-shot）、禁区（不碰审计防线/他人条目）、交付格式（分支+报告 `docs/report/zcode交付/`+DONE 留言指针）
- 留言板 DISPATCH 指针 → zcode

### 阶段 2：zcode 逐件开发（并行子代理）
- zcode 每件独立 worktree/分支，**做完一件交一件**（DONE 留言：分支+sha+测试 EXIT+遗留申报）——**禁止攒批交付**
- 主对话低频监视：DONE 驱动收货，无异常不打扰

### 阶段 3：分级审计（20261001 用户拍板——免审/抽检/全审三档）
- **免审直合并**：纯文档件、复验回填件（零新代码）、脚本/工具小件
- **抽检 ~30%**：常规代码件（UI/日志/批量治理类）——其余直合并，出问题下包修
- **全审保留**：**安全面/auth/锁序/路由注册/迁移类**（415 误报判定与 434 交叠回归两个反例支撑——安全件误报判定代价不对称+交叠面回归合并时才炸）
- 任何审出问题 → 打回 zcode（留言板 DISPATCH）修复后重审；抽检未抽中的件若下游出问题，追溯补审
- 判词回主对话归档 tasklist

### 阶段 4：合并（wt-merge 或主对话）
- 审计 PASS 件进合并队列；**合并序裁决**规则：超集分支替代子集（少一次合并）、唯一冲突并集解、lagged-base 强制预检（comm 交集+敏感调用点落点）
- 合并后 integrity+定向测试复跑

### 阶段 5：构建+重启更新（Reasonix 自治，不需用户）
- `scripts/build-local-installer.sh`（版本号=1.38.3-<时间戳>，不递增）→ 铺 staging → notes+台账 → **restart_update 自主重启装机**
- 出包前验证清单：pnpm build 8 checks、integrity、locale 预算

### 阶段 6：验证+报告
- **装机自验**（能自动的）：enroll 重跑回读断言、cache-guard 冷编译、止血件日志确认——**验证修复必须用修复后的构建**（旧包 CLI 验证新修复=无效验证，20261001 教训）
- **需要人眼的**：截图找用户（布局/视觉类）
- 撰写批次报告 `docs/report/`+总表回填+tasklist 回写（✅ 标记规范：✅ 在「任务」前+日期紧邻「完成」）

### 阶段 7：清理收尾
- 构建缓存：`go clean -cache`（月度/C 盘紧张时）
- 过时 worktree：回收语义（列清单→确认→`git worktree remove`+prune；junction 先删）
- push GitHub（显式路径提交）→ 守护心跳停止目标核对

## 通信纪律（双通道：bus 优先 + doc-mailbox 保底，20261001 用户拍板）

- **bus 优先**：机器层实时通道（serve 8787 长驻+MCP collab 工具+注入）——通信默认走 bus
- **doc-mailbox 留言板=保底**：bus 故障/未连接时的 fallback + 人可读稳态层——不废弃
- 心跳总线化已落地（bus5）：心跳 prompt 总线优先+文件回落

### bus 实时通道（20261001 实装，MCP 协议层 e2e 验证）

- **端点**：`reasonix serve --addr 127.0.0.1:8787`（bus_mcp `/mcp`，streamableHTTP+SSE）——长驻方案：用户级启动文件夹 vbs（登录自启）或 zcode 侧 shell 托管
- **工具**：collab_inbox_read/collab_send/collab_task_create/collab_task_update（spawn_roles 空则无 spawn=fail-closed）
- **MCP 客户端要点**（curl 探针实测沉淀）：①initialize 带 `Accept: application/json, text/event-stream` 拿 `Mcp-Session-Id` 响应头；②必须发 `notifications/initialized` 且 **sleep ≥1s** 等状态机推进（否则 tools/call 报「invalid during session initialization」）；③响应是 SSE 流（`event: message`+`data: {}`）——剥壳取 data 行
- **鉴权**：Bearer token+X-Zcode-Role 头（enroll 写两侧）；无 token=401 即路由可达证明

### doc-mailbox 协议要点（v1.1）
- 三硬规则：append-only 物理末尾追加 / 时间戳单调 / 游标=物理 EOF（文件块数增长判新）
- 类型：DISPATCH/QUESTION/INFO/DONE+HUMAN 优先；裁决结果必须落留言板（防守护/对方信息差）
- bus 上线后：bus=机器层实时通道，留言板=稳态+fallback（bus 故障降级写留言板）

## 首例（权威参考）
- 夜间批 20261001：十五件零跳过（安全批 5+432+430+bus 系 5+425+420+421），全程留言板通信，报告 `docs/report/zcode交付/`
- 协议：`docs/collab/zcode/README.md` v1.1

## 使用中持续改进
- **20261001 #1 blob 层审计纪律**：zcode 多代理分组期，共享 worktree 工作树常有他人在飞未提交改动（两次复现 go test setup failed 假红）——**审计一律 committed blob 层定案**（临时 worktree @sha 或 git show），共享树状态不作判据；派单模板已固化。
- **20261001 #2 超集替代规则**：同代理串行件（424→380）后者基底含前者 commit 时，**超集分支直接替代单合**（少一次合并），前件审计结论随包含有效。
- **20261002 #3 登记≠派出**：收货登记写「送审计-X」但漏发实际 talk_to_session 审计单=派单落空（320 实锤，审计-2 消息交叉发现才主动补开工）——**先实际发审计单，同轮再登记 status=已派**；两步必须同轮完成。
- **20261005 #4 发出≠消费（收货前先验对方是否真的读过）**：`talk_to_session` 的 sent 日志与 `queued` 状态**只证明消息到了对方邮箱**，不证明有人消费。当日两次误判——一次把「派给 `4.reasonix` 组的人工会话」当成对方异常，一次把「卡在 recovery fence 等人工核实」当成对方空闲/派单失败。**规范**：① 派单对象只选 `reasonix-for-ai` 组（`4.reasonix` 是人工用会话，不得派）；② 收货前用 `read_session_tail(<contact>)` 看对方尾部**实际做了什么**；③ 会话文件「只动 `.meta` 不算活动」，正文 `.jsonl` 变了才算。
- **20261005 #5 被 fence 拦住的会话在 UI 上显示为「空闲」**：`recovery fence` 阻塞态不在状态机枚举内（`desktop/frontend/src/lib/projectTreeTopic.ts:406` 的活跃计数判据缺该态）→ 会话被拦时**底部活跃数不含它、界面无进展**，与真空闲无法区分。**协作者「静默」排查序**：① `read_session_tail` 看尾部有无 `tool_recovery` / `"state":"started"`；② 若有 → 它在**等人工核实**（UI 点「检查状态→核实生效」即续轮），不是怠工；③ 再考虑真静默。任务 482 正在去阻塞化，落地后该态将显式可识别。
- **20261005 #6 会话打不开 / 投递被拒先查 lease 残留**：出现 `this session is already open in another Reasonix window` 且全机只有一个 desktop 进程时 = **本进程 lease 泄漏**（`desktop/detached_idle_release.go:109 releaseDetachedSession` 的 308-O4 释放链漏 `Release`，任务 485）→ 该会话永久 busy、collab 投递被拒。**处置：重启 desktop 即自愈**（OS 锁随进程退出释放）；**不要手删 lease 文件**（锁在活进程句柄里，删了也不生效）。


---

## 心跳任务模板（2026-10-07 精炼版，源自 guard-fork3 守护心跳多轮迭代实测）

> 多轮迭代沉淀的守护心跳写法。四件套定位：心跳=触发点（到点点名），流程细节在技能，状态在留言板/tasklist，判据在本模板。

### 模板骨架（六段，直接套用）

```
【总览·<日期> <定调来源>】
① <主对话角色与协调节奏>（contact/topicId）
② <执行线核心目标，分 A/B/C 组>（每件带任务号与交付形态）
③ <在飞收尾件>（tip/sha 现状）

【唤醒先读（两通道互不替代）】
① 总线收件箱：get_session_status / peek_own_inbox / read_session_tail（不可用时 drain_inbox）；
② 外部留言板：<板路径>（独立状态源，非总线降级通道）。
两通道皆无新留言、且无状态变化 → 一句话结束。

【职责（禁令照旧）】
只做三件事，禁止写代码 / 改任务 / 跑测试：
① 读状态：<板路径>（有新留言必须在唤醒消息里提示主对话收货）+ <人工核实清单路径>；
② 查活跃：get_session_status 查主会话（topicId=<id>）与全部在飞子对话；
③ 唤醒决策：主会话 running → 不打扰，一句话结束。

【判据（实测沉淀，继续有效）】
- auto_guard 检查不算实质推进；主会话近 2 小时无实质动作（无板写入 / 无 git 提交 / 无派单）⇒ 即使 running 也发常规唤醒（带状态指针：在飞件/待收货件/阻塞项）；
- 留言板例外：板上有未收货新留言（新于主对话最后「已读至」游标）⇒ 周期到即发【守护唤醒·留言板收货】；
- ❌ 不要报告「在飞子代理 N 个」（无观测通道）；worktree dirty ≠ 有子代理在飞；
- ✅ 只报告可直接观测事实四项：① 板新留言+字节数；② 主线 tip+未动时长；③ 已交付未合并分支数（wt-* tip 不在主线祖先链）；④ 主对话 lastActivity。

【项目专属上下文】
- <仓库路径与 worktree 规范>；<tasklist 工具与权威源>；<口径约定>；<出包/装机脚本与验证命令>；<报告目录>

自愈：删除本心跳以用户明确确认为准。
```

### 迭代沉淀的四条硬判据（模板的核心价值，勿删）

1. **2h 无实质动作即唤醒（即使 running）**——auto_guard 自答不算推进（2026-10-06 zcode 队列清零空转 1.5h 实测）；
2. **留言板例外优先于静默**——有未收货留言必唤醒，不等 2h；
3. **不臆测不可观测态**——「在飞子代理数」无通道即不报；`worktree dirty ≠ 子代理在飞`（误报实录两起）；
4. **只报四项可观测事实**——板字节/tip 时长/未合分支数/lastActivity，每项带实测命令。

### 配套：执行线并行度心跳（zcode 日/夜切换）

- 夜间（23:00）放宽 zcode 并行子代理上限至 6~9；日间（09:00）收敛回常规——两条独立心跳，prompt 各 3~5 行只改并行度数值与窗口说明。
