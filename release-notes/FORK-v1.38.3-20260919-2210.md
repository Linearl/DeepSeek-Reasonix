# Reasonix Fork 桌面版 v1.38.3-20260919-2210 — Release Notes（hotfix：性能监控 + heap profile）

> **增量基线**：上一包 **`v1.38.3-20260919-2124`**。本包为内存调查的观测工具包，
> **含 2124 的全部内容**（171 实施已回退），新增监控与 heap profile 能力。

## 新增：性能监控 + heap profile（任务 184，默认关）

**目的**：定位"工作时段内存膨胀至 14.6-20GB + 磁盘持续写"的根因——此前
只能靠事后单点采样，本包提供时间序列与 heap 剖析，一次复现即可定因。

**使用（两步）**：
1. 设置 → 实验特性 → 开启「性能监控」（`experimental_perf_monitor`）→ 重启
2. 正常开始工作，触发内存膨胀后：
   - `logs/perf/perf-sample-YYYYMMDD.jsonl`——5s 一行时间序列（进程内存/
     IO/磁盘关键文件大小/tab 与驻留数/阈值告警 WARN），7 天滚动 + 20MB 上限
   - `logs/perf/heap-*.pprof`——**每 60s 自动落一份 heap profile**（保留 3 份），
     或设置里手动「立即抓取」
   - 汇总一行命令：`node desktop/scripts/perf-summary.mjs logs/perf`

**已内置的首条外部观测**（2.2 分钟 13 样本）：进程 WS ≈ **20GB**（min 17.8 /
max 20.2），private +5.2GB/h；磁盘 `sessions-v4` 目录 **11.7GB 且 +1GB/h 增长**；
events.jsonl 1.3GB（+2.7MB/h，判定正常）——**磁盘增长大户与内存量级接近，
pprof 将回答"Go 堆 vs 文件映射"这最后一问**。

**实现要点**：App 方法抓 heap（不开网络端口，零风险）；非 Windows 降级；
关闭时采样 goroutine/句柄零残留；默认关等价性测试；integrity 56/56。

## 保留内容

2124 的全部内容（会话管理工具 / 关 tab 不阻塞 / 测试防线 / 首载提速与预取 /
预取冗余修复 / 引导消息可见性 / 版本参数修复）。171 实施保持回退状态。
