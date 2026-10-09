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
- 合并后必跑 `node scripts/check-fork-integrity.mjs`（锚点完整性）。

## 4. 版本与出包

- **版本号=`1.38.3-YYYYMMDD-HHMM`**（时间戳精确到分，长期停 1.38.3 靠时间戳区分包；不自增小版本号）。
- **release notes 增量基线=上一时间戳包**（只写相对上一包新增，禁堆叠历史）；文件名与包版本同名。
- **出包收尾三件套**：①产物落 `desktop/build/bin/` ②**铺 staging**（`%LOCALAPPDATA%\Programs\Reasonix\staging\` 四件套：reasonix-desktop.exe/reasonix-cli.exe/reasonix-update-helper.exe/version.txt）③台账+notes。**验证**=`cat staging/version.txt`==新版本号；切换成功痕迹=`versions/<旧>.replaced-<ts>/`。
- 每次出包留档台账（`docs/handoff/fork开发-出包台账-*.md` 持续追加）。

## 5. 构建与预算

- 构建=`bash scripts/build-local-installer.sh`（约 2.5-3.5min）；worktree 可直接出包（脚本以自身位置为基准）。
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

## 相关 memory

fork-strategy-no-upstream-merge-20261005 / fork-开发八条铁律 / fork-追齐上游版本-merge-的规则 / fork-包版本号带构建时间戳 / release-notes-基线规则修订 / 出包收尾必含-铺-staging / 合并上游后必跑-fork-完整性核对清单 / 合并核对必须含css / Bundle-预算棘轮步长放宽 / 装机bug源码绿产物红判定法
