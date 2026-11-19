// description: workflow-inputs-format 契约测试 — 校验所有 .workflow.yaml 的
//              context.inputs / context.outputs 元素必须是 {name, description} 对象。
//              与 workflow.schema.yaml 的 inputs/outputs item.required_fields 同步。
// update_time: 2026-07-01

const { test, describe } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const yaml = require("js-yaml");

const WORKFLOW_DIR = path.join(__dirname, "..", "templates", "workflows");

function findWorkflowFiles() {
  return fs
    .readdirSync(WORKFLOW_DIR)
    .filter(
      (f) =>
        f.endsWith(".workflow.yaml") &&
        !f.endsWith(".bak") &&
        f !== "route-template.workflow.yaml"
    )
    .map((f) => path.join(WORKFLOW_DIR, f));
}

function loadContext(filePath) {
  const data = yaml.load(fs.readFileSync(filePath, "utf8"));
  const wf = (data && data.workflow) || {};
  const ctx = wf.context || {};
  return {
    inputs: Array.isArray(ctx.inputs) ? ctx.inputs : [],
    outputs: Array.isArray(ctx.outputs) ? ctx.outputs : [],
  };
}

function assertObjectList(label, entries, filePath) {
  assert.ok(Array.isArray(entries), `${label} in ${path.basename(filePath)} must be an array`);
  for (let i = 0; i < entries.length; i++) {
    const e = entries[i];
    assert.strictEqual(
      typeof e,
      "object",
      `${label}[${i}] in ${path.basename(filePath)} must be an object, got ${typeof e}`
    );
    assert.ok(e !== null, `${label}[${i}] in ${path.basename(filePath)} must be an object, got null`);
    assert.strictEqual(
      typeof e.name,
      "string",
      `${label}[${i}].name in ${path.basename(filePath)} must be string, got ${typeof e.name}`
    );
    assert.ok(
      e.name.length > 0,
      `${label}[${i}].name in ${path.basename(filePath)} must be non-empty`
    );
    assert.strictEqual(
      typeof e.description,
      "string",
      `${label}[${i}].description in ${path.basename(filePath)} must be string, got ${typeof e.description}`
    );
    assert.ok(
      e.description.length > 0,
      `${label}[${i}].description in ${path.basename(filePath)} must be non-empty`
    );
  }
}

describe("workflow inputs/outputs 必须是 {name, description} 对象", () => {
  const files = findWorkflowFiles();
  test("应至少扫描到 10 个 workflow", () => {
    assert.ok(files.length >= 10, `expected >= 10 workflow files, got ${files.length}`);
  });

  for (const fp of files) {
    describe(path.basename(fp), () => {
      test("inputs 每项都是 {name, description} 对象", () => {
        const { inputs } = loadContext(fp);
        if (inputs.length > 0) {
          assertObjectList("inputs", inputs, fp);
        } else {
          assert.equal(inputs.length, 0);
        }
      });
      test("outputs 每项都是 {name, description} 对象", () => {
        const { outputs } = loadContext(fp);
        if (outputs.length > 0) {
          assertObjectList("outputs", outputs, fp);
        } else {
          assert.equal(outputs.length, 0);
        }
      });
    });
  }
});
