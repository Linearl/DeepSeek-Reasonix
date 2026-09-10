# Reasonix Fork: main-v2-stable

> **这是什么**：**长期维护的 fork 分支**——在追齐上游的同时，保留我们确实需要、但上游不一定会合并的特性。
> **当前基线**：官方 `v1.38.3`（2026-09-09 追齐 1.38.2~1.38.3，283 个上游提交）。
> **上游差异台账（逐版本，权威）**：`release-notes/FORK-vs-upstream.md`
> **各版本 release notes**：`release-notes/FORK-vX.Y.Z.md`
> **完整性核对清单**：`scripts/check-fork-integrity.mjs`（每次合并上游后必跑）

## 与上游的关系（决策，2026-09-10 确定）

**长期并行，不废弃。** 三条原则：

1. **保持对齐** —— 持续追齐上游版本（1.31.3 → 1.38.3 已追齐 6 个版本）。每次 merge 逐文件论证、保留魔改、双向 tree 比对，禁止机械取一侧。
2. **吸收合理改进** —— 上游的重构或修复只要更优就采用，**哪怕它会替换掉我们自己的实现**。例：路径身份统一（#9511）是删掉 fork 自己叠的那一层、改用上游的文件系统感知实现；远程准入栅栏（#8749 系列）直接移植上游 7 个提交。
3. **向上游反馈** —— 对上上游也有价值的能力以**高质量 issue + PR** 回馈。上游吸收后就不必长期自己背：会话移交即为例（上游 #9692 覆盖了我们的 #8749，随即关闭该 PR）。

**为什么不能简单回归上游**：我们需要的部分特性上游不一定会合并（项目分组 #9222、颜色筛选 #9221 至今 open）。但「不合并」不等于「不协作」——继续提、继续对齐、继续吸收，是这个 fork 的长期姿态。

## 这个 fork 提供了什么

相对上游的差异规模：**314 个文件、+23320/-1083 行**（截至 v1.38.3）。

### 桌面 UI

| 能力 | 入口 / 说明 |
|---|---|
| **本地服务器 / 远程网关** | 设置 → 集成与连接 → 本地服务：开关远程网关、看监听地址、一键复制 gateway token；手机端（GrandCouncil）/ Tailscale 接入 |
| **已授权写目录面板** | 设置 → 权限：查看/增删会话级与用户全局写目录，可切换到窗口内任意会话管理 |
| **合并恢复副本** | 会话右键 → 合并恢复副本：把多份 `*-recovery-*` 择最全者转正、其余进 `.trash` |
| **项目分组**（fork 特有，#9222） | 项目树头部「新建分组」+ 项目右键「移动到分组」；**可折叠，状态跨重启保持** |
| **颜色筛选与排序**（#9221） | 项目树头部调色板按钮：按颜色筛选/排序，**支持多选**（上游只有设色数据层，无筛选 UI） |
| **搜索历史提问** | 长会话内搜索并跳转历史提问，滚到顶部加载更早历史 |
| **输入框草稿持久化** | 草稿 / 粘贴块 / 附件路径跨重启不丢（localStorage + `pagehide` 落盘，256KiB 上限） |
| **子代理委派档位** | 输入框「+」菜单 → 子代理委派（light / balanced / aggressive） |
| **Topicbar 更多菜单** | 懒加载溢出菜单：导出会话（markdown/json/pdf/image）、复制、变更坞、终端、会话摘要 |
| **计划任务 / 心跳** | 侧边栏「自动化」：provider / model 可配置 |
| **子代理进度 TPS** | 工具 / 子代理卡片实时显示 `~N tok/s` 流式吞吐 |
| **桌面日志轮转** | `logs/desktop/desktop.log` 4MB 轮转、25 份上限（GUI 无控制台时的诊断日志） |

### Agent / 上下文

| 能力 | 说明 |
|---|---|
| **只读轮次预算加倍** | `extend_research_budget` 工具：软预算触发收敛后可 10→20→40→80（每 turn ≤3 次，时间闸门同步加倍） |
| **每轮上下文预算行** | 每轮注入 `<context-budget>41k/128k (32%)</context-budget>`；临近阈值时提示把关键决策写进项目文档 |
| **路径作用域规则** | `.reasonix/rules/**/*.md` + `paths:` frontmatter，按工作区实际文件清单过滤后折进系统提示；项目规则遮蔽同名用户规则 |
| **乐观并发写入** | 写工具可选 `expected` 基线参数，内容不匹配即 stale-content 拒绝，替代整工作区串行锁 |
| **子代理委派档位** | `/subagent-policy` 以 transient block 注入，零重建、零持久化 |
| **高速模型执行模式** | 勾选「高速模型」后每轮注入 `<exec-speed-mode>high</exec-speed-mode>` |
| **分片压缩并行化** | 分块压缩片段走有界 worker pool（此前串行），超窗时半切递归 |
| **任务完成摘要** | 后台作业完成通知携带结果摘要（400 字符、CJK 安全、单行化） |
| **hook 作用域** | `HookConfig.AppliesTo`：`main` 跳过子代理、`subagent` 仅子代理（修「`match:*` hook 冻结子代理全部工具」缺陷） |

### 服务端 / 远程

