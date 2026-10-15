# handoff-913（P2 frontend：输入框偶发失焦）

## 结论（根因链三面，代码面定性）

1. **主根因①导航遮罩收焦无人归还**：`runtimeTransitioning`（navigationSurfaceIntent 非空）→ `composerSurfaceHidden` → composer-decision-host 整块 `hidden+inert+aria-hidden`（App.tsx ~5900），浏览器**静默**收走 textarea 焦点（focus 落 body，通常不派发 blur）。导航 settle 后**没有任何归还路径**——decision 释放有恢复、导航释放没有。触发面=打字中程序化导航：快捷键开题/协作跳转/任务监控跳会话/后台 runtime 揭示/换 tab。与用户 1015「打字中断/需重新点击」现场吻合。
2. **盲区②**：任务 276 禁用翻转自愈的放弃路径（重试耗尽/被 draft epoch 连坐取消）原先零痕迹——desktop.log 276 三探针全静默 ≠ 没发生，只说明失焦走的是盲区。
3. **盲区③**：App decision 释放恢复是单发 `focus()`——目标该帧仍 hidden/inert/disabled 时静默失败，无重试无留痕。

## 修复（3 commits，wt-913）

- `6272e8fcf` 切片1：新增 `desktop/frontend/src/lib/composerFocus.ts` 共享恢复器（落位校验+有界 3 帧重试+耗尽留痕+重试间隙用户点走让位）；`useLocalUiLifecycles.ts` 新增 `useRuntimeTransitionFocusRestore`（focusin/out 追踪，遮罩落下时焦点确在 composer 面才记账；settle 时焦点空闲且无决策面接管才归还，归还前落 navigation-release 正向日志）；App.tsx 两路径接线（decision-release 走共享器 + 导航释放 hook）。
- `c80631b99` 切片2：Composer.tsx 自愈放弃留痕 2 条（`disable-flip heal exhausted retries` / `cancelled by draft epoch`）；新增 `src/__tests__/composer-nav-restore.test.tsx` 21 断言（自动被 run-tests 发现，无需注册）。
- `3f4f46f3e` bundle 棘轮：largest initial JS chunk raw 实测 1190.1 超 1190.0 线 0.1 → 按低峰批/231/318/B2 先例 +10 一次到位 1200.0，独立 commit 报备派活方。

## 验证记录

- 新回归 composer-nav-restore 21/21；composer-focus-heal 12/12；navigation-surface-transition 35/35；navigation-surface-lifecycle / decision-footer-lifecycle / app-lifecycle / decision-slots-lifecycle / desktop-navigation-lifecycle / runtime-status-lifecycle / app-lifecycle-probe 全绿；composer 全族（test:composer）复跑全绿；`tsc --noEmit` 0 诊断；eslint（改动 5 文件）净；`vite build` + `check:bundle` 八项 PASS。
- **预存红申报**：composer 全族首轮连跑与 tsc --noEmit 并行时 `composer-content-menu-running` 4 断言偶发 FAIL（guidance enqueue 时序型），单跑与复跑均 24/24 全绿 → 判定 CPU 争用 flake，与本件无关。`composer-quick-command-bang` 打印 `attachEvent is not a function` TypeError 噪音为既有现象（多轮均打印、测试仍全过）。

## 复现/用户验证项

1. 复现旧症：在输入框打字中途按快捷键开新题/切会话/触发协作跳转——旧版遮罩落定后键入全落 body、必须重新点击；修复后应自动恢复输入焦点。
2. 复发排查：`grep composer-focus desktop.log`——出现 `navigation-release` / `decision-release` / `exhausted retries` 行 = 机制已触发但归还失败（带时间戳与当时的 tab 操作对链）；**完全无日志** = 走的是未覆盖盲区，带日志报 913 续查。

## 残留/风险

- 切片1 微调语义：hook 的重试 abort 只查新遮罩（不再查决策面 mid-retry）——决策面若在重试 3 帧窗口内出现，会打在隐藏输入框上静默失败并留痕，无用户可见影响（decision 释放路径在其释放时仍会归还）。
- 嫌疑面结论：893A TodoPanel 挂载 / 811 折叠 / 860 ask / 881 双入口 未见新增 focus/blur/keydown 夺焦点；根因落在导航遮罩链（证据：hidden+inert 接线 + 导航释放无归还路径）。
- `dist/.gitkeep` 曾出现 stat 假脏（内容与 HEAD 一致），`git checkout --` 即净。
- 分支按纪律保留未合并；合并由用户指定会话执行。
