# Reasonix Fork 桌面版 v1.38.3-20260916-1950 — Release Notes

> **构建**：2026-09-16 19:55 ｜ **安装目录**：`versions/v1.38.3-20260916-1950/`
> **基线**：官方 `v1.38.3`。本仓库长期停在该版本，包版本带时间戳只为区分**同一版本号下的多次构建**。
>
> 本文件**只列本版相对上一版包的新增内容**。基线版本的完整说明（v1.38.2 ~ v1.38.3 追齐内容、
> 此前的 fork 修复与增强）见 [`FORK-v1.38.3.md`](./FORK-v1.38.3.md)；逐项差异台账（含开关 / 默认 /
> 承载文件 / 上游吸收状态）见 [`FORK-vs-upstream.md`](./FORK-vs-upstream.md)；用户向的 13 条日常
> 可感知改动见 [`FORK-features-intro.md`](./FORK-features-intro.md)。

## 本版新增

### 修复：设置页点「打开会话监控 / AI意见箱」没有任何反应

**症状**：设置 → 实验特性，开关是开的、按钮也能点，但点下去界面上什么都不出现。此前修过两次
（`7cd41921a` 让它「开着设置也能开面板」、`0ef70829e` 把 z-index 换成 `--z-popover`），都没效果。

**根因**：两个面板组件之前**只有一个挂载点** —— `SidebarRegion.tsx` 里。而 `SidebarRegion` 由
`AppRuntimeView` → `AppRuntime` 一路引用，**`AppRuntime` 根本没被任何地方渲染**（`main.tsx` 只渲染
`App.tsx`）。这套组件是未使用的代码。与此同时，设置页是 `ManagementSurface` 覆盖层，会整个替换外壳。
两条叠加的结果：**点击确实更新了 store，但没有任何组件订阅、也没有任何组件去渲染 portal** ——
所以面板从未被画出来。这也解释了为什么此前每次改「显示层级」都无效：问题根本不在层级。

**修复**：

- 两个面板改挂到 **`App.tsx` 根层**（与设置覆盖层同级），组件始终存在于渲染树；两者本来就是
  `createPortal(..., document.body)`，定位不受挂载点影响。
- **移除侧栏入口**（`SidebarRegion` 里的 4 处按钮与面板挂载）：按你的要求，**只保留设置页这一个入口**。
- 加了**临时诊断日志**（`setSessionMonitorOpen` / `setFeedbackOpen` 各两条，写入 `desktop.log`）：
  会记录「请求被忽略 / 状态已变更」并带上**当前订阅者数量**，用于区分「点击没到达 store」和
  「store 变了但没人渲染」。**问题确认修复后可随时删除**。

**验证**：`pnpm typecheck` 通过；8 项并行检查全 PASS；`check-fork-integrity.mjs` 42/42；
`vite build` 成功；bundle 预算 8/8 PASS。

## 升级提醒

- 覆盖安装即可；会话 / 记忆 / 配置目录与官方版完全兼容。
- **不要回退到 1.34 / 1.38.1 以下的旧版本**：会话事件日志会**就地升级到 schema 2**（`upgraded_from_schema: 1`），
  而旧构建只支持到 1，加载时会以 `uses schema 2; this build supports up to 1` 拒绝打开
  （**文件不会被改动**，是保护性设计，不是损坏）。需要降级前请先在 1.38.3 里导出会话内容。
- **已知问题（本机环境）**：`go test ./internal/boot/` 整包在本机挂起（环境探测单飞机制 `beginProbe`，
  每个测试单跑均通过）。
- **已知问题（未修）**：开启 v4 会话存储后，`desktop.log` 会被
  `session v4 bridge ... "session operation id conflicts with an earlier batch"` 每 30 秒刷一次，
  可能淹没其它日志，排查问题时需先过滤。
