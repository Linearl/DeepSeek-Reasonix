# 任务 894 handoff（2026-10-15）：跨会话回执分级通道——无需行动静默化，需行动保留注入

worktree：`github-repo/worktrees/wt-894`（branch `wt-894`）。P2 backend，三切片。

## 任务一句话

跨会话投递的系统回执（steer 降级排队通知等）此前每条都作为独立 host 消息注入发送方模型——2026-10-14 实测协调线一天 6-7 个轮次纯用于「读回执→判断无需动作→收尾」。本件按用户 1014 批准的**分级通道折中**（全量放返回值方案的四缺点共识已弃）：无需行动类静默化（进 talk_to_session 返回值指引 + UI 通知层，不注入模型），需行动类保留注入；非阻塞禁同步等泵；反向用例（需行动误入静默=漏行动）钉死。

## 分级判定（单一事实源）

`internal/sessioncollab/receipt.go` 新增 `ReceiptNeedsAction(outcome)`——分级判定与 570 回执词表同居一处：

| 结局 | 分级 | 通道 |
|---|---|---|
| `injected` | 无需行动 | 静默（本就无通知） |
| `queued_followup`（followup 正常落点 / steer 降级） | 无需行动 | **静默化**：UI Notice 事件 + 570 回执查询，零模型注入 |
| `refused_hop / refused_provenance / refused_cross_wire` | 需行动 | 保留注入（现状不变） |
| `failed_retrying`（未送达将重试） | 需行动 | 保留注入（现状不变） |
| `open_retry_exhausted` | 需行动 | 现状不变（本就只写 570 回执，不注入；守卫保证永不被静默） |
| **未知值** | **需行动（fail-safe）** | 静默按已知无需行动结局逐个开通；拼错/新增词表值默认保留注入 |

## 改动清单（3 commit）

- `7d0159de6` **切片1**：`ReceiptNeedsAction` + 表测（`TestReceiptNeedsActionTiering`：两侧逐个钉死 + 未知值 fail-safe + 词表穷尽循环）。
- `7f749dbf4` **切片2**：desktop 投递链接线——
  - `runCollabDelivery` 四个通知点全部改走 `collabDelivery.notice(msg, kind, outcome, text)` 路由（**分级判定在回执生成点**：与 recordReceipt 的 outcome 同源）；
  - `collabDelivery` 新增 `notifySilent` 缝（无 seam 时回退 notify，旧夹具行为不变）；
  - 生产接线 `silenceSenderNotice`：①570 回执存储照写（留痕通道不动）②发送方是本窗口存活会话时，Notice 事件（`NoticeCodeCollabDeliverySilent = "collab_delivery_silent"`）打进发送方 tab 事件通道（UI 层，人可见模型不可见）③日志行；**反向守卫**：需行动结局误入此通道 → 大声 WARN + 回退 `notifySenderOnce` 注入（拦截，宁多一轮不漏行动）；
  - 测试：`collabTestDelivery` 夹具改四返回值区分双通道；`TestDegradedSteerIsReported`→`TestDegradedSteerIsReportedSilently`（注入 0/静默 1）、`TestReceiptRecordsDegradedSteer` 改钉新契约（回执照写+注入 0/静默 1）；新增 `desktop/session_collab_894_test.go` 三条：
    - `TestSilentSteerNoticeSurfacesOnSenderTabNotModel`（验收①：UI Notice 事件恰好 1 条 + 码/messageId 断言 + **发送方信箱零落盘=零模型注入**）；
    - `TestSilentChannelMisrouteFallsBackToInjection`（验收③反向：refused 误入静默 → 回退注入送达发送方）；
    - `TestActionOutcomesNeverReachSilentChannel`（验收②：refused + delivery_failed 真实投递链上静默通道零调用、注入通道照旧）。
- （切片3，见 git log）：`talk_to_session` 返回值 `deliveryOutcomeNote` 补分级通道契约（无需行动→UI 层+按需查询；需行动→仍强制注入；返回值仍只含 queued+messageId，**无同步等泵面**）；`get_message_status` 描述与 `queued_followup` 语义文案、`peek_own_inbox`/`mailbox` 描述同步改口径（steer 降级通知不再落信箱，别再指向旧通道）；`TestTalkToSessionReturnCarriesTieredChannelContract`（验收①前半+④：契约词断言 + status 恒 queued 非阻塞钉子）。

## 验收对照

