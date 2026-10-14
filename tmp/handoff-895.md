# 任务 895 交接：sentinel 穿透封堵（wt-895）

- **分支**：`wt-895`（基于 `main-v2-stable` @ 1747f222d），4 个切片已提交，`git status` 净。
- **状态**：主体完成。5 条切片 commit（见下），验收项与红项清单见文末；若中断，从「未竟事项」继续。

## 切片 commit

1. `99724ad76` shellsafe wrapper 家族识别 + SeeThroughWrappers 源级透视（852 四问#4 随件）
2. `e07408627` sentinel 三面封堵 + 开关 sentinel_penetration_guard + 回归矩阵/开销实测
3. `0be89845b` 接线：config/boot/render/desktop 双快照 + App setter（即时生效）
4. `9e22cc101` 前端：safetyCostControl 卡开关条目 + 三语 i18n + task895 验收测试
5. （本文件）

## 关键事实

- 开关：`[sentinel] penetration_guard`（用户全局段，项目仓不可降；缺省 = `sentinel.PenetrationGuardDefault` = **开**，依据实测开销）。
- 判定面：`checkForbiddenBash` 前置 `shellsafe.SeeThroughWrappers`（bash/sh/zsh/dash/ksh -c/-lc 字面脚本、eval 拼接、sudo/env/裸赋值、子 shell/花括号展平，深度封顶 8）；force push 走 `gitPushSubcommandIndex` 跳全局选项（codex find_git_subcommand 对标），force/dry-run 按字段只扫 push 之后。
- 回归矩阵：`internal/sentinel/penetration_test.go`，8 组 42 例两态对照，**24 条穿透路径封堵（≥19 达标）**；矩阵同时钉死「开关关=895 前现状」。
- 开销（本机 AMD Ryzen AI 7 H 350，-count=1 三轮）：P50 off≈40.7~50.8µs → on≈51.2~53.7µs（+2.4~10.5µs）；P99 无回退（±6% 噪声）；bench ns/op 80.0→90.6µs（+13%）。
- UI：`安全/成本控制` 卡内单行追加（卡结构/成员/灯不动），`SetSentinelPenetrationGuard` 落盘即拨运行期原子（无需重启）。

## 测试与红项

- 绿：sentinel / shellsafe / shellparse / control / config（除下述）/ boot（除下述）/ agent 子集（Sentinel|Gate、ToolCall|Approval）/ desktop 子集（除下述）/ 前端 task895（42 断言）、task517（65）、task722（43）、bridge-tags、tsc --noEmit。
- **预存红①**：`config` `TestServeRenderTableCoversEveryKey`——`serve.web_dist` 在 base 上即红（HEAD 的 render.go 无 web_dist、coverage 表无豁免条目，实证于 main-v2-stable）。与本件无关。
- **预存红②**：`desktop` `TestSettingsExposesEffectiveSandboxWriteRoots`——`${HOME}/.m2` 路径展开断言，overlay 回 base 同红。与本件无关。
- **环境敏感红（重点申报）**：`boot` `TestEffectSummaryOverflowShrinksNextSummaryThroughRealBuild`（agent 压缩轨迹断言）。本机实测：本树 3 红 2 绿、base 2 绿；overlay 交叉（base forbidden+其余本件=绿；base 接线+本件 forbidden=红）双向翻转；本树 -v 模式绿。失败形态为 summary 请求尺寸序列 [35502 35502 …] 不收缩，涉及 ms 级 cache/TTL 归因（日志 attribution gap 6~12ms），895 改动面（sentinel bash 判定/接线）与该测试语义路径零耦合（全程无 bash 工具调用、零 sentinel.blocked 记录）。定性：**压缩控制流固有时序敏感 + 本机负载波动**，非 895 语义回归。建议另立项（压缩触发单飞化或该测试隔离），不在 895 扩权。

## 未竟事项

- 无阻塞项。若需复测开销：`go test ./internal/sentinel/ -run TestPenetrationGuardOverheadP50P99 -v -count=1`。
- boot 环境敏感红的复现/对照命令已写在上面「测试与红项」。
- worktree 可回收：`git worktree remove ../worktrees/wt-895`（合并由主对话指定会话执行，勿自行 merge）。
