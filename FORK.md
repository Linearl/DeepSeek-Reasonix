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

## 结构性纪律（2026-09-13，任务 38 沉淀）

追齐上游不只是"解 merge 冲突"，还有一层更隐蔽的成本：**我们自己制造的分叉**。任务 38 做 App.tsx 结构收敛时，反复撞到同一类问题，因此固化以下纪律。

### 1. 搬代码前，先查上游在目标位置有没有同名文件

```bash
git ls-tree -r --name-only upstream/main-v2 -- <目标路径>/ | grep -i <关键词>
```

**为什么**：任务 38 有**两批**（`sidebarIm`、`noticePreview`）都是"把代码从 App.tsx 搬到新文件"，而**上游早已在 `app-runtime/` / `app-shell/` 有做同一件事的文件**。结果是同一逻辑两套实现并存（`sidebarIm` 那次是 618 行两份），merge 时两份都要处理 —— **搬迁本为收敛，反而制造了新分叉**。

**正确顺序**：查上游 → 有则**先比对、把 fork 独有的部分并进去**；没有才新建文件。

### 2. 文本比对只作线索，不是判决

同名不等于重复。已见过的失效模式：

- **共享前缀**：`DesktopNavigationIntent` 文本上与上游一致，实际 fork 版**多三个 union 分支**（比对只看到第一支，因为那一支里恰好有分号）。
- **参数差异**：`sidebarImConnectionsFromBot` 只差一个 `nativeRuntime` 参数 —— 恰恰说明**上游才是新版**，fork 抄的是旧版。
- **行为不同**：`sameStringList` / `errorMessage` / `baseName` 两边都在，实现不同（上游更健壮），**盲目取上游会改变应用行为**。

**结论**：脚本给出候选，**逐个人读**后再决定是"删拷贝"、"取上游"还是"保留差异"。

### 3. 每次动 App.tsx 前后，跑这两个审计

| 脚本 | 查什么 | 基线 |
|---|---|---|
| `desktop/frontend/scripts/check-app-state-wiring.mjs` | App.tsx 里**被读却无人写 / 被写却无人读**的 state | 30 个 useState 全部正常 |
| `desktop/frontend/scripts/check-duplicate-definitions.mjs` | 同名导出定义在**多个文件** | 2 个（均为合理同名） |

两者都接了 `check:app-layers`（`build` 链里会跑），输出是**信号**不是门禁。**数字变大就说明又抄了一份或又漏接了一处**。

### 4. 结构收敛的真实目标

任务 38 的验收曾写作"`App.tsx` ≤200 行"，实测后修正为：**这靠分批搬状态达不到**（5380 行里 state 声明只占约 50 行，大头是 JSX 与 129 个 `useCallback`）。**≤200 行属于"重写成 `AppRuntime` 形态"，挂到 Electron 迁移（任务 61）一并达成**；本任务的实际价值是**持续消除双源**——它挖出的都是真缺陷（恒 0 的 epoch、两个 flag 驱动同一 footer、重复的上游探测）。

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

### 特性 × 设置入口 × 日志覆盖（2026-09-14 盘点）

设置的 22 个 tab 定义在 `desktop/frontend/src/components/SettingsNavigation.tsx` 的
`SETTINGS_NAV_TABS`。盘点结论：**28 项特性里只有 2 项有专属设置页、4 项有设置项，其余 22 项没有任何
UI 入口** —— 所以对大部分 fork 特性，**日志是唯一的可观测手段**，这也是本轮加强日志的原因。