1. ✅ 无需行动类零注入：`TestSilentSteerNoticeSurfacesOnSenderTabNotModel`（UI 事件 1 条 + 信箱 0 封）+ `TestDegradedSteerIsReportedSilently`/`TestReceiptRecordsDegradedSteer`（注入通道 0）。
2. ✅ 需行动类保留注入零变化：`TestActionOutcomesNeverReachSilentChannel` + 既有族测试原样绿（`TestHopExhaustedIsReportedAndSettled`/`TestUnverifiableProvenanceIsRefusedAndReported`/`TestDeliveryFailureDoesNotLoseMessage`/`TestRefusalNoticeReachesTheSender` 均未改断言）。
3. ✅ 反向用例拦截或告警：`ReceiptNeedsAction` 未知值→需行动（表测钉死）+ `TestSilentChannelMisrouteFallsBackToInjection`（生产守卫回退注入，WARN 日志实测出现）。
4. ✅ 非阻塞：改动全在泵侧结算点；`talk_to_session` 未新增任何等待（`TestTalkToSessionReturnCarriesTieredChannelContract` 钉 status 恒 queued/pending；599 的 retryAfterMs 契约测试原样绿）。
5. ✅ 既有 collab 回执族测试全绿：`go test -run "Collab|Inbox|Mail|Receipt|Steer" .`（desktop 37s ok）+ `internal/sessioncollab` 全量 ok + `internal/event` ok + `internal/agent -run "TestTalkToSession|TestGetMessageStatus|TestPeek|TestMailbox"` ok + `TestTalkToSessionReturnCarriesRetryAfterMs`（599 契约）ok。desktop/agent 两个全量包各余 2/7 个红，**已用基线 commit 逐个复跑证明全部预存、与 894 无关**（见下「全量套件」）。

## 双侧留痕

- 570 回执存储逐字未动（`recordReceipt` 调用点与词表原样）；静默类回执照写，`get_message_status` 按 messageId 可查（talk_to_session 返回值就带 messageId）。
- 信箱 `*.inbox.jsonl`/`*.sent.jsonl` 与 MarkNotified 语义不变（静默通道根本不写信箱）。

## 全量套件（2026-10-15 接力会话补跑完成）

- **desktop 全量**：`go test -timeout 40m -count=1 .` 跑完 1992s，**7 个 FAIL**：`TestPumpDerivedHopExhaustionIsAHopRefusal`、`TestSettingsExposesEffectiveSandboxWriteRoots`、`TestCatalogTopicTitleLocalizesAtSidebarBoundary`、`TestCatalogManualDefaultTitleIsNotLocalized`、`TestBuildTabControllerBlocksWhenSessionLeaseHeld`、`TestStartTopicActivationFailureDetachesPreviousAndReattaches`、`TestEnsureBlankTabConcurrentModelSwitchKeepsLastSelection`。
- **internal/agent 全量**（-timeout 25m）：**2 个 FAIL**：`TestToolCheckpointReloadRefreshesProjection`（restart left stale projection）、`TestInterruptedRecoveryKeepsRedactedFailureSummary`（MockProvider no scripted turn）。首跑撞默认 600s 超时（包常态 729s），非失败。
- **基线对照定性（关键）**：将本 worktree 只读 clone 到系统临时目录、checkout 基线 `6280772ea`（894 三 commit 之前）逐个复跑——**9/9 在基线同样红，desktop 7 个失败信息逐字一致，全部为预存红，894 零回归**。基线对照后临时 clone 已弃（系统 temp 自清理，未污染工作区）。
- 894 验收相关的 5 族测试（sessioncollab / event / agent 子集 / desktop 定向 / desktop 回执族）全绿，见上「验收对照」。

## 残留与边界

1. **跨进程/跨机器发送方无 UI 面**：`silenceSenderNotice` 只能给本窗口存活 tab 发 Notice 事件；远端发送方的静默回执可见面 = 570 回执（get_message_status）+ 日志。这是分级通道方案的既定边界（返回值+按需查询兜底），不是缺陷；若后续要全局可见，可在信件中心 UI 聚合 receipts/ 目录（未做）。
2. **`steerDegradedByPanel` 返回值注记**继承新文案（同一 payload 字段拼接），无独立分支。
3. **前端本地化未加**：`collab_delivery_silent` 码在 i18n 表无条目，前端按既有 fallback 显示原文（与 `collab_reply_nudge` 同待遇）；如需多语言另立小件。
4. `notifyDegradedSteer`（无调用点的历史函数）未删——任务书未含清理项。

## 恢复方式

```bash
cd github-repo/worktrees/wt-894
go test ./internal/sessioncollab/ ./internal/event/ -count=1
go test ./internal/agent/ -run "TestTalkToSession|TestGetMessageStatus|TestPeek|TestMailbox" -count=1
cd desktop && go test -run "TestSilent|TestActionOutcomes|TestDegradedSteer|TestReceipt|TestDelivery|TestHopExhausted|TestUnverifiable|TestRefusalNotice|TestIdempotent" -count=1 .
cd desktop && go test -timeout 40m -count=1 .   # 全量包级
```

落地纪律：保留分支 wt-894，**不合 main-v2-stable**（合并由用户指定会话执行）。
