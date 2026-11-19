---
description: "文档保鲜审计汇总示例——敏感路径已脱敏"
update_time: 2026-07-01
---
# 文档保鲜审计汇总 — run#000003 (示例)

**时间**: 2026-05-28 | **版本**: dev_11 | **范围**: docs/2.tech/**/*.md (92 文件)
**阈值**: 1d 演示 | **审计**: 8 文档 × 4 代理 → 1 reviewer

## 流程完整性

| 阶段 | 产出 | 状态 |
|------|------|------|
| scan | scan-result.json | ✅ |
| batch-01-api | opus-deep + sonnet-structural + haiku-quick + sonnet-devil (4/4) | ✅ |
| batch-02-architecture | opus-deep + sonnet-structural + haiku-quick + sonnet-devil (4/4) | ✅ |
| review | review-01.md | ✅ |
| summary | summary.md | ✅ |

## 审计结果

| 判定 | 文档 | P0 | P1 | P2 |
|------|------|----|----|----|
| **stale** | 01-前端API.md | 3 | 2 | 1 |
| **stale** | 04-错误码规范.md | 2 | 1 | 0 |
| **partial** | 02-后端API.md | 0 | 2 | 1 |
| **current** | 03-Docker服务.md | 0 | 0 | 0 |

## P0 清单（5 项，4/5 获 3/4+ 共识）

1. **文件管理路径错误** — dir→mkdir, delete→/ 等 5 处 (4/4 共识)
2. **phantom 函数** — fetchMarkdownFilePreview 不存在 (3/4 共识)
3. **VALIDATION_ERROR 矛盾** — 文档范例 vs 实际用法 (3/4 共识)
4. **chunkConcurrency 误差** — 文档写 4 实际为 3 (3/4 共识)
5. **services/ 目录覆盖率低** — 文档 9 个子域, 实际 30+

## 自适应阈值更新

| 文档 | 审计结果 | 乘数变化 | 有效阈值 |
|------|---------|---------|----------|
| 03-Docker服务.md | current | 1.0→2.0× | 336h→672h |
| 其他 7 文档 | stale/partial | 1.0× (重置) | 基准值 |

详见: review-01.md | 配置: config/document-freshness.yaml
