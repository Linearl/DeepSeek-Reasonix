# Reasonix Fork 桌面版 v1.38.3-20260920-1150 — Release Notes（大会话首开提速 23×）

> **增量基线**：上一包 `v1.38.3-20260920-1140`。内容 = 1140 全部 + 任务 187 第二轮。

## 性能：大会话**首次打开** 71 秒 → 亚秒（尾部重放）

**根因（分阶段计时定位）**：467MB 会话首开的瓶颈 100% 在 **DAG 全量解码**
（19.49s），物化仅 3.7ms——「尾部优先」因此做在重放层。

**实现**：
- `replaySessionDAGTail`：只重放日志尾部 20MiB 窗口（起点吸附行边界；
  窗口外 parent 记 orphans）；**实测 842.7ms vs 全量 19.49s = 23.1×**
- `loadSessionTranscriptTail` 首屏入口：**>32MiB 走窗口，小会话逐字等价**（零回归）；
  与写路径的 `loadSessionTranscript` 故意分离
- `tailTruncated` 入状态：调用方可感知「非完整转录」；窗口视图带 head system 消息
- 任何不确定 → 安全回退全量（宁慢不装假）

## 安全：写保护三道口子（防截断转录落盘丢历史）

1. `agent.LoadSessionTail` 仅 desktop hydrate 使用；recovery/GC/migrate 等
   12+ 写路径调用点**不动**（仍走全量）
2. `Session.TailTruncated()` 暴露截断事实
3. **Save 入口检测 tailTruncated → 先全量重放再写**（端到端测试：
   截断 Session → Save → 磁盘仍是全量）

## 修复：重放失败负缓存（防循环阻塞）

超限拒绝（SessionReplayLimitError）记入进程内负缓存（键=path+size+mtime）——
同一日志**只重放一次**，后续 open 直接错误横幅（此前每次访问重复 6-15 秒
同步重放，阻塞其它操作——「需求开发」切换 8 秒的真凶）；日志更新后缓存失效。

## 验证

6 个新测全 PASS（尾部一致性/小会话逐字/端到端 Save 保护/负缓存键控+失效）；
build 两模块 + session 29.4s + agent 212.7s 零预存；integrity 63/63。

## 装机后确认

1. **「需求开发」等大会话首开**：`open-topic:history` 应从 71.7s → 亚秒
2. `refusing replay` 日志**最多一次**（负缓存生效）
3. 小会话切换不回归（227ms 水位保持）
