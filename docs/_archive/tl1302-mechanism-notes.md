# tl1302 研究笔记（中间产物）——Global 泄露机制源头深查

日期：2026-10-31 ｜ 只查不修 ｜ 现场取证时点：21:00-21:30（应用 reasonix-desktop v1.38.3-20261031-1812 运行中，PID 18928）

## 1. 写入面全景（L1 = desktop-projects.json）

收点（topic_registry.go）：registerTopic / unregisterTopic，守卫测试 topic_registry_write_guard_test.go。
守卫检测字段：`.GlobalTopics =` `.GlobalPinnedTopics =` `.DeletedTopics =` `.Topics =` `.PinnedTopics =` —— **不含 `.GlobalGroups` / `.TopicIDs`（分组成员面在守卫盲区）**。

### A. 经收点的调用方（1262/1283 已覆盖）
| # | 调用点 | scope 来源 |
|---|---|---|
| 1 | tabs.go:3442 NewTopic 新建 | scopeForTopicRoot(workspaceRoot) |
| 2 | tabs.go:6461/6465 indexNewTopicForSidebar（CreateTopic，含 heartbeat CreateTopic） | 同上 |
| 3 | tabs.go:7468/7490 ensureTopicIndexedState | 调用方传入，1283 resolveTopicIndexTarget 改道 |
|   | - app_modes.go:548/583（新会话/首 turn） | tab.Scope |
|   | - tabs.go:4930（tab build 修复链） | tab.Scope |
|   | - tabs.go:7738（restoreSessionTopicIndex） | meta.Scope→改道 |
|   | - tabs.go:8012（renameTopic） | UI 传入 |
| 4 | fork_tab.go:209 fork 补登 | project 固定 |
| 5 | topic_legacy_migration.go:175 迁移批量 | 物理目录推导（global 两目录/项目目录） |
| 6 | topic_legacy_migration.go:328 修复扫描批量 | 同上 + otherWorkspaceSidebarIndexHoldsTopic(1095 防影子) |

### B. 不经收点的 L1 写手（守卫盲区/第二实现）
| # | 写手 | 说明 |
|---|---|---|
| 7 | desktop 分组成员写口：project_tree_organization.go:680/762/869/1038、manage_sessions.go:464 | GlobalGroups/Groups.TopicIDs 直接 append，无归属校验；守卫不扫 |
| 8 | serve 侧 internal/organization（FileTopic/SaveSessionGroups/…）→ organization.UpdateFile | **第二套独立的 desktop-projects.json 写实现**：无收点、无归属校验、不同步 topic-registry 索引、自带 sidecar 写手；宿主 desktop/servepool_host.go 同进程同配置目录 |

### C. 派生数据写入方（不写 L1，但决定渲染/再生）
| # | 写手 | 说明 |
|---|---|---|
| 9 | **global root 标题状态表**（desktopTopicState root=""，Roaming/reasonix/desktop/topic-state-v1.sqlite + global/desktop-topic-titles.json 遗留桥） | setTopicTitleWithSource/createTopicState(root="") 的全部调用方；**orderedTopicIDs 把它当注册列表补全** |
| 10 | catalog v8.sqlite：SyncMetadata（喂入含标题补全）+ ReconcileDirectory/IndexSessionPath（meta 归属） | 清理仅删 metadata_present=0 且无 session 支撑行 |
| 11 | pinNewEmptySessionBranchMeta（tabs.go:3638） | 空 sessionPath 分支：scope=global 时空壳文件建于 global 会话目录、meta.Scope=global |
| 12 | saveTabSessionMetaSnapshot（tabs.go:9209） | 目录纠偏**不对称**：文件在项目目录→强制 project；在 global 目录→放行 tab.Scope |
| 13 | session_collab.go:444 collab 创建 | m.Scope=请求 scope |
| 14 | topic_legacy_migration.go:143 迁移写 meta | 按目录 scope 改写 meta |

### 关键读点（再生机制的两个入口）
- **orderedTopicIDs(explicit, titleMap)**（tabs.go:6722）：**把标题表里有、注册列表没有的 id 全部 append 到尾部**。使用点：
  1. syncSessionCatalogMetadata.appendTopics（session_catalog.go:344）→ SyncMetadata 喂入 → **catalog_topics (global,T) 永远 metadata_present=1**（真根因渲染燃料）
  2. indexedBlankTopicIDLocked（tabs.go:3710，EnsureBlankTab 复用）→ 从标题表挑空白 topic 复用 → **空壳再生面**
- recordFromOrder（sessioncatalog/reconcile.go:226）：meta.TopicID 非空→按 meta.Scope/Root 建行；空→按目录。

## 2. sidecar 与项目 topic 列表的权威关系（步骤②结论）

- 成员 SSOT = desktop-projects.json（GlobalTopics / Projects[].Topics）。sidecar（desktop-project-tree-organization.json）只承载排序/分组/revision 投影（「旧版本不可重写的持久副本」）。
- 方向：save 时 projects→sidecar（organizationFromProjectsFile）；load 时 sidecar→仅排序投影（applyPersistedTopicOrder 只过滤/重排现有成员，**不增员**，store.go:273-295 实证）。
- 结论：sidecar 不是泄露源。desktop 与 internal/organization 是同一对文件的**两套平行实现**（类型不同、行为相近、写手不同）。

## 3. 现场考古（步骤④，7 会话逐一）

现状核对（21:16 版 desktop-projects.json）：**7 条全部不在 globalTopics**；6 条在 ai-passport project.Topics、1 条在 cooking project.Topics；globalGroups 无一命中。**L1 已净。**

