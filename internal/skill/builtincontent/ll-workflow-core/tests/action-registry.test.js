/**
 * action-registry.test.js — action-registry.yaml 契约测试
 *
 * 覆盖 v0.2.0 动作冗余合并后的两个核心 mode-switch 动作：
 *   - docs.maintain_requirements_md（mode: upgrade | sync | upgrade_then_sync）
 *   - config.manage_project（mode: read | write）
 *
 * 测试范围：mode 入参的声明形式、默认值、enum、副作用、产物的 mode 区分。
 * 不测试运行时行为（skill 不含运行时代码）。
 */

const { test, describe, before } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const yaml = require('js-yaml');

const REGISTRY_PATH = path.join(__dirname, '..', 'templates', 'action-registry.yaml');

let actionById;

before(() => {
  const reg = yaml.load(fs.readFileSync(REGISTRY_PATH, 'utf8'));
  actionById = Object.fromEntries(reg.actions.map((a) => [a.id, a]));
});

function getInput(action, name) {
  return action.inputs.find((i) => i.name === name);
}

function getOutput(action, name) {
  return action.outputs.find((o) => o.name === name);
}

describe('docs.maintain_requirements_md — mode 切换契约', () => {
  const ACTION_ID = 'docs.maintain_requirements_md';

  test('action 必须存在于注册表', () => {
    assert.ok(actionById[ACTION_ID], `${ACTION_ID} should be registered`);
  });

  test('mode 入参：可缺省，默认 upgrade_then_sync', () => {
    const mode = getInput(actionById[ACTION_ID], 'mode');
    assert.ok(mode, 'mode input should exist');
    assert.equal(mode.required, false, 'mode should be optional (has default)');
    assert.equal(mode.default, 'upgrade_then_sync', 'default should be upgrade_then_sync');
  });

  test('description 必须列出全部三种 mode 取值', () => {
    const desc = actionById[ACTION_ID].description;
    for (const m of ['upgrade', 'sync', 'upgrade_then_sync']) {
      assert.ok(desc.includes(`\`${m}\``), `description should mention mode ${m}`);
    }
  });

  test('upgrade 模式专属产物：upgradeReport + improvedCount', () => {
    const outputs = actionById[ACTION_ID].outputs.map((o) => o.name);
    assert.ok(outputs.includes('upgradeReport'), 'should expose upgradeReport');
    assert.ok(outputs.includes('improvedCount'), 'should expose improvedCount');
  });

  test('sync 模式专属产物：syncReport + packageCount + syncedFiles', () => {
    const outputs = actionById[ACTION_ID].outputs.map((o) => o.name);
    assert.ok(outputs.includes('syncReport'), 'should expose syncReport');
    assert.ok(outputs.includes('packageCount'), 'should expose packageCount');
    assert.ok(outputs.includes('syncedFiles'), 'should expose syncedFiles');
  });

  test('mode 字段必须出现在 outputs 中（回声）', () => {
    const modeOut = getOutput(actionById[ACTION_ID], 'mode');
    assert.ok(modeOut, 'mode output should exist for echo / context propagation');
  });

  test('side_effects 必须区分 upgrade（动 run_result.json）与 sync（动 requirements.md）', () => {
    const se = actionById[ACTION_ID].side_effects.join('\n');
    assert.match(se, /升级模式.*run_result\.json/, 'upgrade branch should mention run_result.json');
    assert.match(se, /同步模式.*requirements\.md/, 'sync branch should mention requirements.md');
  });

  test('filterDirs 必须标注"仅 mode=upgrade 时生效"', () => {
    const filterDirs = getInput(actionById[ACTION_ID], 'filterDirs');
    assert.ok(filterDirs, 'filterDirs input should exist');
    assert.match(filterDirs.description, /upgrade/, 'filterDirs should be documented as upgrade-only');
  });

  test('apply 入参：默认 false（dry-run），true 落盘', () => {
    const apply = getInput(actionById[ACTION_ID], 'apply');
    assert.ok(apply, 'apply input should exist');
    assert.equal(apply.default, false, 'apply default should be false (dry-run)');
    assert.match(apply.description, /dry-run|落盘/, 'apply description should explain dry-run semantics');
  });

  test('usage_boundary 必须明确与 docs.sync_governance_entries 互斥', () => {
    const ub = actionById[ACTION_ID].usage_boundary;
    assert.ok(ub, 'usage_boundary should be defined');
    assert.match(ub, /docs\.sync_governance_entries/, 'boundary should reference docs.sync_governance_entries');
    assert.match(ub, /互斥|严格/, 'boundary should state mutual exclusion');
  });
});

