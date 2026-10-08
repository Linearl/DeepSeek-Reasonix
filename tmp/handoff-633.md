# 任务 633 handoff（2026-10-08，宿主重启前留档）

## 任务一句话

「摘要失败 · 已停重试」文案在多轮压缩仍在进行时误导用户以为流程终止。修法：去掉终态措辞、按真实状态区分三态（本轮未完成稍后重试 / 短视图已保留仅刷新失败 / 未形成短视图稍后重试），Go 侧透出结构化 `foldInstalled` 字段支撑前端区分。worktree：`github-repo/worktrees/wt-633`（基线 `8a833f467` = main-v2-stable tip）。

## 已完成（代码全部落盘，尚未验证、尚未全量 commit）

1. **Go 侧透出 foldInstalled**：
   - `internal/agent/projection.go` — `ContextMaintenanceReceipt` 新增 `FoldInstalled bool json:"fold_installed,omitempty"`（含 task 303/633 注释）
   - `internal/agent/context_receipt.go` — `recordContextMaintenanceOutcome` 增加 `foldInstalled bool` 参数（第 5 参）；`recordContextMaintenanceBlocked` 传 `false`；`emitContextMaintenance` 透传 `FoldInstalled: r.FoldInstalled`
   - `internal/agent/context_manager.go:301` — `summaryFailed` 把 `foldInstalled` 传入 record（唯一需要 true 的调用点）
   - `internal/event/event.go` — `event.ContextMaintenance` 新增 `FoldInstalled bool json:"foldInstalled,omitempty"`
   - `internal/eventwire/wire.go` — wire 结构体 + `ToWire` 转换透传
2. **前端映射**：
   - `desktop/frontend/src/lib/contextMaintenanceTypes.ts` — `WireContextMaintenance.foldInstalled?: boolean`；`formatContextMaintenanceNotice`：`failed + foldInstalled` → 新键 `context.maintenanceRefreshFailedSummary`，`failed` → 改写后的 `maintenanceFailedSummary`，`blocked` → 改写后的 `maintenanceBlockedSummary`
   - `desktop/frontend/src/lib/useController.ts:2021-2030` — context_maintenance case：`level = m.status === "failed" && !m.foldInstalled ? "warn" : "info"`（fold 已保留降为 info）
3. **三语 locale**（en.ts 是 DictKey 源，`Record<DictKey,string>` 编译期强制三语键集一致）：
   - zh.ts / zh-TW.ts / en.ts：`maintenanceBlockedSummary`→「摘要未形成短视图 · 稍后自动重试」；`maintenanceFailedSummary`→「摘要未完成 · 稍后自动重试」；新增 `maintenanceRefreshFailedSummary`→「短视图已保留 · 摘要更新失败，稍后自动重试」。**全仓不再有「已停重试」**（locale 棘轮：净增 1 键 ×3 语，已改 2 键 ×3 语）

## 状态机定论（报告可直接用）

`failed`/`blocked` receipt 是**代内（generation-scoped）同视图回退，不是终态**：`contextMaintenanceBlocked`（context_receipt.go:31）只压同一 inputHash 的本代自动重试；视图变化/下轮/下代自动重试；超硬顶还有截断救援（发独立 applied/truncate 通知）。真正放弃（截断也失败 → ErrCompactionRequired；手动压缩失败）走 turn error 路径，不经这两条通知。`foldInstalled`（task 303 语义，summaryFailed 的 reason 里已有 "(compaction kept)" 字样但前端无法用）= 同一 Prepare 的前一轮 ladder 已装折叠、只是摘要刷新失败——正是截图场景。任务书验收第 3 条（切模型全链路真实复现）超出小件预算，已在验收矩阵降级为映射级测试，报告需注明。

## 剩余（续做者按序）

1. **Go 测试**：`internal/agent/` 加 `foldInstalled` 透传断言（推荐：`fakeProvider.streamErrs`（compact_test.go:35，逐次弹出后回退成功）+ `agentOverForce` harness；直接调 `recordContextMaintenanceOutcome(..., true)` 断 receipt + FuncSink 捕获 event 的透传也可，最稳）。注意 `_test` 文件里对 `recordContextMaintenanceOutcome` 的旧 5 参调用需要补第 6 参 `false`（先 `git grep -n "recordContextMaintenanceOutcome(" -- internal '**/*_test'` 查）
2. **前端测试**：更新 `desktop/frontend/src/__tests__/context-maintenance-notice.test.ts` —— failed 映射不再含「已停重试」、failed+foldInstalled → refresh 键、blocked 新文案、三语 locale 文件含新键且全仓 locale 无「已停重试/已停重試/auto-retry stopped」（readFileSync 断言，仿该文件既有风格）
3. **验证**：`go build ./...`（**当前仍在后台跑**，exec_fa6889e6，未出结果）→ `go test ./internal/agent/ -run 'Compact|Summary|Context|Maintenance' -count=1`（压缩相关零回归）+ eventwire 测试；前端 `pnpm typecheck`（会抓三语键集）、`tsx src/__tests__/context-maintenance-notice.test.ts`、`pnpm build`（含 check-all-parallel + bundle budget）；根目录 `node scripts/check-fork-integrity.mjs`
4. **commit**：中文「633：」开头、显式路径（本 handoff 之后的收尾 commit）
5. **报告**：`docs/report/zcode交付/zcode交付-633-压缩文案-20261008.md`（结论先行：三态文案映射表 + 状态机定论 + locale 净增 1 键报备 + 验收矩阵逐条对照 + 预存红/未验证项如实列出）
6. **回执**：commit sha + 验收对照

## 环境注意事项

- 本仓库 grep 极慢（Windows FS），一律用 `git grep`；`sed -n` 读大文件也会超时，用 Read 工具带 offset
- Edit 工具偶发 30s 超时但**实际已写入**，报错后先 Read 核对再重试
- `pnpm` 在 `desktop/frontend/` 下跑；Go 在仓根跑
- 纪律：不 push/merge/出包；不动 tasklist/docs/collab（报告目录除外）；预存红如实绕行报备
