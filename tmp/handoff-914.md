# 任务 914 交接：每轮时间锚附显式中文日期+周几

分支 `wt-914`（基于 `main-v2-stable` @ 6280772ea），worktree：`github-repo/worktrees/wt-914`。

## 背景

2026-10-15 当日两起误判事故：主对话把「周四」误判为「周一」。现状锚 `<current-time>2026-10-15T15:55:00+08:00 Thu — host clock when this turn started</current-time>` 已含 ISO 时间与英文周几缩写，但模型容易扫过 `Thu` 直接误读。任务要求在每用户回合注入处单点附中文显式日期+周几。

## 实现决策

**扩展 current-time 块格式**（任务书允许「或扩展 current-time 格式，实施方定」），而非新增 `<current-date>` 标签：

- 新格式：`<current-time>2026-10-15T15:55:00+08:00 Thu — 2026-10-15 星期四 — host clock when this turn started</current-time>`
- 理由：新增标签需要动 `TransientUserBlockTags` 注册表、strip 正则、steer 回放分类三处，改动面大且历史会话兼容成本高；扩展同一块则单点改动（`internal/agent/current_time_block.go` 的 `CurrentTimeBlock`），注入点 `agent.go:732 withTurnPreferences → WithCurrentTime` 不变，前缀去重（`hasLeadingInjectedBlock`）自动生效。
- 中文字段与 ISO 字段**同一 `time.Time`、同一 zone 派生**，构造上不可能互相矛盾。

## 改动清单

| 文件 | 内容 |
|---|---|
| `internal/agent/current_time_block.go` | `chineseWeekday` 表 + `CurrentTimeBlock` 追加 `2006-01-02 星期X` 字段 |
| `internal/agent/current_time_block_test.go` | 更新钉格式测试；新增 3 个验收测试（见下） |
| `FORK.md` | 特性表「每轮当前时间锚」行补 914 说明 |

## Commit

1. `16c3f23e0` 914 每轮 current-time 锚附显式中文日期+周几（防模型误读英文周几缩写）
2. （本文件 + FORK.md 为第二个切片 commit）

## 验收对照

| 验收项 | 结论 | 证据 |
|---|---|---|
| ① 新回合注入可见中文日期+周几 | ✅ | `TestCurrentTimeBlockCarriesExplicitChineseWeekday`（事故日 2026-10-15 周四固定时钟断言 `2026-10-15 星期四` 在块内）；`TestCurrentTimeBlockRendersExactTimestamp` 钉全格式 |
| ② 连续跨天日期随回合刷新 | ✅ | `TestTurnAnchorsRefreshChineseDateAcrossMidnight`（23:50 周四 → 00:10 周五，两回合各自块内日期正确翻转）；既有 `TestTurnPreferencesCarriesFreshCurrentTimeAnchor`（8h 漂移）仍绿 |
| ③ 三处时间锚无矛盾 | ✅ | `TestCurrentTimeBlockAnchorsAgreeAcrossFullWeek`：整周扫描（跨月界 9-27→10-03），把渲染出的 ISO 戳解析回 `time.Time` 再独立重算中文字段，逐日断言一致。三锚口径：ISO 戳 / 中文日期周几同源同 zone；文件时间戳锚（`CollabSentTimeText`，任务 866/1553 路径）未在本次改动面，格式保持 `RFC3339+英文星期` 不变，无新矛盾引入 |
| ④ boot golden | ✅ 无需再生成 | golden 捕获面 = system prompt / 工具 schema / 稳定前缀（`internal/boot/testdata/golden/`），current-time 块走 user 回合头、从不进捕获面（grep 证实 golden 内无 current-time）；`go test ./internal/boot/ -run Golden` 全绿 |

## 测试结果

- `go test ./internal/agent/`：除预存红 `TestToolCheckpointReloadRefreshesProjection`（基线对照，改动前即 FAIL，与本任务无关）外全绿；新增 4 测试全 PASS
- `go test ./internal/boot/ -run Golden`：ok
- `go test ./desktop/ -run "CollabSentTime|SentTime"`：ok（desktop 是独立 module）

## 互证项：心跳 prompt 硬编码周X（评估，未改）

扫描 `%APPDATA%\reasonix\heartbeat-tasks.json`：仅 2 处硬编码周几，均在**调度型** prompt 内：

1. `paper-weekly-mon-0900`（论文周报）：「今天是周一。……抓取过去一周（上周一至周日）」——每周一 09:00 触发，硬编码与调度一致
2. `sanfa-resume-thu-832`（宣发恢复）：「今天是周四，距……限制已超 72 小时」——一次性任务，文案与触发日绑定

**评估结论：不改**。理由：(a) 这两处的「今天周X」是与调度周期绑定的语义（周一抓上周报），不是单纯报时；改成引用注入锚反而丢失「本任务应在周X执行」的意图；(b) 914 落地后每回合注入已自带中文周几，模型即使对硬编码文案存疑也有权威锚可校验，误判面已收敛；(c) `heartbeat-tasks.json` 是并发热点，无必要不动。若后续新增不绑定周期的普通心跳 prompt，建议直接信任注入锚、勿再硬编码周几。

## 残留

- 无代码残留。`CollabSentTimeText`（跨会话消息发送时间戳）仍是英文星期格式——不在 914 范围（那是 866/1553 的收件人侧展示路径），如需统一中文可另立小任务。
- 预存红 `TestToolCheckpointReloadRefreshesProjection` 属 main-v2-stable 既有，需另行排查（非 914 引入，基线已录）。
