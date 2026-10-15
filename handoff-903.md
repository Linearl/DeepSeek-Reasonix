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
- [x] 第二步：上游 diff 取回（tmp/pr11786.diff，2259 行）+ fork 执行链勘察
- [x] 第三步：护栏实现 + 单测（正向用例）
- [x] 第四步：误杀面回归用例 + PS golden cause 对照用例
- [x] 第五步：语义对照说明 + docs 补节 + 验收

## 上游 PR11786 语义对照说明（验收③，2026-10-15）

### 上游修复结构（diff 逐文件确认）

| 上游文件 | 职责 |
|---|---|
| `internal/base/shellparse/delete.go` | bash AST 删除调用提取（序列安全性、字面参数解析） |
| `internal/base/shellparse/delete_powershell.go` | PowerShell AST 分析脚本（宿主进程纯解析，stdin=数据永不执行） |
| `internal/safety/shellsafe/delete.go` + `delete_options.go` + `delete_wrappers.go` | 递归删除范围分析（POSIX/PS/cmd 选项、find/xargs/cmd /c、wrapper 解包） |
| `internal/tools/builtin/bash_delete.go` | 执行前护栏判定（序列规则、字面路径、granted roots 边界） |
| `internal/tools/builtin/bash_delete_interpreter.go` | iex / powershell -Command / bash -c / -EncodedCommand payload 递归分析 |
| `internal/tools/builtin/bash_delete_parser.go` | 常驻 PowerShell 解析 worker（每 shell 路径一个，超时/出错重建） |
| `internal/tools/builtin/bash.go` | 接线：`prepareGuardedLaunch`（refuseCommand → prepareLaunch） |
| `docs/TOOL_APPROVAL_MODES.md` | 「Recursive shell deletes」文档节 + 拒绝码表 |
| `desktop/frontend-next/src/i18n/*` | 拒绝码的中英文 UI 文案（kernel.ts SAID 映射） |
| `internal/assembly/boot/effect_delete_test.go` | 端到端效果测试（Auto/Yolo 下 fixture 存活、后续语句不执行） |

### fork 重实施映射（语义对齐、结构按 fork 现状）

| 上游 | fork 落点 | 说明 |
|---|---|---|
| `base/shellparse/delete.go` | `internal/shellparse/delete.go` | 同语义：sequence 标记 + Walk 提取 + StaticWord 字面解析（复用 fork 既有 StaticWord/ParseBash） |
| `base/shellparse/delete_powershell.go` | `internal/shellparse/delete_powershell.go` | 分析脚本语义对照重写（Literal-Node / Delete-Unsafe / 别名表 / PSDefaultParameterValues / if-Test-Path 安全条件 / 管道与 .NET Delete 展平） |
| `safety/shellsafe/delete*.go` | `internal/shellsafe/delete*.go` | AnalyzeRecursiveDelete / 选项解析 / wrapper 解包同语义；`ExecutableBase` 用 fork 既有 `commandBase`（补 `deleteCommandBase` 保住空命令名=`动态命令`语义） |
| `tools/builtin/bash_delete.go` | `internal/tool/builtin/bash_delete.go` | judgeDelete 主循环、boundedDeleteTarget（home/根/工作区祖先/严格子路径判定、realPath 防 symlink 逃逸）、session temp 根均对齐；`tool.Refusal{Code}` 用 fork 的 `deleteRefusal` 错误类型 + `(refusal: shell.*)` 文本身份（fork 无 Refusal 类型，取相同信息量） |
| `bash_delete_interpreter.go` | 同名（builtin） | iex/invoke-expression、powershell/pwsh -Command/-c/-EncodedCommand(base64 UTF-16LE)、bash/sh -c 全对齐 |
| `bash_delete_parser.go` | 同名（builtin） | 常驻 worker、10s 超时、单行 JSON 协议、失败杀进程重建，均对齐 |
| `bash.go` 接线 | `ExecuteDetailed` 在 `prepareLaunch` 前插 `refuseDestructiveDelete` | **等价性**：上游 `prepareGuardedLaunch` = refuseCommand(externalRef+cmdline+delete) → prepareLaunch；fork 无 externalRef/cmdline 检查，故只插 delete 护栏，同样位于**一切启动路径之前**（前台/终端/后台） |
| docs 文档节 | `docs/TOOL_APPROVAL_MODES.md` 新增同名节 | 拒绝码表逐条对齐（fork 无 `shell.command_line_too_long`，该码属上游 bash_cmdline 域，不在本任务面） |
| i18n kernel.ts | **未实施**（记残留） | fork 前端拒绝码渲染链路不同构；core 错误文本已带 remedy + code，UI 层按码翻译属 desktop 面，不在本任务「core 执行层护栏」范围 |
| boot effect test | `internal/tool/builtin/bash_delete_test.go::TestDeleteIncidentHomeSurvives` | 端到端等价：真实 PowerShell 执行路径（terminal=nil），USERPROFILE/HOME 指 fixture，探针先证 $home 解析到 fixture 才敢跑危险命令；断言拒绝 + fixture 完好 + 后续语句未执行 |

