---
description: "文档保鲜审计——Phase 5 最终汇总模板"
update_time: 2026-07-01
---
# 审计汇总模板 (summary.md)

## 用途

Phase 5 主对话读取所有 reviewer 的复核报告后，生成最终汇总。

## 模板

```markdown
---
description: "文档保鲜审计汇总 — {date} run#{seq}"
---

# 文档保鲜审计汇总 — run#{seq}

**时间**: {scan_time} | **版本**: {dev_version} | **范围**: docs/2.tech/**/*.md ({total} 文件)
**阈值**: {threshold_summary} | **审计**: {audited_count} 文档 × 4 代理 → {reviewer_count} reviewer

## 流程完整性

| 阶段 | 产出 | 状态 |
|------|------|------|
| scan | scan-result.json | ✅ |
{batch_rows}
| review | review-*.md ({M} 份) | ✅ |
| summary | summary.md | ✅ |

## 审计结果

| 判定 | 文档 | P0 | P1 | P2 |
|------|------|----|----|----|
| **stale** | {doc} | {p0} | {p1} | {p2} |
| **partial** | {doc} | {p0} | {p1} | {p2} |
| **current** | {doc} | {p0} | {p1} | {p2} |

## P0 清单（{total_p0} 项）

1. **{title}** — {description}（共识 {N}/4）
2. ...

## 自适应阈值更新

| 文档 | 审计结果 | 乘数变化 | 有效阈值 |
|------|---------|---------|----------|
| {doc} | current | {old}x→{new}x | {base}h→{effective}h |
| {doc} | stale | 1.0x (重置) | {base}h |

## 下一步

### 立即修正
1. {action}

### 近期修正
1. {action}

详见: review-*.md | 配置: config/document-freshness.yaml
```