catalog v8.sqlite：7 条全部存在 `scope=global` 的 catalog_topics 行且 **metadata_present=1**（21:15:10 SyncMetadata 刚跑过仍=1 → 喂入仍含它们）；global scope metadata_present=1 共 **163** 条 = globalTopics(156) + 7（差值恰为泄露 7 条）。

global 标题状态（root=""）7 条残留全部 `title_source=manual`、`created_at=0`（纯标题记录）：

| 会话 | topic_id | global 标题写入(本地时) | 空壳文件 | 会话本体 |
|---|---|---|---|---|
| 烹饪复盘分享-续 | topic_20261022-074604_db35032a8b35b723 | 10-22 16:08 | 20261023-060524.344349700-session.jsonl（0 字节，meta.Scope=global，rev=1 从未更新，10-23 06:05） | cooking 项目 collab（10-22 15:46） |
| 剧情核对 | topic_20261025-152234_8ceb9db3490b506f | 10-27 14:42 | 20261027-082459.297801600-session.jsonl（0 字节，meta.Scope=global，rev=1，10-27 16:24 本地） | ai-passport collab ×2（10-25） |
| 赤途·ch02狩猎者重构 | topic_20261027-145936_d9892c251ea72e4d | 10-27 23:01:55 | 无 | ai-passport collab（10-27 14:59，meta.Scope=project rev=51 活跃） |
| 赤途·批2批3产物审查 | topic_20261027-145949_4dbe7a990f6db955 | 10-27 23:01:55（同分钟） | 无 | 同上族 |
| 赤途·批1产物审查 | topic_20261027-145959_d5d4e16f3c7f6383 | 10-27 23:02:19（同分钟） | 无 | 同上族 |
| 赤途·ch02狩猎者重构v2 | topic_20261027-175312_d98982751a59eb03 | 10-28 01:53 | 无 | ai-passport collab（10-27 17:53） |
| 赤途·全量画面OCR管线 | topic_20261028-002624_5e207d395ebbe30d | 10-28 08:26 | 无 | ai-passport collab（10-28 00:26，rev=655 高活） |

- ai-passport 项目侧 topic-state（projects/d--1.workspace-ai-passport/desktop/topic-state-v1.sqlite）存在且正确（10 条含 6 个泄露题）——**global 侧是残留副本，不是唯一副本**。
- desktop.log 滚动卷仅覆盖 ~10-28 之后（24 卷 × ~24h），标题写入事件多在保留期外；retarget 日志（1283 Info）在现存卷 0 条。
- 空 shell meta 实证：`"scope":"global","topic_id":<项目会话 topic>,"content_digest":e3b0c44…`（sha256 of empty=0 字节）、revision=1。
- heartbeat：26 任务中 20 个 scope=None（→heartbeatRunScope 强制 global）；赤途守护任务绑定的是心跳自产 global 会话（合法）。1283 遗留项实锤仍在。

## 4. 根因链（结论）

1. 误钉时代（≤10-28，早于 1262/1283 到机）：global 侧打开链/修复链/重钉链把 7 个项目会话的 manual 标题写进 **global root 标题状态表**，其中 2 个还在 global 会话目录留下 0 字节空壳（meta.Scope=global）。
2. 1262/1283 到机后把 **L1 注册列表收敛干净**（现FileInfo 证实生效）——但收敛面=列表三面（topics/pinned/groups），**从不触碰标题状态表，也不移除空壳文件**。
3. `orderedTopicIDs(ids, 标题表)` 把标题表当注册列表补全：①SyncMetadata 喂入 → catalog (global,T) 行每 30s 刷新 metadata_present=1 → Global 区（catalog 驱动）永久渲染 7 条；②EnsureBlankTab 复用候选 → 新空壳再生面。2 个存量空壳同时让 catalog_sessions(global,T) 行永存（SyncMetadata 清理豁免有 session 支撑的行）——双燃料。
4. 结论：**列表层修复无效复发的真根因 = 标题状态表是未纳管的第四注册面 + orderedTopicIDs 把它提升为影子 SSOT**。标题残留不清+空壳不移，任何列表层修补都无法让 Global 区停止渲染。

## 5. 覆盖矩阵缺口清单（对照 1262/1283）

| 写入/再生面 | 1262 | 1283 | 缺口 |
|---|---|---|---|
| L1 列表（收点 8 调用方） | ✓ 同事务收敛 | ✓ 拦截+改道 | （冷机索引 miss 时 redirect 不生效，funnel filter 读活文件兜底→双保险成立） |
| 分组成员 TopicIDs（desktop 5 口） | 仅 project 登记时清 | ✗ | 写口无归属校验；守卫不扫 .GlobalGroups/.TopicIDs |
| serve 侧 organization 第二实现 | ✗ | ✗ | 绕收点/绕索引/绕守卫；同文件第二写手 |
| 标题状态 global root（第 4 注册面） | ✗ | 仅改道后的新流跟随 | **存量残留零清扫**；未改道流（open fallback、EnsureBlankTab 复用）仍写 |
| orderedTopicIDs 标题补全（feed/复用两读点） | ✗ | ✗ | **真根因**：影子注册表 |
| global 目录空壳文件 | ✗（只删无 session 孤儿 catalog 行） | meta 触碰自愈（meta 层） | 文件永不移动→catalog_sessions 行永存→清理豁免 |
| saveTabSessionMeta 目录纠偏不对称 | ✗ | ✗ | global 目录放行 tab.Scope 写回 meta.Scope=global |
| heartbeat scope=None→global | ✗ | ✗（明示遗留） | 20/26 任务 None |
