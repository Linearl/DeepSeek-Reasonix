# 414 EBDD 中断协议 handoff（wt-414r）——终态：四阶段全部落地

**更新 2026-10-15 12:0x**｜分支 `wt-414r`（基 main-v2-stable `1c632752f`，worktree `github-repo/worktrees/wt-414r`，toplevel 已核）｜**未合并**（按纪律保留支交主对话合并线）

## 阶段进度（全绿）

| 阶段 | 状态 | 产物 |
|---|---|---|
| 残盘盘点 | ✅ 回板 | 1310 救援 wt-414 零丢失；阶段1可移植/阶段2-4零产物（旧支基线落后 801 提交不带入） |
| 1 体验评测集 | ✅ commit `6cb08bafd` | benchmarks/experience 4 项 bench（移植+新基重验：vet/fmt/包级测试全绿、真机 72h PASS/FAIL、-json/退出码1 实测；干净基基线对照无预存红） |
| 2 badcase 闭环 | ✅ 文档 | `docs/handoff/批次整体规划/批次开发计划-汇总/批次阶段门清单-20261015.md`：四级自判+第五项规则+抽核 4/4（890/879/885/811 断言 HEAD 实存、钉原症状；908 在修不入抽） |
| 3 装机二分 | ✅ 文档 | `docs/drafts/装机验证清单-可自动与必须人工二分-20261015.md`：A1-A10 可自动（带脚本/命令）/ B1-B4 必须人工 + 首个替换范例 A9=bench 接装机链；与 909 边界已注明 |
| 4 telemetry 聚合 | ✅ commit `b2d78201f` + 修复 `55de81b0e` | bench 加 M9 WARN 巡检（-warn-patterns/-warn-max-count 可配）+ -trend 周趋势（-trend-worse-pct 可配，慢化浮出，不改退出码）；单测 5+1；README 更新 |

## 阶段 4 附加交付（仓外）

- **heartbeat 周任务已入** `%APPDATA%\reasonix\heartbeat-tasks.json`：`ebdd-weekly-experience`（`168h|weekly:mon@09:40`，enabled，yolo，newConversationEachRun），prompt=定位仓库→找上期 JSON→跑 `-window 168h -json`（+ `-trend 上期`）→中文汇报四态/趋势/WARN→落 `docs/report/ebdd周趋势/ebdd周趋势-<date>.md`；**revision 5809**（改动均递增；文件会被守护进程 Go json 重序列化，`<`→`\u003c` 属正常）。
- **首期周报已落**：`docs/report/ebdd周趋势/ebdd周趋势-20261015.md`（基线期，13 行判定表；证据 `tmp/414-weekly-20261015.json` 为修复后读数）。
- 真机读数另有：`tmp/414r-bench-final.json`（1015 晨 72h，阶段1）、`tmp/414r-bench-s4.json`（72h + -trend 实测，首响 ↑慢化 +12.5% 浮出）。

## 关键教训（接力必读）

1. **WARN 巡检首版静默假 PASS（已修 55de81b0e）**：全量留档时 168h 窗 6.1 万条 `config: unknown key` 噪音按「旧文件优先」先吃满 warnCollectCap=20000，慢 switch 行被挤出池 → 168h n=0 而 72h n=710（矛盾即信号）。修法=采集期按模式过滤 + WarnTruncated 截断显式 FAIL 行。**改动采集/配额类逻辑必须用「大窗 vs 小窗单调性」这类不变量自检。**
2. heartbeat-tasks.json 是并发热点（revision 5804→5809 期间被他会话多次递增）：**编辑前必读最新态，改完必 revision++，改后 json.load 验证**；Edit 用不含 `<`/`>` 的锚串可避开 `\u003c` 转义匹配问题。
3. 上期 JSON 缺失/损坏时 -trend 只 stderr 提示不改退出码——报告里写明即可。

## 残留 / 后续（非本任务范围）

- 支 `wt-414r` **未合并**：tl 414 状态维持「进行中」，合并进 main-v2-stable + tl done 回写由主对话合并线执行（回写依据=merge-base --is-ancestor 核过 HEAD）。
- 真机 4 条体验门 FAIL + WARN 745 条是**存量真实劣化**（boot 热 P95=45s、首响 P95=129.5s 最刺眼），是否立任务治理待用户/主对话定（20261009 交付报告已同问）。
- 阶段 3 的统一收口脚本（check-acceptance 类）归 **909**；本任务二分清单是其一期输入。
- 阶段门清单已被本任务写入批次文档，**批 31 计划引用它**（414/419/909 同窗）——主对话排产时挂一句即可生效。
