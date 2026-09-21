---
name: ll-iteration-intake
description: 迭代回路 阶段 1/3——日常反馈与问题分析，把意见箱/崩溃残留/性能告警/tasklist 状态/上游动向扫成「需求初稿」写进候选池等人确认；支持对意见箱做分类统计与痛点聚类分析（原 feedback_analysis 能力已并入）。当用户说"跑一轮迭代巡检""收一下反馈""分析意见箱""最近有什么要做的""盘点待办"，或 heartbeat 定时任务点名本技能时使用。
runas: inline
---

# 迭代收口（阶段 1/3）：反馈 → 分析 → 需求初稿

> **回路位置**：本技能 = 阶段 1（感知与初稿）→ [[ll-iteration-plan]]（排序与范围）→ [[ll-iteration-parallel-dev]]（执行与发版）。
> **边界（硬）**：本技能**只产出初稿到候选池**。不写正式任务、不改代码、不出包、不动 `01-总表.md`。
> 正式任务必须经**人工确认**后按第 5 步走「确认→入库」路径。
> 本技能为**本地技能**：不需要跨会话协作即可运行（区别于阶段 3 的 [[ll-iteration-parallel-dev]]）。

---

## 0. 唯一输出物

| 输出 | 位置 |
|---|---|
| 需求初稿条目（追加） | `docs/tasklist/16-需求候选池.md`（不存在则用第 3 步模板新建） |
| 意见箱深度分析（可选，用户点名时） | `feedback-inbox/analysis-YYYYMMDD.md` |
| 收尾摘要（对话输出，一行） | 新增候选 N 条 / 滞留 M 条 / 异常项 |

工具：写文件用 `write_file` / `edit_file`（**不要** PowerShell `Set-Content`，中文会坏）。

---

## 1. 五个输入源（逐个扫，不跳）

| # | 源 | 位置 | 判据 |
|---|---|---|---|
| 1 | **意见箱** | `%APPDATA%\reasonix\feedback-inbox\feedback-*.md` | 主目录下的即为**未消纳**；**跳过 `archived/` 子目录**（已处理项都在里面）；`analysis-*.md` 是产出不是输入 |
| 2 | **崩溃残留** | `%APPDATA%\reasonix\crash-pending\`、`crash-fatal\` | 目录内有新文件（非空）即报警；读里面的摘要/日志片段 |
| 3 | **性能告警** | `%APPDATA%\reasonix\logs\desktop\desktop.log` | 全新 `perf monitor threshold`（metrics：workingSetMb / eventsMb / storeMb / v4OperationMb）——按 metric 聚成一条候选，别每 3 秒一条 |
| 4 | **tasklist 状态** | `docs/tasklist/`（`python scripts/tasklist_db.py sql "..."`） | `watching` 到期该复看、`partial` 有剩余项、`blocked` 的外部条件可能已解 |
| 5 | **上游动向** | `gh api` 查 esengine/DeepSeek-Reasonix 新 issue/PR | 涉及 fork 魔改面（classic 布局、协作工具、实验开关、v4 存储）才立候选；其余只记录不立项 |

**扫法提示**：
- 源 1/2 是"文件新增"判据 → 与上一轮巡检的产出对账，避免重复立候选（先读候选池里已有条目）
- 源 3 先看时间窗（只取上次巡检之后的告警），别把整份日志捞一遍
- 源 4 用 SQL 精确取，不要全文读 md（9000+ 行）：`SELECT num,status,COALESCE(due_at,'-'),substr(title,1,40) FROM tasks WHERE status IN ('watching','partial','blocked')`

---

## 2. 分析：每条候选都要过三问

1. **是真问题还是已知/已修？** —— 查 `tasklist_db.py sql`（同主题任务）+ `git log --oneline -20`（近期是否已修）；引用包版本的反馈要核对后续包是否已修。已修待实测 vs 新缺口 要分开写。
2. **影响面多大？** —— 排序参考：阻塞日常 > 数据安全/丢失 > 高频摩擦 > 体验改进 > 内部整洁。写清"谁受影响、多大、是否阻塞"。
3. **证据够不够立初稿？** —— 够：现象 + 时间戳/日志片段/截图路径/复现命令。不够：条目仍要记（状态写「证据不足」+ 还缺什么），但不进阶段 2 排序。

---

## 2.5 意见箱深度分析（用户点名「分析意见箱」时的增强模式）

> 原 `feedback_analysis` 内置技能已并入本节（2026-09-21）。日常巡检走第 1-4 步即可；用户要求**单独出一份意见箱分析**时，在本轮巡检中追加本模式的产出。

对意见箱全部 `feedback-*.md`（跳过 `archived/` 与 `analysis-*.md`）做一次结构化分析，写 **一个** 文件 `analysis-YYYYMMDD.md` 到意见箱目录：

- **TL;DR 计数**：按 category（`bug` / `idea` / `praise` / `other`）与 tag 频次统计
- **痛点聚类**：相似文本聚成组（同一 UI 面 / 同一失败模式），每组计数并引用 1-2 条短原文
- **建议下一步**：Top 3-5 条具体可执行动作（按聚类规模 + 新近度排序）
- **不删除、不改写**源笔记

---

## 3. 条目格式（写进候选池）

```markdown
### C-YYYYMMDD-NN 一句话标题
- 来源：feedback-YYYYMMDD-HHMMSS / perf 告警 / 用户对话 / 上游 issue#NNNN
- 现象与证据：<时间戳 + 日志片段 + 截图路径 + 复现命令>
- 影响面：<谁受影响 / 多大 / 是否阻塞日常>
- 建议优先级：P0-P2 + 一句理由
- 预估：S/M/L（改动面）
- 关联：任务 NNN / 记忆条目 / 上游 issue
- 状态：待确认 ｜ 已确认 → 任务 NNN ｜ 已拒绝（理由）｜ 冷置（超 30 天）
```

编号 `C-YYYYMMDD-NN`：按天递增，同日从 01 起。

> ⚠️ **绝不要**在候选池里用 `## 任务 N` 标题格式——`tasklist_db.py scan` 会把它当正式任务入库，污染权威源。

