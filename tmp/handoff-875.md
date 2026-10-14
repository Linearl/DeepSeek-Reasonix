# 875 handoff（P2 backend，readBack 默认值放宽）

状态：**完成**（2026-10-14）。分支 wt-875，两切片，status 净。

## 裁决与甄别结论

- 宿主开关甄别：readBack 挂在 603「工具优化」族 `experimental_tool_optimizations`（`internal/tool/builtin/editfile.go` 第 19 行注释自证，boot.go:967 → workspace.go:85-86 → `BindReadBack`）。**有现成宿主开关，随该域改默认，未新开关**。
- 603 现行实现仍是「参数存在但默认关」（`p.ReadBack && e.readBack`），未覆盖本任务。
- 603 gate 豁免 / 562 三档徽章 / 877 首拦指引为同族邻件，与默认值正交，零改动。

## 实现

- `internal/tool/builtin/editfile.go`、`multiedit.go`：`ReadBack` 参数 `bool` → `*bool`；`useReadBack := family && (p.ReadBack == nil || *p.ReadBack)`。family 开启时缺省 = 读回，`readBack:false` 显式退出；family 关闭逐字节不变（铁律 2）。
- schema：`readBack` 字段加 `"default":true`，描述改写（Defaults to true... Set false only to opt out）。
- `internal/config/config.go` / `render.go`：注释同步 task 875 语义（render 的 key 行文案变更，非 key 变更；`renderServeConfig` 覆盖表不受影响）。

## 测试

- builtin 新增：`TestReadBackDefaultOnWithFamilyLit`、`TestReadBackFalseOptsOutWithFamilyLit`、`TestReadBackMultiEditDefaultOnAndOptOut`。
- agent 门级：`TestEditWithoutReadBackStillRequiresAReRead` 语义翻转 → `TestEditDefaultReadBackSatisfiesNextRoundEvidenceGate` + `TestEditReadBackFalseStillRequiresAReRead`。
- 关闭态测试零改动实证：`TestReadBackOffIgnoresTheParameter`、`TestFamilyOffEditWithReadBackParamBehavesAsBefore`。
- golden：baseline 采样默认配置（family 关），readBack 字段不入 schema，字节未变，**无需重生成**；`go test ./internal/boot -run Golden` ok。

## 预存红对照（均已在 HEAD 上复现，与本任务无关）

- `internal/config`：`TestServeRenderTableCoversEveryKey`——`web_dist` 键未入 serve 渲染表（701 遗留）。
- `internal/agent`：`TestToolCheckpointReloadRefreshesProjection`、`TestInterruptedRecoveryKeepsRedactedFailureSummary`。
- `internal/boot`：`TestEffectSummaryOverflowShrinksNextSummaryThroughRealBuild` 在 `-count=3` 下 flake（单跑过，全量包跑偶发）。

## 残留风险

- 前端 settings 提示文案（zh/zh-TW/en `settings.toolOptimizationsHint`）仍写「支持 readBack 参数」，未改写为「默认开启」——纯文案，不影响行为；如需可由前端件补。
- schema 加了 `default:true` 关键字，个别严格 schema 校验器对未知关键字敏感度低，实测 provider 请求路径未见校验拦截（boot golden 通过）。

## Commits

- `7e4f74f08` 875 readBack 默认值放宽——实现
- `fc5fb2f69` 875 两态测试
