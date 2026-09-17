# Reasonix Fork 桌面版 v1.38.3-20260917-2130 — Release Notes

> **增量基线**：上一包 **`v1.38.3-20260917-2030`**。本文件只列相对 2030 的新增/变更，
> 不堆叠更早包的内容——历史沿 `FORK-v1.38.3-20260917-2030.md` 链式回溯。
> 包版本号长期停在 `v1.38.3`（与上游对齐），时间戳只用于区分同版本号的多次构建。

## 本版新增：切 tab 渲染层优化 + OpenCode Go 兼容修复

两任务经 worktree 并行开发、零冲突合并后入库（另两线 117/118 与 155 仍在开发，后续包跟进）。

### 切 tab 渲染层修复（任务 151 第三轮）— 大会话来回切不再重付渲染成本

数据层此前已优化到位（切换时日志 `switch-tab total=0ms`），但单个 Transcript 实例在
每次切 tab 时仍把整个条目列表换成新 tab 的——大会话每次切换都重付一次全量渲染。
本版改为 **per-tab pane 驻留**：切走的 tab 内容保持挂载仅隐藏，切回直接显示。

- 真实 Chromium 实测：100 轮/tab 切换 109ms → **8ms**；300 轮/tab 首帧 496ms → **148ms**。
- 驻留上限默认 2（当前 + 上一个），可在 DevTools 里调
  `localStorage.setItem("reasonix.transcriptResidency", "1")`（回退旧行为）~ `"4"`，
  重启窗口生效。
- 内存代价：每多驻留一个 tab ≈ 多一份窗口 DOM，属可接受代价；后续可继续优化。
- 新增日志：`desktop.log` 搜 `tab switch render`，`render=<ms>` 为渲染耗时、
  `resident=` 为驻留 pane 数（一直是 1 说明驻留未生效，付了重建成本）。
- 数据层链路（local-snapshot / hydrate veto）零改动；分屏与「加载更早」不受影响。

### OpenCode Go 路由 tool 消息 400 修复（任务 165）— 第二轮对话必挂根治

OpenCode Go 官方网关拒绝消息级 `name` 字段，而此前为兼容 MiMo 后端给所有 tool 消息
都带了 `name` 键 ⇒ OpenCode Go 用户第二轮（出现工具调用后）起所有请求 400。

- 现在仅对 OpenCode Go 官方 chat 路由（host 严格等于 opencode.ai 且路径 /zen/go/v1）
  省略 tool 消息的 name 键；MiMo 等严格后端仍照发（#4711 回归测试守住）。
- 仿冒域名、自定义代理、其他后端一概不受影响（12 例路由隔离测试）。

---

**验证**：tsc clean；transcript-residency 21 / session-monitor 45 等 6 套前端测试绿；
provider 4 包全绿（含 MiMo 回归红线与红-绿对照）。
**已知存疑**：transcript-viewport 1 例超时断言疑为预存（主仓基线无法取得干净对照），
装机实测若遇「跳到底部」异常请反馈。
