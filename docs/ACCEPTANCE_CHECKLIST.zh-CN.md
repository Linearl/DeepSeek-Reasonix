# 结构化验收清单（任务 909 · 验收自动化一期）

**立 2026-10-17**｜来源：用户 2026-10-15 晨令「我们的验收还不够自动化，需要制定一个清单，然后实现对应项目的自动测试，比如切换 tab 耗时，切换模型/effort 耗时等」。

**目标**：把晨间人工验收（`docs/drafts/验收清单-*.md` 工作区模式）转为「自动测试背书 + 仅留真需人工的项」。本档是**唯一权威清单**：每项验收都必须能回答「机器断言是什么、跑什么命令、人工还剩什么、怎样算过」。

**与既有件的关系**：
- `benchmarks/experience/`（任务 414 EBDD 体验评测集）= 本清单耗时类断言的执行引擎；指标清单见其 `README.md`（M1-M13）。
- 工作区 `docs/drafts/装机验证清单-可自动与必须人工二分-20261015.md`（任务 414 阶段 3）= 装机链 A/B 表；本档的自动断言即 A9 的具体化与扩容。

---

## 一、清单模板（新增验收项时照此追加一行）

| 字段 | 含义 |
|---|---|
| 任务 | tasklist 编号 |
| 验收面 | 一句话说清验什么 |
| 自动断言 | 可执行命令 + 阈值/判据（回归测试、bench 门、探针读数、grep census 均可） |
| 人工步骤 | 机器验不了的残留操作（无则写「无」） |
| 通过判据 | 怎样算过：退出码 0？P95 在门内？人工回「N 通过」？ |

## 二、首批清单（一期，≥5 项，含用户点名三项耗时）

| # | 任务 | 验收面 | 自动断言（命令 + 阈值） | 人工步骤 | 通过判据 |
|---|---|---|---|---|---|
| 1 | 196/909 | **切换 tab 耗时** | `go run ./benchmarks/experience -bench tab-switch -window 168h`：render P95 < 150ms（M6 门）；firstFrame / switch-tab total / 后端 stage 越线普查为 OBSERVE 行 | 无（长会话切 tab 偶发卡顿若 OBSERVE 行恶化再立任务） | 退出码 0 且 render 行 PASS |
| 2 | 602/909 | **切换模型耗时** | `go run ./benchmarks/experience -bench switch-cost`：model fast path `elapsed_ms` P95 < 1500ms（`-switch-fast-max-ms` 可配）；fallback 计数行 OBSERVE | 无 | 退出码 0；model 行 PASS（样本随 909 出包后才从 N/A 转正，见 §四） |
| 3 | 334/148/909 | **切换 effort 耗时** | 同上 `-bench switch-cost`：effort fast path `elapsed_ms` P95 < 1500ms；effort 读取 ≥150ms 慢读 census OBSERVE（639/889 截断样本） | 无 | 退出码 0；effort 行 PASS |
| 4 | 363/414 | **boot 耗时** | `go run ./benchmarks/experience -bench boot -window 168h`：热路径 boot total P95 < 1000ms（M1 门，363①）；冷启动 OBSERVE | 无（装机后首启体感归冷启动 OBSERVE 行） | 退出码 0 且热路径行 PASS/N/A |
| 5 | 363/298 | **打开历史对话耗时** | `-bench open-topic`：合成 P95 < 2000ms（M2 门）；open session failed 0 条（M3 门）；≥2s 慢开 census 0 条（M4 门） | 无 | 退出码 0 |
| 6 | 370 | **发消息→首响** | `-bench first-response`：P95 ≤ 60s（M5 门，遥测分桶上沿） | 无 | 退出码 0 或 FAIL 时带归因回执（首响劣化已有 919/927 归因线，不重复修） |
| 7 | 414 | **WARN 巡检** | `-bench warn-patrol -warn-patterns <模式> -warn-max-count N`（heartbeat 周任务 `ebdd-weekly-experience` 自动跑） | 无 | 周报 PASS |
| 8 | 各任务通用 | **装机链其余自动项**（版本/integrity/heartbeat/panic 扫描等 A1-A8、A10） | 见工作区 `docs/drafts/装机验证清单-可自动与必须人工二分-20261015.md` A 表 | 无 | A 表逐项绿 |
| 9 | 各任务通用 | **必须人工项**（UI 行为可感知面、主观观感、决策拍板、长时程观察） | 无（B 表四类；收窄路径=每任务自带 badcase 回归断言） | 按批追加到工作区 `验收清单-*.md`，明早统一验 | 用户回「N 通过」 |

## 三、一键全跑（装机后验收链一段）

```bash
go run ./benchmarks/experience -window 168h -json acceptance-readings.json   # 全部 6 门；任一越线退出码 1
```

---

## 四、首批读数留档（2026-10-17 05:50，本机 168h 窗，v1.38.3-20261016-2039 装机日志）

| 项 | 读数 | 判定 |
|---|---|---|
| 切 tab render | n=234 P50=41ms **P95=101ms** Max=197ms | PASS（门 150ms） |
| switch-tab summary total | n=204 P50=73ms P95=922ms Max=2835ms | OBSERVE |
| 切换模型 fast path | n=0 | **N/A——装机版本（2039）早于 909 读数打点**；909 出包装机后首次切模型即产样本 |
| 切换 effort fast path | n=1 P50=P95=Max=4ms | PASS（门 1500ms） |
| 切换 fallback 重建 | 0 次 | OBSERVE 正常 |
| effort 读取 ≥150ms 慢读 | n=169 P50=386ms P95=1638ms Max=5838ms | OBSERVE（168h 窗累计；条数升高需归因，历史归因见 889/928） |

JSON 留档：`tmp/909-switch-cost-readings.json`、`tmp/909-tab-switch-readings.json`。

## 五、人工残留项（明确列出，机器不背）

1. **UI 行为正确性**（折叠/拖拽/双入口/文案三态等）：无机器 oracle，逐批进工作区 `验收清单-*`。
2. **主观观感**（卡片醒目度、顺眼程度）：用户审美判定，一次通过即留档同批不重复验。
3. **决策拍板类**：不是验证，走用户决策流。
4. **长时程现场观察**（静置 N 小时类）：优先哨兵日志机器化，纯观察项保留。

## 六、增量维护（交接说明）

**规则：每任务交付时，验收行里必须补一条自动断言——哪怕只是「go 包级测试 -run TestXxx 覆盖」。**

1. **新任务立项/交付时**：往 §二表追加一行。断言优先级：
   ① 既有 bench 门已覆盖（引用 M 编号即可，如切 tab 类直接引用 M6）；
   ② 需要新打点 → 仿 909 接线（Go 侧 slog 行 + elapsed_ms/计数 → collect.go 收样本 → benchSwitchCost 类门 + 测试钉样本契约）；
   ③ 纯行为回归 → 写成 go 测试，自动断言栏填 `-run TestXxx`。
2. **阈值出处必须锚 tasklist 验收**（无出处时标注锚点与余量理由，仿 `defaultSwitchFastMaxMS` 注释）。
3. **读数类断言的三态纪律**：PASS/FAIL/N/A 明示——无样本是 N/A 不是 PASS（老日志无打点字段时绝不冒 0 计入，见 `switch_cost_909_test.go` 负面用例）。
4. **Bench 指标清单同步**：加门必须同步 `benchmarks/experience/README.md` 指标表（M 编号顺延）。
5. **人工项不消亡原则**：自动化只压缩人工面，不吞掉「真需人工」的判断——B 表四类见 §五。