| 能力 | 说明 |
|---|---|
| **serve pool + 单入口网关** | 按项目懒启动 serve，`/p/<project-id>/*` 反向代理，bearer token，空闲回收 |
| **独立 CLI 网关** | `serve-pool` 子命令：headless Linux / NAS（systemd）托管 |
| **多项目会话浏览** | `GET /projects` 只读浏览器（读同一份 desktop-projects.json） |
| **图片上传端点** | `POST /attachments`（≤64MiB base64）→ 返回 `.reasonix/attachments/...` ref |
| **会话所有权移交** | `/release-session`、`/takeover-session` + 心跳过期自动释放 + `.takeover-request` marker |
| **`heldBy` 会话占用暴露** | `/sessions` 返回带 `heldBy`；列表 8 并发 preview + 非阻塞标题 |

### 配置

| 能力 | 说明 |
|---|---|
| **推理档位协议扩展** | GLM low/medium/high/**max**、MiMo none/low/medium/high、Kimi K3 专属档位归一化 |
| **用户全局公共写目录** | `[sandbox] allow_global`：跨项目免审批写目录 |

### fork 独有包（上游无此目录）

`internal/rules`、`internal/safego`、`internal/jsonutil`、`internal/servepool`

## 数据兼容红线

- 不修改 `*.jsonl`、`*.events.jsonl`（WAL）、`*.meta`、`*.context.json` schema。
- 不修改 checkpoint v3 格式。
- 不修改 `config_version`。
- 不修改 CLI / ACP / 扩展协议 / Provider 请求序列化。
- 不改默认数据路径：`~/.reasonix` / `%APPDATA%\reasonix`。
- 不修改 `desktop/updater*.go`、`desktop/internal/update/`、`internal/repair/update.go`。

## Release 与安装

- fork 仓库自行出 release 包，走正常安装流程覆盖官方安装。
- **版本号必须与上游对齐**：fork 在两个上游版本之间修多少 bug 都不自增，上游发新版才跟进。
- 关闭自动更新：`[desktop] check_updates = false`。
- 不要删除/替换 `reasonix-update-helper.exe`。
- 回迁官方：优先官方安装器覆盖安装；也可重新开启 `check_updates` 等官方更高版本自动更新。

### Fork release 产物

- CLI：`bash scripts/fork-release.sh [version]` 本地构建多平台 CLI（unsigned）。
- Desktop：`.github/workflows/release-fork.yml` 手动触发，构建 unsigned 安装包/归档并发布到 fork 的 GitHub Release。
- 注意：fork release 不做 MINISIGN/SignPath/Apple 签名，不生成 `latest.json`，不镜像 R2；因此 fork 版请走手动安装，不要依赖自动更新。

## 验证状态（2026-09-10 更新）

- [x] `go build ./...` 主 module 与 `cd desktop && go build ./...` 均通过。
- [x] `scripts/fork-release.sh`（CLI release 构建脚本）。
- [x] `.github/workflows/release-fork.yml`（unsigned desktop release workflow，手动触发）。
- [x] 本地安装包：`scripts/build-local-installer.sh`（rsrc 图标注入 + wails build + NSIS）。
- [x] 测试：`internal/agent` / `internal/control` / `internal/recovery` / `evidence` / `tool/builtin` / `goaleval` / `boot` 全绿；desktop 定向（ServePool / Heartbeat / Settings / Remote / Admission）通过。
- [x] `tsc --noEmit` 0 错误；`node scripts/check-fork-integrity.mjs` **31/31**。
- [x] v1.38.3 已发布（tag `desktop-v1.38.3` + 9 平台产物）。

## 发布前检查清单（fork desktop）

1. **代码**：`git status` 干净 → `go build ./...` → `cd desktop && go build ./...` → `cd desktop/frontend && npx tsc --noEmit` → `node scripts/check-fork-integrity.mjs`（须全绿）
2. **文档**：`release-notes/FORK-vX.Y.Z.md` 含本版全部改动；`release-notes/FORK-vs-upstream.md` 台账同步；`desktop/wails.json` 的 `productVersion` 与 tag 版本一致
3. **本地包**（推荐先跑一遍）：`nohup bash scripts/build-local-installer.sh > /tmp/build.log 2>&1 & disown`（**加** `preserve_background_processes`；前台 115s 会被 SIGTERM，MSYS 无 `setsid`）→ 装后走关键路径
4. **推送**：`git push origin main-v2-stable`
5. **tag**：先 `gh release delete desktop-vX.Y.Z -R Linearl/DeepSeek-Reasonix`（**不带** `--cleanup-tag`）→ 删远端 tag → 再推 tag。顺序反了会把重推的同名 tag 一并删掉
6. **dispatch**：`gh workflow run release-fork.yml -R Linearl/DeepSeek-Reasonix -f tag=desktop-vX.Y.Z`
7. **验证**：`gh release view desktop-vX.Y.Z -R Linearl/DeepSeek-Reasonix`，确认 9 平台产物齐全

**两个必背的坑**：① `gh` 默认解析到 upstream（esengine）→ 必须显式 `-R Linearl/DeepSeek-Reasonix`，否则 404；② fine-grained PAT 会 403 → 用 `env -u GITHUB_TOKEN gh ...` 切 keyring OAuth token。
