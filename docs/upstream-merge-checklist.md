# Upstream Merge Checklist / 合并上游核对清单

> **每次合并 upstream/main-v2 之后、构建发布之前，必须执行本清单。**
> 背景：git auto-merge 曾三次静默丢弃 fork-only CSS 块（#9222 分组 194 行、
> #9221 颜色筛选 42 行、--fg-faint token 换名）——无冲突标记、无构建报错，
> 只有运行时裸奔。人工核对不可靠，所以第 1 步是可执行脚本。

## 1. 自动核对（必跑）

```bash
node scripts/check-fork-integrity.mjs
```

脚本覆盖 40 项 fork-only 标记（CSS 类 / TSX 符号 / Go 符号 / 构建配置）。
**exit 0 才能继续**；exit 1 时按输出恢复缺失特征（从功能 origin commit
`git log --all --oneline -- <file>` 定位），修完重跑。

新增 fork-only 功能时，往 `scripts/check-fork-integrity.mjs` 的 `CHECKS`
数组加一行（feature / file / patterns）——清单与功能同步生长。

### 1.1 登记纪律（2026-09-13 补充，起因：33b6c32ec 被静默冲掉一个月）

「本地快照切 tab」优化（`33b6c32ec`，2026-08-26）在 1.38.3 追齐 merge 时被
上游重构顶掉——文件在、函数在、`go build`/`tsc`/既有测试全绿，**只有函数内
逻辑回到了上游版**。静态存在性检查抓不住，一个月后才被用户感知性能回归。
由此固化三条纪律：

1. **落地即登记**：fork-only 特性合入 main-v2-stable 的**同一个会话/提交**
   内，同步往 `CHECKS` 加锚点行。事后补登记 = 空窗期裸奔。
2. **行为语义级魔改同等登记**：perf/逻辑小 diff（函数内几行）比新文件更
   容易被 auto-merge 吞——不存在冲突标记，解冲突时天然倾向取上游版。
3. **锚点选语义性字符串**：赋值/调用形态（如 `skipHistory: hasLocalItems`、
   `RecoveryChainPreviewFor(mainPath, chainPath)`、`contentSnapshotCache`），
   不选注释、不选纯符号名。函数还在但逻辑被顶掉时，只有语义锚点能报警。
4. **可上游化的优化提 PR**（上游 merged 后从 CHECKS 销账）——上游吸收是
   最强的防冲掉：fork 不再独自维护该代码。

## 2. Token 契约（必跑）

```bash
node desktop/frontend/scripts/check-theme-token-contract.mjs
```

上游可能退役主题 token（如 1.33 退役 `--fg-muted`）。恢复旧 CSS 或合并
上游样式后，此脚本拦截已退役 token；迁移参照现役用法（如 `--fg-faint`）。

## 3. 前端构建与测试

```bash
cd desktop/frontend
npx tsc --noEmit
npx vitest run src/__tests__/   # 或按改动范围挑选
```

## 4. 手工冒烟（构建后）

- **项目分组**：头部「新建分组」存在且能建组；组标题可折叠且状态重启后保持；右键「移动到分组」可用；组标题字体 / 颜色与会话组一致
- **项目颜色筛选**：色板按钮弹菜单；可**多选**
- **列表与计数**：分组计数 = 真实成员数（不随 classic 预览 / 分页缩水）
- **长对话来回切 tab**（≥2 个非活跃长会话）：切回应**即时**（不重走
  history 加载）——33b6c32ec 本地快照优化的行为级验收（2026-09-13 被冲掉
  一个月才发现，此场景即其冒烟）
- **副本预览**：「工作阶段版本与副本」扫描 → 候选链区出现；任一链预览弹窗
  数字与行内一致；「查看尾部消息/加载更多」点击即生效
- 会话输入框：粘贴长文本/图片 → 重启 → 草稿与粘贴块恢复
- 后台 writer 存活期间：主对话 read_file / wait 可用

## 5. 提交纪律

- 显式路径 `git add`（禁止 `-A`/`.`）
- 版本号不自增（对齐上游），release notes 同步更新
- `wails build` **不带 `-s`**（-s 跳过前端构建，dist 会停留在旧 bundle——
  2026-08-29 事故：本地包前端全部为旧代码，用户实测功能"全部失效"）
