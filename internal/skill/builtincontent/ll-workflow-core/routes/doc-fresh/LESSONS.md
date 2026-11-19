---
description: "文档保鲜审计——踩坑经验教训（持续追加）"
update_time: 2026-07-01
---
# 文档保鲜审计——经验教训

> 持续追加。新增条目放在最前面。格式：**现象 → 根因 → 规避**。

---

## 2026-05-29: 代理响应时间差异大，batch 并行时 review 可能遗漏

**现象**: run#000005 batch-03 的 sonnet-structural 代理耗时 774s（~13min），而 haiku 仅 150s。reviewer 在 11/12 报告完成时启动，缺失的 1 份报告在 review 完成后 7min 才到达。

**根因**: 不同模型、不同文档复杂度的代理响应时间差异可达 5 倍（haiku 150s vs sonnet 774s）。`max_batch_parallel` 加速整体但无法消除单代理的尾部延迟。

**规避**:
1. 【强制】Phase 4（review）必须在**所有 batch 的 4/4 报告齐全**后才启动，不等齐会遗漏发现
2. 【建议】Phase 3 完成后用 `ls batch-*/` 统计缺失报告数，缺失 > 0 时禁止进入 Phase 4
3. 【建议】对超时（>600s）的代理：先检查报告是否已写入（可能已写完但通知延迟），再决定是否恢复或替换

**经验**: 并行优化解决的是吞吐量问题（12 代理 3 batch 并行 vs 顺序），但尾部延迟是无解的——总耗时由最慢的那个代理决定。

---

## 2026-05-29: Windows 定时任务缺少脚本路径

**现象**: 定时任务 Action 的 Argument 中只传了 `--repo-root` 和 `--audit-root`，没有传 Python 脚本路径。python 收到参数但无脚本可执行。

**根因**: `setup-schedule.ps1` 中 `$ScannerScript` 已验证存在但未拼入 `$action.Argument`。

**规避**: Action 格式应为 `python "绝对路径/check-doc-freshness.py" --repo-root "..." --audit-root "..."`。

---

## 2026-05-29: 子代理输出路径漂移

**现象**: 代理将报告写入 `docs/8.test/reports/04-auto_audit/...` 或 `docs/4.misc/...`，而非 `04-auto_audit/results/...`。

**根因**: 相对路径在不同代理的工作目录下解析不一致。opus 倾向于将 `04-auto_audit/` 理解为 `docs/` 的子目录。

**规避**: `output` 和 `repo_root` 字段强制使用绝完整路径。模板中不再使用 `{run_dir}` 拼接。

---

## 2026-05-29: 去重逻辑误用扫描 SHA

**现象**: 首次扫描 83 篇全部被标记为 `recurring_skipped`，实际仅 8 篇曾被 AI 审计。

**根因**: `state.json` 中 `git_sha` 是扫描时写入的，去重逻辑用它代替 `audited_sha`（审计后写入），导致"被扫描过"="被审计过"。

**规避**: 引入 `audited_sha` + `audited_at` 字段，仅在 `--mark-audited`（Phase 6）时写入。TTL 窗口（默认 14d）防止永久死锁。

**经验**: 扫描（stale 判定）和审计（内容验证）是两个独立阶段，状态字段必须区分 `scanned_at`/`scanned_sha` 和 `audited_at`/`audited_sha`。

---

## 2026-05-29: opus 代理不写文件

**现象**: 约 1/3 的 opus 代理完成代码探索后不写文件，或写入 0 字节。haiku/sonnet 无此问题。

**根因**: 未确定。可能是 opus 在大量 Read/Grep 后上下文耗尽，忘记写文件这一最后步骤。

**规避**: 
1. Phase 3 完成后检查每个报告文件大小 > 0
2. opus 超时 180s → SendMessage 恢复；再超时 60s → 换 sonnet
3. 连续失败 3 次 → 主对话直接处理

**经验**: opus 适合深度分析但需要可靠性兜底。关键 batch（如首次审计）可考虑 sonnet×4 全量，opus 作为补充。

---

## 2026-05-29: 多代理并行超 API 限流

**现象**: 4 代理 + 主对话 = 5 并行，超出 API key 限制（4），部分调用卡死。

**规避**: 主对话 + 后台代理总数 ≤ 4。派 4 代理时先派 3 个，等 1 个完成后派第 4 个。

---

## 2026-05-29: 空扫描结果残留目录

**现象**: `results/` 下累积大量仅含 `scan-result.json`（stale=0）的空目录。

**规避**: 审计完成后清理仅含 `scan-result.json` 无 batch 子目录的结果目录。

**TODO**: 扫描脚本增加 `--skip-if-clean` 选项，0 stale 时不创建目录。

---

## 2026-05-29: 审计 prompt 展开太长

**现象**: 主对话每次派代理都需要 15+ 行 prompt（~500 tokens），32 代理 × 500 = 16K tokens 浪费在重复文本上。

**根因**: 将完整审计任务描述内联在 Agent prompt 中，每次调用重复传输。

**规避**: 改为"字段列表 + 模板路径引用"格式（7 行，~80 tokens）。模板文件放在 `prompts/` 下，子代理启动后自行读取。

**经验**: 凡反复调用的代理任务，提示词应模板化，调用时只传变量。
