# 任务 650 交接：乐观并行胶囊分段控件 + 实验室迁提效组（wt-650b）

- **分支**：`wt-650b`（基于 `main-v2-stable` @ f8a09feec），1 个切片已提交，`git status` 净。
- **状态**：**主体早已合入**——本任务派单时写的「wt-650 有前任 commit 续做」已过时：前任的两个 commit（852130a03 胶囊控件 + 1b39857cb 迁提效组）已于 2026-10-09 经合并令 6690279c4 进入 main-v2-stable（合并时冲突已解），wt-650 worktree 事后已清理。按派单异常预案重建 wt-650b，无 patch 可迁移——迁移对象是**残留红项**（见下）。

## 前任审读（852130a03 + 1b39857cb，已入主线）

- 852130a03：SettingsPanel 实验室-乐观并行详情由 set-check checkbox 改 `set-seg` 两段分段控件（关闭|开启），与权限档位/autopilot 开关同款 CSS 族；绑定直读 `sandbox.optimisticWrite`（右段=乐观），`disabled={busy}` 联动保留；三语新增 `settings.optimisticParallel.on/off`；hint 措辞「勾选」→「开启/关闭」。
- 1b39857cb：纯 UI 迁移，实验室条目 automation→efficiency（`experimentTiers.ts` 档位表 + `render.go` 渲染表两处注释与归属同步；配置键 `optimistic_write` 与语义零变化）。
- 合并令 6690279c4 已解 SettingsPanel.tsx / render.go 冲突；后续 2b786c2f5（561 计数钉 36→37、efficiency 7→8）与 3d8b018b3（task-280 anchor 改钉胶囊形态）已把合并后的联动面钉完。

## 本次切片（96a53bfb9，wt-650b）

**残留定位**：`settings-optimistic-parallel.test.ts` 在 HEAD 上 30/32 红 2——不是 650 本体回归，是**任务 724 布局 yaml 化**把实验室分组树从 SettingsPanel 内嵌 render table 移交 `lab-layout.yaml` + `labLayoutDefault.ts`（内置默认=安全网回退）后，该测试的两条字符串断言没随迁（561 测试当时同步迁了，本测试漏掉）：

1. 组归属断言钉 `'{ id: "optimisticParallel", group: "efficiency", '`（panel 源）——panel 里已无此表；
2. on 态断言钉 `"on: Boolean(s.sandbox?.optimisticWrite)"`（panel 源）——on 态改由 `onKeys: ["optimisticWrite", ...]` 引用 + `labLightKeys.ts` 谓词承载。

**修法（561 口径）**：组归属改双源钉——`labLayoutDefault.ts` 与 `lab-layout.yaml` 各钉「提效组有、自动化组无」（分块取 `key: efficiency`/`key: automation` 段防串段误报）；on 态改钉 onKeys 引用 + 谓词正则（`optimisticWrite: (s) => Boolean(s.sandbox?.optimisticWrite)`）。胶囊控件断言族（set-seg 绑定/直达 setter/三语 on-off 键/checkbox 退役四条负钉）与 Go 侧键回归钉**一字未动**——650 本体无回归。新增2 断言，35/35 绿。

## 验证（全在 wt-650b）

- settings-optimistic-parallel 35/35（修前 30 pass 2 FAIL）
- 实验开关族全绿：561-lab-regroup-merge 201/201、364-lab-regroup 16/16、722-lab-layout-mechanism 43/43、775-lab-param-cards 33/33
- `tsc --noEmit` 0 错；check-all-parallel 10/10；`vite build` 成功
- bundle 8/8 PASS：initial JS gzip **577.4 / 578.0 KiB** 线内、largest 335.9 / 337.0、raw 3050.9 / 3055.0（改动仅测试文件，零 bundle 影响，无需报备抬线）
- 样式同族核验（656 双宽度类坑无涉）：optimisticParallel 详情用 `set-seg`/`set-seg__btn`/`set-seg__btn--on` 三类，与 splitView/fullAccess 等同族逐字同构，styles.css 20697 段统一样式，无第二宽度类叠加。

## 未竟事项

- 无。交分支名 `wt-650b`，等合并令（不自行 merge）。
