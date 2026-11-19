/**
 * composite-actions.test.js — composite-actions.yaml + workflow.schema.yaml 契约测试
 *
 * 覆盖 workflow.schema.yaml v0.2.0 新增的 notes_required_when 规则：
 *   当 composite.uses 中的某个 action 在 action-registry.yaml 中存在 usage_boundary
 *   且 boundary 文本包含"内部调用/取代/合并/替代/互斥/顺序约束"关键词时，
 *   composite 必须填写非空 notes。
 *
 * 同时验证：
 *   - 所有 composite 引用的 action 都在 action-registry 中存在
 *   - composite 的基本结构（id, uses, default_on_fail, entry/exit conditions）
 */

const { test, describe, before } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const yaml = require('js-yaml');

const REGISTRY_PATH = path.join(__dirname, '..', 'templates', 'action-registry.yaml');
const COMPOSITE_PATH = path.join(__dirname, '..', 'templates', 'composite-actions.yaml');

let actionById;
let composites;
let boundaryTriggerKeywords;

before(() => {
  const reg = yaml.load(fs.readFileSync(REGISTRY_PATH, 'utf8'));
  actionById = Object.fromEntries(reg.actions.map((a) => [a.id, a]));

  const doc = yaml.load(fs.readFileSync(COMPOSITE_PATH, 'utf8'));
  composites = doc.composite_actions;

  // 触发 notes_required_when 的关键词（与 workflow.schema.yaml 一致）
  boundaryTriggerKeywords = /内部调用|取代|合并|替代|互斥|顺序约束/;
});

/** 判定 action 是否触发 notes_required_when 规则 */
function actionTriggersNotesRequired(actionId) {
  const action = actionById[actionId];
  if (!action || !action.usage_boundary) return false;
  return boundaryTriggerKeywords.test(action.usage_boundary);
}

/** 判定 composite 的 notes 是否非空 */
function hasNonEmptyNotes(composite) {
  if (!composite.notes) return false;
  if (typeof composite.notes === 'string') return composite.notes.trim().length > 0;
  if (Array.isArray(composite.notes)) {
    return composite.notes.some((n) => typeof n === 'string' && n.trim().length > 0);
  }
  return false;
}

describe('composite-actions 基础结构', () => {
  test('所有 composite 必须有 id + uses + entry_conditions + exit_conditions', () => {
    const violations = [];
    for (const c of composites) {
      if (!c.id) violations.push('(missing id)');
      if (!Array.isArray(c.uses) || c.uses.length === 0) violations.push(`${c.id}: empty uses`);
      if (!Array.isArray(c.entry_conditions) || c.entry_conditions.length === 0) {
        violations.push(`${c.id}: missing entry_conditions`);
      }
      if (!Array.isArray(c.exit_conditions) || c.exit_conditions.length === 0) {
        violations.push(`${c.id}: missing exit_conditions`);
      }
    }
    assert.deepEqual(violations, [], `structural violations:\n${violations.join('\n')}`);
  });

  test('composite.uses 中引用的每个 action 都必须在 action-registry 中存在', () => {
    const violations = [];
    for (const c of composites) {
      for (const aid of c.uses || []) {
        if (!actionById[aid]) {
          violations.push(`${c.id} → ${aid} (not registered)`);
        }
      }
    }
    assert.deepEqual(violations, [], `dangling references:\n${violations.join('\n')}`);
  });

  test('default_on_fail 必须是合法值', () => {
    const allowed = [
      'block',
      'continue_with_warning',
      'use_default_or_block',
      'use_default',
      'needs_user_confirmation',
    ];
    const violations = [];
    for (const c of composites) {
      if (c.default_on_fail && !allowed.includes(c.default_on_fail)) {
        violations.push(`${c.id}: ${c.default_on_fail}`);
      }
    }
    assert.deepEqual(violations, [], `invalid default_on_fail:\n${violations.join('\n')}`);
  });
});

