# Reasonix Fork 桌面版 v1.38.3-20261031-0912 — Release Notes

> 基于 v1.38.3-20261031-0810（包59）以来：单一紧急修复——包59 打开大日志会话必崩的回归。受影响用户建议尽快覆盖安装。

## 本版内容

- **修复：打开大日志会话必崩（包59 回归，tl1291）**：包59 的 events 瘦身特性里，折叠记账梯的四个内部账本（regime/strikes/archivedAt/floorSeen）从未被创建，默认配置下首个超过阈值的会话保存会直接写空账本导致程序崩溃（`panic: assignment to entry in nil map`）。表现为：打开 events 日志较大的会话（本例约 80MB 日志）必崩、反复崩；且崩溃点在日志已重写、元数据未更新之间，该会话随后可能无法加载（提示格式问题或 "no longer writable"）。修法：账本改为首次使用时自建（lazy-init），任何入口都不再可能碰到空账本。
- **新增回归防线**：一条「不初始化直接跑」的用例把缺口钉死——此前所有用例都先初始化再驱动，恰好掩盖了该缺陷；本用例在未修复时会复现与线上遥测逐字一致的 panic（已做负向验证）。

## 使用提示

- 升级后：大日志会话恢复正常打开与保存；不再出现本型崩溃循环，无需任何额外操作。
- **之前受影响会话的恢复**：崩溃时落盘的是完整有效的折叠日志，且加载路径对损坏日志有 fail-open + 下次保存自动修复（repair）设计——装新包后重新打开这些会话，大概率直接恢复；若提示 "no longer writable"，关掉该会话 tab 再重新打开一次即可（写权限会重新授予）。个别仍无法打开的会话，用 设置 → 实验室/存储 的修复入口；仍不行请反馈给开发线做专项修复。
- 折叠瘦身本身（包59 的 tl1285 特性）不受影响，记账在修复后正常工作。

## 门禁与质量

- 定向 7/7 绿（fold-probe 全组含新增 TestFoldProbeLadderSurvivesFreshProcessNilMaps + 17s e2e TestFoldFloorFallbackThroughRealSave）；负向验证 PASS（撤修复复现遥测同款 panic）。
- 预存红申报：TestCrashAtRevisionLedgerHealsOnNextSave / TestSaveRecoveryBranchPersistsDivergedSnapshot / TestContentProbeDAGMatchesFullLoad 在基线 4cf85eda4 单跑同样红（预存，与本修复无关）。
- wails 官方管线出包 EXIT=0，ldflags 版本注入 1.38.3-20261031-0912，staging 四件套已铺。
