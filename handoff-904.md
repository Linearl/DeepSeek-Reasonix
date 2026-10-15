# 904 实施计划：上游 9597+10446（PR10617）Windows shell 拆 2 子件

- 任务：904（P1 desktop，批 33）
- 上游：issue 9597 + 10446（PR 10617 合并件）——a) prefer-bash 静默回退 PowerShell / 非标准 Git Bash 漏检；b) 前台共享 PTY 损坏非 ASCII + 120s 假超时
- 分支：wt-904（基于 main-v2-stable @ 6280772ea）
- 日期：2026-10-15
- 语义权威：`docs/report/调研报告【非诊断】/上游修复类issue同款筛查-1.38.3后-20261014.md`（第 8 行立项表）+ 上游 PR10617 diff + issue 9597/10446 正文（gh 实取）

## 上游缺陷语义（issue 正文为准）

### 9597（发现面，修复件）

三个根因（issue 作者自析）：
1. **非标准安装漏检**：Git for Windows 装 D 盘且 bash 不在 PATH → 旧版只扫四个标准根。**上游 #9519 已修**（注册表 InstallPath + PATH git.exe 推导 + Scoop/Choco），fork 已有同款（`shell_inventory.go` windowsBashCandidateSources 五级发现序）。
2. **回退静默**：桌面端 `ResolveShell(..., warn=nil)` 丢警告；CLI 提示在显式选 bash 时反而被抑制。
3. **WindowsApps WSL 别名漏排除**（PR10617 本体修的）：`%LOCALAPPDATA%\Microsoft\WindowsApps\bash.exe` 是 0 字节 App Execution Alias，WSL 装了就能过 `bash -c true` 健康探针 → 被选中后所有命令送进 Linux VM，Windows 工作区路径全失效。上游修法：`isWindowsWSLBashPath` 归一化后判 `/microsoft/windowsapps/` + `/bash.exe` 后缀。

### 10446（前台 PTY，验证先行）

上游 v1.38.9 新增「前台 bash 共享会话级 PTY」（#10327，`internal/persistentshell`）→ ConPTY 屏幕渲染改写 UTF-8 字节（提交版→□??交??）+ 宿主哨兵（REASONIX_END）检测失败 → 假超时 120s + shell 重置。上游修法：前台改管道持久进程，绕开 ConPTY 渲染。

## fork 现状（读码结论，2026-10-15）

### a) 发现面

- **根因 1 已覆盖**：`internal/sandbox/shell_inventory.go` windowsBashCandidateSources 五级发现（config → PATH git 推导 → 注册表 InstallPath HKLM/HKCU+WOW64+Uninstall → 标准根含 Scoop/Choco）；`resolveShell` 强制 prefer 找不到时有 `warnMissingShell`（boot.go:820 传 stderr，CLI 显式 prefer=bash 缺失会警告）。
- **根因 3 未覆盖（本件修复点）**：fork `shell.go:204 isWindowsWSLBash` 只查 `%SystemRoot%` 前缀，**不识别 WindowsApps 别名**。PATH 顺序让 WindowsApps 先命中时，探针通过 → WSL launcher 被当 Git Bash 选中。且 `exec.LookPath("bash")` 只返回首个匹配，被 isWSL 排除后不会续扫 PATH。
- 上游同 PR 的 `effectiveShellPreference`（Windows 强制 bash→auto 降级）删除、pwsh/powershell 跨版本路径忽略、shellInventoryKey 仅 Windows 小写——fork 前者本来就没有（fork 从不降级显式 bash，是上游中间态差异）；后两项属同 PR 邻接面，不在「非标准 Git Bash 发现面」scope，**不跟**（判据 4：新增≠要跟）。

### b) PTY 前台输出（结构不同构 → 不复现方向明确）