---

## 4. 消纳：每条感知产物必须有终态

| 情况 | 处理 |
|---|---|
| 真是新问题 | 立候选（第 3 步） |
| 与已有候选重复 | 合并到较早那条，补证据，注明"（合并自 X）" |
| 已是已知任务/已修 | **不立**，但要在收尾摘要里说明"X 已由任务 NNN/提交 sha 覆盖"，并在源文件标记 |
| 无可执行价值 | 记「已拒绝 + 理由」（短，一行），避免下轮重复分析 |
| 证据不足 | 立候选但标「证据不足」，写清还缺什么 |

**源文件归档**：意见箱条目处理完后 `mv` 进 `feedback-inbox/archived/`（**目录即状态**；若文件名带旧 `-已转入tasklist` 后缀，一并去掉）；**未处理的留在主目录不动**。归档时在正文首行追加一行 `> 已转入任务 NNN · YYYY-MM-DD`，便于回查去重。

**冷置**：候选池里超过 30 天仍未确认的条目 → 状态改「冷置」，并在后续巡检输出里折叠（不删除、不重复报告）。

---

## 5. 人工确认后 → 纳入 tasklist（交接给阶段 2）

人工挑了要做的候选后，按此路径入库（**已由任务 195 全流程验证，无需新代码**）：

```bash
cd docs/tasklist
python scripts/next-task-id.py                 # 取号即占坑（防多会话撞号）
# → 写正式正文到对应领域文件（保留候选池里的证据链 + 补 file:line 与可证伪验收）
# → 01-总表.md 加登记行
python scripts/tasklist_db.py scan             # 重建派生索引（必跑）
python scripts/tasklist_db.py sql "SELECT num,status,title FROM tasks WHERE num=N"   # 逐个校验
git add <显式路径> && git commit -m "tl: ..."   # 内层仓库，禁止 add -A
```

入库后把候选池该条状态改为 `已确认 → 任务 NNN`。

**排序与范围由 [[ll-iteration-plan]] 负责**——本技能不做优先级裁决。

---

## 6. 沉淀动作（判断层回路，每轮都做）

扫最近 N 份「单轮开发计划」（`handoff/*单轮开发计划*.md`）里的**「决策记录」段**：

- 同一类**决策理由**出现 **≥2 次** ⇒ 生成一条候选（来源写「决策沉淀」），内容 = **技能补丁提案**：把该理由写进 [[ll-iteration-plan]] 的判据表（**不直接改技能**，走人工确认）
- 升格被接受后，对应的记忆条目降级为「证据」（保留但不再作为执行依据）

> 这是「记忆记偏好 → 沉淀回技能」的触发点：记忆是弱约束（召回不保证），技能是强约束（点名必读）。

---

## 收尾报告（对话输出，一行）

```
迭代巡检 YYYY-MM-DD HH:MM ｜ 新增候选 N 条（P0 x / P1 y / P2 z）｜ 已拒绝 K 条 ｜ 滞留/异常：<一句话>
```

新增为 0 时只输出：`迭代巡检 <时间>：无新增候选。`

---

## 关联

- [[ll-iteration-plan]]（阶段 2：排序与范围）｜ [[ll-iteration-parallel-dev]]（阶段 3：执行与发版，**依赖跨会话协作**）
- [[collect_issues]]（意见箱的既有专用技能；本技能覆盖更广，两者对源 1 的处理一致）
- 候选池：`docs/tasklist/16-需求候选池.md`
- 方案文档：`docs/自主迭代-执行层缺口分析与落地方案-20260920.md`
