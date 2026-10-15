# 915 handoff（P2 小件：multi_edit 注入面暴露——工具优化族键 + extras 通道）

状态：**完成**（2026-10-16；wt-915 七 commit，基 6280772ea = main-v2-stable，status 净）。
任务书 = `tl show 915`（四拍板：Extras 暴露/工具优化族/默认开启/默认值放实验室面板）。

## Commits（每片即 commit，顺序执行）

1. `44255510d` handoff 计划落盘（方案定稿：config bool→*bool 根治面板默认开，弃 view 层存在性探测——render 任意保存会把缺键物化成 false，方案脆）
2. `6ee6316c4` Go boot：providerSurfaceExtras 族开追加 multi_edit（extras 通道，restart_update 可叠加）+ 四测试（extras 两态/registry 可见性两态/真实 Build 捕获 tools 含 multi_edit 且 schema 带 readBack/显式关态 use_capability tool:multi_edit 端到端执行钉）
3. `855b12fe4` Go config/desktop：族键 bool→*bool（缺键 nil=boot 现状等价+面板呈开；render 条件渲染防缺键被物化成显式关——722 子键同款；setter 恒写非 nil）+ 两视图 *bool 透传 + desktop 三态视图测试/默认写入物化测试/条件渲染测试
4. `cc17bef91` 前端：族卡 ?? true 呈开 + 首载缺键写入 true（write-on-show best-effort，670 同款）+ 卡灯谓词同源 + hint 三语补第二效果+生效时机+新装默认开说明 + 类型 nullable
5. `1bdd9f76e` 锚测试 task915-multi-edit-injection（26 断言全绿）
6. `3f1dad058` 棘轮：locale gzip 双 +1.0（zh 余 0.24<1.0 抖动区 → 106.2；zh-TW 繁体同批必炸先例预抬 → 108.4）
7. `2195d1483` boot 测试适配 *bool 补漏文件

## 实现要点

- boot：`providerSurfaceExtras`（boot.go:2996）`ToolOptimizationsEnabled()` 时追加 `"multi_edit"`；`addBuiltins`
  readBack 参数同源走该 helper。`applyUnifiedProviderToolSurface` 的 `reg.Get` 检查天然生效（multi_edit 恒注册）。
- config：`ExperimentalToolOptimizations *bool`——nil（缺键）boot 读 off=现状等价（golden/forbidden 零波及）、
  render 不落盘（缺键永不被无关保存物化成显式关）、面板呈开；`&false`/`&true` 显式值恒渲染（81/123 lost-save）。
- 面板默认开链路：缺键 → 两视图 *bool=nil → JSON null → 前端 `?? true` 呈开 + 首载 effect 写入 true →
  下次重建 runtime 即含 multi_edit（验收第 3 条）。写入失败 best-effort 重试（下次打开设置）。
- 存量语义：老配置里被旧 render 物化过的 `= false` 视为显式关（495 现状等价口径，不动）。

## 验收七条（全过）

- [x] **开态**：`TestBootRequestCarriesMultiEditWhenFamilyLit`——真实 Build 捕获 tools 含 multi_edit + schema 带 readBack（875 语义进注入面）+ restart_update 不越界；
- [x] **关态/Go 默认（nil）**：golden/forbidden（boot_test.go:2373）/lean surface/use_capability matrix 全套原样绿（`go test ./internal/boot -run "...Golden|..."` 281s ok）；
- [x] **面板默认开**：`TestSettingsViewToolOptimizationsThreeStates`（缺键=nil/显式开/显式关三态，两视图一致）+ `TestToolOptimizationsDefaultWriteMaterializesTheKey`（写入物化+无关保存不丢）+ 前端锚断言；
- [x] **config render 表防线**：`TestRenderTOMLRoundTripsLabSwitches`/`TestLabRenderKeysAllTaggedWithTier`/`TestLabFeatureTierCountsMatchTableA` 绿（族键已渲染、无新键）；
- [x] **hint 三语 + build**：三语第二效果+生效时机+默认开说明；`pnpm build` 8 项 bundle 预算全 PASS（zh 104.8/106.2、zh-TW 106.5/108.4）+ dist 产物 grep 自证（zh/zh-TW/i18n chunk 均含新文案）；
- [x] **执行面恒可用钉**：`TestUseCapabilityDispatchesMultiEditWhileFamilyExplicitlyOff`——显式关态 use_capability tool:multi_edit 端到端 patch 成功且 tools 数组不含 multi_edit；
- [x] **红线三条**：CoreProviderToolNames 未动（diff 空）、edit_file prefer 尾句未动（diff 空）、registry 全量注册未变（multiedit.go:12）。

## 预存红申报（均与本件无关，基线对照实证）

- `internal/config TestServeRenderTableCoversEveryKey`——`web_dist` 键未入 serve 渲染表（701 遗留，875 handoff 同款申报）；wt-915 与干净基线同挂。
- `desktop` 三布局 `Test*QuickClicksSerializeWorkspaceRebuild`——环境敏感间歇 flake（`begin_tab_turn` 慢 19s+ 撞 `waitNotRunning` 5s 界）：
  wt-915 4 跑 3 过 1 挂，**干净基线主仓同 4 跑 3 过 1 挂**，归属=负载敏感非本件引入。
- `internal/boot` 全量套件在并发负载下 10m 默认超时（`TestBuildHonorsSessionDirOverride` 单跑 9.6s 过）——环境时长问题非断言失败。

## 残留

- 无源码残留。合并令由主对话批发；出包不在本件范围。
- 装机后人工验证建议：新装/删 `experimental_tool_optimizations` 键 → 打开设置→实验室→工具优化卡呈开、config.toml 出现 `= true`、重启后会话 tools 含 multi_edit。