| 特性 | 设置入口 | 日志覆盖（2026-09-14 后） |
|---|---|---|
| 本地服务器 / 远程网关 | ✅ 设置 → 连接 → **本地服务**（fork-only tab） | ✅ gateway access log + `feature=servepool` 生命周期 |
| serve pool + 单入口网关 | ✅ 同上 | ✅ spawn / 空闲回收 / degraded 退避 |
| 独立 CLI 网关（`serve-pool` 子命令） | ❌ CLI | ⚠️ 同上（无 GUI） |
| 已授权写目录面板 | ✅ 设置 → 权限 → 本会话已授权写目录 | ⚠️ 待补（`allow_global` 命中） |
| 用户全局公共写目录（`allow_global`） | ⚠️ 设置 → sandbox | ⚠️ 待补 |
| 会话所有权移交 / `heldBy` | ❌ serve 命令 + HTTP | ✅ `session_ownership.go` 原有 10 处 + `/sessions` 每响应一条汇总（`feature=session-ownership`） |
| 多项目会话浏览（`GET /projects`） | ❌ HTTP | ❌ |
| 图片上传端点（`POST /attachments`） | ❌ HTTP | ❌ |
| 合并恢复副本 | ❌ 会话右键菜单 | ✅ 已有 |
| 项目分组（#9222） | ❌ 项目树头部 | ✅ 创建 / 移动 / 折叠展开（`feature=project-groups`；折叠状态跨重启持久化，故必须留痕） |
| 颜色筛选与排序（#9221） | ❌ 项目树头部 | ✅ 筛选应用/清空 + 结果可见数（`feature=project-colour-filter`） |
| 搜索历史提问 | ❌ 长会话内 | ❌ |
| 输入框草稿持久化 | ❌ 自动 | ✅ 三处静默失败：读不出 / 写失败（降级内存）/ 超 256KiB 未持久化（`feature=draft`） |
| Topicbar 更多菜单 | ❌ | ❌ |
| 子代理委派档位 | ⚠️ 设置 → 子代理 + 输入框「+」 | ❌ |
| 子代理进度 TPS | ❌ | ❌ |
| 计划任务 / 心跳 | ❌ 侧边栏「自动化」 | ⚠️ 部分 |
| 桌面日志轮转 | ❌ | ✅ 本机制（4MB × 25） |
| 只读轮次预算加倍 | ❌ 工具 | ✅ 每次扩展记 rounds/remaining（`feature=research-budget`） |
| 每轮上下文预算行 | ❌ | ✅ Debug 级，含 advisory 标志（`feature=context-budget`） |
| 路径作用域规则 | ❌ | ❌ |
| 乐观并发写入（`expected`） | ❌ 工具参数 | ⚠️ 部分 |
| 高速模型执行模式 | ⚠️ 设置 → `settings.highSpeedModel` | ❌ |
| 分片压缩并行化 | ❌ | ✅ 每次运行的形状：chunks / concurrency / minCalls / budget（`feature=compaction-parallel`） |
| 任务完成摘要 | ❌ | ❌ |
| hook 作用域（`AppliesTo`） | ⚠️ 设置 → hooks | ⚠️ 部分 |
| 推理档位协议扩展 | ⚠️ 设置 → 模型 | ⚠️ 部分 |
| 历史分页 / 上翻加载 | ❌ | ✅ 计时落盘（`458cf74b6`）+ 三种失败原因与快切命中（`feature=history-paging`，`3accbc984`） |

**日志约定（新增，2026-09-14）**

1. **级别**：`REASONIX_DESKTOP_LOG=info|debug|trace`（默认 `info`）。设在环境变量而非设置项，因为要排查的失败往往发生在设置 UI 可用之前，且复现时用户本来就在改环境。
2. **前缀**：fork 特性相关的日志带 `"feature", "<name>"` 字段（`servepool` / `history-paging` / `sandbox-allow-global` …），便于 grep，也便于与上游行为区分。
3. **计时**：跨进程/跨 IPC 的阶段用 `desktop/tab_timing.go` 的 `ReportTabSwitchTiming` 落盘（前端 → 后端单向、fire-and-forget、低于阈值丢弃）。**加计时通道优先于读代码猜**——2026-09-14 切 tab 慢的定位就是靠它一次定死的。

## 数据兼容红线

- 不修改 `*.jsonl`、`*.events.jsonl`（WAL）、`*.meta`、`*.context.json` schema。
- 不修改 checkpoint v3 格式。
- 不修改 `config_version`。
- 不修改 CLI / ACP / 扩展协议 / Provider 请求序列化。
- 不改默认数据路径：`~/.reasonix` / `%APPDATA%\reasonix`。
- 不修改 `desktop/updater*.go`、`desktop/internal/update/`、`internal/repair/update.go`。

## 新特性开发规范（2026-09-16，依据上游 discussion #10269 教训）

