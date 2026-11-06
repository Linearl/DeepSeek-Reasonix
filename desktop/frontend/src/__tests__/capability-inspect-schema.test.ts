// Run: tsx src/__tests__/capability-inspect-schema.test.ts
// tl1438: machine-schema consumer for use_capability inspect output.

import { capabilityInspectSchema } from "../lib/capabilityInspect";

let passed = 0;
let failed = 0;

function eq(a: unknown, b: unknown, label: string) {
  if (JSON.stringify(a) === JSON.stringify(b)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(b)}, got ${JSON.stringify(a)}\n`);
    failed += 1;
  }
}

console.log("\ncapability inspect schema consumer");

const inspectArgs = `{"action":"inspect","capability_id":"tool:heartbeat_task_upsert"}`;
const inspectOutput = JSON.stringify({
  id: "tool:heartbeat_task_upsert",
  kind: "tool",
  name: "heartbeat_task_upsert",
  description: "Create or update one scheduled heartbeat task on the desktop app.",
  parameters: [
    { name: "enabled", type: "boolean", required: false },
    { name: "expected_revision", type: "integer", required: true, description: "Revision from the latest heartbeat_task_list result." },
    { name: "scope", type: "string", required: false, enum: ["", "global", "project"] },
  ],
});

const parsed = capabilityInspectSchema("use_capability", inspectArgs, inspectOutput);
eq(parsed?.params.length, 3, "inspect output with parameters parses to three rows");
eq(parsed?.params[1]?.name, "expected_revision", "row order follows backend derivation");
eq(parsed?.params[1]?.required, true, "required flag survives the round trip");
eq(parsed?.params[2]?.enum, ["", "global", "project"], "enum values survive the round trip");
eq(parsed?.params[0]?.type, "boolean", "type survives the round trip");
eq(parsed?.params[0]?.description, undefined, "absent description stays undefined");
eq(parsed?.note, undefined, "no note when a schema is present");

eq(
  capabilityInspectSchema("use_capability", inspectArgs, JSON.stringify({
    id: "tool:no_param", description: "prose only", schema_note: "this tool's engine definition declares no per-parameter schema",
  })),
  { params: [], note: "this tool's engine definition declares no per-parameter schema" },
  "no-schema output yields the labelled note without fabricated rows",
);

eq(capabilityInspectSchema("use_capability", `{"action":"call","capability_id":"tool:x"}`, inspectOutput), undefined, "call results are not parsed");
eq(capabilityInspectSchema("bash", inspectArgs, inspectOutput), undefined, "non-proxy tools are not parsed");
eq(capabilityInspectSchema("use_capability", inspectArgs, "capability unavailable: no such server"), undefined, "plain-text output is not parsed");
eq(capabilityInspectSchema("use_capability", inspectArgs, undefined), undefined, "missing output is not parsed");
eq(capabilityInspectSchema("use_capability", inspectArgs, "not json {"), undefined, "invalid JSON output is not parsed");
eq(capabilityInspectSchema("use_capability", inspectArgs, JSON.stringify({ id: "tool:x", description: "prose only" })), undefined, "inspect output without machine layer stays untouched");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
