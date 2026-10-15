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

## 904a 实施记录（2026-10-15）

**修复**：`internal/sandbox/shell.go` —— `isWindowsWSLBash` 重写为上游 PR10617 语义：
- 抽纯函数 `isWindowsWSLBashPath(path, windowsRoot)`：`\`→`/`、剥 `\\?\` 前缀、小写、pathpkg.Clean 归一化后比对；
- 判定面从「%SystemRoot% 前缀」扩为「%SystemRoot% 前缀 **或** `/microsoft/windowsapps/`+`/bash.exe` 后缀」——WindowsApps 0 字节 App Execution Alias（WSL 装了就能过 `bash -c true` 探针）从此不再被当 Git Bash 选中；
- 环境读取（SystemRoot/windir、GOOS 门）留在 `isWindowsWSLBash` 壳里，纯函数任意 host 可测。

**为什么只修这一点**：issue 9597 三个根因中，根因 1（非标准安装漏检）上游 #9519 修法 fork 已有同款五级发现序（config→PATH git 推导→注册表 InstallPath→标准根+Scoop/Choco）；根因 2（回退静默）fork `warnMissingShell` 已在（boot.go:820 传 stderr）；根因 3（WindowsApps 别名）fork 缺——即本件。上游同 PR 的 pwsh/powershell 跨版本路径忽略、shellInventoryKey 大小写改造属邻接面，不在「非标准 Git Bash 发现面」scope，按「上游动作不自动跟」不引入。

**新增测试** `internal/sandbox/shell_wsl_alias_test.go`（2 组）：
- `TestIsWindowsWSLBashPath` 13 表项：System32 启动器/大小写/正斜杠/`\\?\` 前缀/WindowsApps 别名三形态→true；非标准 Git Bash/标准 Git Bash/usr\bin/别名非 bash.exe/Windows 根同前缀目录/空→false；无 Windows root 时别名仍排除。
- `TestWindowsAppsAliasExcludedFromNonStandardDiscovery`：复现 9597 机器形状（PATH 首命中=别名且探针通过 + 非标准 `D:\app\Git\Git\bin\bash.exe` 不在 PATH）→ 解析落到非标准 Git Bash 而非别名；候选构造器同排除；全缺失 → PowerShell 回落 + warn 点名 "not found"/"bash"（不静默）。

**验收**：单测绿（上述 2 组 + shell 域 24 项全过：ResolveShell 决策表/候选排序/能力报告/配置路径安全边界/WSL 排除回归等）；gofmt 0、go vet 净。
**预存红申报**：包内 `TestNormalizeWriteDirResolvesSymlinkParent`、`TestResolveCanonicalPathLocalBehaviorRegression`、`TestEnsureWriteDirRejectsApprovalIdentityChange` 三条在**干净 main-v2-stable 主仓同样红**（symlink/规范路径环境相关，与 shell 域无关，非本件引入）。

## 904b 实证记录（2026-10-15）：**不复现 → 销项**

### 定案：上游 10446 缺陷在 fork **结构性不存在**，不复现，不实施修复

### 读码链（复现所需两层在 fork 均不存在）

| 上游 10446 要素 | fork 对应 | 结论 |
|---|---|---|
| ① 传输层：前台 bash 共享**会话级 PTY**（#10327 新特性，`internal/persistentshell`）| fork **无 persistentshell 包**；bash 工具前台 = `internal/shellrun.RunForeground`：`proc.CommandContext` 独立子进程 + `cmd.Stdout=cmd.Stderr=同一 MultiWriter`（runner.go:118-126 注释钉死单 pipe 保序），**每调用一进程，无任何 PTY/ConPTY** | 寄生缺陷无宿主 |
| ② 哨兵层：宿主依赖 `REASONIX_END_<hash>` 唯一结束标记判定完成 | fork 前台链路**无 REASONIX_END 类哨兵**（全仓 grep 无此机制）；完成判定 = 子进程退出 + pipe EOF（shellrun timeout/cancel/exit 分类）| 污染哨兵→假超时的第二层不存在 |
| ConPTY 使用面 | 仅 `desktop/terminal_process_windows.go`——服务**用户主动打开的集成终端**（terminalManager UI 会话，用户看得见的真终端），不参与模型 bash 工具路径；上游修复也声明不动用户终端 | 不在缺陷面 |
| 非 ASCII 解码 | `shellrun/codepage.go decodeConsoleOutput`：UTF-8 原样放行 → 系统代码页候选逐一试解（含 U+FFFD 双重拒收）→ CP936/54936 时 GB18030 兜底；`progressWriter` pending 缓冲跨 pipe 读拆字符（上游 #11311→#11327 移植，`773d98b6d`）| 已有修复面 |
| 超时语义 | `context.WithTimeoutCause` 真实进程组 deadline（fork 默认 300s 非上游 120s，`config.go:2514`）| 真超时，非哨兵假超时 |

### 实证（新增 `internal/shellrun/foreground_cjk_repro_904_test.go`，5 测试全绿）

复现矩阵原形状 + 超时/解码断言，真实 Git Bash 前台执行（`go test ./internal/shellrun/ -run 'TestForegroundCJK|TestForegroundGBK|TestNoSharedPTY' -v`，2026-10-15 实测 6.8s 全 PASS）：

1. `TestForegroundCJKOutputBytePerfectNoFakeTimeout`——上游复现命令 A 原形状 `printf '中文测试A\n中文测试B\n'; echo MARKER_END`：state=completed、三段逐字在、无 U+FFFD、耗时 0.92s 远低于超时。**不复现乱码、不复现假超时**。
2. `TestForegroundCJKQuotedLsNoFakeTimeout`——上游唯一失败行「前台裸 `ls -a` + 中文文件名」（三个真实 fixture）：文件名逐字在、无 `$'` tty quoting 踪迹（pipe stdout 下 ls 不进 tty 格式）、completed。**不复现**。
3. `TestForegroundCJKTimeoutKeepsDecodedOutput`——中文输出后 sleep 10 + 500ms 超时：state=timed_out/timeout（真 deadline），且超时前已收中文输出完好无替换符。证超时层是真进程组超时、与输出编码解耦。**不复现「超时吞输出/污染输出」**。
4. `TestForegroundGBKConsoleOutputRealShell`——真实子进程 printf 发 GBK 原始字节（invalid UTF-8 上 pipe，即 CP936 控制台程序形状）→ 解码链还原「中文GBK通道验证完成」。**不复现乱码**。
5. `TestNoSharedPTYInForegroundPath`——结构守卫：连续两次前台运行持有**不同**子进程/Tracked 句柄且各自独立 completed（共享会话 PTY 若回归，此测试红，倒逼重做本分析）。

对照上游矩阵：上游「前台裸 ls=乱码+假超时」行在 fork 对应形状为全绿 → **不复现二选一取「不复现」，销项**。上游修复主体（persistentshell 管道化）在 fork 无对应结构，按「验证先行类：不复现则销项不硬造」不实施。

### 真实 UI 无法测的部分（标注）

- 桌面**集成终端面板**（用户开的 ConPTY 真终端，`desktop/terminal_process_*`）在无头环境只能跑已有 `TestWindowsTerminalProcessConPTYSmoke`（ASCII 冒烟）；中文渲染依赖前端 xterm 渲染与用户交互，**无法无头取证**——但该终端是用户主动打开的可见 UI，不是上游 10446 的「模型前台 bash 调用」，且上游修复明确不改用户终端，故不构成销项风险。
- ACP 客户端终端（`clientio.RunCommand` 走 terminal/create 协议）输出由客户端终端渲染，宿主只回传字节，同理不在 fork 自有解码链内；需真实 ACP 客户端（如 Zed）联调，超出无头范围。
