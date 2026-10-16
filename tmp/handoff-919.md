# handoff-919 —— 真机体验门 FAIL 治理（P0 批 34 主件）

- worktree：`github-repo/worktrees/wt-919`（main-v2-stable @ f8a09feec）
- 数据源：414 bench 周趋势第 1 期基线（`docs/report/ebdd周趋势/ebdd周趋势-20261015.md`，证据 `tmp/414-weekly-20261015.json`）
- 状态：计划（进行中）

## 四门读数（基线）

| 门 | 读数 | 阈值 | 判定 |
|---|---|---|---|
| boot 热路径 P95 | 45011ms（n=160，P50=6429ms） | < 1000ms | FAIL |
| 打开历史对话→可发消息 P95 | 4021ms（n=85，P50=963ms） | < 2000ms | FAIL |
| 切 tab render P95 | 170ms（n=674，P50=51ms） | < 150ms | FAIL |
| 发消息→首响 P95 | 129509ms（n=33，P50=16561ms） | ≤ 60s | FAIL |

## 治理方向（任务书）

1. **boot 46.9s 长尾分层**：稳态 941ms 达标，超标在长尾——按三层真相归因（bench slogparse 的 stage 拆解 + 日志分位分层）。
2. **open-topic 4.4s**：n=7 慢开 census P50=3681ms；定位打开链路 stage 热点。
3. **render 174ms**：切 tab 战役已治 readSnapshot（861）；余热=React commit 链 #49/#50 方向——861 handoff 已代码级定位三热点：
   - `Transcript.tsx:178` useSyncExternalStore 每 token 全列表 reconcile
   - `MarkdownHistory.tsx:198-202` geometryChanged 回写环（bump measurementRevision）
   - `TranscriptWindow.tsx:204/218/323` 三处 setMeasurementRevision
   - 861 建议：行级 memo + geometryChanged 同帧脏合并。
4. **首响 145s**：与压缩/投递链同域，912 修复（重试韧性+永不全量）后的增量面——从 turn ledger/slog 定位首响超时分布（排队/压缩阻塞/provider 首 token）。

## 执行步骤

1. [x] worktree + 本计划 commit
2. 四门逐一定位：bench JSON + 真机日志分层归因（每门给归因结论+量级）
3. 逐门修复（源码改动走 Edit/Write，每步即 commit，中文 message「919 」开头）
4. bench 复测（`go run ./benchmarks/experience -json`），回阈或达标路径+量级依据
5. 允许 PARTIAL 收口：部分门治理 + 部分门达标路径，但每门必须有归因与量级

## 验收

- bench 复测四门回阈，或给出达标路径 + 量级依据（能实测就实测；不能实测标注推演）
- 测试：改动面相关包级测试全绿；预存红对照申报
