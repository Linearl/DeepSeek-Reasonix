# 任务 893-B handoff（2026-10-14）

worktree：`github-repo/worktrees/wt-893`（基线 `1747f222d` = main-v2-stable tip）。本件只做 B 档（P2 frontend 最小档）；A 档宿主生命周期保留、C 档同回合回收、tl 权威源口径变更、提上游均不在本件。

## 任务一句话

会话 todo 跨回合被清空后（设计如此：todo 是回合级临时视图），右侧「任务」面板常显「当前会话暂无任务」，用户误判 AI 没在跟踪任务（原话「待办为空，确定立了？」）。修法：面板空态区分三态——从未有过任务（原文案）/ 有过任务但已按回合封存（新文案 + 上一回合 N 项）/ 有活跃任务（TodoPanel，不变）。**纯前端完成，无需后端字段**：数据源可区分两态（见下）。

## 数据源结论（B 档可行性的关键调查）

「从未 write」vs「write 过后为空」可由前端已持有的信号区分，四个独立信号任一存在即判「有过任务」（sealed）：

1. **transcript**：`state.items` 里的 `todo_write` 工具项——host 侧 `desktop/app_history_tools.go historyToolCall` 对 todo_write **特意保留 args**（其他工具归档截断），hydration 后 `historyMessagesToItems` 造 `status:"done"` 项 → 重载会话也能看到；失败写（error）和 subagent 写（parentId）不算数。
2. **`todoArchive`**：更早的全终态批次（task 152）。
3. **`meta.dismissedTodoBatches`**：sidecar 持久（`desktop/todo_dismissal.go`），跨重载。
4. **`meta.canonicalTodos`**：非 nil 即 kernel 快照过清单。

count（「上一回合 N 项」）取最优来源：live list > archive[0] > canonical；只有 dismissed 批次或仅 transcript 时 count=null → 用不带数字的通用封存文案。

## 改动清单（3 个 commit）

- `52802ec83` lib：`src/lib/todoVisibility.ts` 加 `todoDockEmptyState()` 纯函数（never|sealed + count）；新组件 `src/components/TodoDockEmptyState.tsx`（`data-todo-empty-state` 状态位）；三语文案键 `rightDock.todoSealed` / `rightDock.todoSealedCount`（en/zh/zh-TW）。
- `b70d2322e` 接线：`App.tsx` 挂载树右侧 dock todos 空态传入 todos/archive/items/dismissedBatches/canonicalTodos；`WorkspaceDockRegion.tsx` 增可选 `todoEmpty` prop（**缺省保持原文案，向后兼容**）；`dockRegionBuilders.ts` 透传。AppRuntimeView 未挂载遗留壳暂未接（prop 已就位，后续要接直接传）。
- `53f2f8ffd` test：`src/__tests__/todo-dock-empty-state.test.tsx` 35 断言（纯函数 14 + 组件三态 + dock 向后兼容 + 活跃任务态无空态渲染 + {count} 插值 + 用户截图场景 + 三语键）。

## 验收自检（全部通过）

- ✅ 三态渲染测试：never / sealed（带 count、不带 count）/ active（TodoPanel 分支不变，todo-sidebar-dock 既有 63 断言全绿）。
- ✅ 用户截图场景：写过 3 项签核（全 completed + dismissed）后次回合 → `todoDockEmptyState` 判 sealed、count=3 → 面板显「任务列表已按回合封存（上一回合 3 项）」，不再显「暂无任务」（测试用例覆盖）。
- ✅ i18n 三语：新键 en/zh/zh-TW 齐备，`Record<DictKey,string>` 编译期强制镜像；comment-leak 检查 0 leak。
- ✅ 既有测试全绿：test-todo-visibility、todo-sidebar-dock(63)、todo-panel-lifecycle、task152-todo-tree(15)、conversation-projection、check-app-layers。
- ✅ `tsc --noEmit` 0 错；tsc-test-gate 我的文件 0 诊断（预存红见下）。
- ✅ **bundle 棘轮：未炸线无需抬**——largest chunk gzip 333.6 / 334.5 线（余 0.9KiB），initial JS 574.1 / 574.8，全部 PASS。

## 预存红申报

- `tsc-test-gate`：worktree fresh install 报 47 条基线外诊断，主仓旧 node_modules 报 43 条；差值 4 条全在 `composer-goal-toggle.test.tsx`（未触碰文件），用 clean-HEAD probe worktree + 同一 node_modules 复现 47 → 判定为**依赖安装年代差**导致的预存红，与本件 diff 无关。本件文件 0 诊断。
- `check-duplicate-definitions`：5 处重复声明（VersionEntry 等），预存，与本件无关。

## 残留风险

1. **sealed 文案的语义边界**：canonicalTodos 非空但 live list 因 dismiss 不显示时，count 取 canonical 长度——若 canonical 比最后可见批次新（如 host advance 合成写），数字可能与用户记忆中的列表差 1；影响极小（文案只说「上一回合 N 项」）。
2. **AppRuntimeView 遗留壳未接线**：未挂载树的 dock 空态仍永远显 never 文案；`todoEmpty` prop 已就位，若该壳将来重新挂载需调用方补传信号（已注释标注）。
3. **A/C 档未做**：宿主侧跨回合保留、同回合回收属后续档；本件只保证「不再误导」，不改变回收行为本身。
4. 首个 turn 进行中（todo_write 正在 running、尚未 done）时短暂判 never——该窗口内面板本来也无清单可显，可接受。

## 恢复方式

```bash
cd github-repo/worktrees/wt-893/desktop/frontend
node scripts/test-todo-visibility.mjs && npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/todo-dock-empty-state.test.tsx && npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/todo-sidebar-dock.test.tsx
```

落地纪律：保留分支 wt-893，**不合 main-v2-stable**（合并由用户指定会话执行）。
