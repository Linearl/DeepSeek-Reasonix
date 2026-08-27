# Reasonix Fork 桌面版 v1.31.4 — Release Notes

> 本版本基于官方 `v1.31.4`，面向**实时交互 + 跨模型兼容 + 首次安装体验**做了一系列 fork 增强。数据/会话/记忆目录与官方版完全兼容，**覆盖安装即可，无需迁移**。
> v1.31.3 的并行写入、子代理三档、长会话压缩阈值、会话秒切等增强已包含在内（详见 [v1.31.3 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.31.3)）。

## 使用攻略

- 所有 fork 功能均可在 **设置 → 权限 / 通用** 或**会话输入框右上角**找到入口；CLI/TUI 用斜杠命令。
- 安装**覆盖安装即可**，无需卸载、无需迁移数据；已关闭自动更新，未来可随时切回官方版。

## 概览

**Reasonix Fork v1.31.4 — 实时引导 + MiMo 兼容 + serve 端点扩展 + 压缩刷新记忆 + 写目录管理**

本版本聚焦五件事：**让 bash 等耗时工具执行期间用户可继续对话**（实时引导）、**让 MiMo 模型推理档位自动可用**、**扩展 serve 模式能力**（多项目浏览 + 图片上传）、**压缩后自动刷新持久记忆**、以及**全新的已授权写目录管理 + 全局公共目录**（让 DeepSeek 乱写随机子目录不再反复弹审批，详见下文 #9167）。同时修复了 16 项体验问题（标准模式混合命令误拦、前端 bundle 预算、`+fork` tag 的 Windows 构建、shell contract 误拦、归档死循环、GFM 表格流式渲染、压缩面板不刷新等）。

发布日期：2026-08-27（fork 补充构建，含 #9167 写目录管理）

---

## ✨ 重点内容（新增）

### 实时引导 — 耗时工具执行期间可继续对话

bash 执行期间发送新消息，**bash 不会被杀死**，继续在后台运行，新消息立即开始下一轮。灵感来自 Claude Code 的 `interruptBehavior` 机制。**无需配置**，自动生效。

**用法（桌面版/CLI/TUI）**：当 bash 正在执行（如 `go build ./...`、`npm install`）时，直接在输入框输入新消息发送即可。bash 在后台继续运行，agent 立即响应新需求；bash 完成后其输出仍会被记录到会话中。

**原理**：每个工具声明自己的中断行为——`bash` 声明为 `continue`（中断后继续后台运行），其他工具声明为 `cancel`（中断后停止）。用户输入新消息时，agent 向 bash 发送 SIGINT 但不 kill 进程，新消息立即进入下一轮对话。

### MiMo 推理档位自动检测

MiMo 模型（`*.xiaomimimo.com` 全域，包括 `api.xiaomimimo.com` 和 `token-plan-cn.xiaomimimo.com`）无需显式设置 `reasoning_protocol`，推理档位（none/low/medium/high）自动出现。（#9435）

**用法（桌面版）**：连接 MiMo 模型后，输入框右上角推理档位下拉自动显示可用选项。

**用法（CLI/TUI）**：`/effort low|medium|high` 或 `/effort none` 关闭推理。

**注意**：MiMo 的 low/medium/high 三档在服务器端行为相同（fine-grained 推理尚未支持），但接口已预留，未来小米开放差异化时自动生效。

### serve 多项目会话浏览（GET /projects）

新增 `GET /projects` 端点，读取 `desktop-projects.json` 列出全部项目的会话（复用 `config.ProjectSessionDir` + `agent.SessionPreview`）。移动端/网页端单个 serve 即可浏览全部项目。（#8789）

**用法**：
```bash
# 启动 serve
reasonix serve --port 8788

# 列出所有项目
curl http://localhost:8788/projects

# 列出某项目的会话（复用 /sessions 端点）
curl http://localhost:8788/sessions?project=<project-slug>
```

### serve 图片上传（POST /attachments）

新增 `POST /attachments` 端点，接受 JSON body（base64 编码图片），落盘到 `.reasonix/attachments/`，返回 `@ref` 路径。客户端通过 `POST /submit {"input": "@ref ..."}` 发送图片消息，复用 agent 全套多模态管线。（#8855）

**用法**：
```bash
# 上传图片
curl -X POST http://localhost:8788/attachments \
  -H "Content-Type: application/json" \
  -d '{"filename":"photo.jpg","contentType":"image/jpeg","data":"<base64>"}'

# 返回 {"ref":"@ref/attachments/photo-abc123.jpg"}

# 发送图片消息
curl -X POST http://localhost:8788/submit \
  -H "Content-Type: application/json" \
  -d '{"input":"描述这张图片 @ref/attachments/photo-abc123.jpg"}'
```

### 压缩后自动刷新持久记忆

会话压缩后（自动或手动 `/compact`），系统提示中的 **`# Memory` 持久记忆段** 会用**磁盘上最新记忆**重新组装——包括**其他会话（tab）在本会话期间新写入的记忆**。此前持久记忆只在会话启动时折叠进系统前缀，长时间会话即使压缩也无法感知新记忆，导致跨会话记忆脱节。

**原理**：压缩是低频的缓存重置点，压缩本身已会重算历史段，因此在此顺便刷新记忆段不会额外付出前缀缓存失效代价。系统提示其余部分（基础 prompt、核心策略、Skills 索引、persona）保持不变，会话历史连续不中断。

