// description: route-frontmatter 契约测试 — 校验 SKILL.md frontmatter 与 config/route-registry.yaml 的路由一致性
// update_time: 2026-07-01
//
// 注：tests/ 下的 *.test.js 是 CommonJS JS 文件，不带 YAML frontmatter（frontmatter 仅适用于 *.md）。

const { test, describe, before } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const yaml = require('js-yaml');

const SKILL_MD = path.join(__dirname, '..', 'SKILL.md');
const ROUTE_REGISTRY = path.join(__dirname, '..', 'config', 'route-registry.yaml');

let skillMd;
let skillDescription;
let registry;
let shortNames;
let routeIdentities; // [{short_name, aliases: Set<string>}]

before(() => {
  skillMd = fs.readFileSync(SKILL_MD, 'utf8');
  const fmMatch = skillMd.match(/^---\n([\s\S]+?)\n---/);
  assert.ok(fmMatch, 'SKILL.md must have YAML frontmatter');
  const fm = yaml.load(fmMatch[1]);
  assert.ok(fm.description, 'SKILL.md frontmatter must have description');
  skillDescription = fm.description;
  registry = yaml.load(fs.readFileSync(ROUTE_REGISTRY, 'utf8'));
});

function getRoutes() {
  return registry.routes || (registry.registry && registry.registry.routes) || [];
}

before(() => {
  const routes = getRoutes();
  shortNames = routes.map((r) => r.short_name);
  routeIdentities = routes.map((r) => ({
    short_name: r.short_name,
    aliases: new Set([r.short_name, ...(Array.isArray(r.aliases) ? r.aliases : [])]),
  }));
});

describe('SKILL.md frontmatter to route-registry 一致性', () => {
  test('注册表必须有 11 条路由', () => {
    assert.equal(getRoutes().length, 11, '应注册 11 条路由（init/req/dev/aud/fix/branch/merge/gt/df/hchk/haud）');
  });

  test('SKILL.md 全文必须出现注册表每个 short_name', () => {
    for (const sn of shortNames) {
      assert.ok(skillMd.includes(sn), 'route short_name "' + sn + '" 未在 SKILL.md 全文出现');
    }
  });

  test('SKILL.md frontmatter description 必须包含每条路由至少一个可识别名', () => {
    for (const idt of routeIdentities) {
      const hit = [...idt.aliases].some((a) => skillDescription.includes(a));
      assert.ok(
        hit,
        'description 缺少路由 ' + idt.short_name + ' 的任何可识别名（含别名）',
      );
    }
  });

  test('每个 route 必有 workflow 字段', () => {
    const routes = getRoutes();
    for (const r of routes) {
      assert.ok(r.workflow, 'route ' + r.short_name + ' missing workflow');
      assert.equal(typeof r.workflow, 'string', 'route ' + r.short_name + ' workflow must be string');
    }
  });

  test('variants 节点必须有合法 executor', () => {
    const routes = getRoutes();
    for (const r of routes) {
      if (!r.variants) continue;
      for (const v of r.variants) {
        assert.ok(v.executor, 'variant ' + v.short_name + ' missing executor');
        assert.ok(
          v.executor === 'cc_workflow' || v.executor === 'traditional_yaml',
          'variant ' + v.short_name + ' bad executor: ' + v.executor
        );
      }
    }
  });

  test('variants 必须有 short_name', () => {
    const routes = getRoutes();
    for (const r of routes) {
      if (!r.variants) continue;
      for (const v of r.variants) {
        assert.ok(v.short_name, 'variant missing short_name on route ' + r.short_name);
      }
    }
  });

  test('route id 必须唯一', () => {
    const ids = getRoutes().map((r) => r.id);
    assert.equal(new Set(ids).size, ids.length, 'duplicate route ids: ' + JSON.stringify(ids));
  });

  test('route short_name 必须唯一', () => {
    const sns = getRoutes().map((r) => r.short_name);
    assert.equal(new Set(sns).size, sns.length, 'duplicate short_names: ' + JSON.stringify(sns));
  });
});
