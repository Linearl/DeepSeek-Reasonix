# Upstream Merge Checklist / 合并上游核对清单

> **每次合并 upstream/main-v2 之后、构建发布之前，必须执行本清单。**
> 背景：git auto-merge 曾三次静默丢弃 fork-only CSS 块（#9222 分组 194 行、
> #9221 颜色筛选 42 行、--fg-faint token 换名）——无冲突标记、无构建报错，
> 只有运行时裸奔。人工核对不可靠，所以第 1 步是可执行脚本。

## 1. 自动核对（必跑）

```bash
node scripts/check-fork-integrity.mjs
```

脚本覆盖 34 项 fork-only 标记（CSS 类 / TSX 符号 / Go 符号 / 构建配置）。
**exit 0 才能继续**；exit 1 时按输出恢复缺失特征（从功能 origin commit
`git log --all --oneline -- <file>` 定位），修完重跑。

新增 fork-only 功能时，往 `scripts/check-fork-integrity.mjs` 的 `CHECKS`
数组加一行（feature / file / patterns）——清单与功能同步生长。

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

- 项目分组：折叠状态重启后保持；分组计数 = 真实成员数；色板按钮弹菜单
- 会话输入框：粘贴长文本/图片 → 重启 → 草稿与粘贴块恢复
- 后台 writer 存活期间：主对话 read_file / wait 可用

## 5. 提交纪律

- 显式路径 `git add`（禁止 `-A`/`.`）
- 版本号不自增（对齐上游），release notes 同步更新
- `wails build` **不带 `-s`**（-s 跳过前端构建，dist 会停留在旧 bundle——
  2026-08-29 事故：本地包前端全部为旧代码，用户实测功能"全部失效"）