- fork **无 `internal/persistentshell`**；bash 工具前台 = `internal/shellrun.RunForeground`：独立子进程 + 单 pipe 合并 stdout/stderr（`cmd.Stdout=cmd.Stderr=MultiWriter`），**无任何 PTY/ConPTY 参与**。
- 唯一 ConPTY 面：`desktop/terminal_process_windows.go`——服务的是**用户主动打开的集成终端**（terminalManager UI 会话），不是模型 bash 工具路径；上游修复也明确不动用户终端。
- 哨兵：fork bash 前台链路**无 REASONIX_END 类哨兵**，完成判定 = 进程退出（pipe EOF），不存在「哨兵被污染→假超时」第二层。
- 编码解码链已有：`shellrun/codepage.go decodeConsoleOutput`（UTF-8 原样 → 系统代码页候选 → GB18030 for CP936，#11311/#11327 移植）+ progressWriter 跨读拆字符 pending 缓冲（`773d98b6d`）。
- 默认超时：fork `defaultBashTimeoutSeconds=300`（非上游 120），且是真实进程组超时。
- 结论预判：**不复现**——上游缺陷寄生在「共享会话级 PTY」这一 fork 不存在的特性上。仍按任务书走实证：读码链 + 单测构造 GBK/UTF-8 中文输出 + 真实 shell 复现命令 + 超时路径断言。

## 实施计划

### 904a（修复件）

1. `shell.go`：`isWindowsWSLBash` 重写为上游语义——抽纯函数 `isWindowsWSLBashPath(path, windowsRoot)`（归一化 `\`→`/`、剥 `//?/` 前缀、小写、Clean），SystemRoot 前缀 **或** `/microsoft/windowsapps/`+`/bash.exe` 后缀 → WSL。任意 host 可测。
2. 单测（`internal/sandbox/shell_wsl_alias_test.go` 新文件）：
   - 纯函数表测：SystemRoot/大小写/`\\?\` 前缀/WindowsApps 别名（真实 LocalAppData 形态）/真 Git Bash 不误标/空路径；
   - 非标准路径发现场景：PATH 首命中=WindowsApps 别名被排除 → 回落到非标准候选 `D:\app\Git\Git\bin\bash.exe`（issue 9597 复现路径）被发现；
   - 缺失场景 warn：prefer=bash 全缺失 → warn 含 "not found" + 回落 PowerShell（对齐上游 TestMissingWindowsGitBashFallsBackToPowerShellWithWarning 语义）。
3. 包级测试全绿 + gofmt/vet 净 → commit「904a …」（显式路径）。

### 904b（验证先行，不复现则销项不硬造）

1. 新增 `internal/shellrun/foreground_cjk_repro_904_test.go`（复现形状测试，wt-902 repro-test 先例）：
   - 上游复现命令形状 `printf '中文测试A\n中文测试B\n'; echo MARKER_END` 经真实 shell 前台跑 → 中文逐字节正确、MARKER 到达、state=completed、无超时；
   - 真实中文文件名 `ls -a`（上游裸 tty 失败用例；fork stdout 是 pipe，ls 不走 tty 多列/quoting 路径）；
   - GBK 输出构造走既有注入 seam（引用既有 TestRunForegroundDecodesSystemCodePageOutput，不重复造）；
   - 超时路径断言：中文输出后 sleep + 短超时 → state=timed_out 且已收中文输出仍完好（超时是真超时、不是哨兵假超时）。
2. 实测输出（go test -run -v 记录）+ 读码链写进本 handoff「904b 实证记录」→ 复现/不复现二选一定案。
3. commit「904b …」（测试文件 + handoff 更新）。

## 验收标准

- a) 非标准路径 Git Bash 发现/warn 单测绿：WindowsApps 别名被排除且非标准候选被发现；全缺失有明确 warn 不静默（测试实证）。
- b) 复现/不复现二选一实证报告：读码链 + 单测输出落盘，不许口头结论。

## 状态回填

- 904a：见下方「904a 实施记录」。
- 904b：见下方「904b 实证记录」。
