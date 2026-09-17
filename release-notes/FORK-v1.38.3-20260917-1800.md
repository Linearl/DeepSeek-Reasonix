# Reasonix Fork 桌面版 v1.38.3-20260917-1800 — Release Notes

> **构建**：2026-09-17 18:0x ｜ **安装目录**：`versions/v1.38.3-20260917-1800/`
> **增量基线**：上一包 **`v1.38.3-20260917-1735`**。本文件**只列相对 1735 的新增/变更**，
> 不堆叠更早包的内容——看更早的历史请沿 `FORK-v1.38.3-20260917-1735.md` 链式回溯。
> 包版本号长期停在 `v1.38.3`（与上游对齐），时间戳只用于区分同版本号的多次构建。

## 本版新增：任务 161——多 tab 缓存恢复（LRU prune）+ 实验特性「缓存大小调整」

1735 装机实测 + desktop.log 定判：切 tab 慢的真凶 = **singleSurface prune 每次切走
tab 都删除其他 tab 的前端缓存 state**（`resident items empty` veto 实锤），复用判定
根本轮不到执行。

- **LRU prune（根治）**：`commitSingleSurfaceNavigation` 从"删除其他所有 tab 的
  state"改为 **LRU**——保留最近激活的 `maxCachedTabs` 个（默认 12，0 = 不限制），
  只释放最旧的超出者。被保留的 tab 保留 transcript 订阅，后台 patch 照常更新，
  切回即最新。
- **实验特性「缓存大小调整」**（设置 → 实验特性，默认关）：
  - **驻留会话状态上限**（数字输入，0 = 不限制）
  - **正文缓存上限**（滑块 + 输入，32–512 MB，默认 192）
  - **渲染缓存上限**（滑块 + 输入，64–2048 MB，默认 256）
  - **重启后生效**；开关关闭时完全使用默认值（用户值被忽略，控制项禁用）
- 与上游 #10295 的关系：上游把资源占用排在切换速度前（移除活跃豁免）；fork 按用户
  决策恢复驻留体验——**功能型保留**，未来追齐时按既有登记纪律处理。

## 验证矩阵（构建自检，2026-09-17 17:5x）

go build（根 + desktop 两模块）✅ ｜ go test desktop Collab/Preferences/Settings 连续两次 ✅ ｜
`tsc --noEmit` ✅ ｜ tsx session-monitor 45 / hydrate-history-apply 31 passed ✅

## 已知未含

- 157（写目录授权 runtime 热同步 + autopilot 放行档位）已立项待实施（等你选 autopilot 档位）
- 158（意见箱 4 子项：sync 等待语义实测 / set_session_purpose 代理自解析 / create_collab
  跨项目 / dry-run title 核对）已立项
- 任务 161 的设置项改动**需重启生效**（面板内已标注）
