# Fork 与上游差异台账（FORK-vs-upstream）

> **维护规则**：每个 fork 版本一张表，列出相较上游的全部改进点；若某改进**已被上游吸收**，在"上游吸收状态"列注明（吸收版本/PR），避免重复维护或误判仍为 fork 独有。
> 新版本发布时：在台账**追加新表**，并在该版 release note 中引用本文件（`release-fork.yml` 模板已内置此要求）。
> 数据源：各版 `FORK-vX.Y.Z.md` release notes、上游 release 页（esengine/DeepSeek-Reasonix）、PR/issue 台账。

---

## v1.38.3（2026-09-09 追齐 1.38.2~1.38.3，283 commits）

| 改进点 | fork 状态 | 上游吸收状态 |
|---|---|---|
| 图片能力判定：官方 DeepSeek 端点 provider 层保守（仅固定 vision SKU 直接可用），逐模型 override 驱动 wire 门控与能力解析器 | ✅ fork 语义（无 SKU 硬编码） | ⏳ 上游改为「非 text 模型即继承 vision」，fork 不采用（会让相似名静默继承） |
| GLM 强度档（low/medium/high/max → `reasoning_effort`） | ✅ fork 移植到上游 `applyReasoning` | N/A（上游 GLM 仍二元 thinking） |
| 旧推理档位别名迁移（`medium`/`xhigh` → `high`） | ✅ fork 保留 | ✅ 上游新增 `migrateStoredDeepSeekEffort` |
| 状态栏默认值 `text` | ✅ fork 保留 | ⏳ 上游改为 `icon` + 一次性升级 |
| jobs 拆包（`jobs`/`start`/`runtime_state`/`artifacts`/`evidence`） | 🔄 采用上游结构 + 保留 `resultDigest`/TPS/rate | ✅ 上游原生 |
| `#9221` 项目颜色筛选 | ✅ 移植到上游拆分的 ProjectTree | N/A（fork 独有） |
| `#9222` 项目分组 | 🔄 采用上游 `ProjectTreeGroupRows` + `useProjectTreeOrganization` | ✅ 上游等价实现 |
| `#9567` 接管钉尾 | ✅ 按上游 kernel 适配（`setScrollMode("tail-follow")`） | N/A（fork 独有） |
| merge 静默丢失的 13 个文件（11 agent 测试 + 2 Topicbar 组件） | ✅ 已恢复 | N/A（merge 冲突取删除侧所致） |
| fork 独有 `goalSubmit.ts` | ✅ 已恢复 | N/A（上游删除该模块） |
| App.tsx 组合边界（`check-app-entry-contract`） | 🔄 fork 显式豁免 `FORK_MONOLITH_APP` | ⏳ 上游要求 <200 行 + 组合 AppRuntime |
| 构建预算（deferred CSS / locale chunk） | ✅ 按实测重定（122.0 / 66.0 / 66.5 KiB） | N/A（上游自身上调到 120.4） |

## v1.38.1（2026-09-08 整体追齐）

| 改进点 | fork 状态 | 上游吸收状态 |
|---|---|---|
| 超长会话压缩死锁双修复（thinking 摘要空 + GLM 无数字超窗） | ✅ fork 修复 | ✅ 上游已合并（#9882） |
| 分段压缩并行化（~4× 提速）+ fragment 超窗半切 | ✅ fork 增强 | ⏳ PR #9885 OPEN |
| Ask 重放循环修复（#9693 对齐） | ✅ 取上游原生 | ✅ 原生 |
| catalog 大小写双目录账目错位修复 | ✅ 数据+代码 | ⏳ 上游 path_identity 为不同方案 |
| 幻影引用 TopicbarMoreMenu 清理 | ✅ fork 修复 | N/A（fork 残留） |
| overlay store 类型修复（SettingsInitialFocus） | ✅ fork 修复 | N/A（fork 增强） |
| AppRuntime 大重构适配 | 🔄 阶段 1.5 | N/A |

### v1.38.1 实测回归修复（2026-09-08 二批，commit b28cf270f / 6ea488bbe / 1f8c3fe50 / a99b4303e）

