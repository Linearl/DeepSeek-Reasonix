# Reasonix Fork 桌面版 v1.38.3 — Release Notes

> 本版本基于官方 `v1.38.3`，整体追齐上游 1.38.2~1.38.3 全部演进，并保留全部 fork 增强与修复。数据/会话/记忆目录与官方版完全兼容，**覆盖安装即可，无需迁移**。
> v1.38.1 的追齐内容（1.35~1.38.1 演进 + 压缩双修复 + 分段压缩并行化 + 长任务体验）已包含在内（详见 [v1.38.1 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.38.1)）。
> **Fork 与上游差异全览**：见 [FORK-vs-upstream.md](./FORK-vs-upstream.md)。

## 使用攻略

- 覆盖安装即可，无需卸载、无需迁移数据。
- 所有 fork 功能入口：**设置 → 权限 / 通用** 或**会话输入框右上角**。

## 概览

**Reasonix Fork v1.38.3 — 追齐上游 1.38.2 ~ 1.38.3（283 个提交）**

上游两个版本以**稳定性与正确性修复**为主：Windows 原子快照重试、会话列表写入围栏、MCP 子进程先退役后取消、设置快照选择器运行时校验、连接标签刷新、元数据读取隔离等。

fork 侧：**魔改全部保留**，并按上游重构同步了配置与能力层（图片能力判定、推理档位迁移、状态栏默认值、jobs 拆包），并恢复了本次 merge 中静默丢失的 13 个文件与 2 处 fork 功能。

发布日期：2026-09-09

## 🔁 追齐内容（v1.38.2 ~ v1.38.3）

- **Windows 稳定性**：任务监控原子快照发布重试、并发更新读取保留规范状态
- **会话正确性**：延迟列表写入的运行时权限围栏、可移植 transcript 文件名校验
- **MCP 生命周期**：子进程先退役再取消其 context，避免僵尸进程
- **设置 / 桌面**：快照选择器运行时边界校验、会话恢复后连接标签刷新、元数据读取与提示历史身份隔离
- **Agent 健壮性**：被放弃的批处理 goroutine 在下一轮状态重置前等待完成；controller 发布路径记录重建授权
- **inbox / 控制**：controller 关闭时 join inbox 扫描

## 🔧 Fork 修复（本轮）

### 追齐适配（保魔改 + 采用上游重构）

- **图片能力判定**：官方 DeepSeek 端点在 provider 层保持保守（仅固定 vision SKU 直接可用），逐模型 override 仍驱动 wire 门控与能力解析器——**无任何 SKU 硬编码**，新模型由能力探测与用户开关决定
- **GLM 强度档保留**：上游 `applyReasoning` 重构后，fork 的 `low/medium/high/max → reasoning_effort` 映射完整移植（构造期不再被二元能力表拒绝）
- **推理档位迁移**：旧配置里的 `medium`/`xhigh` 别名在读取时迁移为 `high`（即使能力表尚无该模型条目）
- **状态栏默认值**：保持 fork 的 `text` 默认（上游改为 `icon`）
- **jobs 拆包**：采用上游 `jobs/start/runtime_state/artifacts/evidence` 拆分，fork 的 `resultDigest` / TPS / rate 语义完整保留

### merge 静默丢失的恢复（13 个文件 + 2 处功能）

- **11 个上游 agent 测试**（fleet / fleet_graph / session_lease / spawn_boundary / subagent_progress ×2 / task_background_queue / task_profile / compact_threshold / delegation_origin / zz_probe2）——merge 的 modify/delete 冲突取了删除侧；恢复后 **155s 全绿**
- **2 个 Topicbar 组件**（TopicbarExportMenu / TopicbarSessionActions）——`TopicbarActionsRegion` 仍引用后者
- **fork 独有 `goalSubmit.ts`**——App.tsx 仍引用
- **#9221 颜色筛选**：移植到上游拆分的 ProjectTree（状态 / refs / 过滤 / 控件 / 两处调用）
- **#9567 接管钉尾**：按上游 kernel 适配（`setScrollMode("tail-follow")` + `scrollToBottom`）

### 构建与预算

- 前端 `tsc --noEmit` **0 错误**——修掉 merge 截断的 Composer approval modebar（该语法错误此前掩盖了其余 89 个诊断）
- `pnpm build` 全绿；预算按实测重定：deferred CSS **122.0 KiB**（上游 1.38.3 自身上调到 120.4）、中文 locale **66.0 / 66.5 KiB**
- `check-app-entry-contract` 对 fork 的 App.tsx 巨石加显式豁免（`FORK_MONOLITH_APP`）
- `check-fork-integrity` **30/30**——清理 9 项已由上游等价实现或 1.38.1 对齐时移除的过期检查

## 🆕 Fork 增强与修复（v1.38.3 后续轮次）