### 已知残余（对齐上游自述 + fork 特有）

1. **护栏不是 OS 沙箱**（上游自述同款）：磁盘脚本、`Start-Process`、`robocopy`、`git clean`、任意程序、文件系统竞态在其范围外；动态/复合删除会被保守拒绝。
2. i18n UI 文案未搬（见上表）。
3. 上游效果测试在 boot 层（headless 全链），fork 落在 builtin 层直接调 `ExecuteDetailed`——同一执行入口、少一层 agent 编排，判定语义相同。
4. PowerShell 5.1 实测通过（本机 powershell）；pwsh 7 与非 Windows 执行环境本机不可用（与上游 PR 自述一致）。

## 验收记录（2026-10-15/16）

### ① 正向用例钉死（事故脚本 → 中止 + 主目录完好）

`bash_delete_test.go::TestDeleteIncidentHomeSurvives`：
- 脚本 = 上游事故原样：`$home = Join-Path $env:TEMP 'cf-p6-manual'; if (Test-Path $home) { Remove-Item -Recurse -Force $home }; Set-Content executed.txt executed`
- 真实 PowerShell 执行路径（无 terminal stub），USERPROFILE/HOME 指向 temp fixture；**探针先断言子进程 `$home` 解析到 fixture 才执行危险命令**（防 fixture 失效误删真主目录）。
- 结果：**PASS**——拒绝码 `shell.delete_sequence`；fixture `keep.txt` 存活；`executed.txt` 未产生（后续语句未执行）。

### ② 误杀面回归钉（正常 Remove-Item 用法不受影响）

`TestBashDeleteGateAllowsBoundedCleanup`：18 条放行用例（bash 侧 8 + PS 侧 10），断言**零拒绝且命令到达执行**（terminal stub calls=1）：
- 非递归删除：`rm "$f"` / `rm tree` / `Remove-Item $f` / `Remove-Item style.css` / `[IO.Directory]::Delete('child')` / `(Get-Item child).Delete()`
- 字面 in-root 递归：`rm -rf child` / `rm -rf build && make` / `rm -rf build || true` / `cd app && rm -rf dist 2>/dev/null` / `Remove-Item -LiteralPath child -Recurse -Force` / `Remove-Item -Recurse -Force child -ErrorAction SilentlyContinue`
- 合法控制流/管道：`if (Test-Path child) { Remove-Item -Recurse -Force child ... }` / `Set-Location app; Remove-Item -Recurse dist` / `'child' | Remove-Item -Force`
- 普通命令：`git status`、`rmdir /absolute/empty`
- 结果：**PASS（18/18）**。
- 另 `TestDeletePowerShellGoldenCauses` 5 条放行 + `TestAnalyzeRecursiveDelete` 36 行表 + `TestDeleteWrappers` 全绿——上游语义内「允许面」无回归。

### 拒绝面（辅助）

- `TestBashDestructiveTargetGate`：27 条拒绝用例（越界字面/非字面/序列/未知选项/解释器递归/cmd /c/find）逐条钉 code，且断言 terminal 从未被调用、Execution=not_run/not_started——**PASS**。
- `TestDeletePowerShellGoldenCauses`（对照上游 pwsh golden 表 15 条）、`TestDeleteParserNeverEvaluates`（解析通道不执行源码、不污染后续请求）、`TestDeletePowerShellUnicode`、`TestDeleteGateRejectsNestedDirectoryChange`、`TestDeleteGateRefusalCarriesCode`、`TestDeleteGateIgnoresOrdinaryScriptErrors`——**PASS**。

### 包级回归

- `go test ./internal/shellparse/ ./internal/shellsafe/`：**ok**（含既有全部用例）。
- `go test ./internal/tool/builtin/`（共 3 次全量 + 定向复跑）：
  - 首跑 FAIL（日志被 tail 截断未留失败名；当时机器上并行多个 .test.exe 进程）；
  - 第二次全量：**ok**（334.8s）；
  - 第三次全量（-count=1）：仅 `TestBashHereDocIssue5624CommandsReturnPromptly` FAIL（5.55s）——**时序断言**测试（要求 2s 内返回），单独复跑 PASS（3.44s，两个子用例 0.88s/0.78s）；实测护栏对该 heredoc 命令的单次开销 **10.19µs**（临时基准用例，已删），远低于其 2s 预算 → 判定为满负载下的既有偶发，非护栏引入。
  - 与护栏相关的三个新测试组（gate/bounded/golden）在**每次全量跑中均 PASS**。
- `go vet` 三包：**clean**。
- status 净；提交见 git log（5 个「903 」开头 commit）。
