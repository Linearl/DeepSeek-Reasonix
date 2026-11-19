---
description: "文档保鲜审计——子代理审计报告示例（已脱敏）"
update_time: 2026-07-01
---
# 深度分析审计报告 — batch-01-architecture (示例)

> 本文件为示例报告，内容已脱敏。实际输出格式与此一致。

## Doc-1: `2.tech/architecture/01-总体架构.md`

### P0 事实错误

| 编号 | 行号 | 文档描述 | 实际代码 | file:line 证据 |
|------|------|----------|----------|---------------|
| P0-01-001 | L45 | 全局限流 5000/15min | 实际 100/15min | `backend/configs/config.yaml:12` |
| P0-01-002 | L52 | 登录限流 10/15min | 实际 5/15min | `backend/configs/config.yaml:18` |

### P1 描述过时/不准确

| 编号 | 行号 | 文档描述 | 实际代码 | file:line 证据 |
|------|------|----------|----------|---------------|
| P1-01-001 | L78 | PostgreSQL 端口 7000 | docker-compose 默认 5432 | `docker/docker-compose.yml:25` |

### P2 建议优化

- [P2] L120: 建议补充 Docker 镜像版本表

## 验证通过的声明

- [x] React 18 + Vite 5 + TypeScript — 与 `webapp/package.json:12,18` 一致
- [x] 三端口（5280/5281/4000）— 与 `config/platform.config.json` 一致
- [x] 9 张数据库表名 — 与 `backend/src/database/migrations/` 一致

---

## Doc-2: `2.tech/architecture/04-重试机制设计.md`

### P0 事实错误

| 编号 | 行号 | 文档描述 | 实际代码 | file:line 证据 |
|------|------|----------|----------|---------------|
| P0-04-001 | L172 | `chunkConcurrency` 值为 4 | 实际为 3 | `backend/configs/config.yaml:45`, `webapp/src/hooks/useUpload/chunk.ts:28` |

### P1 描述过时/不准确

| 编号 | 行号 | 文档描述 | 实际代码 | file:line 证据 |
|------|------|----------|----------|---------------|
| P1-04-001 | L88 | 核心函数 `uploadFileChunked()` | 实际为 `uploadFile()` + 内联 while 循环 | `webapp/src/hooks/useUpload/uploadEngine.ts:156-210` |

---

## 汇总

| 文档 | P0 | P1 | P2 |
|------|----|----|----|
| 01-总体架构.md | 2 | 1 | 1 |
| 04-重试机制设计.md | 1 | 1 | 0 |
| 05-nginx反向代理架构.md | 0 | 0 | 1 |
| 06-模块间通信拓扑.md | 0 | 0 | 1 |

**审计时间**: 2026-05-29 | **代理**: opus 深度分析 | **验证断言**: 40+ 条
