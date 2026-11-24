# 门禁元数据盘点台账

> 政策与判级标准：`docs/gate-metadata-retirement.md`（tl1756）。本台账只记逐轮盘点结果与拍板记录。
> 清零率 = 累计已退役 ÷（累计已退役 + 当前存量）；退役数在拍板执行后于下一轮盘点更新。
> 盘点工具：`scripts/gate-metadata-audit.mjs`（只读，不是门禁；`--with-tsc` 为全量非增量深验，与门禁同参同解析器）。

---

## 轮次 1（2026-11-23，wt-tl1756 首轮试点）

盘点执行点：分支 `feat-tl1756` @ `1025eb668d`（基点 `baeb97db2f`，起点 6dd0591243=main-v2-stable tip）；执行人 tl1756 实施线。

### 1. tsc 豁免（desktop/frontend/scripts/tsc-test-baseline.json）

- 存量：163 文件 / 226 对 file+错误码（批量登记 2026-10-10，任务 745）。
- 深验：已完成（全量非增量 `tsc --noEmit --pretty false -p tsconfig.test.json`，解析复用门禁导出的 `parseDiagnostics`）。
- 判级：仍需要 154 文件｜含 stale 候选 9 文件 10 对｜悬空 0｜待深验 0。

**可退役候选清单（候拍板，只列不删；逐对拍板前建议修一处核一处）**：

| # | 文件 | 豁免对 | 关联重构波证据（欠账大概率已还清的成因） |
|---|---|---|---|
| 1 | `src/__tests__/collab-inbox-degraded.test.tsx` | TS2741 | 协作信箱类型面近期多轮改动（tl1238 后续链） |
| 2 | `src/__tests__/collab-inbox-failure.test.tsx` | TS2741 | 同上 |
| 3 | `src/__tests__/collab-inbox-panel.test.tsx` | TS2741 | 同上 |
| 4 | `src/__tests__/composer-stop-unknown.test.tsx` | TS6133+TS6192 | tl1765(修一) `bfc85d7c16` 整文件重写，76/76 绿 |
| 5 | `src/__tests__/external-opener.test.tsx` | TS6133 | 任务1100 `f0a47035ac` 重写 topicbar 面 |
| 6 | `src/__tests__/project-tree-organization-races.test.tsx` | TS2339 | tl1149b 返修② `0d9f0a0ced` 语义适配重写 |
| 7 | `src/__tests__/provider-image-input.test.tsx` | TS2322 | tl1424e-4 `eb746b1fdb` 显式清钉（b 类 2 钉清零件） |
| 8 | `src/__tests__/task603-tool-optimizations.test.ts` | TS2305 | tl1487七组 `f8ff88b791` pathRules 整链退役 |
| 9 | `src/__tests__/transcript-viewport.test.tsx` | TS2304 | 预存红清理② `b5b553ddba` 重写修复，53/53 三连绿 |

**拍板请求 A**：若确认退役，从 `entries` 删除上表 10 对并同步 `_meta.pairs` 226→216（门禁「条目只减不增」语义不变）。

### 2. fork-integrity 判例（scripts/check-fork-integrity.mjs CHECKS）

- 存量：847 条 / 1927 锚 / 432 文件（任务书口径 856 为任务创建时点数字）。
- 机械判级：绿 842｜锚失效 5｜文件缺失 0。
- 钉龄（登记行 git blame）：0-29 天=69｜30-89 天=778｜90-179=0｜≥180=0｜未知=0；最大钉龄 86 天 = 判例库爆发登记期（2026-08 下旬）起点，与 QA 报告「57→847 持续涨」一致；**「钉龄>180 天半年人工审计」条款首轮无对象**。

**门禁红 5 条人工判定（证据齐）**：