上游 Electron 迁移（#9988）后的负反馈链（#10222 升级断裂、#10106 黑窗、#10171「先把基础做好」）说明：**架构野心若大于执行质量与用户退路，会直接反噬稳定用户**。fork 开发新特性时遵守：

1. **破坏性变更必须留退路**
   - 动存储布局 / 安装布局 / 会话格式前先问：旧版能否回退？回不来则**双写 + 验证再切**，禁止一刀切。
   - fork 无自动更新：文档写清手动安装/回退路径，不让用户自己撞墙。
2. **新能力默认实验开关**
   - 凡改变默认行为 → `experimental_*` 开关，**默认关**；关闭时零回归、可 A/B 对比。
   - 开关全链路：config → setter → **渲染表** → UI（任务 81/123 踩过漏渲染表的坑）。
   - 开关若**只在启动时读取**（如会话存储 v4 在 boot 建 bridge、boot 期注册的工具），保存后必须**主动提示 + 提供一键重启**，不能只写一行「需重启生效」让用户自己关掉再打开（2026-09-16 落地：设置页 banner + `App.RestartDesktop`，且**不**依赖 `experimental_restart_update` 门控）。
3. **先收敛再扩功能**
   - 一个 worktree 一条线；合并前跑完整验证（build + tsc + fork-integrity + 关键路径手测）。
   - 连续大改后留「稳住」窗口，不立刻叠下一批（对齐 #10171 社区诉求）。
4. **启动/打包链路先搜现成防护**
   - 黑窗根因是 launcher 未用仓库里已有的 `proc.Command()`。动启动链 / staging / 重启（任务 81/129）时禁止裸 `exec.Command` / 裸路径拼接，先 `git grep` 现成工具。
   - 出包验证覆盖：安装、升级、回滚、首启、无黑窗。
5. **权限与并发：默认可放开，不叠更严的锁**
   - 上游工作区整锁是效率痛点；fork 已有乐观并发写（#9213）与 `allow_global`。新特性涉及写权限时优先并行/目录级授权。
6. **保住 fork 独有「好用的旧东西」**
   - 经典布局、项目分组、颜色筛选等不轻易退役；合并上游必跑 `check-fork-integrity.mjs`，防静默丢样式/能力。
7. **双通道意识**
   - 版本号对齐上游但**不自动追最新**；上游不稳时停在稳定基线（1.38.3 策略）。
   - 架构级实验（Electron 线、session-v4 等）先本地包验证，不进默认 fork release。

**参考**：discussion #10269 主贴（xiaokay2099）+ 本 fork 作者评论（Linearl：双通道 / 实验开关 / 双写 / 并行写与路径审批痛点）。

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
3. **本地包**（推荐先跑一遍）：`nohup bash scripts/build-local-installer.sh > /tmp/build.log 2>&1 & disown`（**加** `preserve_background_processes`；前台 115s 会被 SIGTERM，MSYS 无 `setsid`）→ 装后**逐项验证**（对应规则 4 的出包验证要求）：
   - **安装**：覆盖安装成功，快捷方式/图标正常
   - **升级**：从上一包升级后数据完好（会话、配置、项目）
   - **回滚**：能退回上一版本继续用（对应规则 1「留退路」）
   - **首次启动**：冷启动直达主界面，不卡启动页
   - **无黑窗**：Windows 启动瞬间不闪 console 窗口（#10106 同类问题的验收项）
4. **推送**：`git push origin main-v2-stable`
5. **tag**：先 `gh release delete desktop-vX.Y.Z -R Linearl/DeepSeek-Reasonix`（**不带** `--cleanup-tag`）→ 删远端 tag → 再推 tag。顺序反了会把重推的同名 tag 一并删掉
6. **dispatch**：`gh workflow run release-fork.yml -R Linearl/DeepSeek-Reasonix -f tag=desktop-vX.Y.Z`
7. **验证**：`gh release view desktop-vX.Y.Z -R Linearl/DeepSeek-Reasonix`，确认 9 平台产物齐全

**两个必背的坑**：① `gh` 默认解析到 upstream（esengine）→ 必须显式 `-R Linearl/DeepSeek-Reasonix`，否则 404；② fine-grained PAT 会 403 → 用 `env -u GITHUB_TOKEN gh ...` 切 keyring OAuth token。
