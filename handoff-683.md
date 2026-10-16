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

（施工中回填）
