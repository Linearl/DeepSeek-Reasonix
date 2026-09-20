// Run: node --import tsx src/__tests__/provider-label.test.ts
//
// Task 198: `providerDisplayLabel` is the single place that decides which provider
// name a user-facing surface shows. Resolution order is explicit `display_name` >
// built-in default label > `name`, and it never touches the routing identity: an
// install that renamed nothing keeps the same `<provider>/<model>` refs.

import { providerDefaultLabel, providerDisplayLabel } from "../lib/providerLabel";

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

console.log("\nresolution order: display name > built-in default > identity");

ok(providerDisplayLabel({ name: "mimo-pro", displayName: "MiMo Pro" }) === "MiMo Pro", "explicit display name wins");
ok(providerDisplayLabel({ name: "mimo-pro", displayName: "  MiMo Pro  " }) === "MiMo Pro", "display name is trimmed");
ok(providerDisplayLabel({ name: "mimo-pro", displayName: "" }) === "MiMo Pro", "empty display name falls through to the default label");
ok(providerDisplayLabel({ name: "minimax-M3", displayName: "   " }) === "MiniMax M3", "blank display name falls through to the default label");
ok(providerDisplayLabel({ name: "acme-relay" }) === "acme-relay", "unknown name falls back to the identity byte-for-byte");
ok(providerDisplayLabel({ name: "acme-relay", displayName: "Acme Relay" }) === "Acme Relay", "unknown name still honours an explicit display name");

console.log("\nbuilt-in default labels (the ids users complained about)");

ok(providerDefaultLabel("deepseek-flash") === "DeepSeek Flash", "deepseek-flash reads as DeepSeek Flash");
ok(providerDefaultLabel("deepseek-pro") === "DeepSeek Pro", "deepseek-pro reads as DeepSeek Pro");
ok(providerDefaultLabel("mimo-pro") === "MiMo Pro", "mimo-pro reads as MiMo Pro");
ok(providerDefaultLabel("mimo-flash") === "MiMo Flash", "mimo-flash reads as MiMo Flash");
ok(providerDefaultLabel("minimax-M3") === "MiniMax M3", "minimax-M3 matches case-insensitively");
ok(providerDefaultLabel("deepseek") === "DeepSeek", "official deepseek reads as DeepSeek");
ok(providerDefaultLabel("glm-cn") === "GLM CN", "glm-cn reads as GLM CN");
ok(providerDefaultLabel("  mimo-api  ") === "MiMo API", "default labels ignore surrounding whitespace");

console.log("\nauto-generated connection ids keep a short code");

ok(
  providerDefaultLabel("opencode-go-3d668098626d385cb9e2084d75c5db36") === "OpenCode Go (3d66)",
  "uuid connection shows its readable stem plus a 4-character code",
);
ok(
  providerDefaultLabel("opencode-go-anthropic-3d668098626d385cb9e2084d75c5db36") === "opencode-go-anthropic (3d66)",
  "unknown stem keeps its own text and only appends the code",
);
ok(
  providerDisplayLabel({ name: "opencode-go-3d668098626d385cb9e2084d75c5db36", displayName: "OpenCode Go (Recommended) · 3" }) === "OpenCode Go (Recommended) · 3",
  "an explicit display name still beats the generated code",
);

console.log("\nzero migration: the routing identity is never rewritten");

const provider = { name: "minimax-M3", displayName: "" };
ok(providerDisplayLabel(provider) === "MiniMax M3" && provider.name === "minimax-M3", "resolving a label leaves provider.name untouched");
ok(`${provider.name}/MiniMax-M3` === "minimax-M3/MiniMax-M3", "model refs keep using the untouched provider name");

// The ids this install actually runs (read out of the live config.toml while fixing
// task 198). Every one of them must keep rendering a readable label — this is the
// user-visible half of the bug, and it must not silently regress if someone trims the
// table. Names the table does not know still fall back to the id (asserted above).
console.log("\ncoverage: every provider id this install runs has a readable label");

const installProviderIds = [
  "deepseek-flash",
  "deepseek-pro",
  "mimo-pro",
  "mimo-flash",
  "minimax-M3",
  "minimax",
  "deepseek",
  "mimo-token-plan",
  "mimo-api",
  "glm-cn",
];
for (const id of installProviderIds) {
  ok(providerDefaultLabel(id) !== id, `${id} resolves to a readable default label`);
}
ok(
  installProviderIds.every((id) => providerDisplayLabel({ name: id }) === providerDefaultLabel(id)),
  "none of them needs a user-configured display name to read correctly",
);

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
