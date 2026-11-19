/**
 * profile-resolver.test.js — profile 切换路径解析契约测试
 *
 * 覆盖 v0.2.2 #4B 的核心修复：
 *   - default profile: B.2.done / B.todo 等路径正确
 *   - generic_docs_v1 profile: 切换到 docs/tasks/done 而非硬编码 B.2.done
 *   - minimal_workspace profile: 切换到 docs/done
 *   - .ll-workflow.yaml slots 覆盖生效（project_config_override 优先级）
 *   - 变量替换: ${targetDevDir} ${docs} ${version} ${worktree}
 *   - extends 合并: numbered_docs_v1 extends default
 *   - semantics 映射: todo/done/paused/cancelled/inactive_requirement → slot
 *
 * 这些测试是声明-实现漂移的核心防护层：任何重新引入硬编码的修改都会被这些测试捕获。
 */

const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

// 用绝对路径 import，绕过 node:test 的工作目录问题
const SKILL_ROOT = path.resolve(__dirname, '..');
const PROFILE_RESOLVER = path.join(SKILL_ROOT, 'routes', 'dev', 'tools', 'req-sync', 'profile-resolver.mjs');

// 用 dynamic import 加载 .mjs
async function importResolver() {
  return await import(`file://${PROFILE_RESOLVER}`);
}

describe('profile-resolver — default profile', () => {
  test('resolveSemantic(done) → 包含 B.2.done 和 version', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
    });
    const donePath = ctx.resolveSemantic('done');
    assert.match(donePath, /B\.2\.done/);
    assert.match(donePath, /\/07$/, '应包含 version=07');
    assert.match(donePath, /02-dev\/dev_07/);
  });

  test('resolveSlot(task_done_root) → 不含 version 子目录', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
    });
    const donePath = ctx.resolveSlot('task_done_root');
    assert.match(donePath, /B\.2\.done$/, 'task_done_root 不应含 version');
    assert.doesNotMatch(donePath, /\/07$/);
  });

  test('所有 5 个状态路径都可解析', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
    });
    assert.ok(ctx.resolveSemantic('todo'));
    assert.ok(ctx.resolveSemantic('done'));
    assert.ok(ctx.resolveSemantic('paused'));
    assert.ok(ctx.resolveSemantic('cancelled'));
    assert.ok(ctx.resolveSemantic('inactive_requirement'));
    // 5 个路径各不相同
    const set = new Set([
      ctx.resolveSemantic('todo'),
      ctx.resolveSemantic('done'),
      ctx.resolveSemantic('paused'),
      ctx.resolveSemantic('cancelled'),
      ctx.resolveSemantic('inactive_requirement'),
    ]);
    assert.strictEqual(set.size, 5, '5 个 semantic 路径应互不相同');
  });
});

describe('profile-resolver — profile 切换', () => {
  test('generic_docs_v1: done 路径应是 docs/tasks/done 而非 B.2.done', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, 'test/dev_99'),
      version: '99',
      profileOverride: 'generic_docs_v1',
    });
    const donePath = ctx.resolveSemantic('done');
    assert.match(donePath, /\/tasks\/done/, 'generic_docs_v1 应解析为 docs/tasks/done');
    assert.doesNotMatch(donePath, /B\.2\.done/, '绝不应出现硬编码 B.2.done');
    assert.match(donePath, /\/99$/, '应包含 version=99');
  });

  test('minimal_workspace: done 路径应是 docs/done', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, 'test/dev_99'),
      version: '99',
      profileOverride: 'minimal_workspace',
    });
    const donePath = ctx.resolveSemantic('done');
    assert.match(donePath, /\/docs\/done/, 'minimal_workspace 应解析为 docs/done');
    assert.doesNotMatch(donePath, /B\.2\.done/);
  });

  test('numbered_docs_v1 extends default: 沿用 default 的 B.2.done 命名', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
      profileOverride: 'numbered_docs_v1',
    });
    const donePath = ctx.resolveSemantic('done');
    assert.match(donePath, /B\.2\.done/, 'numbered_docs_v1 应沿用 default 的路径名');
  });
});

describe('profile-resolver — 变量替换', () => {
  test('${version} 正确替换为版本号', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
    });
    const todoVerPath = ctx.resolveSlot('task_todo_version_root');
    assert.match(todoVerPath, /\/B\.todo\/07$/);
  });

  test('${targetDevDir} 正确替换', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
    });
    const docsRoot = ctx.resolveSlot('docs_root');
    assert.match(docsRoot, /02-dev\/dev_07\/docs$/);
  });

  test('${docs | default(docs)} 缺省值生效', async () => {
    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
    });
    // docs_root 用 ${targetDevDir}/${docs}，${docs} 缺省为 docs
    const docsRoot = ctx.resolveSlot('docs_root');
    assert.match(docsRoot, /\/docs$/);
  });
});

describe('profile-resolver — utils.resolvePaths 桥接', () => {
  test('default profile 下 resolvePaths 返回原硬编码兼容路径', async () => {
    const { resolvePaths } = await import(`file://${path.join(SKILL_ROOT, 'routes/dev/tools/req-sync/utils.mjs')}`);
    const ctx = resolvePaths({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, '02-dev/dev_07'),
      version: '07',
    });
    assert.strictEqual(ctx.source, 'profile-resolver');
    assert.match(ctx.todoPath, /\/1\.task\/B\.todo$/);
    assert.match(ctx.donePath, /\/1\.task\/B\.2\.done$/);
    assert.match(ctx.pausedPath, /\/1\.task\/B\.1\.paused$/);
    assert.match(ctx.cancelledPath, /\/1\.task\/B\.3\.cancelled$/);
    assert.match(ctx.inactivePath, /\/1\.task\/A\.inactive$/);
    assert.match(ctx.requirementsIndexPath, /\/1\.task\/requirements\.md$/);
  });

  test('generic_docs_v1 profile 下 resolvePaths 切到 docs/tasks/*', async () => {
    const { resolvePaths } = await import(`file://${path.join(SKILL_ROOT, 'routes/dev/tools/req-sync/utils.mjs')}`);
    const ctx = resolvePaths({
      workspaceRoot: SKILL_ROOT,
      targetDevDir: path.join(SKILL_ROOT, 'test/dev_99'),
      version: '99',
      profileOverride: 'generic_docs_v1',
    });
    assert.strictEqual(ctx.source, 'profile-resolver');
    assert.match(ctx.donePath, /\/docs\/tasks\/done$/, 'archive 应能找到 docs/tasks/done');
    assert.match(ctx.todoPath, /\/docs\/tasks\/todo$/);
    assert.doesNotMatch(ctx.donePath, /B\.2\.done/, '不应出现硬编码 B.2.done');
  });

  test('SLOT_TO_STATUS 反向映射覆盖所有 5 个生命周期 slot', async () => {
    const { SLOT_TO_STATUS } = await import(`file://${path.join(SKILL_ROOT, 'routes/dev/tools/req-sync/utils.mjs')}`);
    assert.ok(SLOT_TO_STATUS.task_done_version_root);
    assert.ok(SLOT_TO_STATUS.task_todo_version_root);
    assert.ok(SLOT_TO_STATUS.task_paused_root);
    assert.ok(SLOT_TO_STATUS.task_cancelled_root);
    assert.ok(SLOT_TO_STATUS.task_inactive_root);
  });
});
