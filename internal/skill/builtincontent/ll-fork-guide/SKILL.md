---
name: ll-fork-guide
description: DeepSeek-Reasonix fork 开发指南——fork 定位与上游协作方针、八条开发铁律、合并上游规则、出包收尾三件套、构建与预算棘轮、常见坑速查。当用户在 Linearl/DeepSeek-Reasonix fork 上做开发/合并上游/出包/排障、问 fork 与上游差异、版本号规则、release notes 规范、bundle 预算时使用。
---

# ll-fork-guide（Reasonix Fork 开发指南）

> 权威全文=`github-repo/reasonix/FORK.md`（结构化纪律）+ `github-repo/reasonix/REASONIX.md`（工程规则/预存失败）；本技能=速查层，冲突时以 FORK.md 为准。

## 1. 定位与上游协作（2026-09-10 确立，**2026-10-05 用户拍板修订**）

- **fork 不废弃、长期与上游并行**：吸收合理改进（哪怕替换我们实现）/ 向上游反馈高质量 issue+PR。
- **⚠️ 不再追齐上游版本（2026-10-05 拍板）**：追齐类 merge 工作**暂停**；**bug fix cherry-pick 保留**；classic 布局保留；减法候选 R3 作废。§3 合并规则现仅适用于 cherry-pick 与必要修复，不用于整版本追齐。
- **停留 1.38.3 的原因**：上游两轮问题大爆发（1.21~1.25、1.35~1.38.12 尤其 1.38.5 转 Electron 后）；我们踩过坑，选 Wails 基线稳定窗口。
- **上游主线只维护状态**：长期可能做破坏性探索——**必须在 release notes 单独说明**（含退路与影响面），不静默改默认行为。
- **⚠️ 上游已于 2026-11-12 在 README 宣布停止维护**（退役 feedback channel/崩溃上报/telemetry，代码保留）：不再向上游提 issue/PR；动向巡检绕行=`git fetch upstream` + `git log upstream/studio`（gh api 对该仓 pulls 端点 404）。
- **推荐经典桌面风格**（classic）：作者自用、功能最全；其他风格（workbench/creation）功能可能略有缺失。

## 2. 开发判据与八条铁律

- **最高判据 = 用户当场裁决**（2026-10-02 任务 376 升格，同 [[ll-iteration-plan]] 判据 0）：用户当场裁决/显式指令优先于任何预设规则与下述铁律——直接从之，在决策记录留证即可。
- 下列八条源自 #10269 教训（2026-09-16/17），**2026-10-02 起降级为证据**（决策时参考，不与用户裁决或新判据冲突）：

1. **破坏性变更留退路**：动存储/安装布局/会话格式先问旧版能否回来；会话格式升级双写+验证再切。
2. **新能力默认实验开关**（`experimental_*` 默认关）：全链 config→setter→**渲染表**→UI（81/123 漏渲染表=保存被静默丢弃）；关态零回归可 A/B。
3. **先收敛再扩功能**。
4. **启动打包链先搜现成函数**。
5. **权限别用完美锁换效率**。
6. **保住旧 UI**（classic 布局四处：normalizeDesktopLayoutStyle/SetDesktopLayoutStyle/singleSurfaceLayout/heroLayout，每次 merge 必手工恢复）。
7. **双通道不自动追版本**。
8. **双方案并存**：上游方案作保底，我们的更优方案作实验特性。

## 3. 合并上游规则

- **小步提交+逐文件论证+保魔改+双向 tree 比对**（1.38.3 实战沉淀）。
- 搬代码前先查上游目标位置同名文件；文本比对只作线索不作判决。
- **modify/delete 冲突会静默丢文件**（fork 无该文件→取删除侧）——必须 tree 比对。
- **CSS 必核**：auto-merge 静默丢块先例（#9222 分组 194 行）。
- 合并后必跑 `node scripts/check-fork-integrity.mjs`（锚点完整性）+ `node scripts/check-architecture.mjs`（结构红线，**基线增量报告**：只报 `architecture-baseline.json` 外的新增违规，存量不刷屏；上游大改可能引入跨模块倒挂 import，新增必须清零后才能构建）。

