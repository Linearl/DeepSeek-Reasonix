# handoff-880 — opencode go 三路由 effort 参数支持（preset 补 reasoning protocol 声明）

工作树：`github-repo/worktrees/wt-880`（分支 wt-880，基 main-v2-stable @ 1747f222d）
状态：**已完成**（两切片已提交）

## 甄别结论（交付说明④）

### 三路由定义（internal/provider/opencode_go.go）
- chat：kind=openai，`https://opencode.ai/zen/go/v1`（wire 字段 `reasoning_effort`）
- anthropic：kind=anthropic，`https://opencode.ai/zen/go`（wire：`thinking.type=adaptive` + `output_config.effort`）
- responses：kind=responses，`https://opencode.ai/zen/go/v1` stateless（wire 字段 `reasoning.effort`）

### 真实缺口（effort 全矩阵探针实测，非「端点不支持」）
- **chat 路由**：opencode-go entry 24 个模型中 **15 个无 reasoning protocol 声明**（`/effort` 报 "effort is not configurable"，即用户 2026-10-14 截图根因）：glm-5.1、deepseek-v4-flash-vision-exp（仅 recommended 有声明）、deepseek-v4.1-flash、glm-5.3-flash、hy4-preview、longcat-2.0/2.5、mimo-v2.5/2.5-pro/2.6-flash/2.6-pro、minimax-m2.7、qwen3.7-plus、qwen3.8-max、space-bunny-free——全部是 pi 目录 merge 进 catalog 的新增模型，preset 人工表未跟进。
- 根因 B：legacy `opencode-go`（compatibility tier）与 `opencode-go-recommended` 两个 preset 装出**同名 entry**（"opencode-go"），`UpsertProvider` 按名替换 → 安装顺序翻转时声明少的表覆盖声明多的表（probe 实证 glm-5.1 声明丢失）。
- **anthropic / responses 路由**：无配置缺口——anthropic 有 `e.Kind=="anthropic"` 档位兜底（auto|low|medium|high|xhigh|max），responses 有 entry 级 SupportedEfforts 声明，探针 NormalizeEffort 全通过；只需 wire 单测钉住。

### 档位权威来源
pi 目录 `models_catalog.json` 的 `thinkingLevelMap`（module `github.com/sky-valley/pi@v0.99.26`，go.mod 钉版）：值为 null=端点拒绝该档；off 映射字符串（如 hy3/hy4 →"none"）为 wire 值；tlm 整体 null（longcat/mimo/minimax/qwen3.7-plus）=无档位映射 → 通用 OpenAI 三档（与既有 kimi-k2.7-code 人工声明先例一致）。

### 覆盖判定
711=TabBar 徽章、601=GLM per-request effort（provider 层）、602=跨 provider 热替换，均未动 preset 声明；git log 无 880 提交。**未被后续批覆盖，属实做。**（871 只改 responses DetectVendor，与 effort 无关。）

## 改动（两切片）

### 切片1 a229eeeee — preset 补声明（internal/config）
- `provider_presets_opencode_go.go`：新增 `openCodeGoChatEffortDefaults()`（15 个模型的 protocol+supported_efforts+default，tlm 派生）+ `mergeOpenCodeGoChatReasoning()`（hand-tuned 表优先）+ `openCodeGoLegacyChatOverrides()`；recommended preset 复用共享表。
- `provider_presets.go`：legacy `opencode-go` preset 改用 `openCodeGoLegacyChatOverrides(...)`，与 recommended 共享声明，同名 entry 不再互相降级。
- 测试 `provider_presets_opencode_go_effort_test.go`：①三路由全模型 effort 可配矩阵（含默认档 NormalizeEffort(auto)）②tlm 档位穿透/拒绝（qwen3.8-max high 拒绝、xhigh 通过；hy4-preview none 通过 low 拒绝；deepseek-v4.1-flash disabled 拒绝）③双 preset 同名模型声明 parity。

### 切片2 — 三路由 wire 单测（internal/boot/opencode_go_effort_wire_test.go）
preset entry → ResolveModel → NormalizeEffort（模拟 /effort）→ NewProvider → httptest 捕获请求体：
- chat：`reasoning_effort`（默认档 glm-5.3=low / 显式 high / mimo medium / deepseek-v4.1-flash low+thinking.type=enabled）
- anthropic：`thinking.type=adaptive` + `output_config.effort=high`
- responses：`reasoning.effort`（preset 默认 low + 显式 medium）

## 测试与预存红
- `go test ./internal/config/`：仅 `TestServeRenderTableCoversEveryKey` 失败（`web_dist` 键）——**预存红**，基 commit 主仓同样失败。
- `go test ./internal/provider/`：`TestOpenCodeGoModelInfoUsesExactLocalCatalog`、`TestPiCatalogModelInfoForProviderRequiresExactServingRoute` 失败——**预存红**，基 commit 主仓同样失败（pi 目录元数据断言）。
- `go test ./internal/boot/`：`TestEffectSummaryOverflowShrinksNextSummaryThroughRealBuild` 首轮失败、单独重跑通过——压缩数值抖动 flaky，与本任务无关（改动只涉 config preset）。
- `go test ./internal/provider/openai|anthropic|responses/`：全绿。
- `desktop` 模块 `-run "OpenCode|Effort|Preset"`：全绿（46.9s）。
- `go build ./...`、`go vet`：干净。gofmt：干净。

## 残留风险
1. 未做真实端点探测（4xx/2xx）——沙箱无 OPENCODE_GO_API_KEY；档位表依据 pi/models.dev 静态快照（opencode 官方目录同源），若端点行为滞后于目录快照，个别档位可能被 4xx 拒绝（用户可 /effort auto 回退）。
2. tlm=null 模型（mimo/longcat/minimax/qwen3.7-plus）按通用三档声明，若端点后续支持更宽档位（xhigh 等）需再补。
3. hy4-preview/longcat-2.5-preview-free 等 preview 模型目录更新较快，preset 表需人工跟进（pi catalog 升级会自动扩 Models 列表但不带 effort 声明——矩阵测试会在 catalog 扩容时红，提示补声明）。
