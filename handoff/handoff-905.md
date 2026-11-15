# 任务 905 交接：MigrateV2 迁移无快照不可回滚（上游 11846 / PR11857）

- **分支**：`wt-905`（基于 `main-v2-stable` @ 6280772ea），2 个提交，`git status` 净。
- **状态**：**完成**。触发面确认为非零 → 已实施修复+单测，包级测试全绿。

## 切片 commit

1. `7a043c01a` 落实施计划 handoff-905.md
2. `6d92b759d` MigrateV2 迁移前写 .bak 快照 + 4 条单测（本文件随后收尾提交）

## 缺陷陈述

上游同款：`MigrateV2()`（v1→v2 memory 迁移）就地重写 legacy memory 文件，无备份快照；迁移中途失败时旧数据已被覆盖，不可回滚。
fork 锚：`internal/memory/store_v2.go:40-70` `writeMemoryAtomic(path, render(...))` 就地重写。
语义权威：筛查报告 PR11857（上游修复=迁移前写 `.bak` 快照，改动面约 20 行）。

## 计划（架构前提先行）

1. **触发面确认**（先做，决定走修复还是销项）：
   - grep 全仓 `MigrateV2` 调用链：谁调用、什么条件触发（启动路径？首次检测 v1 格式？显式命令？）。
   - 读部署语义：fork 部署中 v1→v2 迁移面是否还存在——
     - fork 是否改写过 memory 存储格式/路径（v1 文件是否可能出现在现部署）；
     - 是否已有迁移完成标记 / 版本检查使 v1 文件不再触发迁移；
     - 现网部署态：用户库是否已全部迁完（迁移是否一次性已消化）。
   - **触发面为零 → 验证后销项报告**（不硬造修复）：调用链证据 + 现部署态证明，落 `docs/report/zcode交付/`。
   - **触发面存在 → 实施修复**：迁移前写 `.bak`（迁移成功后保留；失败时可回滚），补单测（迁移后 `.bak` 存在且与原内容字节一致 + 失败路径回滚）。
2. 每步即 commit；提交中文 message「905 」开头，显式路径。
3. **不 merge 进 main-v2-stable**，合并由用户指定会话执行。

## 关键事实（触发面确认结果：**存在，走修复**）

- **调用链**：全仓唯一调用点 `internal/boot/boot.go:878` —— 每次 boot 构建系统提示前无条件执行 `memory.StoreFor(config.MemoryUserDir(), root).MigrateV2()`；迁移失败仅 warn 不阻断启动。
- **触发条件**（`store_v2.go:72-74`）：memory 目录下任一 `.md` fact 文件，frontmatter **缺 `id` 或 `revision<=0`** 即被 `writeMemoryAtomic(path, render(...))` 就地重写（v1→v2）。已含 id+revision 的文件被 skip（幂等）。
- **触发面实证（2026-10-15 扫现部署库）**：`C:\Users\yinji\AppData\Roaming\reasonix\memory\global` + `projects/*/memory` 共 **548 个 fact 文件，1 个仍为 v1 旧格式**：`projects\C--Users-yinji-OneDrive-workspace-codex_test - 2\memory\prefer-chinese-thinking.md`（frontmatter 只有 name/title/description/metadata，无 id/revision；metadata.type=user）。下次在该项目 root 触发 boot 即被就地重写——**触发面非零**。
- **触发面结构性可重现**（非一次性）：v1 格式可随时再进入 memory 目录——用户手写 fact 文件、多机同步（reasonix-sync/OneDrive）回灌旧文件、旧备份恢复。且任何无 id/revision 的 `.md`（含 loadMemory 能解析的最小 frontmatter 或无 frontmatter 文件）都会触发。上游 issue 11846 同理成立（上游修法=迁移前持久化原始字节，备份失败则拒绝迁移）。
- **上游 PR11857 语义**（WebFetch 核过）：新增 `internal/state/memory/migration_backup.go`；备份由 store 层负责、**不进 revision history**；**备份失败则拒绝迁移**（原始字节持久化后才重写）；保留精确原始字节（CRLF/未知键/原文件名）；幂等、中断重试保留首份拷贝。
- **fork 锚**：`store_v2.go:62-90` 迁移循环 `writeMemoryAtomic(path, []byte(render(memory, memory.Name)), 0o644)` 直接覆盖 `path`，`raw`（原始字节）已读到手（67 行）却未留快照。
- **改动面**：迁移循环内重写前写 `<name>.md.bak`（原始 raw 字节）；.bak 已存在则保留首份（幂等+中断重试语义）。约 15 行+单测。

## 测试与红项

- 绿：`go test ./internal/memory/ -count=1` 全包通过（30.8s）；`go vet ./internal/memory/` 干净；`go test -run TestStoreV2` 通过。
- 新增 4 条单测（`store_v2_test.go`）：
  1. `TestStoreV2MigrationWritesByteIdenticalBackup`——CRLF+未知键+无 id/revision 的旧文件迁移后 `.bak` 与原内容字节一致，且用 .bak 可完整回滚（验收「迁移前字节可恢复」）；
  2. `TestStoreV2MigrationBackupFailureRefusesRewrite`——备份路径被目录占用（备份写失败）→ 迁移拒绝执行、原文件一字未动（验收「迁移失败可回滚」）；
  3. `TestStoreV2MigrationRetryKeepsFirstBackupCopy`——已有 .bak 时保留首份拷贝（中断重试不覆盖迁移前快照，对齐上游幂等语义）；
  4. `TestStoreV2MigrationBackupStaysOutOfReaders`——.bak 不进 List/Index（所有读取路径按 .md 后缀过滤）。
- 无预存红（memory 包基线本件改动前后均绿）。

## 残留与交接注意

- **现部署库有 1 个待迁移文件**：`projects\C--Users-yinji-OneDrive-workspace-codex_test - 2\memory\prefer-chinese-thinking.md`（v1 旧格式）。下次在该项目 boot 时会被迁移——**修复合入前勿在该项目触发 boot**（或先手工备份该文件）。修复合入后此文件迁移会自动留 .bak。
- 未动主仓 `main-v2-stable`；合并由用户指定会话执行。回收：`git worktree remove ../worktrees/wt-905`（合并后）。
- 上游完整修法是独立 `.migration-backup` 目录+manifest；本件按任务书 ~20 行面采用同目录 `.md.bak` 后缀方案，语义对齐点：备份失败拒绝迁移、保留首份、原始字节精确（raw 直写不经 parse/render）。