## 4. 版本与出包

- **版本号=`1.38.3-YYYYMMDD-HHMM`**（时间戳精确到分，长期停 1.38.3 靠时间戳区分包；不自增小版本号）。
- **release notes 两种基线（2026-09-21 修订，权威=release-notes/WRITING-GUIDE.md）**：**发版级**（GitHub Release）基线=**上一个发布的版本**（非上一包，否则大特性漏写）；**过程包**（内部验证）基线=上一时间戳包。文件名与包版本同名（精确到分）。
- **出包收尾三件套**：①产物落 `desktop/build/bin/` ②**铺 staging**（`%LOCALAPPDATA%\Programs\Reasonix\staging\` 四件套：reasonix-desktop.exe/reasonix-cli.exe/reasonix-update-helper.exe/version.txt）③台账+notes。**验证**=`cat staging/version.txt`==新版本号；切换成功痕迹=`versions/<旧>.replaced-<ts>/`。
- 每次出包留档台账（`docs/reasonix/handoff/fork开发-出包台账-*.md` 持续追加）。

## 5. 构建与预算

- 构建=**`scripts/desktop-build.sh <os/arch> <版本>`**（wails 官方管线+ldflags 版本注入，约 2.5-9min）；worktree 可直接出包。**agent 内跑必须 PATH 注入 `~/go/bin`**（否则 `wails: command not found` EXIT=127，20261119 实锤）。
- **发版流程**（正式 GitHub Release）：**全文与子任务清单见仓库 `docs/RELEASE-GUIDE.zh-CN.md`（2026-11-19 实战沉淀，13 条踩坑集）**。速查：材料齐（发版级 notes+vs-upstream 台账加本版表+docs-site/changelog 双写+README）→ push（443 失败带 `-c http.proxy=http://127.0.0.1:40808`）→ 推 tag `desktop-v<版本>` → `gh workflow run "Release fork desktop" -f tag=...`（CI 多平台构建）→ 更新 discussion 公告。
- **bundle 预算棘轮**：实测超限**一次到位**（gzip **+1.0 KiB** / raw +10 KiB，2026-09-30 用户令步长翻倍取代旧 +0.5），注释带实测值+任务名；**禁挤牙膏**。
- 改 locale 必跑 `pnpm build`（check-bundle-budget 的 gzip 与 vite 报值不一致）。
- Windows 坑：pnpm.cmd 的 ENOENT、wails 的 frontend:install 路径基准=desktop/frontend、rsrc 偶发失败重跑即过。

## 6. 常见坑速查

| 症状 | 根因/处置 |
|---|---|
| 装机功能不对 | 先查产物层：同锚双向 grep（重建 dist vs 装机 exe）定打包旧快照 |
| 「重启并更新」无源可切 | staging 没铺（三件套之②） |
| 保存设置回弹 | applyConfigChange 的 rebuild 失败拖垮已保存值（374 方案 B：保存成功即 UI 成功） |
| 合并后测试红 | 先基线对照归因（预存 vs 引入），预存记 REASONIX.md `## Pre-existing failures` |
| worktree 前端炸 | 缺四件套环境（junction node_modules + wailsjs 生成物） |
| 中文乱码 | 禁 PowerShell Set-Content；用 write_file 或 python UTF-8 |

## 7. 实验室结构、三档与生效时机（2026-10-18 任务 992 重组后）