| 问题 | 根因 | 修复 |
|---|---|---|
| 7 项 UI 回归（未分组/乐观写入/会话授权写目录/子代理委派/本地服务页/搜索历史/经典风格选项） | merge 时 fix-i18n-keys.py 用 Title-Case 英文 fallback 覆盖 fork 文案值（185 个）+ 15 个 keys 被删 | 回填 fork 旧值 + 恢复 keys（三语言） |
| 项目栏上方按钮跟上游 | merge 用上游 TopicbarSessionActions 替换 fork TopicbarMoreMenu 三件套 | 恢复 fork 三件套，回退接线 |
| 经典（classic）布局选项消失 | 上游 1.38 删除选项，fork 类型/渲染仍在 | 恢复 options 数组 |
| styles.css 丢 topicbar__more / recovery-copies 样式 | merge 取上游 | 恢复 6 块 |
| **恢复副本异常增生 + 对话分叉**（10 分钟 8 副本、单会话 110 副本文件、desktop.log 17 次 diverged） | resume 时多条 leading system（fresh prompt+memory 段）不落盘，`messagesWithoutLeadingSystem` 只剥 1 条 → `CloneWithMessagesIfCompatible` 判不兼容 → fallback `NewSession` **丢持久化基线** → 每轮 checkpoint 判 diverged → fork recovery branch | `messagesWithoutLeadingSystem` 剥全部连续头部 system（对齐投影层语义），resume 继承基线走正常 CAS |
| TestWritableHooksReserveWholeParentWorkspace 失败（预存） | fork 提前实装的 #9592 排除段与上游 v1.38.1 最终设计冲突 | 回退排除段；**#9592 fork 提前实装作废，以上游为准** |
| TestMalformedToolArgsReturnHostValidationContract 失败（预存） | fork `repairTruncatedToolCallArgs` 把完整但非法的 args 一律修成 `{}`，吞掉上游验证纠错契约 | 只修真截断（闭合未终止 string/括号+去尾逗号），完整非法原样放行 |
| 11 个孤儿测试破坏 agent 包构建 | 引用未移植实现（#9521/#9522 等） | `git rm`（内容存 *.go.hold，移植实现后恢复） |

### v1.38.1 封版批次（2026-09-09，commit 57e8e98bc / 5c048217b）

| 问题 | 根因 | 修复 |
|---|---|---|
| **经典风格仍不持久化**（用户实测） | `ApplyUserConfigUpgradesOnStartup`（pricing.go:284）启动迁移把 `layout_style="classic"` 强写 `workbench`（上游 1.38 下架经典风格），fork 保留 classic 选项 → 每次启动被抹 | 移除该迁移段（fork 分歧点），测试改断言 classic 保留；此前 `862438ec7` 的读侧 overlay 修错了层 |
| **自定义模型图片开关被锁**（deepseek-v4.1 等新 SKU，用户实测） | 两层 SKU 硬编码：① capability resolver 对官方 deepseek 非 vision-exp 一律标「协议限制」+ `EnableAllowed=false`（前端走此分支）② wire 层 `DeepSeekImageInputAllowed` 固定 SKU 门控（openai/anthropic/responses 三个构建点共用） | ① 删除 resolver 特判 ② wire 层改为 fork 语义：已知纯文本（flash/pro）硬禁 + vision-exp 保留无 metadata 默认 + **其余模型信任 capability metadata 与用户 override** ③ 删除前端 deepseek.com fallback。原则：桌面版发布永远落后官方 API，不硬编码 SKU 挡新模型 |
| 模型列表添加后未落盘（v4.1） | 编辑器 deferred save（添加只改表单 state，需再点表单「保存」）；叠加「活跃会话时保存被拒」的误导 | 模型列表 dirty 时显示未保存提示（en/zh/zh-TW）；「活跃会话禁止改配置」记入跟踪项待评估 |

## v1.34.0（2026-08-31，合并上游 v1.33.0→v1.34.0；2026-09-07 重发布追加）

| 改进点 | 来源 PR | 上游吸收状态 |
|---|---|---|
| GLM 思考强度档暴露 | #9642 | fork 独有 |
| 搜索历史提问面板样式恢复 | #9218 | fork 独有 |
| 健康会话无法关闭/归档修复 | #9617 | fork 独有 |
| 交付验收框间歇缺失修复（ctx 竞态） | #9601 | fork 独有 |
| 超 1M 会话压缩失败 ContextLimitError→chunked 回退 | — | fork 独有（与上游 #9572 同根因家族） |
| 目标评估器空响应暂停（boundedllm 忽略 ChunkReasoning） | #9679 系 | 🔄 已提上游 PR #9679（OPEN） |
| 拖拽排序自动复位修复（manual 序渲染权威源回归 desktop-projects.json，catalog 只供条目数据） | — | fork 独有（2026-09-07 补） |
| GLM preset 深度思考档（bridge） | — | fork 独有 |

---

## v1.33.0（2026-08-29，合并上游 v1.32.1→v1.33.0→main-v2 bba8f8eb6）

