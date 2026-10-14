# 811 跨会话消息自动折叠双症状 · handoff（wt-811）

分支 wt-811（基 main-v2-stable@41f87feb0）。状态：**实现+测试完成，待 commit/汇报**。

## 三样本（jsonl 原文锚点）

1. **20261012 审计回执**（threadId=msg_5f1512238b685c85）：header `… → 发至 contact_id=… (hop=1)`（旧投递无时间尾）。
2. **今日转线回执**（threadId=msg_b3a2abd01930a490，13:21）：`… (hop=1)，发送于 2026-10-14T13:21:00+08:00 Wed`。
3. **症状2·换线通知**（msg_c1351f06c5c90c08，13:18，hop 后缀缺失）：`… contact_id=sc_72b5d11bb18354c739f614d8，发送于 2026-10-14T13:18:00+08:00 Wed`。其 120-rune preview 截断点实测落在 `…T13:18:00+08…`，与 20261014 截图逐字吻合。

## 根因（已定案）

- **症状1（超长回执不折叠）**：758 的 SteerCollabCard 直渲无任何折叠控件——705 折叠只实现在 Message.tsx user-row 卡；同一消息落 ↪ steer notice 时永远全文铺开。
- **症状2（三行无入口）**：reconcile 气泡只带 `row.preview`（单行+120 rune 截断）；866/1553 的「，发送于 <时间>」尾被旧 `(\S+)` 捕获吞进 chat id（`，` 非空白），单行分支 body 退化成时间戳残片，且全文无入口。

## 修复（4 文件 + 1 新测试）

1. `lib/collabMessage.ts`：id 捕获 `[^\s，,]+`；单行分支剥 hop 尾后再剥 `SENT_TIME_TAIL_RE`（锚定日期形态，容忍 `…`）。
2. `components/TranscriptCards.tsx`：SteerCollabCard 复用 705 开关/阈值/摘要条 CSS 加折叠；`foldable` prop，723 deferred 展开传 false（不二次折叠）。
3. `lib/inboxSurfaceReconcile.ts`：新增 `hydrateQueuedGuidanceBubbles`（ReadInboxItem 换全文，读失败降级 preview，保持 ↪ 前缀契约）。
4. `lib/useController.ts`：reconcileQueuedGuidanceForTab 接线气泡升级。
5. 新测试 `src/__tests__/task811-collab-fold-unify.test.tsx`：**37/37 绿**（A 解析三样本+758 回归 / B 开关两态+723 不二次折叠 / C 气泡升级与降级）。

## 验证结论

- `tsc --noEmit` 绿；eslint 改动文件干净。
- 直接相关 6 套件全绿：722(43)/758(35)/723 steer-deferred-dedup(22)/inbox-surface-reconcile(12)/user-message-fold(21)/collab-contact-names(30)。
- **预存红申报**：task705 断言写死 `optional: 22,`（现值 29）；task704 同族 tier-count 旧口径红；全量 run-tests 另有 ~45 个失败套件。**基线对照证实**：临时还原 4 文件后同批套件红数逐一相同（identity-retry 1、task649 1、tab-switch-hydration 7、web-search 等 exit=1 无 FAIL 行）——均为基线预存红，与本任务无交集。

## 待办

- bundle 棘轮：`pnpm build` 后台跑中（/tmp/build.out）。
- 切片 commit ×3：解析 / 折叠统一+气泡升级 / 钉测+handoff。
