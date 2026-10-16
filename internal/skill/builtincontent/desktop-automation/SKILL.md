---
name: desktop-automation
description: "Wails+WebView2 桌面版 UI 自动化自验——给运行中的桌面实例开 CDP 端口，用 playwright 连接后做导航/点击/截图/断言。当用户要求「桌面版 UI 自动化」「驱动 reasonix-desktop 验证界面」「WebView2 连 CDP」「桌面版界面回归自验」「给桌面版截图断言」时使用。触发词：桌面自动化、桌面自验、WebView2 CDP、desktop UI verify。"
runas: inline
---

# desktop-automation：Wails/WebView2 桌面 UI 自动化（playwright + CDP）

原理：fork 桌面版 = Wails + WebView2，WebView2 是 Chromium 内核，启动时带 `--remote-debugging-port` 即暴露 Chrome DevTools Protocol（CDP）端点（loopback only）。playwright 用 `chromium.connectOverCDP` 接管这个端点后，**与网页自动化同一套快照-交互-断言工作流**，无需任何桌面级驱动库。

分工边界：组件级/纯前端问题走 vite preview 自验（`desktop/frontend/bench/*.mjs`，mock 数据 headless）；**真机运行时行为**（bindings、后端状态、真实布局）才走本技能。

## 1. 起 CDP 端点（双通道，选一）

### 通道 A：产品开关（task 342，适合长期开着自验的开发机）

用户全局 config 加 `[desktop] experimental_cdp_debug_port = true` → **重启桌面版**（端点在进程创建时固定，不能热开）→ 端点写入 `%APPDATA%\reasonix\logs\desktop\cdp-endpoint.txt`（Windows；内容 `127.0.0.1:<port>`，`pending` 表示浏览器子进程还没回报端口）。随机 loopback 端口，关停自动删文件。

### 通道 B：隔离测试实例（推荐——不碰用户实例，固定端口好脚本化）

```bash
# exe 位于仓库 desktop/build/bin/reasonix-desktop.exe（没有则先 wails build）
mkdir -p /tmp/rx-cdp/home /tmp/rx-cdp/appdata
cp "$APPDATA/reasonix/config.toml" /tmp/rx-cdp/appdata/ 2>/dev/null || true
cp "$APPDATA/reasonix/.env" /tmp/rx-cdp/home/ 2>/dev/null || true   # 缺 .env 则 Models() 为空，仅影响模型列表页
REASONIX_HOME=/tmp/rx-cdp/home APPDATA=/tmp/rx-cdp/appdata \
  REASONIX_WEBVIEW2_EXTRA_ARGS=--remote-debugging-port=9222 \
  ./reasonix-desktop.exe &
```

`REASONIX_WEBVIEW2_EXTRA_ARGS` 是 fork 专用通道（vendored go-webview2 loader 追加），任何构建都可用，无需开产品开关。**铁律：端口必须在启动时给定**——已在跑的实例先正常退出，再带参重启；`--user-data-dir` 冲突/单实例互斥时用上面的隔离 APPDATA 规避。

## 2. playwright 连接

playwright 是 `desktop/frontend` 的 devDependency（`pnpm install` 过即可用）。CLI 子命令（open/screenshot/codegen）**不能**接已有 CDP 端点；连接必须走库 API `chromium.connectOverCDP`，用一次性 node 脚本经 bash 工具执行即可（这不是常驻框架，脚本用完即弃）：

```bash
cd desktop/frontend   # 依赖解析靠 cwd，勿在别处跑
node - <<'EOF'
const { chromium } = require("playwright");
(async () => {
  const browser = await chromium.connectOverCDP("http://127.0.0.1:9222");
  for (const ctx of browser.contexts())
    for (const p of ctx.pages()) console.log("PAGE:", await p.title(), p.url());
})();
EOF
```

`connectOverCDP` 不需要 playwright 自带浏览器（复用对方实例），故无需下载 `.pw-browsers`。

### 连通性自检（go/no-go 三步，任一失败即停在排障）

1. `bash` 工具跑 `curl -s http://127.0.0.1:9222/json/version`——拿得到 `Browser` 字段才算端点活着；
2. `connectOverCDP` 不抛错；
3. `pages()` 非空且能读出 Wails 页面（url 多为 `http://wails.localhost/...` 或 `index.html`）。