| 改进点 | 来源 PR | 上游吸收状态 |
|---|---|---|
| 历史加载 reload fallback | #9468 | fork 独有 |
| kill_shell 终止持锁子代理 | #9564 | fork 独有 |
| MiMo 截断响应污染历史 HTTP 400 | #9566 | 🔄 已提上游 PR #9679（OPEN） |
| 会话改名左侧树不同步（#8280 残留） | — | fork 独有 |
| 项目分组 classic/creation 布局渲染（#9222 完善 + CSS 恢复） | #9222 | fork 独有 |
| 活跃 turn 流式对齐（逐项滚动，不再三块复用） | #9565 | ✅ 已合入上游 main-v2（f6e639c7b） |

## v1.32.0（2026-08-29，合并上游 v1.32.0）

| 改进点 | 说明 | 上游吸收状态 |
|---|---|---|
| （本版以吸收上游为主：远程 SSH 工作区、持久有序回合、对话导航） | 保留 fork 全部增强 | 上游已吸收我们 5 个 PR：#9158/#9374/#9376/#9155/#9032 |

## v1.31.4（2026-08-27）

| 改进点 | 来源 PR | 上游吸收状态 |
|---|---|---|
| 实时引导（耗时工具执行期间可继续对话） | — | fork 独有 |
| MiMo 推理档位自动检测 | — | fork 独有 |
| serve 多项目会话浏览（GET /projects）+ 图片上传（POST /attachments） | — | fork 独有（GC/移动端依赖） |
| 压缩后自动刷新持久记忆 | #9376 | ✅ 上游合并（v1.32.0 吸收） |
| 已授权写目录管理 + 全局公共目录 | #9167 → #9174/#9175 | fork 独有 |
| 项目分组 + 项目颜色筛选/排序 | #9222/#9221 | fork 独有 |
| 损坏 branch-meta 自动重建 | #9155 | ✅ 上游合并（v1.32.0 吸收） |
| 17 项体验修复（混合命令误拦、bundle 预算、shell contract、归档死循环、GFM 流式渲染等） | — | fork 独有（个别随后续版本随上游演进） |

## v1.31.3（fork 独立版起点）

| 改进点 | 来源 PR | 上游吸收状态 |
|---|---|---|
| 子代理三档委派（保守/均衡/激进，会话级持久化 + CLI 斜杠命令） | #9004 | fork 独有 |
| 并行写入安全检查（路径级并行 + 期望值校验） | #9213 #9111 | fork 独有（#9111 改进思路已被 #9137 部分吸收） |
| 长会话压缩阈值可降至 30%、保留量随阈值联动（DeepSeek 缓存涨价对策） | #9158 | ✅ 上游合并（v1.32.0 吸收） |
| 切换会话无感、秒开（#9016 家族） | #9016 | fork 独有 |
| 分组内归档单会话 / 残留后台任务不卡归档 / 恢复失败不复活+强制归档 | #9374 系 | 部分 ✅（#9374 已随 v1.32.0 吸收；归档体验细节为 fork 增强） |
| 会话滚动稳定 / 提问定位条不密集 | — | fork 独有 |

## v1.30.0（2026-08-20，基于上游 v1.30.0）

| 改进点 | 来源 PR | 上游吸收状态 |
|---|---|---|
| 项目分组标题字号 | #9032 | ✅ 已合入上游（desktop-v1.30.0，merge 3f70dca6） |
| 写入冲突按路径判定（吸收 #9107/#9111 思路） | #9137（SivanCola 实现） | ✅ 上游实现（原始 PR credit 保留） |

## v1.28.0（2026-08-18，基于上游 v1.28.0）

| 改进点 | 来源 PR | 上游吸收状态 |
|---|---|---|
| （无独立改进项——累计/issue 贡献，仅致谢） | — | — |

## v1.25.4（2026-08-16，基于上游 v1.25.4）

| 改进点 | 来源 PR | 上游吸收状态 |
|---|---|---|
| 项目树拖拽排序 | #8608 | 上游已含（功能由 SivanCola #8958 整合实现，release notes 仍 credit 本 PR） |
| 项目内会话分组折叠 | #8699 | 同上（#8958 实现） |
| 折叠项目活跃指示器 | #8598 | 同上（#8958 实现） |

## 服务端（serve/servepool）专属能力（多版本累计）

| 能力 | 说明 | 上游吸收状态 |
|---|---|---|
| servepool 多实例管理 + 网关（cookie 认证、421 修复、stale 端口清理） | #8995 家族 fork 增强 | fork 独有 |
| 会话所有权生命周期（takeover 协议、心跳 90s 自动释放、GC/远程端让渡） | 2026-09-06/07 | fork 独有 |
| release-session `to` 参数可选（纯释放语义） | 2026-09-06 | fork 独有 |