**用法**：无需配置，自动生效。多开 tab 时，在 tab A 写入记忆、切到已开着的 tab B 触发一次**压缩**（或等到 auto-compact），B 即可加载到该新记忆。

### 已授权写目录管理 + 全局公共目录（#9167）

**「已授权写目录」透明可控**。此前工作区能写哪些目录既看不见也管不了，深层级写入还反复弹审批。现在**设置 → 权限 → 沙箱与工作区**新增两块：

- **本会话已授权写目录**（会话级，进程内内存）：列出本会话已批准的写目录，可查看、添加、删除。
- **全局公共写目录**（用户级，跨项目生效）：所有项目/会话写入这些目录**及其任意子目录**均免审批。

**解决痛点**：DeepSeek 爱写 `C:\tmp\xxx` 这类随机子目录——即使父目录批过，下次建新子目录又弹审批、YOLO 也卡住。把父目录（如 `C:\tmp`）加到**全局公共写目录**后，其下任意新子目录都不再反复申请权限。（#9174 后端 + #9175 前端）

**用法**：
- 设置 → **权限** → **沙箱与工作区**，在「有效写根 / allow_write」下方：
  - **本会话已授权写目录**：输入目录 →「添加」，可对当前会话临时放行（会话结束即清空）。
  - **全局公共写目录**：输入目录 →「添加」，跨所有项目永久生效（写入用户 config `[sandbox] allow_global`），子目录自动覆盖。

---

## 📦 上游 v1.31.4 新功能与改进

本版本同步吸纳官方 v1.31.4 的 8 项改进：

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

## 🔧 Fork 修复（16 项）

- **前端 bundle 棘轮预算在 v1.31.4 CI 反复卡发布**：`check-bundle-budget.mjs` 的初始 JS gzip 预算被实测的 433.2 KiB 顶破（433.0），简体中文语言包也被 57.1 KiB 顶破 57.0。同一前端隔天即成功，说明是工具链/哈希漂移在阈值附近的抖动，叠加 v1.31.4 新增文案（如写目录面板）。将初载 JS 棘轮预算放宽到 435.0 KiB、简体中文语言包放宽到 57.7 KiB（与繁体一致），避免 ~0.2–0.7 KiB 漂移中止 release 构建。
- **Windows 安装包在 `desktop-*+fork` tag 下构建失败**：`windows-resource` stamper 要求纯数字版本号，而 `desktop-v1.31.4+fork` 会把手写 `+fork` 泄漏进版本号（`strconv.ParseUint: "4+fork"`）。`desktop-build.sh` 现在同时剥离 `v` 前缀、`-rc` 预发布后缀与 `+fork` 构建元数据后缀，`+fork` tag 即可正常出 Windows 安装包。
- **标准模式不再误拦「变更+验证」混合命令**：标准（非交付）回合下，`go build ./... ; go test ./...` 这类「一个命令里先变更再验证」的复合指令此前会被 shell contract 拦截（`blocked: mixed mutation and verification command`）。现改为标准模式放行（参考 codex：bash 本身的 `&&`/`;` 语义对模型透明，拆开会徒增往返、无实质安全收益）；仅交付（closed-loop）回合保留严格拦截，因为那里变更会污染验证 receipt。（参考报告 `issues/reports/标准模式混合指令拦截根因分析-codex对比-20260827.md`）
- **shell contract 误拦纯验证命令**：`go test ./...`、`go vet` 等在会话有状态变更后仍被拦截。根因是 `bashMayMutate` 在静态参数解析失败时跳过验证检测。新增 `bashBaseLooksLikeVerification` 宽松兜底。（#9405）
- **恢复失败会话无法归档的死循环**：超大/异常会话恢复失败后，lease 未释放导致归档被拒，重启自动恢复再次失败。`acquireTopicArchiveOwnership` 现在 fallback 到非运行时 guard，打破死循环。（#9393）
- **GFM 表格流式渲染延迟**：流式输出中表格已完成后行不显示，需等空行或流结束。`streamingCommitTarget` 新增 `|` 检测，每行即时提交。（#8972）
- **压缩后上下文面板不刷新**：手动压缩成功后右侧面板的上下文占用不变，导致用户误以为压缩未生效并重复触发。`compaction_done` 事件现在会触发面板刷新。
- **会话改名/自动起标题后历史面板立即刷新**。（#8597）
- **归档修复套件**：残留后台任务不再卡归档（#9374）、分组内单会话可单独归档、归档失败不再误移出分组。（#9374）
- **滚动错排修复**：向上滑动后行测量冻结导致错排，仅对未就绪行应用冻结高度 shim。（#9366）
- **config 持久化修复**：`optimistic_write` 配置写入 `config.toml` 时被漏掉，导致重启后设置丢失。
- **provider deletion + OpenCode group 原子删除**修复。

---

## 升级提醒

- **覆盖安装即可**，无需卸载、无需迁移数据；已关闭自动更新。
- `compact_ratio` 允许 30%~85%（默认 80%）；`recentTailBudget` 随 `compact_ratio` 联动。
- v1.31.3 的并行写入、子代理三档、长会话压缩阈值等增强已自动包含，无需额外配置。
- 如需回到官方版：未来官方发新版本后，点击更新或重启自动更新即可，**数据无损**。
