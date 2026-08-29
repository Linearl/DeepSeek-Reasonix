#!/usr/bin/env node
// Fork integrity checklist — run after EVERY upstream merge (docs/upstream-merge-checklist.md).
//
// Why: git auto-merge has silently dropped fork-only CSS blocks three times
// (#9222 group styles 194 lines, #9221 color filter 42 lines, --fg-faint
// token swap) with no conflict markers and no build errors — only runtime
// breakage. This script greps every fork-only feature marker (CSS class, TS
// symbol, Go symbol) that must survive a merge and fails loudly when any is
// missing. Exit 0 = all checks pass; exit 1 = list of missing markers.
//
// Usage: node scripts/check-fork-integrity.mjs   (from the repo root)

import { readFileSync, existsSync } from "node:fs";
import { join } from "node:path";

const ROOT = process.cwd();

// Each entry: feature label, repo-relative file, patterns that must all
// appear in that file. Add a row whenever a fork-only feature lands.
const CHECKS = [
  // ── CSS（auto-merge 静默丢块高发区）──────────────────────────────
  { feature: "#9222 项目分组 CSS", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__group", ".project-tree__group-count", ".project-tree__group-caret"] },
  { feature: "#9221 颜色筛选 CSS", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__color-filter", ".project-tree__color-opt", ".project-tree__color-swatch", ".project-tree__action-btn--active"] },
  { feature: "#9221 颜色筛选锚点", file: "desktop/frontend/src/styles.css", patterns: [".project-tree__color-filter {\n  position: relative;"] },
  { feature: "heartbeat 编辑器样式", file: "desktop/frontend/src/custom/features/heartbeat/heartbeat.css", patterns: [".heartbeat-editor__model-override", ".heartbeat-editor__input"] },

  // ── 前端 TS ─────────────────────────────────────────────────────
  { feature: "#9221 颜色筛选 TSX", file: "desktop/frontend/src/components/ProjectTree.tsx", patterns: ["colorFilter", "renderColorFilterControl", "project-tree__action-btn"] },
  { feature: "#9222 分组 TSX + 持久化", file: "desktop/frontend/src/components/ProjectTree.tsx", patterns: ["collapsedGroups", "loadProjectGroupCollapsed", "dropProjectGroupCollapsed"] },
  { feature: "#9518 分组计数后端权威", file: "desktop/frontend/src/components/ProjectTreeOrganization.tsx", patterns: ["memberCount", "group.topicIds?.length ?? 0"] },
  { feature: "projectGroups 存储层", file: "desktop/frontend/src/lib/projectGroups.ts", patterns: ["loadProjectGroupCollapsed", "persistProjectGroupCollapsed", "dropProjectGroupCollapsed"] },
  { feature: "#9580 草稿持久化存储层", file: "desktop/frontend/src/lib/composerDraftPersistence.ts", patterns: ["composer:drafts:v1", "pagehide", "MAX_PERSISTED_BYTES"] },
  { feature: "#9580 草稿 v2 接入", file: "desktop/frontend/src/components/Composer.tsx", patterns: ["loadPersistedComposerDraft", "persistComposerDraft", "persisted.pastedBlocks.map"] },
  { feature: "#9570 Markdown 门控放宽", file: "desktop/frontend/src/components/MarkdownHistory.tsx", patterns: ["markerInView", "MARKDOWN_TAIL_BLOCKS) {"] },
  { feature: "#9567 接管钉尾", file: "desktop/frontend/src/components/Transcript.tsx", patterns: ["handleSurfacePaintReady", "stick.current = true"] },
  { feature: "#9521 TPS chip", file: "desktop/frontend/src/components/ToolCard.tsx", patterns: ["tok/s"] },
  { feature: "#9521 TPS 状态字段", file: "desktop/frontend/src/lib/useController.ts", patterns: ["tokensPerSec"] },
  { feature: "#9468 reload fallback", file: "desktop/frontend/src/lib/useController.ts", patterns: ["loadOlderHistory"] },

  // ── Go 后端 ─────────────────────────────────────────────────────
  { feature: "#9572 摘要安全前缀", file: "internal/agent/compact_projection.go", patterns: ["trigger != CompactionTriggerManual", "maximumSafeSummaryPrefixEnd"] },
  { feature: "#9572 Unknown 网关回退", file: "internal/agent/compact_projection.go", patterns: ["a.lastAdmission().ObservedWindow > 0 || a.contextWindow > 0"] },
  { feature: "#9592 P0 只读/管理豁免", file: "internal/agent/tool_write_coordination.go", patterns: ["!plan.effects.WorkspaceMutation && !parentWriteGuardTarget(plan.runTool.Name())"] },
  { feature: "#9592 P2 hook 声明", file: "internal/hook/hook.go", patterns: ["MutatesWorkspace *bool"] },
  { feature: "#9592 P2 细化判定", file: "internal/hook/runner.go", patterns: ["h.MutatesWorkspace != nil && !*h.MutatesWorkspace"] },
  { feature: "#9070 heartbeat 模型覆盖", file: "desktop/heartbeat.go", patterns: ["heartbeatModelRef", "SetModelForTab(tabMeta.ID, ref)"] },
  { feature: "#9522 P1 预览入 job buffer", file: "internal/agent/subagent_progress.go", patterns: ["attachJobOutput", "writeJobPreview"] },
  { feature: "#9522 P1 完成摘要", file: "internal/jobs/jobs.go", patterns: ["resultDigestOf", "result begins"] },
  { feature: "#9522 P2 task_id 续跑/引导", file: "internal/agent/task.go", patterns: ["steerBackgroundTask", "steerSlots", "subagentSteerHookFromContext"] },
  { feature: "#9521 TPS 采样", file: "internal/agent/subagent_progress.go", patterns: ["progressRateSampler", "TokensPerSec"] },
  { feature: "#9520 上下文预算行", file: "internal/agent/context_budget_block.go", patterns: ["context-budget", "approaching auto-compaction"] },
  { feature: "#9520 系统提示契约", file: "internal/config/config.go", patterns: ["ContextManagementPolicy"] },
  { feature: "#9526 task 后台引导", file: "internal/agent/task.go", patterns: ["Do not sleep or poll for progress"] },
  { feature: "#9566 截断参数修复", file: "internal/agent/run_loop.go", patterns: ["repairTruncatedToolCallArgs"] },
  { feature: "#9564 kill_shell 非变更分类", file: "internal/evidence/classify_profile.go", patterns: ["kill_shell"] },
  { feature: "写协调体系 #9111", file: "internal/agent/tool_write_coordination.go", patterns: ["parentWriteGuardTarget", "reserveCoordinatedParentWrite"] },
  { feature: "乐观写 #9213", file: "internal/agent/tool_write_coordination.go", patterns: ["optimisticWrite"] },

  // ── 构建配置 ────────────────────────────────────────────────────
  { feature: "release notes 存在", file: "release-notes/FORK-v1.33.0.md", patterns: ["Fork 修复"] },
  { feature: "wails 版本号", file: "desktop/wails.json", patterns: ["1.33.0"] },
];

let failed = 0;
const results = [];
for (const check of CHECKS) {
  const path = join(ROOT, check.file);
  if (!existsSync(path)) {
    failed += 1;
    results.push(`MISSING FILE  ${check.file}  (${check.feature})`);
    continue;
  }
  const content = readFileSync(path, "utf8");
  const missing = check.patterns.filter((p) => !content.includes(p));
  if (missing.length > 0) {
    failed += 1;
    results.push(`MISSING       ${check.file}  (${check.feature})\n  patterns: ${missing.map((p) => JSON.stringify(p)).join(", ")}`);
  } else {
    results.push(`OK            ${check.feature}`);
  }
}

for (const line of results) process.stdout.write(line + "\n");
process.stdout.write(`\n${CHECKS.length - failed}/${CHECKS.length} checks passed.\n`);
if (failed > 0) {
  process.stdout.write(
    "\nFork-only features are missing from the working tree — likely dropped by an\n"
    + "upstream auto-merge (no conflict markers, no build errors). Recover from the\n"
    + "feature's origin commit (git log --all --oneline -- <file>) and re-run.\n",
  );
  process.exit(1);
}
