# Release Notes — FORK v1.38.3-20261105-1130（包 75）

> 基线：上一时间戳包 v1.38.3-20261105-1010（包 74）。本包 = tl1394 GLM 早停根因修复（单独成包快速交付用户痛点）。
> 主线 tip：28f64c78f。

## 缺陷修复

- **GLM「早停」根因修复**（tl1394，用户主诉「glm-5.3-flash 在 reasonix 经常早停而 zcode 不会」终解）：
  - **根因**：wedge 看门狗 quiet 判定只读 turn event ledger，而**同步 turn 家族（desktop 空闲桥/bot/ACP）根本不往 ledger 记账**→ 同步 turn 继承上一个普通轮的陈旧时间戳（最远 126 小时前）→ 刚开 1-2 秒的轮被按旧账误判卡死枪毙（UI 弹「the running turn stopped responding…」）
  - **修复**：quiet 判定取 ledger ∨ sink 全量 liveness 较新者（两本账取最新）+ synchronous turn admission 临界区 liveness.reset 堵 stale 继承 + desktop sweep 文案修正（no ledger→no turn activity）
  - **双向测试钉**：stale ledger 上的活同步 turn 不 heal（反事实：还原后第一案必红）/真 wedged 同步 turn 仍 heal（tl1288 看管自救保留）
  - **zcode 对照论证**：zcode（AI SDK streamText）无流内 idle watchdog 亦无 turn 级 quiet 杀——早停差异全部来自 reasonix 侧机制，本包修掉痛感最大面
  - **日志统计佐证**：近 7 天 9 次 heal，6 次 stale 误杀形态（quiet 远超阈值）占 67%，4 例带误杀铁证
- 已知边界：SSE idle 300s 防半开设计保留（未观察到实际触发）；真静默 3 例的「snapshot conflict 后 turn 卡 provider 前」机制未挖（heal 行为正确，候另开单）；同步 turn durable ledger 治本候单

## 门禁

构建 EXIT=0（三跑：E1×2=locale 棘轮触线+eslint shim，均已修复核）；control 双钉 PASS（TestSyncTurnOnStaleLedgerMustNotHeal/TestWedgedSyncTurnStillHeals）+ build EXIT=0。基线对照：包 74（1010）不含本件。
