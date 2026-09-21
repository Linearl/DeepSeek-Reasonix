---
name: ll-iteration-parallel-dev
description: 迭代回路 阶段 3/3——多 worktree 并行开发全流程：主会话分解任务、建 worktree、派开发会话、拉取式收货、合并（交专门会话）、独立审计、发版与回写 tasklist。信息通道 = 每线一个状态文件 + 短决策循环（拉取代推送）。当用户要求"用并行会话/worktree 开发多个任务""分工给几个会话并行做""并行开发然后你合并""发版/出包并回写任务清单"时使用。⚠️ 依赖跨会话协作——未在实验室开启「跨会话通信」总开关时本技能不可用。
runas: inline
---

> **回路位置**：[[ll-iteration-intake]]（初稿）→ [[ll-iteration-plan]]（排序与范围）→ **本技能**（并行开发 → 发版 → 验证 → 回写）。
> 本技能接收阶段 2 产出的「单轮开发计划」（`handoff/<会话名>-单轮开发计划-YYYYMMDD.md`），其中每条任务的**验收标准必须可证伪**——第 6.2 节直接对着它验证。
> **⚠️ 依赖声明**：本技能需要**跨会话协作**（`talk_to_session` / `create_collab_session` / 收件箱）。未在 **设置 → 实验室 → 跨会话通信** 开启总开关时，协作工具族不注册，**本技能不可用**。开启前已打开的会话需重建控制器才能看到协作工具。
> **沿革**：本技能由 v1（parallel-worktree-dev，七步流程+发版链）与 v2（拉取式信息通道，2026-09-21 用户拍板）合并而成，2026-09-21 起为唯一并行开发技能；决定依据 = `issues/reports/并行协作跨会话汇报滞后-根因诊断-20260921.md` 第 8 节。

# 并行 worktree 开发（管理只统筹 · 拉取代推送）

主会话 = 指挥官：不写业务代码，负责任务分解、派活、合并协调、审计协调、出包。
开发会话 = 执行者：在独立 worktree 里改代码、自验、写状态文件。
合并会话 = 专门负责合并的会话（⚠️ 合并不由管理会话自己扛）。
审计会话 = 独立第三方：只读审查合并结果，出 PASS/修复项。

## 0. 铁律

1. **管理对话不介入具体开发，只负责任务统筹。**
   - **允许**：定位分析、调研、短问题处理、**文档撰写**、执行命令（只读定位类）、派活、拍板
   - **禁止**：**写业务代码**、跑实现类长任务、深挖单点（判据：**同一问题超过 3 次工具调用仍定位不到 → 派给 wt**）
   - **代码合并交给专门的对话**（合并线，如 `wt-merge`）
2. **每轮 = 短决策循环**：`读状态 → drain 收件箱 → 派活/拍板 → 尽快结束 turn`。管理会话轮次应保持**个位数**。
   - 反例实证：某批次管理会话跑到 **100 轮 / 上下文 98.81%**，同期待办区堆积 **9 条**跨会话消息 —— 长 turn 就是滞后的直接原因。
3. **消息可以带状态正文**（人类阅读方便、不必跳文件），**但同时要落盘一份**（留痕、有据可查、便于后续引用）。

## 1. 状态文件（唯一的进展通道）

- **每个 wt 一个文件**：`tasks/<批次>-进展-<线>.md`（管理会话读目录下多个文件；**不共用单文件**，避免多进程并发追加交错）
- **备选（记录待用）**：一行一 JSON 的 jsonl（抗交错 + 与状态流同构）——若"多文件管理麻烦"或"需要结构化查询"再切换
- **格式**（每个文件内，一行一条）：

```markdown
| 时间 | 状态 | 阻塞点 | 需要管理决策 |
|---|---|---|---|
| 09:12 | 开发中 | 无 | — |
| 09:40 | 待收货 | commit d5e86f423 已推 | 是否并入本轮出包 |
```

- **wt 义务**（写进派活消息）：① 开工写一行 ② 有意义进展写一行 ③ **需要拍板的事写进最后一列**
- **管理义务**：每个 turn 开始**先读状态文件目录**（`read_file`），不等消息
- **读取成本**：只读需要的部分；文件按批次分、天然有界（防线：别把状态文件读成"读放大"）

## 2. 消息通道（信号 + 内容并存）