## 3. 快照-交互-断言工作流

```js
const pages = browser.contexts()[0].pages();
const page = pages.find(p => /wails\.localhost|index\.html/.test(p.url())) ?? pages[0];
await page.bringToFront();

// 截图（证据件带时间戳防覆盖旧档）
await page.screenshot({ path: `verify-${Date.now()}.png` });

// 断言以 DOM 为主、截图为辅证（批量截图有 Chromium 截图与 DOM 竞态，验收认断言级证据）
await page.locator("text=新建会话").first().waitFor({ timeout: 5000 });

// 交互 → 状态变化后重新快照再断言
await page.locator("button:has-text('设置')").last().click();
await page.locator(".settings-screen").waitFor();
```

选择器纪律：真实实例界面语言跟系统 locale（zh-CN），**双语正则**（如 `新建会话|New session`）防失配；动态列表每次操作前重取，勿缓存 index。

脚本落盘纪律：验证脚本一律 `write_file` 写 UTF-8 文件再 `node xxx.mjs` 执行——**勿用 bash heredoc 内联中文**（Windows 控制台默认 GBK，经 stdin 传入会把中文绞成乱码，选择器静默失配）；构造测试页用 `page.setContent(html)` 而非 `data:` URL（非 ASCII 在 data URL 同样受编码绞乱，2026-10-15 干跑实测）。

## 4. 窗口铁律（不守则点击被拦，症状极具误导性）

1. 测试实例**一律最大化**：`await page.evaluate(() => window.runtime.WindowMaximise())`（wails runtime 最稳，CDP 改窗口尺寸会被应用自身状态管理改回）；
2. 核 `innerWidth×innerHeight`（≥1280 宽）再开始交互——小窗口塌缩会让 sticky header/窗口控件拦截点击，报「点击超时/元素 intercept」；
3. DPI/系统缩放下 viewport≠物理像素：断言用 DOM 尺寸（boundingBox），截图标注注明缩放比。

## 5. 清理（隔离实例五件，缺一留雷）

1. 杀实例：**按命令行含 `remote-debugging-port` 甄别 PID**（`wmic process where "name='reasonix-desktop.exe'" get processid,commandline`），勿误杀用户实例；
2. 删 `/tmp/rx-cdp` 副本；
3. 删临时 registry 键（如写过 WebView2 配置）；
4. 复原被改的仓库文件；
5. 证据件（截图/结果 json）留存归档，勿留在仓库根。

## 6. 排障表

| 症状 | 原因 | 处置 |
|---|---|---|
| Connection refused | 端口没起：实例非带参启动 / 启动即退 | 核启动 env；先退干净再带参重启；查 desktop 日志 |
| 端点文件一直 `pending` | 浏览器子进程未回报端口 | 等数秒重读；仍 pending 换通道 B 固定端口 |
| connect 成功但 pages 空 | WebView2 初始化中 | 等 2-3s 重试（应用启动比网页慢） |
| 点击超时/intercept | 小窗口 sticky header 拦截 | §4 窗口铁律，先 WindowMaximise |
| 选择器批量失配 | 界面语言 locale | 双语正则 |
| 中文选择器静默失配、DOM 里中文成乱码 | 脚本经 GBK 控制台 heredoc 传入 / data: URL 编码绞乱 | 脚本 write_file 落盘 UTF-8 再执行；测试页用 setContent |
| `Cannot find module 'playwright'` | cwd 不对 | 切到 `desktop/frontend` 再跑 |
| 远程子窗口自动化不了 | remote 子窗口无 bindings、启动即不带 CDP 参数 | 预期内，改走主窗口入口验证 |

## 7. 衔接既有自验体系

- 组件级：`desktop/frontend/bench/*.mjs`（vite preview + mock，headless）——先在那里复现不了再上真机；
- 技术细节/pit 档案：`internal/skill/builtincontent/ll-iteration-dev/references/gui-self-verify.md`（窗口铁律、切换耗时日志判读、清理五件出处）；
- 场景操作卡：workspace `tasks/界面自验操作卡.md`（场景→命令表）。

工具约定：执行用 `bash`，读端点文件/日志用 `read_file`，写验证脚本用 `write_file`，找证据件用 `glob`/`grep`。
