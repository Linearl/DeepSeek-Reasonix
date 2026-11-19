const { test, describe } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');

const SKILL_ROOT = path.resolve(__dirname, '..');

async function importResolver() {
  return await import(`file://${path.join(SKILL_ROOT, 'routes/dev/tools/req-sync/profile-resolver.mjs')}`);
}

describe('profile-resolver — schema 校验', () => {
  test('合法 cfg 通过校验（strict mode）', async () => {
    const { validateProjectConfig } = await importResolver();
    const result = validateProjectConfig({
      version: '1.0',
      base_profile: 'default',
      slots: { docs_root: '/tmp/docs', task_todo_root: '/tmp/todo' },
      aliases: [{ name: '建文档', maps_to: 'init' }],
    }, { mode: 'strict' });
    assert.strictEqual(result.valid, true);
    assert.deepStrictEqual(result.errors, []);
  });

  test('strict mode: missing version 抛错', async () => {
    const { validateProjectConfig } = await importResolver();
    assert.throws(
      () => validateProjectConfig({ slots: {} }, { mode: 'strict' }),
      /missing required field: version/
    );
  });

  test('strict mode: 非法 base_profile 抛错', async () => {
    const { validateProjectConfig } = await importResolver();
    assert.throws(
      () => validateProjectConfig({ version: '1.0', base_profile: 'invalid' }, { mode: 'strict' }),
      /base_profile: invalid value "invalid"/
    );
  });

  test('strict mode: 非法 slot key 抛错', async () => {
    const { validateProjectConfig } = await importResolver();
    assert.throws(
      () => validateProjectConfig({ version: '1.0', slots: { bogus_slot: '/x' } }, { mode: 'strict' }),
      /slots\.bogus_slot: unknown slot key/
    );
  });

  test('strict mode: 非法 alias maps_to 抛错', async () => {
    const { validateProjectConfig } = await importResolver();
    assert.throws(
      () => validateProjectConfig({
        version: '1.0',
        aliases: [{ name: 'x', maps_to: 'nonexistent_route' }],
      }, { mode: 'strict' }),
      /aliases\[0\].maps_to: "nonexistent_route" is not a known route/
    );
  });

  test('warn mode: 错误不抛错，返回 valid=false', async () => {
    const { validateProjectConfig } = await importResolver();
    const result = validateProjectConfig({
      version: '1.0',
      slots: { bogus: '/x' },
    }, { mode: 'warn' });
    assert.strictEqual(result.valid, false);
    assert.ok(result.errors.length > 0);
  });

  test('off mode: 跳过校验', async () => {
    const { validateProjectConfig } = await importResolver();
    const result = validateProjectConfig({
      version: '1.0',
      slots: { bogus: '/x' },  // 非法但不校验
    }, { mode: 'off' });
    assert.strictEqual(result.valid, true);  // off 模式不报告错误
  });

  test('project.name 必填', async () => {
    const { validateProjectConfig } = await importResolver();
    assert.throws(
      () => validateProjectConfig({ version: '1.0', project: { type: 'fullstack' } }, { mode: 'strict' }),
      /project\.name: required string/
    );
  });
});

describe('profile-resolver — aliases 消费', () => {
  test('aliases 按 maps_to 聚合', async () => {
    const { resolveCustomAliases } = await importResolver();
    const result = resolveCustomAliases({
      aliases: [
        { name: '建文档', maps_to: 'init' },
        { name: '需求澄清', maps_to: 'req' },
        { name: '需求开发', maps_to: 'req' },
      ],
    });
    assert.deepStrictEqual(result, {
      init: ['建文档'],
      req: ['需求澄清', '需求开发'],
    });
  });

  test('aliases 为空时返回 {}', async () => {
    const { resolveCustomAliases } = await importResolver();
    assert.deepStrictEqual(resolveCustomAliases({}), {});
    assert.deepStrictEqual(resolveCustomAliases({ aliases: [] }), {});
  });

  test('resolveProfile 注入 customAliases 字段', async () => {
    // 创建临时 workspace + .ll-workflow.yaml
    const tmpDir = `/tmp/test-aliases-${Date.now()}`;
    require('node:fs').mkdirSync(tmpDir, { recursive: true });
    require('node:fs').writeFileSync(`${tmpDir}/.ll-workflow.yaml`,
      'version: "1.0"\n' +
      'base_profile: default\n' +
      'aliases:\n' +
      '  - name: "建文档"\n' +
      '    maps_to: init\n' +
      '  - name: "需求澄清"\n' +
      '    maps_to: req\n');

    const { resolveProfile } = await importResolver();
    const ctx = resolveProfile({
      workspaceRoot: tmpDir,
      targetDevDir: tmpDir + '/docs',
      version: '07',
    });
    assert.deepStrictEqual(ctx.customAliases, {
      init: ['建文档'],
      req: ['需求澄清'],
    });
    assert.strictEqual(ctx.projectConfig.version, '1.0');

    require('node:fs').rmSync(tmpDir, { recursive: true });
  });
});
