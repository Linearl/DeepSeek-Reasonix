# Reasonix Fork 桌面版 v1.38.3-20260917-1735 — Release Notes

> **构建**：2026-09-17 17:3x ｜ **安装目录**：`versions/v1.38.3-20260917-1735/`
> **增量基线**：上一包 **`v1.38.3-20260917-1655`**。本文件**只列相对 1655 的新增/变更**，
> 不堆叠更早包的内容——看更早的历史请沿 `FORK-v1.38.3-20260917-1655.md` 链式回溯。
> 包版本号长期停在 `v1.38.3`（与上游对齐），时间戳只用于区分同版本号的多次构建。

## 本版新增：任务 151 B 级——驻留缓存误杀根治 + 监控显示修正（desktop.log 实测定判）

1555 装机实测 + desktop.log 定判：fork开发 在「复用缓存秒切」与「no-reusable-cache
全量重载 6841ms」之间反复——驻留明明有 117 项内容。根因 = `hasReusableCachedTranscript`
对 sessionPath 做**严格字符串全等**，而同一会话文件的两处存储拼写（tabs meta vs
controller session path）会漂移，漂移即否决。

- **basename 匹配**：会话文件名（时间戳.纳秒-模型）全局唯一，复用判定改为比较文件名
  （大小写折叠）；recovery 副本带 `-recovery-<hash>` 后缀仍不会误配
- **veto 诊断日志**：导出 `explainReusableCache`，否决分支写
  `hydrate cache veto tab=... <原因>`（含两个 path）——下次一眼定判，不再停在裸 reason 标签
- **监控显示修正**：`slowestStageFor` / `reportStageSummary` 此前混入该 tab **全部历史**
  切换记录（无时间窗）——截图里辣椒识别2 显示 total 5156ms 而各实时分段均 <300ms，
  即旧数据残留。两者都改为**最近一次切换窗口**（从最新 `:total` 回溯）

## 1555/1655 实测结论对照

- `switch-tab:history` 复用缓存路径已稳定 <300ms（151.A 生效确认）
- 剩余慢场景 = path 漂移触发的偶发全量重载（本版根治）+ 未驻留会话首切（151.C 待办）
- ancillary context 的大头（大会话 1034ms）是 controller 层计算，缓存失效边界未定，本版不做

## 验证矩阵（构建自检，2026-09-17 17:2x）

`tsc --noEmit` ✅ ｜ tsx session-monitor 45 passed ✅ ｜ hydrate-history-apply 31 passed ✅ ｜
check-fork-integrity 42/42 ✅（本轮未动 Go 代码）

## 已知未含

- ancillary context 的 controller 层缓存（1034ms 大头）
- 151.C 驻留数扩大（待「未驻留首切」耗时分布）
- 157（写目录授权热同步 + autopilot 档位）待用户选档
