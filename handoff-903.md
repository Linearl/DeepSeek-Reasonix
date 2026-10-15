# 903 实施计划：PS `$home` 赋值失败后继续执行递归删主目录（data-loss 级，core 执行层护栏）

- 任务：903（P1 backend，批 33 data-loss 级）
- 上游：issue 11782 / PR 11786【重实施，非 cherry-pick】
- 分支：wt-903（基于 main-v2-stable @ 6280772ea）
- 日期：2026-10-15
- 语义权威：`docs/report/调研报告【非诊断】/上游修复类issue同款筛查-1.38.3后-20261014.md` + 上游 PR11786 diff

## 缺陷描述（上游）

agent 生成「隔离 HOME 复跑」命令：第一句 `$home = Join-Path $env:TEMP "..."` 对只读自动变量赋值
抛非终止 WriteError，`;` 语句序列**继续执行**；随后 `if (Test-Path $home)` 测得真实主目录，
`Remove-Item -Recurse -Force $home` 递归强删 `C:\Users\<user>`（实际事故：~/.config、~/.reasonix、
~/sessions 被静默清空，285 条逐项删除错误）。data-loss 级。

## 上游 PR11786 修法（语义摘要，diff 为准）

- 主体：**pre-launch 护栏**——递归删除目标不是「granted roots 内的字面路径」时，命令在启动前被拒
  （即使 Auto/YOLO 已批准）；Bash 与 PowerShell 各做解析（PS 用 AST），拒绝时保留 typed refusal 身份给模型。
- 落点：`internal/base/shellparse/delete*.go`（解析）、`internal/safety/shellsafe/delete*.go`（判定）、
  `internal/tools/builtin/bash_delete*.go`（接线）、`boot/effect_delete_test.go`（效果测试：fixture 存活 +
  未授权模式不执行）、i18n 文案、`docs/TOOL_APPROVAL_MODES.md`。
- 明确不做：不加全局 `$ErrorActionPreference=Stop`（5.1 探针证明会破坏重定向原生 stderr 的兼容性）。
- 已知保守面：动态/复合删除会被保守拒绝；PS native 解析有有界启动开销。非 OS 级隔离（脚本/别名/
  间接执行/竞态仍是 gap）——PR 自己声明「structural pre-launch guard」。

## fork 现状（结构不同构 → 重实施）

- fork 无 `internal/base/shellparse/`、无 `internal/tools/`（fork 是 `internal/tool/`）。
- fork `internal/shellsafe/` 存在（对应上游 `internal/safety/shellsafe/` 域），但无 delete 判定面。
- fork `internal/tool/builtin/bash.go:120`：PowerShell 描述里有 `Remove-Item -Recurse -Force (rm -rf)`
  提示——**只是用法提示，无任何执行护栏**。
- 改动面（任务书指定）：**core 执行层护栏**，即 bash builtin 执行前的检查路径；不搬上游文件。

## 实施计划

1. **勘察**：读 fork bash builtin 执行链（bash.go → shellsafe → sandbox 执行），确定 pre-launch 检查
   插入点与「granted roots」数据来源（workspace + additional_write_dirs）。
2. **取上游 diff**：`gh pr diff 11786 --repo esengine/DeepSeek-Reasonix` 存 tmp，逐文件语义对照。
3. **重实施**（按上游语义、fork 结构）：
   - 删除命令提取：识别 Bash `rm -rf` 家族与 PS `Remove-Item -Recurse/-r [-Force]` 家族（含 `;` 链、
     `if (...) {}` 块内的语句——事故命令正落在 if 块里）；
   - 目标判定：递归删除目标必须是**字面路径**且**严格位于 granted roots 内**；变量目标（`$home`、
     `$env:USERPROFILE`、`$HOME` 等）与不可静态判定的目标 → 拒绝执行（pre-launch，返回 typed refusal）；
   - 误杀面控制：正常 `Remove-Item`（非递归）、递归删 workspace 内字面路径、broad glob 之外的合法
     用法不得被拦（验收②）；
   - 接线点按 fork 机制，不引入上游文件布局。
4. **测试**：
   - 正向用例（验收①）：构造「$home 赋值失败 + `Remove-Item -Recurse -Force $home`」脚本 → 执行被
     中止（未启动子进程/未发出删除）且主目录 fixture 完好；
   - 误杀面回归（验收②）：正常 Remove-Item 用法全列表不被拦；
   - 对照上游 test 语义补分支用例。
5. **handoff 补上游 PR11786 语义对照说明**（验收③）。
6. 包级测试全绿、status 净、显式路径提交、中文 message「903 」开头。

## 验收标准

- ① 正向用例钉死：`$home` 赋值失败 + `Remove-Item -Recurse -Force $home` → 执行中止、主目录完好（有测试实证）。
- ② 误杀面回归钉：正常 Remove-Item 用法（非递归删除、递归删 roots 内字面路径等）不受影响（有测试实证）。
- ③ 上游 PR11786 语义对照说明进 handoff。
- status 净、显式路径提交、message「903 」开头、禁 stash/merge/rebase、不写主仓。

## 记录（每步即 commit）

- [x] 第一步：worktree wt-903 创建，本计划落盘
- [ ] 第二步：上游 diff 取回 + fork 执行链勘察（插入点定案）
- [ ] 第三步：护栏实现 + 单测（正向用例）
- [ ] 第四步：误杀面回归用例
- [ ] 第五步：语义对照说明 + 验收 + 汇报
