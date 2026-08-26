# Reasonix Fork 桌面版 v1.31.4 — Release Notes

> 基于官方 `v1.31.4`，**新增 4 项 fork 独有功能 + 12 项修复/改进**，并同步吸纳官方 8 项稳定性改进。
> 数据/会话/记忆目录与官方版完全兼容，**覆盖安装即可，无需迁移**。
> 前一版本 v1.31.3 的并行写入、子代理三档等增强已包含在内（详见 [v1.31.3 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.3)）。

---

## ✨ 新功能（fork 独有）

### 1. 实时引导 — 耗时工具执行期间可继续对话

bash 执行期间发送新消息，**bash 不会被杀死**，继续在后台运行，新消息立即开始下一轮。灵感来自 Claude Code 的 `interruptBehavior` 机制。**无需配置**，自动生效。

### 2. MiMo 推理档位自动检测

MiMo 模型（`*.xiaomimimo.com` 全域）无需显式设置 `reasoning_protocol`，推理档位（none/low/medium/high）自动出现。修复了 EffortCapabilityForEntry + NormalizeEffort 双路径的遗漏。（#9435）

### 3. serve 多项目会话浏览（GET /projects）

新增 `GET /projects` 端点，读取 `desktop-projects.json` 列出全部项目的会话（复用 `config.ProjectSessionDir` + `agent.SessionPreview`）。移动端/网页端单个 serve 即可浏览全部项目。（#8789）

### 4. serve 图片上传（POST /attachments）

新增 `POST /attachments` 端点，接受 JSON body（base64 编码图片），落盘到 `.reasonix/attachments/`，返回 `@ref` 路径。客户端通过 `POST /submit {"input": "@ref ..."}` 发送图片消息，复用 agent 全套多模态管线。（#8855）

---

## 🚀 改进

- **会话切换无感秒开**：已缓存会话立即显示，无遮罩无转圈；缓存数提升到 8 个。（#9385）
- **右侧提问定位条去密集化**：采样上限 120→60、密度阈值下调。（#9218）
- **压缩比例保留原文联动**：`compact_ratio` 阈值降低时，保留原文量自动按比例缩减。（#9406）
- **压缩提示文案与界面一致**：文案从"圆环显示 prompt ÷ 窗口"改为数字卡片描述。（#9157）
- **分组标题字号略大于普通会话**。（#9032）

---

## 🔧 修复

- **shell contract 误拦纯验证命令**：`go test ./...`、`go vet` 等在会话有状态变更后仍被拦截。根因是 `bashMayMutate` 在静态参数解析失败时跳过验证检测。新增 `bashBaseLooksLikeVerification` 宽松兜底。（#9405）
- **恢复失败会话无法归档的死循环**：超大/异常会话恢复失败后，lease 未释放导致归档被拒，重启自动恢复再次失败。`acquireTopicArchiveOwnership` 现在 fallback 到非运行时 guard，打破死循环。（#9393）
- **GFM 表格流式渲染延迟**：流式输出中表格已完成后行不显示，需等空行或流结束。`streamingCommitTarget` 新增 `|` 检测，每行即时提交。（#8972）
- **会话改名/自动起标题后历史面板立即刷新**。（#8597）
- **归档修复套件**：残留后台任务不再卡归档（#9374）、分组内单会话可单独归档、归档失败不再误移出分组。（#9374）
- **滚动错排修复**：向上滑动后行测量冻结导致错排，仅对未就绪行应用冻结高度 shim。（#9366）
- **config 持久化修复**：`optimistic_write` 配置写入 `config.toml` 时被漏掉，导致重启后设置丢失。
- **provider deletion + OpenCode group 原子删除**修复。

---

## 📦 上游 v1.31.4 同步（8 项）

| # | 改进 | 说明 |
|---|---|---|
| #9155 | 元数据损坏自动重建 | 全零/空白 meta 不再卡死保存/关闭 |
| #9115 | 后台任务通知优化 | 安静长任务不再误报"卡住" |
| #9379 | 降低 DB 读取成本 | 退役 30 天去重安装数全表扫描，回收 ~5.6GB |
| #9248/#9341 | 稳定的会话滚动 | 流式输出期间滚动锚点更稳 |
| #9141 | 数学渲染改进 | `\boxed` 跨浏览器修复 + CLI LaTeX 增强 |
| #9314 | 侧栏活动顺序 | 切换会话不再错排 |
| #9312 | 停止反复重索引 | 空闲会话不再循环进入索引/修复 |
| #9341 | 显式收尾恢复 | 标准回合不再启动隐藏后续轮询 |

---

## 升级提醒

- **覆盖安装即可**，无需卸载、无需迁移数据；已关闭自动更新。
- `compact_ratio` 允许 30%~85%（默认 80%）；`recentTailBudget` 随 `compact_ratio` 联动。
- v1.31.3 的并行写入、子代理三档等增强已自动包含，无需额外配置。
- 如需回到官方版：未来官方发新版本后，点击更新或重启自动更新即可，**数据无损**。