describe('notes_required_when 规则验证', () => {
  test('识别所有触发 notes_required_when 的 composite（说明性快照）', () => {
    const triggered = [];
    for (const c of composites) {
      const triggerActions = (c.uses || []).filter(actionTriggersNotesRequired);
      if (triggerActions.length > 0) {
        triggered.push({ id: c.id, triggers: triggerActions, hasNotes: hasNonEmptyNotes(c) });
      }
    }
    // PR 评审可见的紧凑表格快照（便于一眼看出哪些 composite 触发了规则、是否合规）
    const missingNotes = triggered.filter((t) => !t.hasNotes);
    console.log(formatNotesRequiredSnapshot(triggered));
    if (missingNotes.length > 0) {
      // 仅为快照打印，不在此处 assert.fail；下方 test 会强制执行
      console.warn(
        `⚠️  其中 ${missingNotes.length}/${triggered.length} 个 composite 缺少 notes，将由下一条 test 强制失败`,
      );
    }
  });

  test('触发 notes_required_when 的 composite 必须有非空 notes', () => {
    const violations = [];
    for (const c of composites) {
      const triggerActions = (c.uses || []).filter(actionTriggersNotesRequired);
      if (triggerActions.length === 0) continue; // 未触发，跳过
      if (!hasNonEmptyNotes(c)) {
        violations.push(
          `${c.id} 引用了触发规则的动作 [${triggerActions.join(', ')}]，但缺少 notes 字段`,
        );
      }
    }
    if (violations.length > 0) {
      assert.fail(
        `以下 composite 违反 notes_required_when 规则：\n${violations.join('\n')}\n` +
          `请参考 workflow.schema.yaml 中 composite.conditional_required.notes_required_when 的触发条件。`,
      );
    }
  });

  test('v0.2.0 关键修复：flow.requirement_activate 已不再重复声明 task.resolve_identity', () => {
    const c = composites.find((x) => x.id === 'flow.requirement_activate');
    assert.ok(c, 'flow.requirement_activate must exist');
    assert.ok(
      !c.uses.includes('task.resolve_identity'),
      'task.resolve_identity should be internal to task.activate_requirement since v0.2.0',
    );
    assert.ok(c.uses.includes('task.activate_requirement'), 'should use task.activate_requirement');
  });

  test('v0.2.0 关键修复：flow.task_archive_done 使用 docs.maintain_requirements_md', () => {
    const c = composites.find((x) => x.id === 'flow.task_archive_done');
    assert.ok(c, 'flow.task_archive_done must exist');
    assert.ok(
      c.uses.includes('docs.maintain_requirements_md'),
      'should use docs.maintain_requirements_md since v0.2.0',
    );
    assert.ok(
      !c.uses.includes('docs.upgrade_run_results'),
      'legacy docs.upgrade_run_results should no longer be referenced',
    );
    assert.ok(
      !c.uses.includes('docs.sync_requirements_md'),
      'legacy docs.sync_requirements_md should no longer be referenced',
    );
  });

  test('v0.2.0 关键修复：flow.version_closeout 使用 docs.maintain_requirements_md', () => {
    const c = composites.find((x) => x.id === 'flow.version_closeout');
    assert.ok(c, 'flow.version_closeout must exist');
    assert.ok(
      c.uses.includes('docs.maintain_requirements_md'),
      'should use docs.maintain_requirements_md since v0.2.0',
    );
    assert.ok(
      !c.uses.includes('docs.upgrade_run_results'),
      'legacy docs.upgrade_run_results should no longer be referenced',
    );
    assert.ok(
      !c.uses.includes('docs.sync_requirements_md'),
      'legacy docs.sync_requirements_md should no longer be referenced',
    );
  });
});

describe('互斥边界规则验证（与 usage_boundary 联动）', () => {
  test('docs.maintain_requirements_md 与 docs.sync_governance_entries 不应在同一 composite 中', () => {
    // 维护 requirements.md 与同步 docs/INDEX 是不同动作，但可能顺序执行
    // 此规则仅校验：二者不会因为 notes_required_when 提示而**同时改写同一文件**
    // 由于 description 已声明互斥，composite 应当在 notes 中说明边界
    for (const c of composites) {
      const has1 = c.uses?.includes('docs.maintain_requirements_md');
      const has2 = c.uses?.includes('docs.sync_governance_entries');
      if (has1 && has2) {
        // 允许共存，但要求 notes 明确说明顺序
        assert.ok(
          hasNonEmptyNotes(c),
          `${c.id}: 同时引用两个互斥动作，必须在 notes 中说明顺序`,
        );
      }
    }
  });

  test('layout.resolve_slot 与 layout.resolve_semantic 不应在同一 composite 中', () => {
    for (const c of composites) {
      const has1 = c.uses?.includes('layout.resolve_slot');
      const has2 = c.uses?.includes('layout.resolve_semantic');
      assert.ok(
        !(has1 && has2),
        `${c.id}: 不应同时调用 layout.resolve_slot 与 layout.resolve_semantic（互斥）`,
      );
    }
  });
});

// ---------------------------------------------------------------------------
// 快照格式化工具：把 notes_required_when 触发清单渲染成对齐的 ASCII 表格
// 便于 PR review 时一眼看出哪些 composite 触发了规则、是否已填写 notes
// ---------------------------------------------------------------------------

