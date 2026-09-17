# Reasonix Fork 桌面版 v1.38.3-20260917-1655 — Release Notes

> **构建**：2026-09-17 16:3x ｜ **安装目录**：`versions/v1.38.3-20260917-1655/`
> **增量基线**：上一包 **`v1.38.3-20260917-1555`**。本文件**只列相对 1555 的新增/变更**，
> 不堆叠更早包的内容——看更早的历史请沿 `FORK-v1.38.3-20260917-1555.md` 链式回溯。
> 包版本号长期停在 `v1.38.3`（与上游对齐），时间戳只用于区分同版本号的多次构建。

## 本版新增：内置技能 collect_issues（意见箱 → tasklist）

- 四步内置 playbook：**读取**意见箱未处理的 `feedback-*.md` → **分析**（新缺口 vs 已修待实测、归属域、严重度）→ **创建任务**（`next-task-id.py` 取号、按主题聚合成一个任务带字母子项、内层 git 提交）→ **重命名**原文件加 `-已转入tasklist` 后缀（唯一允许的变更，永不删除/合并意见）。
- 已注册进 `ShippedPlaybookNames`（随二进制内置，InstallToUserDir 物化可编辑副本）。
- 首批实践：3 条意见已转入任务 157/158 并重命名。

## 本版新增：任务 151 深化（切 tab 第二阶段，按会话监控实测调整）

- **checkpoints 并入 ancillary 并行批**：原来 effort/jobs/context 三个并行完后，checkpoints
  还要**串行**跑一趟（虽只 13–51ms，但排在最后，context/effort 跑长时把整个 ancillary 窗口
  拖长）。现在四个调用同一 `Promise.all`，只有 dispatch 仍等可见性——后台切换不再浪费已
  发起的请求。
- **1555 实测对照**：`switch-tab:history` 已基本消失（仅未驻留会话首切仍有 ≤600ms），
  剩余主瓶颈转移为 **ancillary context（291–1034ms，大会话）**——其大头在 controller 层
  上下文快照计算，缓存失效边界涉及 turn/new/rewind 多事件源，**本版不做**（已记 151 遗留）。
- **驻留策略（151.C）与 context 缓存**仍为待办，需先收集「未驻留首切」耗时分布再定方案。

## 验证矩阵（构建自检，2026-09-17 16:2x）

go build（根 + desktop 两模块）✅ ｜ go test desktop Collab ✅ ｜ internal/skill 全绿 ✅ ｜
internal/agent Collab|ContactID ✅ ｜ `tsc --noEmit` ✅ ｜
tsx session-monitor 45 / hydrate-history-apply 31 passed ✅

## 已知未含

- ancillary context 的 controller 层缓存（最大单块 1034ms，需先定失效边界）
- 151.C 驻留数扩大（待「未驻留首切」耗时分布数据）
- 157（写目录授权 runtime 热同步 + autopilot 放行档位）尚未实施——等你选档