| 类型 | 内容 | 期望行为 |
|---|---|---|
| **唤醒** | "状态文件已更新"（可带摘要正文，人读友好） | 管理下一轮读文件 |
| **阻塞** | "需立即拍板：<一句话 + 2-3 个选项>" | 走 steer（窗口内即时；窗口错过则排队，wt 可继续做别的） |
| **进展** | 可直接写在消息里（人读方便）+ **同时写进状态文件**（留痕） | 管理按需读 |

**「需立即拍板」标注标准（三条全满足才可标，防滥用）**：
① 阻塞（不拍板 wt 只能空等）② 有时限（拖延有明确损失）③ 有选项（2-3 个明确选项）。
禁止标注：进展汇报 / 知悉类 / "顺便问一下" / wt 可自行决定的小事（自行决定 + 事后写进状态文件）。
⇒ 滥用可查：标注频率可从状态流统计，作为批次复盘依据。

## 3. steer 的定位

- `delivery: steer | followup`：默认 `followup`，**steer 被拒自动降级并告知发起方**（`internal/control/inbox_steer.go:161-200`）
- ⇒ steer 是**尽力而为**的即时通道，**只适合紧急打断 / 阻塞拍板**，不当日常汇报通道
- ⇒ 判断标准：**"这条消息晚 10 分钟到达会有什么损失？"** 无损失 → 写状态文件；有损失 → 发消息
- `interrupt`（中止当前 turn）**暂不做**；若出现"必须立刻停某条线"的事故再立项

## 4. 触发方式

- **信号（a）**：wt 更新状态文件后发一条唤醒消息
- **轮询兜底（b）**：管理侧用 heartbeat 定时拉一次状态目录
- **⚠️ 任务完成后必须关闭对应心跳任务**（轮询型心跳是临时手段；忘记关会常驻烧资源）

## 5. 流程七步

### 5.1 任务分解（动手前）

- 每个任务先读 tasklist 正文，确认「要做/验收」细节；没写清的自己补全再派。
- **按域分组**：同域任务给同一会话（前端归前端、Go 归 Go、全栈单独），减少跨会话文件交集。
- 每个会话的指令必须包含：worktree 绝对路径 + 分支名 + 基线 commit、任务正文位置（文件+行号）、
  具体范围、验收标准、汇报要求（commit hash/改动文件/测试结果）、禁项（不 push/不出包/不动 tasklist）。

### 5.2 基建（worktree + junction）

```bash
# 在主仓（reasonix）里建，基线取当前 main-v2-stable HEAD
git worktree add ../worktrees/wt-A -b wt-A main-v2-stable
git worktree add ../worktrees/wt-B -b wt-B main-v2-stable
# 前端任务需要 node_modules：建 junction 指向主仓（省 2min install）
cmd //c "mklink /J wt-B\\desktop\\frontend\\node_modules ..\\..\\..\\reasonix\\desktop\\frontend\\node_modules"
```

- 命名规范：`wt-<主题>`（放 `github-repo/worktrees/`，无下划线前缀）。
- **junction 是借主仓的依赖，不是自己的**——派活消息里要写明「pnpm 结构若报缺依赖就用主仓跑」。

### 5.3 会话编排

- **统一分组**：所有 wt 开发会话/审计会话必须 `create_collab_session(group=...)` 建进**同一个对话分组**——用户在分组下集中查看与管理全部并行会话，不要散建。
- **复用优先是偏好行为，不是硬规则**：默认动作是先 `search_sessions`/`list_addressable_sessions` 查会话目录，同领域已有会话**倾向复用**（积累经验教训 + 上下文缓存命中成本低）。但**新建同样有正当好处**（干净上下文、无历史包袱、任务隔离清晰），故：①权衡后选择即可；②**用户显式要求新建时，一律以用户意见为准**；③新会话建好后 purpose 写清领域职责。
- 建完立刻用返回的 contact_id 派活（talk_to_session，to=contact_id）。**派活消息写 contact_id 让对方回信有址**。
- 审计会话此时就建好（省一轮），等各线齐再派审计指令。
- 一批活跃会话 ≤4 个，防 429。

### 5.4 收货（拉取式）

```text
管理会话每个 turn：
  1. 读 tasks/<批次>-进展-*.md（先拉，不等）
  2. drain 收件箱（消息按 §2 三种类型处理）
  3. 对"待收货"的线：核对 commit / 验证清单 / 预存失败归属（三件）
  4. 派下一步 / 拍板；**合并类工作交给专门会话**
  5. 结束 turn（不顺手做实现）
```

核对三件：commit hash 落在指定分支、验证清单全绿、预存失败用 `git stash` 对照标注归属。
- **汇报里报的「预存失败」必须逐条处置**：本轮引入 → 立即修；确为预存 → 记录归属，不阻塞合并。
- 收货确认消息发回开发会话（它就完成了，可以休息）。

