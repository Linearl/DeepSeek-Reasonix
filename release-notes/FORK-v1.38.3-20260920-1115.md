# Reasonix Fork 桌面版 v1.38.3-20260920-1115 — Release Notes（四线并行合并批）

> **增量基线**：上一包 `v1.38.3-20260920-1005`。内容 = 1005 全部 + 四线并行开发合并（任务 187 重点）。

## 性能：大会话切换秒卡治理（任务 187，重点）

v4 读路径的 offset 索引缓存键含 size/mtime/尾部哈希——**append-only 日志每次写入都令索引失效**，
分页读的每一页都从 0 全量重建索引（116MB 会话 ≈32 页 ≈ 32 遍全量扫 ≈ 3.7GB ioRead），
这就是「切走再切回大会话数秒卡顿」的真身。修复：头部签名（前 4KiB）做缓存键 + 前缀续扫
（异常自动回退全量重建）+ 进程内索引缓存 + 写入路径同维护签名 + 三个运行时计数器
（`sparseIndexRebuilds/Extensions/MemoryHits`，可在日志观测放大是否消除）。
**预期：单次切回大会话 ioRead 从 3.7GB 降到 ~116MB 量级，体感秒卡 → 百毫秒级。**

## 修复：内置项目节点无法移出（任务 186）

`global-workspace` / `sessions` / `projects` 容器三类宿主自有目录不再作为项目节点进入
侧栏（读写两处收口，跨重启稳定）；磁盘会话数据原封不动，Global 区继续承载。

## 新增：引导消息在主输入框编辑（任务 181）

待处理引导条目点铅笔 → **多行全文载入主输入框**（原位更新、占原队列位置）；
草稿双向 stash（附件/引用不丢）；in-flight 询问后作为新引导排队；Escape 取消零副作用；
点条目文本 = 只读全文预览。

## 新增：collab 三件套（任务 162/166/167）

- create_collab_session 支持**指定模型**（必须带 provider，与设置页同一解析；非法引用带可操作错误）
- 支持**批量创建**（sessions[] 上限 20，顶层默认+逐项覆盖；部分失败不回流，只重试失败项）
- 创建时可**携带首条消息**（走既有 mailbox 投递）；跨会话消息在目标会话折叠进既有
  IM 来源卡片显示来源，不再是不可区分的文本块

## 杂项

- 构建脚本硬闸：版本号缺 `-YYYYMMDD-HHMM` 时间戳后缀直接拒绝（杜绝裸 v1.38.3 包）
- Bundle 预算棘轮步长放宽（gzip +0.5 KiB / raw +10 KiB，一次到位禁止挤牙膏）
- 实验室分组标题字号加大加粗

## 验证

定向全绿：render 表防线 / agent collab / session SparseIndex+TrimMessages / desktop
ProjectTree+Restart+PerfMonitor / composer-guidance 15 checks / project-tree 3+9 checks；
inbox-recovery 3 红为已归档预存。desktop 全量在用户空闲时段补跑。

## 装机后重点确认

1. **大会话切换体感**（重点，你在痛的）——配合性能监控看 `ioReadDeltaMb`
2. 内存曲线（你已切仅 v3；确认稳定后可再切双写验证 187 的改善）
3. 侧栏不再出现 global-workspace/projects 内置节点
