# 915 handoff（P2 小件：multi_edit 注入面暴露——工具优化族键 + extras 通道）

状态：**进行中**（2026-10-15 二派；前任两代均死于长推理/重启零残盘，本件每步即 commit）。
分支 wt-915（基 6280772ea = main-v2-stable）。任务书全文 = `tl show 915`（已读，四拍板并入正文）。

## 方案定稿（读码后落定，与任务书一致）

1. **boot 接线**（`internal/boot/boot.go:2993` `providerSurfaceExtras`）：
   族键 `cfg.Agent.ExperimentalToolOptimizations == true` 时 extras 追加 `"multi_edit"`；
   restart_update 分支保留（两开关可叠加，extras 变两元素）。`applyUnifiedProviderToolSurface`
   现有 `reg.Get` 检查天然生效（multi_edit 恒注册 `internal/tool/builtin/multiedit.go:12`）。
2. **面板默认开（本件设计核心）**：config 字段 **`bool` → `*bool`**（`internal/config/config.go:1742`）——
   - nil（键未写入，新装/重置）：`ToolOptimizationsEnabled()` = false → boot 行为=现状（golden/forbidden 零波及）；
     **render 条件渲染**（nil 不落盘，722 子键同款「防跟随者被转显式关」反向：显式值恒渲染、缺键永不被物化成 false）；
   - `&true` / `&false`：显式值，渲染恒落盘（81/123 lost-save 规则保住）。
   - **SettingsView 字段转 `*bool`**（settings_app.go 两视图 + reasoning_display_app.go）：nil 透传到前端 JSON；
     前端 `s.experimentalToolOptimizations ?? true` 呈开态 + 首载 `== null` 时 `SetExperimentalToolOptimizations(true)` 落盘
     （write-on-show，670 AcknowledgeForkNotice 同款 best-effort；失败不打断、下次打开重试）；
     卡片灯 labLightKeys 谓词同 `?? true`。
   - **弃用方案**：view 层 tomlFileDefinesKey 存在性探测——render 首次任意保存会把缺键物化成 false，
     面板默认开在「用户改任何别的设置」后即失效，脆；`*bool` 根治。
   - 存量语义：老配置里被 render 物化过的 `= false`（非用户本意）→ 视为显式关、面板呈关——495 口径现状等价，不动。
3. **UI**：toolOptimizations 卡不新增；hint 三语补第二效果（族开 multi_edit 直注：模型获得完整 schema、
   edit_file「prefer multi_edit」引导闭合；关态仍可经 use_capability 调用）+ 生效时机（boot 快照，改开关需重建 runtime；
   新装面板默认开并自动写入）。
4. **红线**：不动 `CoreProviderToolNames`（agent_preset.go:58）；不动 edit_file prefer 尾句（editfile.go:37）；
   执行面恒可用（registry 全量注册不变）。

## 切片（每片即 commit，中文 message「915 」开头，显式路径）

1. handoff-915.md（本文件）落盘。
2. Go boot：providerSurfaceExtras + 开态全链测试（真实 Build 捕获 tools 含 multi_edit + schema 带 readBack）+
   use_capability 显式关态恒可用钉 + extras 单测。
3. Go config/desktop：字段 *bool + ToolOptimizationsEnabled/render/edit/两视图 + render_lab_tiers_test 夹具适配 + desktop 视图三态测试（缺键=nil/显式关/显式开）。
4. 前端：卡片 ?? true + 首载写入 effect + labLightKeys 谓词 + hint 三语 + task915 锚测试；pnpm build（bundle 棘轮 locale +1.0KiB 预期）+ dist 自证。
5. 验收七条自检 + handoff 终态回写。

## 预存红基线（875 handoff 口径，复跑确认后申报）

- internal/config `TestServeRenderTableCoversEveryKey`（web_dist 遗留）；
- internal/agent 两测 + boot 压缩轨迹 flake（-count=3）。

## 验收对照（七条）

- [ ] 开态：Build 捕获 tools 含 multi_edit + multi_edit schema 含 readBack；
- [ ] 关态/Go 默认（nil）：forbidden 断言/boot_test.go:2373、golden、unifiedBootToolNames 原样绿；
- [ ] 面板默认开：缺键视图 nil→前端呈开+写入 true；写入后重建 runtime 含 multi_edit；
- [ ] config render 表防线绿（族键已渲染、无新键）；
- [ ] hint 三语 + pnpm build 过 bundle 预算 + dist 自证；
- [ ] 执行面恒可用钉：显式关态 use_capability tool:multi_edit 照常执行；
- [ ] 红线三条复核（CoreProviderToolNames/prefer 尾句/registry 全量）。
