# Reasonix Fork 桌面版 v1.38.3-20260917-2030 — Release Notes

> **构建**：2026-09-17 20:2x ｜ **安装目录**：`versions/v1.38.3-20260917-2030/`
> **增量基线**：上一包 **`v1.38.3-20260917-1900`**。本文件**只列相对 1900 的新增/变更**，
> 不堆叠更早包的内容——看更早的历史请沿 `FORK-v1.38.3-20260917-1900.md` 链式回溯。
> 包版本号长期停在 `v1.38.3`（与上游对齐），时间戳只用于区分同版本号的多次构建。

## 本版新增：三线并行开发合并（任务 150 / 23-P1 / 147 / 158 / 159 / 160）

六个任务经 wt-A/B/C 三 worktree 并行开发、零冲突合并、独立审计（五项全 PASS）后入库。

- **任务 150 dream/distill 实测修复**：distill scanner 补 `role="tool"` 与 `tool_calls` 双形态
  （修复前恒 0 提名 → 实测 12 提名）；提名过滤（≥2 种工具且含非通用工具）；dream slug
  多语言保留 + hash 防碰撞；markers 补约束型词（必须/禁止/避免/统一…）。
- **任务 23-P1**：todo 状态机放宽的缺失测试补齐（乱序 completed 接受、消失/回退仍拒、
  storm_breaker 三连击端到端）；P1-d `samePlanStructure` 放宽为「仅搬动已完成项不送审」
  （审计 PASS：骨架身份/相对序不可动，storm_breaker 兜底仍在）。
- **任务 147 设置面板**：busy 收窄为按页 scope + apply 走单一串行队列（防并发 save 互相
  吞掉）；重试按钮解禁；rebuildBusyError 拒绝改为原因 + 已存在入口的可行动提示。
- **任务 158 session-chat 四子项**：sync 等待补 ctx 感知（取消不再睡满 120s）；
  ResolveSessionPath 调用时求值（根治 boot 快照时序——所有自身身份工具受益）；
  create_collab_session 支持跨项目（可选 project 参数，须已注册 root）；delete_session
  dry-run title 补齐（SessionDirectoryTitle 同源单一来源）。
- **任务 159 引导白名单**：补 `running` / `steer_consumed` 两态，guidanceIsDelivering()
  显式分支（禁用 + 原因文案，不制造「点了没反应」）。
- **任务 160 加载更早入口**：默认「加载更早」按钮（追齐上游）；滚动触发改实验开关
  `experimental_auto_load_older`（全链路：config→setter→渲染表→UI，关闭时整体失效）。

## 审计结论（独立审计会话，五项全 PASS 零修复项）

P1-d 语义放宽安全（骨架不可动 + storm_breaker 兜底在）；busy 串行队列无死锁无自等待；
ResolveSessionPath 调用时求值无新时序风险；wheel 监听无泄漏；跨分支合并语义正确
（161 render 四键 / bundle ratchet / SettingsPanel 两特性共存逐点核对）。

## 验证矩阵（构建自检，2026-09-17 19:xx–20:xx）

go build（根 + desktop 两模块）✅ ｜ go test internal/agent **193.7s 全量绿**（合并后）✅ ｜
evidence/skill ✅ ｜ `tsc --noEmit` ✅ ｜ tsx 45+25+9+9+31 全绿 ✅ ｜ check-fork-integrity ✅

## 已知未含

- 任务 150「提炼层换方法」（LLM 归纳层）——定性为独立下一批（150 原文已定性）
- 157（写目录授权热同步 + autopilot 档位）——待用户选档位
- desktop 全量测试 12 红预存失败（provider 定价/官方模板/restart_and_update/项目注册表
  主题，基线对照零新增）——归属已记录，待 owner 清零