### 5.5 合并（专门会话做，不在管理会话里合）

```bash
git worktree add ../worktrees/wt-merge -b wt-merge main-v2-stable
cd ../worktrees/wt-merge
git merge wt-A --no-edit && git merge wt-B --no-edit && git merge wt-C --no-edit
# 合并后全量验证：go build 两模块 + 相关包 test + tsc + 关键 tsx
```

- **零冲突 ≠ 语义正确**：合并后必须核对「基线独有改动是否被 ours 侧保留」
  （三分支都从旧基线建时，diff 里的删除行可能是基线领先而非分支删除）。
- 高风险交叠点逐一 grep：render.go 键、bundle budget ratchet 数值、共享组件两特性共存。
- 跨线「改动是否真在包里」的核对：diff 非空 ≠ 被冲掉（基准错位），用**祖先性 + 关键行逐字**双证据。

### 5.6 审计（独立会话，只读）

审计指令按风险排序 5 项左右，附「已验免重跑」清单。审计产出：
- **PASS/修复项**：修复项回开发会话迭代（或合并线修）；PASS 则闭环。
- 审计的「建议级」（S1-S4）逐条处置：能立即核实的核实；UX/健壮性类记 tasklist 后续；流程类写进本 skill。

### 5.7 发版与回写（后半程——四小节，逐项打勾）

#### 5.7.1 发版链（版本号与台账纪律）

- **版本号不自增**：复用 fork 当前版本号（现为 `1.38.3`），包名带构建时间戳 `1.38.3-YYYYMMDD-HHMM`；`versionDirRE` 只收 `-` 后缀。
- **release notes 先于构建**：增量基线 = 上一时间戳包，**只写增量不堆叠历史**；文件与包版本同名。
- **正式发版**：`release-fork.yml` workflow 手动 dispatch + 传 tag（`desktop-v1.38.3-<时间戳>`）；workflow 会找 `release-notes/FORK-v<基线版本>.md`（时间戳剥掉）并**硬性要求**引用 `FORK-vs-upstream.md`——发版前台账必须先加本版表。
- **出包**：用户明确要求才出（**不主动构建安装包**）；`bash scripts/build-local-installer.sh`；**不要用 wails `-nopackage`**；构建前 `TestDesktopRenderTableCoversEveryKey` 必须绿。
- **台账留档（每次必做）**：`handoff/fork开发-出包台账-*.md` 追加一节（出包时间/版本/HEAD/产物字节数/相对上一包新增提交/已知未含项/验证状态/用户测试结论）。
- **不自动合并主线**：落地默认保留分支 + 交分支名，合并由用户指定会话执行。

#### 5.7.2 人机协同验证（证据三件套 + 基线对照）

- **证据三件套**（缺一不算验过）：**现象** + **日志**（时间戳片段）+ **截图**。修复类必须给「改前 vs 改后」对照。
- **基线对照铁律**：宣布复现或修复前，**同一条命令在改动前后两侧都跑一遍**——合成会话 bench 测不到 app 级运行时契约。
- **性能类**：用 `scripts/perf-probe/`；内存类第一工具是 **heap profile**，不是版本二分。
- **装机验证是硬闸**：包交用户装机 → 用户实测反馈（现象+日志+截图）→ 才算闭环。
- **任务正文的验收条目就是对账单**：阶段 2 写的可证伪验收，逐条勾；勾不了的写「未验 + 为什么」。

#### 5.7.3 回写 tasklist（脚本路径，禁手搬）

```bash
cd docs/tasklist
python scripts/tasklist_promote.py NNN --dry-run    # 先预览（仅 done 可迁）
python scripts/tasklist_promote.py NNN              # 迁移：正文整块搬入已完成文件 + 待办侧留指针 + 总表三处同步
python scripts/tasklist_promote.py --check          # 一致性体检
python scripts/tasklist_db.py scan                  # 重建派生索引（必跑）
python scripts/tasklist_db.py sql "SELECT num,status FROM tasks WHERE num=NNN"   # 逐个校验
```

- **标记写法**（写错 = 状态静默不生效）：`✅` 必须在「任务」**之前**；`done` 需「日期 + 完成」紧邻；`partial` 用「剩余仅」；`watching` 用 `⏳ 观察中`；`blocked` 用 `⏸ 阻塞`。partial 的 `done_at` 留空是正常的。
- **回写依据是提交，不是记忆**：`git log --oneline | grep "task N"` → `git merge-base --is-ancestor <sha> HEAD` 确认已进当前分支；自称 `slice`/`A级` 的按 `partial` 处理。
- **提交用显式路径**（禁 `-A`/`.`）；并行会话共用仓库时先 `git status`。
- tasklist 只由主会话统一更新（写进派活禁项，避免并行冲突）。

