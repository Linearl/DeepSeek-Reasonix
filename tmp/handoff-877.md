# 任务 877 交接（wt-877）——capability 报错文案升级为纠错引导

分支 `wt-877`（基线 `main-v2-stable` @ eb767b6cb）。三切片已提交，工作树净。

## 交付

- `c8d66098d` 切片1：`internal/agent/capability_error_coaching.go`（新，438 行）
  - 8 型族识别表 `capabilityErrorKind` + `classifyCapabilityInputError`（文案优先、结构兜底）：
    missing_id / nested_id / wrong_name / unshaped_id / stringified_args / multiline_json / double_envelope / spread_params，外加 undiagnosed（search 指引兜底）与 self_explanatory（decline 缺 reason、limit 越界、unknown action 等不追加）。
  - `inspectCapabilityEnvelope`：从调用 envelope 结构性提取形态事实（只取键名与 id，不回显参数值）。
  - `capabilityCorrectionFor`：已定诊错族的可复制修正例（直接给改好的参数形状，不指文档）；参数值一律 `…` 占位。
  - `closestCapabilityNames`/`capabilityNameCorrection`：真名错→最接近 catalog 真名建议（本地名全等距离 0 优先，Levenshtein 容差 len/3+1 上限 3）+ 可复制重试例 + `action=search` 占引用。候选源 = catalog 全量 id ∪ registry 工具名（`tool:` 前缀）。
- `eb286ecb2` 切片2：接入四个报错出口
  - `diagnoseCapabilityInputFailure`（capabilityInputError 面）尾部追加 `capabilityInputCorrection`；
  - `argumentValidationMessage`（schema 校验面）尾部追加 `capabilityValidationCorrection`（字符串化/多行、双包 Copyable fix、spread 打包例）；
  - `resolveRegistryTool` registry-miss（真名错主出口）→ `capabilityNameCorrection`；
  - `parseMCPCapabilityID` 裸名/畸形 id 与 `inspect` unknown id → 同一引导函数。
  - errMsg 纪律：引导走第二行，`errMsg=firstLine` 只留原错误行。
- `85deda210` 切片3：`capability_error_coaching_test.go`（新，459 行）
  - 8 型族识别表逐族一测 + self_explanatory 两族静默断言；
  - 7 族修正例内容断言（含参数值不回显 forbid 断言）；
  - **924 家族验收**：`TestWrongCapabilityNameSuggestsClosestCatalogTrueName`——初始触发 `tool:remerber` 返回 `Did you mean "tool:remember"?` + 可复制重试例 + search 占引用，下一轮照提示重发 `tool:remember` 即成功（自愈无需人工干预）；errMsg 不被引导污染；
  - 裸名（漏 `tool:` 前缀）、inspect unknown、spread 打包、双包 Copyable fix、字符串化单行、missing_id 修正例各一测；
  - `TestSuccessPathCarriesNoCoachingGuidance`：成功路径零引导噪声（验收②）。

## 验收对照

1. 924 家族初始触发 → 文案含 catalog 真名提示，单测断言下一轮自愈 ✅
2. 成功路径行为零变化（`TestSuccessPathCarriesNoCoachingGuidance` + 既有全部 use_capability 测试绿）✅
3. 8 型族识别表各至少一测 ✅
4. golden：甄别结论=use_capability 描述在捕获面（tool_schemas.json/provider_request.json），但本件不改描述/系统 prompt；`go test ./internal/boot/ -run TestGoldenBaseline` 绿、零漂移 ✅

## 测试与预存红

- `go test ./internal/agent/ -count=1`：全量通过，除两个**预存红/不稳定**（已在未改动的主仓 main-v2-stable 复现确认与本件无关）：
  - `TestInterruptedRecoveryKeepsRedactedFailureSummary`——主仓同红（`task340_failure_summary_test.go:28`，MockProvider scripted turn 缺口），预存红；
  - `TestToolCheckpointReloadRefreshesProjection`——单独跑绿，全量跑偶发红，不稳定族。
- `go build ./internal/agent/`、`go vet ./internal/agent/` 干净。

## 残留风险

- 近名建议的容差（len/3+1 上限 3）对超短名（2-3 字母）可能给出偏宽建议；当前 catalog 工具名普遍 ≥4 字母，实测无误推。
- wrong_name/unshaped_id 两族引导挂在代理出口（registry-miss/裸名/inspect），报错分类器面刻意不追加（避免双重引导）——分类表对这两族仍可识别（有单测），但 `capabilityInputCorrection` 返回空是设计行为。
- 875（readBack 默认值）与 859（readBack 首拦指引）为同族独立件，本件未越界。

## 恢复方式

```
cd github-repo/worktrees/wt-877 && git log --oneline -3   # 85deda210 / eb286ecb2 / c8d66098d
```