describe('config.manage_project — mode 切换契约', () => {
  const ACTION_ID = 'config.manage_project';

  test('action 必须存在于注册表', () => {
    assert.ok(actionById[ACTION_ID], `${ACTION_ID} should be registered`);
  });

  test('mode 入参：必填且 enum=[read, write]', () => {
    const mode = getInput(actionById[ACTION_ID], 'mode');
    assert.ok(mode, 'mode input should exist');
    assert.equal(mode.required, true, 'mode should be required');
    assert.deepEqual(mode.enum, ['read', 'write'], 'mode enum should be exactly [read, write]');
  });

  test('description 必须明确两种 mode 的语义', () => {
    const desc = actionById[ACTION_ID].description;
    assert.match(desc, /read|加载/, 'description should explain read mode');
    assert.match(desc, /write|写入/, 'description should explain write mode');
  });

  test('outputs 必须按 mode 区分（read 模式产物 / write 模式产物）', () => {
    const outputs = actionById[ACTION_ID].outputs;
    const configExists = getOutput(actionById[ACTION_ID], 'configExists');
    const writeResult = getOutput(actionById[ACTION_ID], 'writeResult');

    assert.ok(configExists, 'configExists output should exist');
    assert.match(configExists.description, /read/, 'configExists should be read-only');

    assert.ok(writeResult, 'writeResult output should exist');
    assert.match(writeResult.description, /write/, 'writeResult should be write-only');
  });

  test('side_effects 仅在 write 模式触发，read 模式无副作用', () => {
    const se = actionById[ACTION_ID].side_effects.join('\n');
    assert.match(se, /write.*创建或覆盖|write.*\.ll-workflow\.yaml/, 'side_effects should describe write behavior');
    // read 不写入；通过 precondition 的措辞间接验证
    const pre = actionById[ACTION_ID].preconditions.join('\n');
    assert.match(pre, /workspaceRoot/, 'read mode precondition should only need workspaceRoot');
  });

  test('projectConfig 仅在 write 模式下必填（precondition 显式说明）', () => {
    const pc = getInput(actionById[ACTION_ID], 'projectConfig');
    assert.ok(pc, 'projectConfig input should exist');
    assert.equal(pc.required, false, 'projectConfig should be optional in YAML schema');
    // 但 precondition 应当约束 write 模式下必填
    const pre = actionById[ACTION_ID].preconditions.join('\n');
    assert.match(pre, /write.*projectConfig|projectConfig.*write/, 'precondition should require projectConfig in write mode');
  });

  test('default_on_fail 必须按 mode 分流', () => {
    const dof = actionById[ACTION_ID].default_on_fail;
    assert.match(dof, /read.*use_default|use_default.*read/, 'read mode should default to use_default');
    assert.match(dof, /write.*block|block.*write/, 'write mode should default to block');
  });

  test('usage_boundary 必须明确与 config.interactive_setup + config.merge_profiles 互斥', () => {
    const ub = actionById[ACTION_ID].usage_boundary;
    assert.ok(ub, 'usage_boundary should be defined');
    assert.match(ub, /config\.interactive_setup/, 'boundary should reference config.interactive_setup');
    assert.match(ub, /config\.merge_profiles/, 'boundary should reference config.merge_profiles');
    assert.match(ub, /互斥|严格/, 'boundary should state mutual exclusion');
  });
});

describe('mode-switch 动作的迁移完整性（v0.2.0 回归保护）', () => {
  test('被合并的旧 action 必须从注册表消失', () => {
    const removedIds = [
      'docs.upgrade_run_results',
      'docs.sync_requirements_md',
      'config.load_project_config',
      'config.write_project_config',
    ];
    for (const id of removedIds) {
      assert.equal(actionById[id], undefined, `legacy action ${id} should have been removed`);
    }
  });

  test('被合并的旧 action 在 description 中被显式提及（追溯说明）', () => {
    const m = actionById['docs.maintain_requirements_md'];
    assert.match(m.description, /docs\.upgrade_run_results/, 'should mention legacy docs.upgrade_run_results');
    assert.match(m.description, /docs\.sync_requirements_md/, 'should mention legacy docs.sync_requirements_md');

    const c = actionById['config.manage_project'];
    assert.match(c.description, /config\.load_project_config/, 'should mention legacy config.load_project_config');
    assert.match(c.description, /config\.write_project_config/, 'should mention legacy config.write_project_config');
  });
});