- **模型测试增强**：设置里的模型「测试」按钮（插座图标）从"仅连通性"升级为**实测指标**——流式跑到结束，显示 **`首字 N ms · M tok/s · K tokens / G ms`**（provider 未返回 usage 时退回两指标行；非流式端点自动降级为首字延迟）。1.38.3 merge 时被静默丢弃的接线已恢复
- **自动化任务可配置化**：任务编辑支持 **provider / model 下拉**（数据源为已配置供应商，缺失时降级为文本框）与 **goal 模式开关 + 目标文本**，并清除旧任务遗留的 goal 状态
- **`todo_write` 压缩后死锁修复**（对齐上游 #10023）：压缩提交后清空本回合的重复结果指纹（字节相同的合法 `todo_write` 不再被当作重复吞掉）；两条状态机校验错误**附当前清单全文**；被去重的工具结果**附当前清单**，便于模型自行纠正
- **"活没干完就停"修复**：核心策略新增 **AutonomyPolicy**（对齐 codex 的 persistence 段——做完再报告，不停在分析或计划）；新增**意图声明 nudge**（宣布"我将先做 X"却结束回合时自动续跑一次，限次）；Auto recovery 的 **Episode 失败预算 6 → 8**，多文件长任务不再被过早硬停
- **子代理 claim 死锁 fail-fast**（上游 #9688）：父 turn 持有重叠写 claim 时，`AcquireWithID` 直接失败而非排队等自己，避免 depth-0 子代理永久挂起
- **JSON 提取统一**：连续块解析的 `lastJSONObject` 抽到 `internal/jsonutil`，goal 评估器与 recovery 评审器共用（删除两份重复实现）
- **文件面板隐藏规则修正**：仓库生成物（`tmp`/`bin`/`stage`）全局隐藏，`@` 引用搜索额外跳过这些目录，二者不再耦合
- **GLM 思考档位修复**：输入栏的强度档位此前只剩「自动 / enabled / disabled」——根因是桌面渲染的是协议层 `Options`（仍带智谱二元 thinking vocabulary），而正确的 effort 表（`auto/disabled/low/medium/high/max`）从未到达 UI。现在 effort 表比 provider options 更丰富时以它为准，GLM 的 `low/medium/high/max` 恢复可选；非智谱的 OpenAI 端点不受影响
- **启动即失败修复（`INVALID_MODEL_REASONING`）**：上一轮的 GLM 档位修复把输入栏专用的 `auto` 别名一并镜像进了协议层 `Options`，而 `provider.Validate` 明确拒绝 `auto` —— 结果是**官方 DeepSeek 各模型（含 beta 别名）在构建 provider 时直接报 `has invalid or repeated effort ID "auto"`，无法发消息**。现在镜像前剔除 `auto`（输入栏自行补 `auto` 项，UI 行为不变），并在 provider 默认值不属于新词表时清空以保留 vendor 默认行为；新增回归测试覆盖官方 DeepSeek 与 GLM 两条路径
- **子代理委派档位收进「+」菜单**：它是很少中途切换的 per-tab 设置，改为与「执行方式」「验收」并列的菜单区（轻量 / 均衡 / 激进）。1.38.3 merge 曾丢掉 `App.tsx` 的接线，导致该控件完全不渲染，本次一并恢复
- **快捷指令**：「+」菜单新增快捷指令区，列出用户自定义的文本片段，选中后插入到光标处（仍由用户按发送）；设置 → 通用 → 系统行为 → 快捷指令可增删改，两处读同一份配置（`desktop.quick_commands`），上限 50 条 / 标题 60 字 / 正文 8 KB

## 🔧 2026-09-10 追加（含在本地包，尚未发 Release）

- **项目分组恢复可用 + 可折叠**：追齐 1.38.3 时上游的**会话级分组**占用了同一渲染位置，fork 的**项目级分组**接线被顶掉——头部「新建分组」按钮消失、`lib/projectGroups.ts` 成了没人调用的孤儿代码。现已恢复：头部「新建分组」（建**空项目组**并把**当前项目加入**）+ 项目右键「移动到分组」+ 组标题**可点击折叠**（▾/▸，状态跨重启保持）+ 标题**字体与颜色与会话组一致**
- **压缩并行化**：分块压缩的片段此前是**串行**跑的（该能力只存在于 PR 分支，fork 主线一直没有）。现改为有界 worker pool，结果按索引写槽保证合并顺序（最旧在前）；首个失败会取消兄弟分片
- **远程窗口准入加固**：远程 turn 的准入现在**绑定到探测实际跑过的那条连接**。此前若在探测与发送之间发生重连，请求会被交给替换后的 Serve，而对方没有 revision 头可拒绝——可能**用陈旧的模型设置跑完一轮**
- **路径去重统一**：删掉 fork 自己叠的一层无条件大小写折叠（它会在**大小写敏感**的文件系统上把两个真实不同的目录误合并），去重统一交给上游的文件系统感知实现
- **上下文预算提示更可操作**：临近自动压缩时，除了「按波次规划」，现在明确提示**把关键决策与进展写进项目文档**——短笔记比重读 transcript 便宜得多
- **检查脚本加固**：`check-fork-integrity` 新增项目分组 **UI 接线**检查项（此前只覆盖 CSS 与存储层，而那正是被静默删掉的部分），共 **31/31**

## 升级提醒

- 覆盖安装即可；会话 / 记忆 / 配置目录与官方版完全兼容。
- **不要回退到 1.34 / 1.38.1 以下的旧版本**：会话事件日志会**就地升级到 schema 2**（`upgraded_from_schema: 1`），而旧构建只支持到 1，加载时会以 `uses schema 2; this build supports up to 1` 拒绝打开（**文件不会被改动**，是保护性设计，不是损坏）。需要降级前请先在 1.38.3 里导出会话内容。
- **已知问题（本机环境）**：`go test ./internal/boot/` 整包在本机挂起（环境探测单飞机制 `beginProbe`，每个测试单跑均通过）；与本次追齐无关——`internal/environment/` 在 merge 中零改动。
