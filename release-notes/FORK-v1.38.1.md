# Reasonix Fork 桌面版 v1.38.1 — Release Notes

> 本版本基于官方 `v1.38.1`，整体追齐上游 1.35~1.38.1 全部演进，并保留全部 fork 增强与修复。数据/会话/记忆目录与官方版完全兼容，**覆盖安装即可，无需迁移**。
> v1.34.0 的会话所有权生命周期、ServePool 稳定性、拖拽排序修复、压缩死锁修复、分段压缩并行化等增强已包含在内（详见 [v1.34.0 Release Notes](https://github.com/Linearl/DeepSeek-Reasonix/releases/tag/desktop-v1.34.0)）。
> **Fork 与上游差异全览**：见 [FORK-vs-upstream.md](./FORK-vs-upstream.md)。

## 使用攻略

- 覆盖安装即可，无需卸载、无需迁移数据。
- 所有 fork 功能入口：**设置 → 权限 / 通用** 或**会话输入框右上角**。

## 概览

**Reasonix Fork v1.38.1 — 整体追齐上游 1.35~1.38.1 全部演进**

上游 5 个版本的架构演进（AppRuntime 组合重构、目录身份键、会话所有权单写入者、压缩管线上游原生 chunked/compact、完成语义重构等）全部吸收。fork 侧同步修复了 catalog 大小写双目录、分段压缩死锁、并完成 4 节功能移植进上游新组合架构。

发布日期：2026-09-08

## 🔁 追齐内容（v1.35.0 ~ v1.38.1）

- **目录身份键（#9618）**：根治 catalog 大小写双目录账目错位（横幅 78/81 卡死）
- **会话所有权单写入者（#9692）**：双 writer 场景的所有权协调
- **Ask 身份围栏 + 提交失败恢复（#9862/#9852/#9842）**：Ask 循环根治
- **压缩管线**：上游原生 chunked /compact（#9632）、summary-request overflow 恢复（#9879）——与 fork 侧 #9878/#9882 双向合流
- **完成语义重构（#9788）**：移除第二验证器
- **Agent 核心简化（#9794）**：planner/continuation 默认关
- **粘性文件固定（#9689）/ 长读取强制（#9708）/ 模型级视觉（#9787）**
- **版本化会话上下文（#9733）**：上下文版本管理
- **Worktree 分叉（#9645）/ 合并回主分支（#9650）**

## 🔧 Fork 修复（本轮）

### 超长会话压缩死锁双修复（2M tokens 实测恢复）
- **思考型模型摘要不再误判为空**：reasoning-only 摘要被正确采用
- **GLM 无数字超窗错误识别**：分段压缩回退正常接管

### 分段压缩并行化（2M 会话压缩提速 ~4×）
- 分块摘要 4 路有界并行 + fragment 超窗自动半切

### Ask 重放循环修复（对齐上游 #9693）
- Ask 卡片确认后循环弹出/切走再切回又弹——已修复

### catalog 横幅常驻修复
- 大小写双目录账目错位（78/81 卡死）已在数据层+代码层修复；横幅只在真实整理动作时显示

### DeepSeek 自定义模型候选列表修复（接入 deepseek-v4.1 等新模型）
- **添加即出现在会话模型切换器**：手动添加的模型此前会被"模型候选管理"面板的旧草稿在保存时抹掉（草稿仅在拉取时刷新，不含手动模型）
- 现在：手动添加的模型始终可见并默认勾选；任何面板保存都不会再丢失手动模型
- 上游 1.38.3 同步修复了自定义模型图片开关（随下一次追齐带入）

### 实测回归三连修（2026-09-09 用户实测反馈）
- **经典风格持久化真正修复**：v1.38.1 起启动配置升级会把 `classic` 强制改写为 `workbench`（上游下架经典风格），导致切换经典后重启必回工作台——fork 保留经典风格，已移除该迁移，切换后重启保持
- **自定义模型图片开关解锁（彻底版）**：移除全部官方 DeepSeek SKU 硬编码——能力解析器此前把 vision-exp 之外的官方模型一律标记「协议限制」，provider 构建层再次硬禁。现在：已知纯文本（flash/pro）保持禁图，**其余模型（含 deepseek-v4.1 与未来官方新模型）由能力探测与用户开关决定**，桌面版落后于官方 API 也不再挡新模型
- **模型列表未保存提示**：provider 编辑表单的模型列表变更后未点「保存」时显示醒目提示，避免误以为添加即生效

### 其它
- 幻影引用 TopicbarMoreMenu 删除（组件从未存在于任何 commit tree——vite/esbuild 不做类型检查所以从未暴露）
- overlay store 类型修复（SettingsInitialFocus 可辨识联合）
- 65 个缺失 i18n keys 补齐×3 语言
- bundle budget 按 fork 实测调整（gzip 469.0 / CSS 118.0 / locale 62.7+63.5 / raw 2510.0）

### 上游反馈
- 压缩根因与修复已同步上游：[issue #9878](https://github.com/esengine/DeepSeek-Reasonix/issues/9878) + [PR #9882](https://github.com/esengine/DeepSeek-Reasonix/pull/9882)（正确性）、[PR #9885](https://github.com/esengine/DeepSeek-Reasonix/pull/9885)（并行化）

## 升级提醒

- 覆盖安装后，**session-catalog v5.sqlite 会自动迁移**（如有 schema 变更）——今日 4 份 bak 备份在手。
- **fork 独有功能**（parkedDelivery、subagentPolicy 设置面板、LocalServerPage）已全部适配上游 1.38 AppRuntime 组合架构。
