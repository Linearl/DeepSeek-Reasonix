# handoff-681 —— zcode 技能迁移三件套（dogfood / feature-boundary-planner / react-best-practices）内置化 wt-681

- 日期：2026-10-15
- worktree：`github-repo/worktrees/wt-681`（分支 `wt-681`，基 `main-v2-stable` @ `f8a09feec`）
- 任务源：`tl show 681`（2026-10-09 立项，调研2 移交；用户拍板不入版本19、排20批）
- 参考源（只读）：`github-repo/zcode/.agents/skills/{dogfood,feature-boundary-planner,react-best-practices}` + 插件技能 `zcode/apps/zcode-cli/packages/browser-use-plugin/skills/web-gui-tester`
- 纪律：切片 commit、显式路径 add、禁 add -A/commit -a、禁 stash/merge/rebase、status 净；中文 message「681 」开头；文件写入走 Write/Edit

## 落点（已核实，与现有 12 个内置技能同形态）

1. `internal/skill/builtincontent/<skill-name>/`（SKILL.md + 子目录辅助文件）；
2. `embed.go`：`//go:embed <skill-dir>` 整目录注册（有辅助文件的技能走整目录形态，同 ll-iteration-dev）；`All()` 只装载 SKILL.md，辅助文件仅随 InstallToUserDir 物化；
3. `install.go` `ShippedPlaybookNames`：追加三名（物化到用户技能目录，与 deep-research 同级）；
4. `shipped_playbooks_test.go`：期望清单追加三名（注册即验收）。

## 移植决策（含取舍，供事后追认）

| # | 决策 | 依据 |
|---|---|---|
| 1 | frontmatter 统一归一为 fork 形态 `name/description/runas: inline`；剥离 zcode 专有键（`allowed-tools`、`disable-model-invocation`、`license`、`metadata`） | fork frontmatter 为宽容解析（序列逗号拼接、映射拍平），残留键虽不炸但属死配置；dogfood 去掉 disable-model-invocation 后变为模型可自主调用（描述限定触发词，风险可控） |
| 2 | react-best-practices：SKILL.md + rules/ 全套（70 规则 + _sections/_template）纯搬；**不搬 AGENTS.md**（112KB 合编文档 = rules 拼接），删除 SKILL.md 中指向它的段落（防死引用）；SKILL.md 加 fork 语境注记（桌面前端=Vite+React+wails，server-\*/async-api-routes 类规则仅 Next.js 服务端场景适用） | 任务书「rules/ 全套纯规范直接搬、零依赖」；AGENTS.md 是冗余拼接物 |
| 3 | feature-boundary-planner：三模板可搬（impact-brief / case-planning 微调引用方式、source-discovery 按 fork 结构重写）；`zcode-feature-graph.yaml` **重写**为 `reasonix-feature-graph.yaml`（ui-surface/state-owner/protocol/persistence/validation 五维，种子路径全部实测存在）；`pnpm dep:refs` → `lsp_references` + `git grep` 兜底；`rg` → `grep`/`git grep`；zcode graph 外部契约文档指针（docs/skills/feature-boundary-graph.md）删除、契约内联；agents/openai.yaml 不搬 | 任务书明示 |
| 4 | dogfood：agent-browser CLI（约 30 处）全量改写为 **Reasonix playwright 路径**（bash 工具跑 Node Playwright 脚本；会话=持久化 context profile；截图落盘 + `view_image` 内置工具核验；录屏=context recordVideo）；吸收 web-gui-tester 黑盒方法论（纯 GUI 黑箱五原则、行动→观察环、瞬态前后同脚本取证、P0-P3 计划、环境准备段隔离、console 只读采集）；与既有体系衔接（预览级：desktop/frontend bench/ + 操作卡；装机级：CDP 实验室开关 + gui-self-verify 链；报告收尾套「装机验证四层法」口径） | 任务书明示；playwright ^1.63.0 已是 desktop/frontend 依赖，bench/*.mjs 为既有同款路径 |
| 5 | 版权出处注释保留（vercel-labs/agent-browser Apache-2.0、Vercel react MIT），但删除「See THIRD-PARTY-NOTICES.md」句（fork 无该文件，属死引用） | 死引用清零是验收项 |
| 6 | dogfood 测 Reasonix 桌面自身时补充 `ui_interact`/`screenshot` 内置工具路径（实验开关门控，task 233） | fork 已有桌面窗口驱动工具，与 CDP 路径互补 |

## 验收（证伪条件）

1. `go build ./...` 过（embed 完整性）；
2. `go test ./internal/skill/builtincontent/` 绿（shipped_playbooks 期望清单含三名）；
3. 三技能目录 grep 清零：`agent-browser`、`mcp__node_repl`、`ZCODE_PLUGIN_ROOT`、`browser-client.mjs`、`pnpm dep:refs`、`zcode-cli/packages`、`packages/ui/src`、`apps/zcode`（出处注释中的仓库名溯源除外）；
4. frontmatter 与既有 12 个同形态（name/description/runas）；
5. 种子图 codeSeeds 文件路径抽验存在。

## 切片

1. handoff 落盘（本文件）
2. react-best-practices（SKILL.md+rules 全套）
3. feature-boundary-planner（SKILL.md+四 references 重写/搬运）
4. dogfood（SKILL.md 改写+taxonomy/report 模板搬运）
5. embed.go + install.go + shipped_playbooks_test.go 注册三技能 + 构建/测试/死引用 grep 验收

## Commits

| commit | 内容 |
|---|---|
| （回填） | handoff 计划落盘 |
