# FORK v1.38.3-20260920-2256

> 基线：`FORK-v1.38.3.md` ｜ 本版仅列相对**上一时间戳包 1150** 的新增
> 类型：**纯观测包** —— 不改变任何运行时行为

## 本版新增

任务 196 第一步：**会话切换的观测补齐**。

背景：用户装 1150 后实测未达标 —— 大会话（「fork 开发」↔「测试会话」）**双向切换仍约 10 秒**，而从异常长会话**切出也慢**；但已有计时器显示各阶段只有 155–300ms ⇒ **慢在未被计时的区间**（`takeover watcher started` → `resume cache state` 之间实测 223ms ～ 4.65s，最长一次 11.8 秒无任何日志）。

三个提交（分支 `wt-187`）：

| 提交 | 内容 |
|---|---|
| `e554bee64` | **resume 链分解**：`load_ms` / `rebind_ms` / `page_ms` / `total_ms`（+ `history_ms`），覆盖原「无日志黑洞」区间。`load_ms` 含 `LoadSessionTail` 的 save-path 锁等待 |
| `5a4f3f016` | **切出侧计时**：新增 `switch-out:snapshot` / `switch-out:release`，**独立阈值 50ms**（切入侧仍 150ms —— 150 会把 120ms 的拆除静默吞掉，而那恰是「拆除慢」与「这里没干活」的分界证据） |
| `6f3bb8682` | **首屏 payload 日志**：记录 first paint 自身的载荷（页大小/条数），用于判断「read 很快但传输/解析很重」的情况 |

**为什么需要这个包**：用户已装 `1150`，但上述观测是 **`wt-187` 分支上的新提交**，1150 不含。装上本包后跑一次双向切换，即可从 `desktop.log` 读出完整分解：

```bash
grep -E "switch-out:|resume session|hydrate stage" desktop.log   # 一趟切换的完整三段
grep "transcript evicted" desktop.log                            # 谁被驱逐（lru|budget|tab-state-lru）
```

读数后的判定方向：
- `hydrate:read` 大 ⇒ bridge 往返/后端（已确认走 `LoadSessionTail` 尾部重放，需看返回页大小与锁等待）
- `hydrate:apply` 大 ⇒ `reason=reset` 触发的全量重建
- `switch-out:*` 大 ⇒ 切出侧（源 tab 快照/释放/移交）
- 两者都小 ⇒ 渲染

## 验证
- `go build ./...` OK；`cd desktop && go build .` OK
- `tsc` 对涉及文件零错误（基线 15 项在 `bench/__tests__` 预存区）
- `session-monitor` harness **45 passed**

## 注意
- **纯观测，零行为变更**，不引入新开关（符合铁律 2 的例外条件：仅新增诊断）
- **未含任何修法** —— 修法待读数定案
