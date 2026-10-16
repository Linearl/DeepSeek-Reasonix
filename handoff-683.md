# handoff-683 —— Wails/WebView2 桌面自动化技能（playwright 驱动 CDP）wt-683

- 日期：2026-10-15
- worktree：`github-repo/worktrees/wt-683`（分支 `wt-683`，基 `main-v2-stable` @ `f8a09feec`，status 净）
- 任务：P2 批 34 波 2 任务 683——参考 CDP 自动化思路，为 fork 桌面版（Wails+WebView2）建立 UI 自动化自验技能
- 纪律：切片 commit、显式路径 add、禁 add -A/commit -a、禁 stash/merge/rebase、status 净；中文 message「683 」开头；文件写入走 Write/Edit

## 用户拍板红线（2026-10-15）

**网页/浏览器任务走 playwright 路径**——技能正文教 playwright 驱动 WebView2 CDP（`--remote-debugging-port` 起 WebView2 → `chromium.connectOverCDP`），**不移植 node_repl+browser-client.mjs 体系**；技能正文 zcode 死引用 grep 清零（验收硬门）。

## fork 现状（勘察结论，写技能的事实源）

1. **CDP 端点已有产品化开关（task 342）**：`[desktop] experimental_cdp_debug_port = true`（用户全局 config，`internal/config/desktop_preferences.go:433`）→ 启动时 `desktop/cdp_debug_port.go` 注入 `--remote-debugging-port=0`（loopback+随机端口），解析出的端点写 `<REASONIX_HOME>/logs/desktop/cdp-endpoint.txt`（`127.0.0.1:port` 或 `pending`），关停即删。改开关需重启生效。
2. **手动通道始终可用**：`REASONIX_WEBVIEW2_EXTRA_ARGS=--remote-debugging-port=9222` 由 vendored loader 追加（`desktop/third_party/go-webview2/pkg/edge/chromium.go:121-128`）——隔离测试实例的标准起法（`internal/skill/builtincontent/ll-iteration-dev/references/gui-self-verify.md` §2.3 已有 recipe：REASONIX_HOME/APPDATA 指 tmp + config/.env 副本）。
3. **playwright 已是前端 devDependency**（`desktop/frontend/package.json` playwright ^1.63.0）；CLI 无 CDP 连接子命令，连接走 playwright 库 `chromium.connectOverCDP(...)`（connectOverCDP 不需要下载 playwright 自带浏览器）。
4. **既有自验工具链衔接点**：`desktop/frontend/bench/`（app-browser.mjs 等 headless vite preview 自验）、gui-self-verify.md 的窗口最大化铁律/en-US locale 钉扎/清理五件、workspace `tasks/界面自验操作卡.md`。
5. **builtin 工具名**（技能正文引用必须匹配）：`bash`、`read_file`、`write_file`、`edit_file`、`glob`、`grep`（`internal/tool/builtin/*.go` Name() 实测）。

## 切片计划

1. **切片 1**：新技能 `internal/skill/builtincontent/desktop-automation/SKILL.md`——启动（双通道：产品开关+端点文件 / EXTRA_ARGS 固定端口）→ playwright connectOverCDP → 快照/交互/截图/断言 → 清理；含窗口最大化铁律、隔离实例 recipe、排障表；正文零 zcode 死引用。
2. **切片 2**：`embed.go` 注册（`//go:embed desktop-automation`）+ `shipped_playbooks_test.go` 名单补 `desktop-automation`；`go build ./...` + `go test ./internal/skill/...` 过。
3. **切片 3**：本机连通性干跑实录（headless Edge/Chrome 起 CDP 端口 → playwright connectOverCDP → 截图/取 title 断言）；GUI 全链验证步骤清单（标注环境前提）写入本文件；最终 commit。

## 验收

① 新技能落 builtincontent + embed.go 注册，`go build` 过
② 驱动 fork 桌面版基本 UI 自动化的验证：本机可实测部分实录进本文件；GUI 环境做不了的部分给步骤清单并标注
③ 技能正文零 zcode 死引用（grep：zcode / node_repl / browser-client / agent-browser）+ 正文工具名匹配 internal/tool/builtin

## 实录

### 切片 1+2：技能落地与注册（commit 2 个）

