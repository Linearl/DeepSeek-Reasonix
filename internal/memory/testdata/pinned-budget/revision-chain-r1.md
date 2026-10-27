---
id: mem-fb5110495f77401f8f7a49b947762408
revision: 1
created_at: "2026-08-17T16:36:00.0000000Z"
updated_at: "2026-08-17T16:36:00.0000000Z"
name: open-source-option-research-method
title: 开源方案跨框架调研模式（gh api 检索 + 移植来源对比）
description: 用 gh api search/repositories 找跨框架同类方案、按 star 排序、查原项目 README 追溯实现来源；适用于"某个思路在各 agent 框架的落地方案"调研
metadata:
  type: feedback
  scope: global
  activation: relevant
  volatility: stable
---

## 开源方案跨框架调研模式（2026-08-17 实战沉淀）

**Why:** 做 reasonix-deepseek-anchor 时，用户要求文案里给"其他 agent 框架用户参考的同类方案"。需要快速找出 DeepSeek V4 "首轮 Minimal 工具 → promote 后全量" 这个思路在 DeepSeek Harness/pi/OpenCode 等的落地实现。

**How to apply:**

1. **GitHub API 检索优先于搜索引擎**：
   ```bash
   # 按关键词+stars 排序（代理可用时）
   export https_proxy=http://127.0.0.1:10808 http_proxy=http://127.0.0.1:10808
   curl -s "https://api.github.com/search/repositories?q=%22anchored-standard%22&sort=stars&per_page=8" \
     | python -c "import sys,json; d=json.load(sys.stdin); [print(i['full_name'],'★',i['stargazers_count'],'-',(i.get('description') or '')[:80]) for i in d.get('items',[])]"
   ```
   - 优点：返回 full_name + stars + description 一屏看全，比搜网页快且准；`sort=stars` 直接排序
   - 技巧：精确短语用 URL 编码引号 `%22...%22`；尝试多个关键词（框架名 + 思路名）

2. **查原仓库 README 追溯实现来源**：同一思路的多个项目往往彼此移植，读源头 README 能确认谁是"原版"、各自的适配点（例：`xiaobright/dsh-anchored-standard` 3.2k★ 是 DeepSeek Harness 原版，`kxh4892636/pi-deepseek-anchor` 是 pi 移植，我们再把 pi 版移植到 Reasonix）。用 curl 直连 raw.githubusercontent.com（配代理）。

3. **结果落地到文案/文档时**：注明"Star/活跃度以你看到时为准"，不把检索当静态事实；每个框架给 1 个代表方案即可，不必列全。

4. **关联**：[[paper-research-methodology]]（论文领域调研，偏会议/时效性）；本文案偏"开源代码/跨框架落地方案"，两者互补。