| # | 判例（文件/缺失锚） | 判定 | 证据 |
|---|---|---|---|
| 1 | #9572 摘要安全前缀（compact_projection.go 缺 `trigger != CompactionTriggerManual`） | **换代候选**（功能在，锚过时） | tl1719 `4f23aadec3` 把 13 处 manual 类分支归一为 `IsManualClassTrigger`；新形态在场 L1328/1354/1488 |
| 2 | 任务530 boot 接线（boot.go 缺 `sessionCollabEnabled(cfg) && cfg.Agent.SessionCollabReplyNudge`） | **换代候选** | tl1741 有意撤 `sessionCollabEnabled` 门（boot.go L4365 注释「随撤开关删除——跨会话协作无条件启用」）；回信提醒本体在 L2356 `if cfg.Agent.SessionCollabReplyNudge` |
| 3 | 任务325 设置默认值前置校验（settings_app.go 缺 `autopilot requires the yolo approval mode`） | **退役候选**（有意删除） | tl1741 `3ae58d0e54`（用户令实验室瘦身，64 文件 −2749 行）整函数删除 `SetDesktopAutopilot` 含该校验；残留面：前端 `SettingsPanel.tsx` L1176 与 `task325-autopilot-yolo.test.ts` L55 仍保留对该已删拒绝文案的匹配——拍板退役时顺带核对 |
| 4 | 任务562 前端档位镜像（experimentTiers.ts 缺 `unstable: 3`） | **换代候选** | 现值 9/51/26/4/0=90；tl1718③ `132b188fc8` 计数 89→90 时漏更 fork-integrity 锚（commit 声明的「计数钉同步」只覆盖了测试面基线钉） |
| 5 | 任务562 验收测试（task562 test 缺 9/50/25/3 计数断言；L170 现值 9/51/26/4） | **换代候选** | 同上 |

**拍板请求 B**：退役 #3 一条（从 CHECKS 删除；如拍板保留残留面清理则另立小件）。
**拍板请求 C**：换代 #1/#2/#5 三条改锚（建议新锚：①`IsManualClassTrigger(trigger)`；②`if cfg.Agent.SessionCollabReplyNudge {`；③L170 现值断言 9/51/26/4/0）；#4 一条建议**改稳定锚形态**（如 `retired: 0,` 所在的档位表结构锚）或维持计数锚但绑定同步纪律——计数锚已是**同类第三次假告警复发**（前判例 `016c10d33d`），建议二选一候拍板。

### 3. 人工判定抽样（15 条，fork 钉龄最旧）

抽样=钉龄最旧 15 条（`--sample=15`，全为 86 天档；2 条无出处标签归未知钉龄桶机制外备注）：#9222 分组 CSS、#9221 颜色筛选 CSS/锚点、heartbeat 编辑器样式（无出处）、#9518 计数后端、projectGroups 存储层（无出处）、#9580 草稿持久化/v2、#9521 TPS chip/状态字段、#9468 reload fallback、#9592 P2 hook/细化、#9070 heartbeat 模型覆盖、#9520 系统提示契约。
判定：**15/15 仍需要**（浅判口径：锚全绿 + 出处可溯 + 功能仍为 fork 活面）；深判（功能价值层）门槛钉龄>180 天，首轮无对象。

### 4. 清零率（本轮起算）

| 源 | 存量 | 累计退役 | 清零率 |
|---|---|---|---|
| tsc 豁免 | 226 对 | 0 | 0.00% |
| fork-integrity 判例 | 847 条 | 0 | 0.00% |

### 5. 本轮机制发现（不改动，候拍板/记档）

1. **562 计数锚假告警同类第三次复发**（`016c10d33d`→本轮×2 条）——建议 `docs/upstream-merge-checklist.md` 升档步骤补一条「晋升/退役提交必须 grep fork-integrity 中本任务计数锚」，或改锚避计数（二选一，候拍板）。
2. **「修债忘删豁免」是实测漂移模式**：tsc 10 对候选全部对应近期重构波的修复（tl1765/tl1424e-4/tl1487/预存红清理② 等），豁免层没人回头清——机制价值实证。
3. **钉龄语义再确认**：判例库主体 90 天内登记，「半年钉龄审计」条款要到 2027-02 下旬才首次有对象；blame 钉龄随单行换代重置（机制文档 §3 已声明，非本轮偏差）。

### 6. 拍板回执区（拍板后填写）

- [ ] 请求 A（tsc 10 对退役）：待拍板 → 执行后 here 记录 + 下轮复跑清候选。
- [ ] 请求 B（任务325 锚退役）：待拍板。
- [ ] 请求 C（4 条换代改锚+#4 形态选择）：待拍板。
- [ ] 发现 1（升档 checklist 补锚同步）：待拍板。
