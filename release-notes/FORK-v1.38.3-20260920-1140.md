# Reasonix Fork 桌面版 v1.38.3-20260920-1140 — Release Notes（193 长效机制）

> **增量基线**：上一包 `v1.38.3-20260920-1130`。内容 = 1130 全部 + 任务 193 长效机制。

## 修复：records 感知自动 compact（193 长效机制，根治冻结）

Save 时读取事件索引的 **MessageCount**（schema 2 流中消息为 records 主部），
超过重放上限 - 40k headroom → **自动折叠历史为单条 replace 快照**（msgs 用
Save 内存态，零回放），records 归 1——事件记录数**永不撞重放闸**，
「辣椒识别2」类冻结从机制上根治（此前 400k 放宽只是延迟撞墙）。

字节硬顶 1GiB 不变；索引缺失/损坏时保守回退字节闸。

## 验证

TestSaveCompactsWhenEventIndexNearRecordCap（近上限检测+小 index 不误触）PASS；
agent 定向族 ok 45.3s；build 两模块 ✓。