describe('requirement.compute_overrides — v0.2.2 4 字段项目配置契约', () => {
  const ACTION_ID = 'requirement.compute_overrides';

  test('action 必须存在于注册表', () => {
    assert.ok(actionById[ACTION_ID], `${ACTION_ID} should be registered`);
  });

  test('4 个项目配置字段必须作为 input 接收', () => {
    const required = ['categoryMapping', 'complexityRules', 'skipClarificationKeywords', 'skipDesignComplexity'];
    for (const name of required) {
      const input = getInput(actionById[ACTION_ID], name);
      assert.ok(input, `${name} input should exist`);
      assert.equal(input.required, false, `${name} should be optional (with project config default)`);
    }
  });

  test('4 个控制开关必须作为 output 暴露', () => {
    const required = ['skipClarification', 'skipDesign', 'effectiveMaxGateRounds', 'unresolvedCount'];
    for (const name of required) {
      const out = getOutput(actionById[ACTION_ID], name);
      assert.ok(out, `${name} output should exist`);
    }
  });

  test('usage_boundary 必须声明与 flow.requirement_activate 互斥', () => {
    const ub = actionById[ACTION_ID].usage_boundary;
    assert.ok(ub, 'usage_boundary should be defined');
    assert.match(ub, /互斥/, 'should declare mutual exclusion');
  });
});

describe('requirement.refine_ears — v0.2.2 openQuestions 真实压缩契约', () => {
  const ACTION_ID = 'requirement.refine_ears';

  test('action 必须存在于注册表', () => {
    assert.ok(actionById[ACTION_ID], `${ACTION_ID} should be registered`);
  });

  test('inputs 必须包含 requirementDraft + openQuestions + latestAnswers', () => {
    for (const name of ['requirementDraft', 'openQuestions', 'latestAnswers']) {
      const input = getInput(actionById[ACTION_ID], name);
      assert.ok(input, `${name} input should exist`);
      assert.equal(input.required, true, `${name} should be required`);
    }
  });

  test('outputs 必须包含 refinedRequirementDraft + openQuestions + unresolvedCount + earsChecklist', () => {
    for (const name of ['refinedRequirementDraft', 'openQuestions', 'unresolvedCount', 'earsChecklist']) {
      const out = getOutput(actionById[ACTION_ID], name);
      assert.ok(out, `${name} output should exist`);
    }
  });

  test('default_on_fail 必须是 continue_with_warning（澄清循环特性）', () => {
    assert.equal(actionById[ACTION_ID].default_on_fail, 'continue_with_warning');
  });
});

describe('task.compute_path_hint — v0.2.2 b_input_intake 前置契约', () => {
  const ACTION_ID = 'task.compute_path_hint';

  test('action 必须存在于注册表', () => {
    assert.ok(actionById[ACTION_ID], `${ACTION_ID} should be registered`);
  });

  test('inputs 必须包含 version + reqId + title + layout', () => {
    for (const name of ['version', 'reqId', 'title', 'layout']) {
      const input = getInput(actionById[ACTION_ID], name);
      assert.ok(input, `${name} input should exist`);
      assert.equal(input.required, true, `${name} should be required`);
    }
  });

  test('outputs 必须仅暴露 taskPathHint（不创建目录）', () => {
    const outputs = actionById[ACTION_ID].outputs.map((o) => o.name);
    assert.deepEqual(outputs, ['taskPathHint'], 'only taskPathHint output');
  });

  test('side_effects 必须显式说明不写盘', () => {
    const se = actionById[ACTION_ID].side_effects.join('\n');
    assert.match(se, /不创建|仅返回/, 'should explicitly state no disk write');
  });
});

describe('state.update_task_status — v0.2.2 statusAxis 扩展契约', () => {
  const ACTION_ID = 'state.update_task_status';

  test('statusAxis input 必须是 task | requirement enum 且默认 task（向后兼容）', () => {
    const axis = getInput(actionById[ACTION_ID], 'statusAxis');
    assert.ok(axis, 'statusAxis input should exist');
    assert.equal(axis.required, false, 'statusAxis should be optional');
    assert.equal(axis.default, 'task', 'statusAxis default should be task (backward compat)');
    assert.match(axis.description, /task.*requirement|requirement.*task/, 'description should mention both axes');
  });

  test('description 必须显式声明两轴独立', () => {
    const desc = actionById[ACTION_ID].description;
    assert.match(desc, /task 轴/, 'should mention task axis');
    assert.match(desc, /requirement 轴/, 'should mention requirement axis');
    assert.match(desc, /两轴独立|互不影响/, 'should declare axis independence');
  });
});

describe('interaction.ask_questions_gate — v0.2.2 gateTemplatePath 契约', () => {
  const ACTION_ID = 'interaction.ask_questions_gate';

  test('gateTemplatePath input 必须为可选', () => {
    const tpl = getInput(actionById[ACTION_ID], 'gateTemplatePath');
    assert.ok(tpl, 'gateTemplatePath input should exist');
    assert.equal(tpl.required, false, 'gateTemplatePath should be optional');
    assert.match(tpl.description, /v0\.3\.0|template_files|gate-questions/, 'description should mention template binding');
  });
});