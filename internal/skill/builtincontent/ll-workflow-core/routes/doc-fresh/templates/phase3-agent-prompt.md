---
description: "文档保鲜 AI 审计 Prompt 模板——Phase 3 代理调用流程（未配置，使用时复制到 04-auto_audit/prompts/ 并修改模型分配）"
update_time: 2026-07-01
---
# 文档保鲜审计 —— Phase 3 代理并行审计流程

## 工作流

```
Phase 1: Python 扫描
  → 04-auto_audit/results/{date}-{seq}/scan-result.json

Phase 2: 主对话分组
  读取 scan-result.json → 按前缀分组 → 每组 ≤4 文档

Phase 3: 并行审计（每 batch 4 代理）
  对每个 batch，派出 4 子代理（全部后台运行）:

  | 编号 | 模型 | 角色 | 中文任务描述 | 输出文件 |
  |------|------|------|-------------|----------|
  | A | {model_deep} | 深度分析 | 逐段比对文档与代码，检查所有端点/函数/配置 | {batch}/{model_deep}-deep.md |
  | B | {model_struct} | 结构检查 | 验证文件路径、路由数量、目录结构 | {batch}/{model_struct}-structural.md |
  | C | {model_quick} | 快速扫描 | 检查日期、版本号、端口、硬编码数字 | {batch}/{model_quick}-quick.md |
  | D | {model_devil} | 反面论证 | 寻找文档仍然有效的证据，至少1条/文档 | {batch}/{model_devil}-devil.md |

  ⚠️ 等待策略:
  - 代理在 180s 内未完成 → 用 SendMessage 恢复它
  - SendMessage 后 60s 仍未响应 → 派一个新的同角色代理替代
  - batch 必须等齐 4 份报告才标记完成
  - 主对话按 batch 逐个确认：检查 batch 目录有 4 个 .md 文件 → 通过 → 下一 batch

Phase 4: 并行复核
  所有 batch 完成后，派 reviewer 子代理:
  - 读取所分配 batch 的全部子诊断报告
  - 合并、去重、裁决冲突
  - 写入复核报告: {run_dir}/review-{NN}.md

Phase 5: 主对话汇总
  主对话读取全部 review-*.md → 生成最终 {run_dir}/summary.md

## 目录结构要求（必须检查）

每个 batch 完成后，主对话验证:
```
{run_dir}/batch-NN-{slug}/
  ├── {model_deep}-deep.md          ← 必须存在
  ├── {model_struct}-structural.md  ← 必须存在
  ├── {model_quick}-quick.md        ← 必须存在
  └── {model_devil}-devil.md       ← 必须存在
```

缺少任何文件 → 该 batch 不算完成 → 恢复或替换对应代理。

## 子代理任务描述语言

全部使用{language}。示例:

```
你是文档保鲜审计子代理。
角色: 深度分析。
审计对象: batch-01-{slug}，包含以下 N 个文档:
  1. {dev_docs_path}/2.tech/{category}/01-xxx.md
  ...
请逐段比对文档描述与实际代码，为每个发现注明 file:line 证据。
将审计报告写入: 04-auto_audit/results/{date}-{seq}/batch-01-{slug}/{model_deep}-deep.md
```

## 复核提示词

```
你是文档保鲜复核代理。
负责 batch: {batch_list}
请读取以上 batch 目录的全部子诊断报告，完成:
1. 合并相同发现 → 标注共识数
2. 裁决分歧 → 给出最终判定
3. 输出修正建议（按 P0/P1/P2 优先级排序）
将复核报告写入: {run_dir}/review-{NN}.md
```
