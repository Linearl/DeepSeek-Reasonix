# Reasonix Fork 桌面版 v1.38.3-20260919-2310 — Release Notes（hotfix：内存膨胀修复 + 171 恢复）

> **增量基线**：上一包 **`v1.38.3-20260919-2210`**。**建议所有 1210 之后的用户升级本包。**

## 修复：内存膨胀根因修复（heap profile 定位）

**症状**：1210 之后的所有包，工作时段进程内存膨胀至 14.6-20GB
（heap profile 铁证：Go 堆 9.74GB 中 **96.33% 在单个函数**
`session.resolveContentPayload`——v4 存储的消息 payload 全量物化）。

**机制**：每次会话快照（turn 运行/切 tab/保存）都会触发
`materializeSnapshotMessages` → 把**全部消息的 v4 payload 全量物化成
[]byte 并驻留**——会话越大（116MB events）驻留越大，多标签页累积后
达 14-20GB。该路径是 v4 架构移植时引入的固有行为，**与是否开启其它
功能无关**，大会话下所有历史版本（含 081553 时代）打开都会触发。

**修复（bound the full-history snapshot reconstruction）**：
1. snapshot 重建改为**有界**：常量预算 96 MiB，边累积边裁剪——峰值 =
   预算 + 一页，不再随会话大小无界增长
2. **保留最新消息**（聊天视图锚在尾部），更早历史由分页 Query 按需取
3. `Snapshot.HistoryTruncated` 显式告知截断（不静默缩短）
4. `budget<=0` = 旧的全量行为（低层 API 兼容，调用方不变）

**恢复：任务 171 实施**（storm breaker 误伤修复 + pending-handoff 落盘）
——此前因二分误判被回退，heap profile 证明其与内存问题无关，本包恢复。

## 验证

- heap profile 方法复测路径：同 116MB events 会话，heap inuse 预期
  9.7GB → ≤96MB + 解析对象（装机后由 184 监控曲线确认）
- go build 双模块 / tsc / desktop TestPerfMonitor+TrimMessages+
  StopAndClose+History 全绿

## 装机后确认清单

1. **内存**：开始工作后内存应稳定在合理水平（不再无界膨胀）
2. **设置 → 会话存储**：请反馈当前显示的档位（我们需要确认四档迁移
   在你机器上的实际落点）
3. 引导消息可见性 / 会话管理工具 / 关 tab 不阻塞 / 首载提速——全部
   保持有效