#### 5.7.4 收尾归档

- handoff 自包含（`<会话名>-<主题>-<YYYYMMDD>.md`；3 天保留策略）。
- 一次性测量/诊断脚本归档到 `scripts/<主题>/`，不要留在 `tmp/`。
- 清理：先删 junction（`cmd //c rmdir <junction>`，**必须验证成功**）→ `git worktree remove --force` → `git branch -D`；磁盘紧张时 `go clean -cache`。
- 本轮结论若要复用 → 写记忆（技术判断 `activation=relevant`）；流程类写进本 skill。

## 6. ⚠️ 大坑与教训（实战全部踩过）

1. **rm -rf 会跟随 Windows junction 删掉目标内容**。删含 junction 的目录树前：先 `cmd /c rmdir <junction>`（只删链接），**必须验证删除成功**，绝不 `2>/dev/null` 吞错——首轮就是这么把主仓 node_modules 删掉的。
2. **junction 删除失败会被 git worktree remove 以 "Directory not empty" 拒绝**——先清 junction。
3. **new Desktop keys 必须带 render.go 渲染行**，否则保存被静默丢弃（`TestDesktopRenderTableCoversEveryKey` 是唯一防线）。
4. **新实验开关走全链路七件套**：config→setter→渲染表→UI→bridge→types→locales（漏一环=保存丢）。
5. **boot 快照 vs 调用时求值**（S4 约定）：collab 类工具的会话路径/身份一律调用时求值，不要 boot 快照。
6. **前端测试用 `npx tsx <file>` 直跑，绝不用 vitest 路由**——前端 harness 是自建的，vitest 报 `No test suite found` 是正常现象不是环境损坏。
7. **heredoc 写含反引号的 markdown 会被命令替换**——markdown 一律用 write_file/edit_file。
8. **主会话派活消息必须带收信地址**（contact_id），否则对方回不了信。
9. **worktree 里跑构建可能报命令找不到**——先确认 junction/依赖完整再归因代码。
10. **并行会话写同一 tasklist 文件会冲突**——tasklist 只由主会话统一更新。
11. **跨会话回信 thread_id 必须用「自己收到的入向消息 id」**（用自己发出的 id 会链路校验失败）；送达判定看对方 `inbox.jsonl` + `seen.json`，`queued` ≠ 已读。
12. **write/edit 在大文件整体替换时会要求"写前已全读"**——`intent=full` 单次约 31KB；`range` 读的累计覆盖不被承认。
13. **对应用会写回的配置文件打补丁，幂等判定必须段级**（应用渲染会重排字段位置 + 加对齐/注释，只查「name 行下一行」必然失效——2026-09-21 provider hidden 实战）。

## 7. 时序参考（首轮实测）

分解 10min → 基建 5min → 三线并行开发 ~60-90min（Go 全量测试 3-6min/线）→
合并+验证 15min → 审计 20min → 收尾（清理/notes/构建）~15min。全程约 2.5-3.5h，
相比串行（六任务 ×1.5h）节省一半以上。

## 8. 反模式

1. 管理会话跑实现类任务 → 长 turn → 消息全排队（截图实证）
2. 把 steer 当日常汇报通道（"尽力而为 + 被拒降级"不适合做稳定通道）
3. 多线共写同一个状态文件（并发追加会交错）——用**每线一文件**
4. 指望"优化消息传输速度"解题（延迟来自"等 turn 结束"，不在路上）
5. 用完心跳不关（轮询型心跳是临时手段）

## 关联

- 迭代回路：[[ll-iteration-intake]]（阶段 1：初稿）｜ [[ll-iteration-plan]]（阶段 2：排序与范围）
- 诊断：`issues/reports/并行协作跨会话汇报滞后-根因诊断-20260921.md`（拉取式通道的决定记录）
- 任务：143（steer|followup）、**202**（C 阶段状态流引擎化，落地后人工写状态退为兜底）、19（协作底座 done）
- 方案文档：`docs/自主迭代-执行层缺口分析与落地方案-20260920.md`
- `reasonix-win-build`（出包细节）；`gh-issue-submit`（上游反馈）
- fork 开发八条铁律（memory）；出包台账 `handoff/fork开发-出包台账-20260914.md`