- **三档徽章（562 口径，发布门槛一部分）**：**推荐**=核心特性，不开启有体验缺口；**可选**=非核心，不开启只小幅影响；**未稳定**=开发测试中，默认关+风险提示。新增实验项落地必须同时定档；前后端两侧同步（前端 `lab/labLayoutDefault.ts` ↔ Go `internal/config/render.go` labFeatureTiers），`render_lab_tiers_test` 钉两侧一致。
- **992 重组后分组**：「工具优化」整卡（编辑读回 / multi_edit 直注 / 宽松模式 / 能力组披露）并入**基础设施组**，原独立「工具优化」组退役（`labGroup.toolOpt` 不再引用）；**运行时复用池、孤儿资源回收升推荐**；**宽松模式升可选**；**重启更新归位**回界面组「更新反馈」卡（971 迁入撤销，卡灯三键不再含 restartUpdate）。
- **生效时机三类（hint 必须与实际一致，992 全量核查）**：①即时生效（多数 UI/偏好）②重启生效（boot 快照注册与渲染表定格的开关）③边界生效（压缩边界/新会话/下次计划运行——如工具表三生效点）。
- **依赖层级**：子项依赖父开关（如决策模式提取 4 键家族）时 UI 必须展示归属（缩进/父关子项收起或置灰）——992⑦ 起为展示要求。
- **常态化方向（tl 993 调研中）**：推荐档核心特性（跨会话协作/自动驾驶等）逐步转「只允许开」，遏制实验室只进不出、开关堆积与依赖兼容成本。
- 入口：设置 → 实验室（左栏底部）；特性速览墙=fork-features.yaml（wall 无开关已生效特性 + labPicks 实验图墙文案），改 yaml+reload 即生效。

## 8. 滚合与漏滚防线（20261031 事故族沉淀）

- **回板 DONE ≠ 主线已含**：zcode 回板 DONE 只证明开发完，必须滚合后核「主线实际内容」。20261031 实测三次漏滚共 16 件（含包59 把 wt-tl1283 内容误登成 tl1273，用户实测全角！！失效才暴露）。
- **对账脚本**：`global-workspace/scripts/check_rollin_reconcile.py`（读 to-main.md 的 `## [N DONE]` → 找 wt-tlN* 分支 → 查 `git log main-v2-stable..branch` 非空即未滚合）——已接入协调线自检心跳第一步，滚合收工前必须跑到空。
- **merge message 不代表内容**（e8028c9ac 声称 1273 实为 1283）：滚合后核文件实际内容或 `git merge-base --is-ancestor <活commit> main`，不能只看 message 与 tl 状态。
- **出包快照时点决定包内容**：Build32 用工作区当时 HEAD，build 启动后滚合的 commit **不进本包**（tl1183 实测：18:12 build、18:24 滚合→二进制无该符号）。出包前先确认「要带的件都已滚合」，build 后滚的件登记「待下包」。
- **出包四件套**（20261031 用户令升级三件套→四件套）：notes + **docs-site/changelog.md 同步双写**（timeline 日组+统计行）+ push + 台账；缺 changelog 同步=没出完。
- **同族分支冲突取舍**：演进版（1266b）与原始版（1266）撞同文件时，保主线演进版实现+取分支独有资产（前端断言套件），按符号名判断新旧（`tabSessionDisconnected` vs `tabSessionEntityGone`）。

## 实验室选项参考配置

同目录 `lab-config.reference.yaml` 为**当前全部实验室开关键与徽章档位**的脱敏参考（生成自 lab-layout.yaml，随版本更新）。用户问「实验室有哪些开关/该开哪些」时：先读该文件按 tier 作答（recommended 建议开、optional 按需、unstable 默认关），再按需深入对应卡片。

## 相关 memory

fork-strategy-no-upstream-merge-20261005 / fork-开发八条铁律 / fork-追齐上游版本-merge-的规则 / fork-包版本号带构建时间戳 / release-notes-基线规则修订 / 出包收尾必含-铺-staging / 合并上游后必跑-fork-完整性核对清单 / 合并核对必须含css / Bundle-预算棘轮步长放宽 / 装机bug源码绿产物红判定法
