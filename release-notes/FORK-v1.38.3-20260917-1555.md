# Reasonix Fork 桌面版 v1.38.3-20260917-1555 — Release Notes

> **构建**：2026-09-17 15:26–15:28 ｜ **安装目录**：`versions/v1.38.3-20260917-1555/`
> **增量基线**：上一包 **`v1.38.3-20260917-1353`**。本文件**只列相对 1353 的新增/变更**，
> 不堆叠更早包的内容——看更早的历史请沿 `FORK-v1.38.3-20260917-1353.md` → 更早包 链式回溯。
> 包版本号长期停在 `v1.38.3`（与上游对齐），时间戳只用于区分同版本号的多次构建。

## 本版新增：任务 156 四修（跨会话协作链路收口）

- **156.A 发送方身份自动铸造**：`talk_to_session` / `talk_to_session_sync` 在发送方尚无
  contact_id 时自动 `EnsureContactID`。此前"只发不收"的会话永远停在 `(未登记)`，
  它发出的消息只能降级为无法回信的单向通知。
- **156.B 幽灵项目死循环（审计 F154-6 升格修复）**：`createCollabSession` 的 scope 判定从
  `workspaceRoot != ""` 改为与 `globalWorkspaceRoot()` 做 `sameDesktopPath`（大小写折叠）比较——
  global tab 的 WorkspaceRoot 本就非空，旧判定把每个全局 collab 会话错挂成 project topic，
  `CreateTopic` 的 ensureProject append 会在用户删除项目后**自动重建**"global-workspace"。
  同时 `desktopSessionDir("")` 空 root 不再退化到进程 CWD（审计 §R6），统一解析全局会话目录。
- **156.C 权限继承**：`createCollabSession` 把设置中的默认工具审批档（Ask/Auto/YOLO，
  经 `desktopNewSessionDefaults`）写入 `BranchMeta.ToolApprovalMode`。此前跨会话消息
  自动唤起的 turn 固定落在「询问」档，无人批准导致 bash 等工具全部
  `approval aborted`——"收信即工作"在需要工具的场景实际不可用。
- **156.D 回信引导**：`talk_to_session` 工具描述明确"收到跨会话消息后**必须**用本工具回信
  （to = From 的 contact_id），不要把回复写在自己的会话里"（发起方看不到）；单向通知文案
  不再要求接收方做只有发送方能做的事。

## 本版新增：任务 151 A 级（切 tab 秒切）

- **放宽缓存复用判定**：`hasReusableCachedTranscript` 去掉 `historyTotalTurns === 0` 否决
  （该计数器对旧 always-skip 行为下驻留的 tab 恒为 0，useController 注释自认不可靠），
  驻留会话切回不再触发全量 history 重载（实测大会话 `switch-tab:history` 3.9–5.2 s）。
  LRU/字节预算仍约束内存，后台刷新对账指纹。
- **诊断日志前置**：`noteHydrateDecision` 的未命中缓存分支写 `desktop.log`
  （`hydrate reloaded history`），复命中的判定过程此前只在内存监控 map 里，事后无法排查
  （2026-09-16 切 tab 排查正是卡在这里）。
- **任务 123 收口**：151.A 即 123「后续估工 3」的实施；123 剩余的 history reconcile
  位于 `!skipHistory` 分支内，本版后驻留切回直接 skip，reconcile 一并消失。
  123 收口为测量基建 + 保留估工 1/2（digest 对比 / 大会话预取，未做）。

## 验证矩阵（构建自检，2026-09-17 15:2x）

`go build ./...`（根 + desktop 两模块）✅ ｜ `go test desktop -run Collab` ✅ ｜
`go test internal/agent -run "Collab|ContactID"` ✅ ｜ `tsc --noEmit` ✅ ｜
tsx 单测 session-monitor 45 passed / hydrate-history-apply 31 passed ✅ ｜
`check-fork-integrity.mjs` 42/42 ✅

## 已知未含（出包后待实测反馈）

- 156.C 只写 meta；若 UI 权限选择器初始值不读 meta 仍显示「询问」高亮，需补前端初始值同步
- 151.B/C 级未做（A 级实测无效才升级，按任务纪律分期）
- 123 估工 1/2（增量 digest 对比 / 大会话预取）未做
