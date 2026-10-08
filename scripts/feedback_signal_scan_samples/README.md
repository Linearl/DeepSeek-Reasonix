# feedback 信号扫描留出样本回归集（任务 344-D）

`feedback_signal_scan.py --self-check` 的固定样本集。**判据/白名单每次改动必跑**：
负样本必须被拦下（误报=0），正样本必须出草稿。持续机制，非一次性。

文件名约定：`<期望>-<序号>-<说明>.<扩展名>`，期望 ∈ `noise`（白名单噪音）｜
`observe`（挂观察，未达阈值）｜ `draft`（应产出信号草稿）。扩展名 `json` 走
crash-pending 分类器，`log` 走 crash-fatal（0 字节判据）。

## 样本清单与来源

| 文件 | 期望 | 来源 |
|---|---|---|
| `noise-01~05-shutting-down.json` | noise | C-20260920-02 六条负样本中的 5 条 `phase=shutting_down`（09-19 22:47 ~ 09-20 08:05，v1.38.3-20260919-2210~20260920-1005）。**原件已被消费未留档，按候选池记载的证据重建**，字段结构取自真实样本 |
| `noise-06-healthy.json` | noise | C-20260920-02 第 6 条 `phase=healthy`（09-20） |
| `noise-07-wedged.json` | noise | 09-29 复查新增的 `phase=wedged` 新形态（字段逐字取自真实样本 `1790679556750025000-18052-1.json`） |
| `noise-08-empty-fatal.log` | noise | crash-fatal 0 字节空 log（C-20260920-02 记载的持续噪音模式） |
| `observe-01-webview2.json` | observe | `windows.webview2.process_failed`（09-24 13:42 真实新 label，单条证据不足挂观察，≥3 次才升草稿——任务 377 口径） |
| `draft-01-unknown-label.json` | draft | 白名单未覆盖的未知 label——必须出草稿（证明扫描器不是只报白名单） |
| `draft-02-nonempty-fatal.log` | draft | 非空 fatal log = 有证据价值的真信号 |

另有两类纯函数用例内置于脚本 `--self-check`（不需 fixture）：perf 告警行解析
（`PERF_ALERT_RE` 命中/不误配普通行）、recovery 副本命名匹配
（`-recovery-<16hex>` 命中/普通会话名不误配）。

## 维护

- 白名单收紧/放开（如 #10634 追齐核对后 abnormal_exit.v2 出白名单）→ 改
  `classify_crash_json` 后必跑 `--self-check`，样本期望值同步更新。
- 新增已知噪音模式 → 先加负样本 fixture 再改判据（TDD 顺序）。
- 规则版本号在脚本 `RULE_VERSION`，指纹含该版本，回归基线按版本区分。
