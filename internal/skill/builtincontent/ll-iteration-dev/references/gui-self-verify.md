# 界面自验技术细节（gui self-verify · 拆分自 2026-09-27 用户指令）

> 主文件只留指针；本文件=技术细节权威。流程契约见 SKILL.md §5/§6，本文件不重复。
> 关联 workspace 文件：`tasks/界面自验操作卡.md`（场景→命令表）、bench 脚本 `github-repo/reasonix/desktop/frontend/bench/`。

## 1. 窗口尺寸铁律（2026-09-27 用户指令：全屏测试 + 考虑缩放）

- **测试实例一律最大化/全屏跑**：小窗口（实测第二实例 viewport 曾塌到 352×26）会让布局重叠 → 点击被 sticky header/`windows-window-control` 拦截，症状（点击超时/元素 intercept）极具误导性。
- 修复法（按可靠性排序）：
  1. 页面内 `window.runtime.WindowMaximise()`（wails runtime，最稳——CDP `Browser.setWindowBounds` 会被应用自身的窗口状态管理改回）；
  2. 之后核 `innerWidth×innerHeight`（≥1280 宽）。
- **缩放**：DPI/系统缩放下 viewport≠物理像素——断言用 DOM 尺寸（boundingBox/elementFromPoint），截图标注时注明缩放比；`page.screenshot` 输出按 viewport 缩放。

## 2. CDP 自验链（对装机同源代码）

1. 构建：目标分支 + `tasks/20260924并行开发-批次7/A线-cdp-patch-zero-behavior.diff`（1877B 零行为版；官方 env/registry 双被 go-webview2 `init()` 清空，唯一通道=API args）；
2. `wails generate module`（bindings 代际对齐，exit2 但 bindings 落地，以 tsc 0 err 为准）→ `pnpm build`（预算八面）→ `wails build -s`；
3. 起独立实例（不碰用户实例）：`REASONIX_HOME=<tmp>/home APPDATA=<tmp>/appdata REASONIX_WEBVIEW2_EXTRA_ARGS=--remote-debugging-port=9222 <exe>`；config 副本放 `<APPDATA>/reasonix/config.toml`、`.env` 副本放 `<REASONIX_HOME>/.env`（缺则 Models()=空）；
4. 连接 `chromium.connectOverCDP("http://127.0.0.1:9222")` → 跑 `bench/gui-click-verify.mjs`；
5. **清理五件**：杀实例（按命令行甄别 PID，别误杀用户实例）→ 删 tmp 副本 → 删 registry 临时键（如写过）→ worktree patch 复原（`git checkout -- <patched file>`）→ diff 归档件保留。

## 3. gui-click-verify 脚本坑序（pit 档案）

| pit | 症状 | 修法（已入脚本） |
|---|---|---|
| 1-6（A 线 0927） | 中文界面选择器失配、菜单 index 漂移等 6 个真机坑 | 双语正则+每次开菜单按全文重找 index |
| 7 | sticky header 拦 navitem 点击 | `navClick` helper：已 active 跳过 → `force:true` → 兜底 `el.click()` evaluate 派发 |
| 8 | 窗口塌缩致全页遮挡 | §1 全屏铁律，跑脚本前先 WindowMaximise+核 viewport |

- 脚本运行目录=`desktop/frontend`（playwright 依赖解析靠 cwd）；`PLAYWRIGHT_BROWSERS_PATH` 由脚本自设（`.pw-browsers`）。
- 结果落 `tasks/20260924并行开发-批次7/gui-click-verify-result.json`（注意：后续跑会**覆盖**旧结果——要留档先备份，2026-09-27 实测 12:11 读数被覆盖教训）。

## 4. 切换耗时日志判读（切出瓶颈归因，0927 实战）

- 关键行（`%APPDATA%/reasonix/logs/desktop/desktop.log`）：
  - `desktop: tab switch timing stage="switch-tab:ancillary context|effort|meta"`（切入侧，单段 160-380ms 属正常）
  - `session: save begin/end ms=N`（**切出侧**——切走触发的会话保存）
  - `session: dag state for save ... reason=nil_cache ms=N`（DAG 派生缓存未命中重算，=196 族现场）
- **判读法**：把「上一次 ancillary 完成 → 下一次 render 启动」的空隙对齐 save begin/end 时间戳；save ms>800 且 `reason=nil_cache` → 切出瓶颈归 196（save 锁内重放族），非 232（surface 驻留）本身。
- 实测样本（1420，2026-09-27 15:47）：`save end ms=1310`（24MB log，dag 888ms nil_cache）→ 用户感知快速来回 >1s。

## 5. 快照/截图纪律

- 批量截图可能撞 Chromium 截图与 DOM 竞态（335 裁决 b）：验收以断言级证据为主，截图抽验好帧；证据件命名带时间戳防覆盖。