- 新技能 `internal/skill/builtincontent/desktop-automation/SKILL.md`（frontmatter name/description/runas: inline）：双通道起 CDP 端点（产品开关 task-342 + 端点文件 / `REASONIX_WEBVIEW2_EXTRA_ARGS` 隔离实例）→ playwright `connectOverCDP`（CLI 无 CDP 连接子命令，正文如实写明走库 API 一次性脚本，经 bash 工具执行）→ 快照-交互-断言 → 窗口铁律 → 清理五件 → 排障表 → 与 bench/gui-self-verify/操作卡三处衔接。
- `embed.go` 加 `//go:embed desktop-automation`；`shipped_playbooks_test.go` 名单补 `desktop-automation`。
- `go build ./...` 过；`go test ./internal/skill/builtincontent/` **ok**（含新增名单断言）。
- `go test ./internal/skill/` 首轮 `TestRunSkillSubagentCancellationReachesRunner` FAIL——隔离重跑 `-count=2` **ok**，为既有计时 flaky（与本任务零文件交集，未处理）。

### 切片 3：本机连通性干跑（2026-10-15 实测）

**做了什么**：无 GUI 全链（起真实 reasonix-desktop.exe + CDP）涉及弹窗干扰用户实例，改走**同链路干跑**——headless Edge（与 WebView2 同 Chromium 内核家族）带 `--remote-debugging-port=9222` 起，逐步复现技能 §2 自检三步：

| 步骤 | 结果 |
|---|---|
| 1. `curl http://127.0.0.1:9222/json/version` | ✅ 返回 `Browser: Edg/155.0.4283.60` + `webSocketDebuggerUrl` |
| 2. playwright `connectOverCDP`（main 仓 frontend cwd，playwright 1.63.0） | ✅ 不抛错；`pages()` 枚举出 2 页（edge://sync + about:blank）——**页面发现即证 CDP 通道活** |
| 3. 导航+中文选择器断言+截图 | ✅ `setContent` 测页 → `locator("text=新建会话").waitFor()` 命中 → `screenshot()` 落 8166B PNG |

**干跑中踩到并已回写技能正文的坑**：
1. heredoc 内联中文经 Git Bash（GBK 默认码页）绞成乱码 → 中文选择器静默失配。修法=脚本 `write_file` 落盘 UTF-8 再 `node` 执行（已写进 §3 脚本落盘纪律+排障表）。
2. `data:` URL 内嵌中文同样绞乱（浏览器按 URL 编码解）。修法=`page.setContent(html)`。

**清理**：干跑 Edge 按命令行含 `rx-cdp-edge-profile` 甄别 PID 全杀、tmp profile/脚本/截图已删；用户实例（reasonix-desktop PID 30120 等）未触碰。

### GUI 全链验证步骤清单（本环境弹窗干扰用户实例，未做；留给桌面验证会话）

前提：装好包或 `wails build` 出 exe；用户实例正常运行不受影响（隔离 APPDATA）。

1. `REASONIX_HOME=/tmp/rx-cdp/home APPDATA=/tmp/rx-cdp/appdata REASONIX_WEBVIEW2_EXTRA_ARGS=--remote-debugging-port=9222 <exe>`（副本 recipe 见技能 §1 通道 B）
2. `curl http://127.0.0.1:9222/json/version` 确认端点；确认 `pages()` 里出现 `http://wails.localhost/...` 页面
3. `page.evaluate(() => window.runtime.WindowMaximise())` → 核 innerWidth≥1280（窗口铁律）
4. 基本 UI 断言：composer textarea 可见 → 点设置按钮 → `.settings-screen` 出现 → 返回
5. `page.screenshot()` 留证 + 中文界面双语选择器验证
6. 清理五件（技能 §5）

**预期风险**（干跑未覆盖，GUI 会话留意）：wails 页面 url 形态（`wails.localhost` 假设待实测校正）、`window.runtime` 可用时机、单实例互斥（隔离 APPDATA 应规避）。

### 验收对照

| 验收项 | 状态 |
|---|---|
| ① 技能落 builtincontent + embed.go 注册，go build 过 | ✅ |
| ② 驱动桌面版 UI 自验：可实测部分实录；做不了部分给清单并标注 | ✅（干跑三步实录 + GUI 清单带预期风险标注） |
| ③ 正文零 zcode 死引用（grep：zcode/node_repl/browser-client/agent-browser 命中 0，exit=1）+ 工具名匹配 builtin（bash×2/read_file/write_file/glob/grep 均为 internal/tool/builtin 实名） | ✅ |
