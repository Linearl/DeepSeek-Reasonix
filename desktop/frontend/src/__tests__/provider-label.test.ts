// Run: node --import tsx src/__tests__/provider-label.test.ts
//
// Task 198: `providerDisplayLabel` is the single place that decides which provider
// name a user-facing surface shows. It must prefer the configured display name and
// fall back to the routing identity byte-for-byte, so installs that never set a
// display name keep rendering exactly what they rendered before.

import { providerDisplayLabel } from "../lib/providerLabel";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nprovider display label");

ok(providerDisplayLabel({ name: "mimo-pro", displayName: "MiMo Pro" }) === "MiMo Pro", "display name wins when configured");
ok(providerDisplayLabel({ name: "minimax-M3" }) === "minimax-M3", "missing display name falls back to the identity");
ok(providerDisplayLabel({ name: "minimax-M3", displayName: "" }) === "minimax-M3", "empty display name falls back to the identity");
ok(providerDisplayLabel({ name: "minimax-M3", displayName: "   " }) === "minimax-M3", "blank display name falls back to the identity");
ok(providerDisplayLabel({ name: "mimo-pro", displayName: "  MiMo Pro  " }) === "MiMo Pro", "display name is trimmed");
ok(providerDisplayLabel({ name: "opencode-go-3d668098626d385cb9e2084d75c5db36", displayName: "OpenCode Go (Recommended) · 3" }) === "OpenCode Go (Recommended) · 3", "auto-generated identity is replaced by its readable label");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
