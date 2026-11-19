---
name: ll-cdp-reasonix-debug
description: 用 CDP（Chrome DevTools Protocol）+ playwright 自主操作与检查 Reasonix 桌面版界面——截图判读、滚动、点击、打开指定面板（如对话版本管理）、检查 UI 缺陷并还原现场。当需要「自己验证 Reasonix 界面/面板问题」「不依赖用户打开界面」「CDP 调试 reasonix」时使用。触发词：CDP 调试、界面自验、操作 reasonix、检查面板、版本管理检查。
---

# CDP 调试 Reasonix 桌面版（自验操作全流程）

给运行中的 Reasonix 桌面实例开 CDP 端点，用 playwright 连接后做截图/滚动/点击/面板导航，**检查完必须还原现场**。基于 desktop-automation 技能 + 2026-11-06 实战沉淀（含选择器防污染、还原纪律等本技能特有坑）。

## 一、前置：开 CDP 端点（一次性，已开则跳过）

1. config.toml 加（若无）：`[desktop] experimental_cdp_debug_port = true`
2. **必须重启 Reasonix**（boot-time 设置，改完不重启端点永远 pending）
3. 端点读取：`%APPDATA%\reasonix\logs\desktop\cdp-endpoint.txt` → 内容 `127.0.0.1:<port>`
4. 活性自检：`curl -s http://127.0.0.1:<port>/json/version` 返回 `Browser` 字段即活

## 二、脚本纪律（三坑，实战全踩过）

1. **脚本必须放 `github-repo/reasonix/desktop/frontend/` 下跑**——ESM（.mjs）按脚本位置解析 node_modules，放别处 `import playwright` 必失败；用 `.mjs` + `import` 形态（`require` 在 ESM 作用域报错）
2. **选择器防转录污染**：transcript 里全是讨论文本，宽匹配（`text=设置`、`/版本/`）必撞到自己的转录/脚本文本。用 `text-is("设置")` 精确匹配 + 限定容器（`.sidebar__navitem`、`[class*=settings]`）；`evaluate` 文本扫描要排除 `closest("[class*=transcript]")`
3. **点不动就 evaluate click**：playwright click 被遮挡层拦截（`intercepts pointer events`）时，`page.evaluate(() => el.click())` 直接派发可绕过（实战：「返回工作区」按钮被拦，evaluate click 成功）

## 三、常用面板导航链（源码指引的真锚）

### 对话版本管理（设置 → 存储与路径）
```
1. 点设置：button.sidebar__utility-button:has(.sr-only:text-is("设置"))
   （或 sidebar__navitem 版，双锚都试）
2. 点 tab：text="存储与路径"
3. 等 lazy load 1.8s → 点入口：text="查看并合并会话副本…"
4. 等扫描 3s+ → 检查面板（管理页全屏态，management-screen）
```

### 还原现场（**必须**，finally 保证）
```
点 text="返回工作区"（evaluate click 兜底）
验证：document.getElementById("management-back") 为 null
```
**纪律**：操作后把用户界面留在原状态——留在设置页/管理页是事故（20261106 实测被用户指出）。

## 四、检查与截图模式

```js
// 滚动实测（找最大滚动容器）
const t = [...document.querySelectorAll("*")].filter(el =>
  ["auto","scroll"].includes(getComputedStyle(el).overflowY) &&
  el.scrollHeight > el.clientHeight + 100
).sort((a,b) => b.scrollHeight - a.scrollHeight)[0];
t.scrollTop += 600;  // 返回 {before, after, max} 即滚动能力实证

// 面板探测（限定 surface 内防假阳性）
const inS = (el) => el.closest("[class*=settings], [class*=Storage], [class*=recovery]");
const cnt = (re) => [...document.querySelectorAll("div,span,th,button,tr,td")]
  .filter(el => inS(el) && el.offsetParent !== null && re.test((el.textContent||"").trim().slice(0,30))).length;
```

截图判读：`page.screenshot({path})` → 用 `view_image` 工具读图（我方视觉判读）；断言以 DOM probe 为主、截图为辅证。

## 五、排障速查

| 症状 | 原因 | 处置 |
|---|---|---|
| 端点 pending 不动 | config 改了没重启 | restart 应用（restart_update restart） |
| import playwright 失败 | 脚本不在 frontend 下 | 挪到 desktop/frontend/ 再跑 |
| 选择器撞转录文本 | 宽匹配污染 | text-is 精确 + 容器限定 |
| click 被 intercepted | 遮挡层/动画中 | evaluate(() => el.click()) 绕过 |
| 探测全 0 但界面有 | 面板未展开/未加载 | 点入口后等 3s+ 再 probe |
| 操作后用户界面变了 | 没还原 | finally 里点「返回工作区」 |

## 六、能力边界

- 只读检查（截图/滚动/探测）随时做；**写操作**（点击删除/归档等）需用户明确授权
- 涉及会话数据的面板（版本管理）检查时**不点破坏性按钮**（删除/清理），只看
- computer-use（/computer-use:run）是另一条需用户逐任务授权的原生通道，本技能 CDP 不需要它
## 七、最小化窗口实测结论（20261106 实证）

| 能力 | 最小化下 | 说明 |
|---|---|---|
| evaluate/DOM/文本/状态读写 | ✅ 可用 | visibilityState 仍为 visible（WebView2 不切 hidden） |
| 布局尺寸断言（getBoundingClientRect） | ❌ 不可信 | viewport 收缩（transcript 886x511 → 400x52），尺寸断言会假红 |
| **截图** | ❌ 残帧 | Chromium 最小化暂停合成（实测拿到 216x36 残块），**截图前必须恢复窗口** |

- **后台态（未最小化被遮挡）已严格验证可用**（20261106：chrome 置前造真后台，Reasonix 被完全遮挡）：DOM=3446、布局 886x511 正常、**截图完整帧 2792x1658**——与最小化（残帧+尺寸失真）形成对比。结论：**后台照常操作，只有最小化才有问题**；截图异常兜底仍是 `bringToFront()`。
- 脚本里的 `page.bringToFront()` 就是为截图/尺寸断言恢复窗口——保留它。
- 找窗口句柄：`Get-Process reasonix-desktop | Select -First 1` 取 `.MainWindowHandle`；`FindWindow(null,"Reasonix")` 会 NOT_FOUND（实际标题带会话名后缀）。
- 最小化/还原窗口（PowerShell）：`Add-Type` 唯一类型名 + `ShowWindow(h,6)` 最小化 / `ShowWindow(h,9)` 还原，`IsIconic(h)` 验证。