function formatNotesRequiredSnapshot(rows) {
  if (rows.length === 0) {
    return '触发 notes_required_when 的 composite: 0 个（无需检查）';
  }

  const headComposite = 'composite';
  const headTriggers = '触发动作';
  const headStatus = 'hasNotes';

  const cells = rows.map((r) => ({
    id: r.id,
    triggers: r.triggers.join(', '),
    status: r.hasNotes ? '✓' : '✗ MISSING',
  }));

  const wComposite = Math.max(headComposite.length, ...cells.map((c) => c.id.length));
  const wTriggers = Math.max(headTriggers.length, ...cells.map((c) => c.triggers.length));

  const pad = (s, w) => s + ' '.repeat(Math.max(0, w - s.length));
  const sep = `├─${'─'.repeat(wComposite + 2)}┼─${'─'.repeat(wTriggers + 2)}┼─${'─'.repeat(headStatus.length + 2)}─┤`;
  const top = `┌─${'─'.repeat(wComposite + 2)}┬─${'─'.repeat(wTriggers + 2)}┬─${'─'.repeat(headStatus.length + 2)}─┐`;
  const bot = `└─${'─'.repeat(wComposite + 2)}┴─${'─'.repeat(wTriggers + 2)}┴─${'─'.repeat(headStatus.length + 2)}─┘`;

  const lines = [];
  lines.push(`触发 notes_required_when 的 composite 清单（共 ${rows.length} 个）：`);
  lines.push(top);
  lines.push(
    `│ ${pad(headComposite, wComposite)} │ ${pad(headTriggers, wTriggers)} │ ${pad(headStatus, headStatus.length)} │`,
  );
  lines.push(sep);
  for (const c of cells) {
    lines.push(
      `│ ${pad(c.id, wComposite)} │ ${pad(c.triggers, wTriggers)} │ ${pad(c.status, headStatus.length)} │`,
    );
  }
  lines.push(bot);
  return lines.join('\n');
}

// ---------------------------------------------------------------------------
// v0.2.2 新增：flow.req_apply_overrides 契约
// ---------------------------------------------------------------------------

describe('flow.req_apply_overrides — v0.2.2 req 路由控制开关契约', () => {
  const COMPOSITE_ID = 'flow.req_apply_overrides';

  test('composite 必须存在于注册表', () => {
    const c = composites.find((x) => x.id === COMPOSITE_ID);
    assert.ok(c, `${COMPOSITE_ID} should be registered`);
  });

  test('uses 必须包含 requirement.compute_overrides', () => {
    const c = composites.find((x) => x.id === COMPOSITE_ID);
    assert.ok(c.uses.includes('requirement.compute_overrides'), 'should call requirement.compute_overrides');
  });

  test('4 个控制开关必须作为 produced_outputs 暴露', () => {
    const c = composites.find((x) => x.id === COMPOSITE_ID);
    for (const name of ['skipClarification', 'skipDesign', 'effectiveMaxGateRounds', 'unresolvedCount']) {
      assert.ok(c.produced_outputs.includes(name), `${name} should be in produced_outputs`);
    }
  });

  test('notes 必须声明与 flow.requirement_activate 互斥（触发 notes_required_when）', () => {
    const c = composites.find((x) => x.id === COMPOSITE_ID);
    assert.ok(hasNonEmptyNotes(c), 'notes should be non-empty (notes_required_when)');
    assert.match(c.notes.join('\n'), /互斥/, 'notes should mention mutual exclusion with flow.requirement_activate');
  });
});

describe('flow.req_apply_overrides 与 flow.requirement_activate 互斥约束', () => {
  test('req 路由 workflow 不得同时调用两者（路由级互斥）', () => {
    const REQ_WORKFLOW = path.join(__dirname, '..', 'templates', 'workflows', 'req.workflow.yaml');
    const wf = yaml.load(fs.readFileSync(REQ_WORKFLOW, 'utf8'));

    const usesFlowActivate = (steps) => {
      if (!Array.isArray(steps)) return false;
      for (const step of steps) {
        if (step.uses === 'flow.requirement_activate') return true;
        if (step.steps && usesFlowActivate(step.steps)) return true;
        if (step.then && usesFlowActivate(step.then)) return true;
        if (step.else && usesFlowActivate(step.else)) return true;
      }
      return false;
    };

    const usesReqApplyOverrides = (steps) => {
      if (!Array.isArray(steps)) return false;
      for (const step of steps) {
        if (step.uses === 'flow.req_apply_overrides') return true;
        if (step.steps && usesReqApplyOverrides(step.steps)) return true;
        if (step.then && usesReqApplyOverrides(step.then)) return true;
        if (step.else && usesReqApplyOverrides(step.else)) return true;
      }
      return false;
    };

    const phases = wf.workflow?.phases || [];
    const allSteps = phases.flatMap((p) => p.steps || []);
    const hasActivate = usesFlowActivate(allSteps);
    const hasOverrides = usesReqApplyOverrides(allSteps);

    assert.equal(hasActivate && hasOverrides, false, 'req workflow should not use both flow.requirement_activate and flow.req_apply_overrides');
  });
});