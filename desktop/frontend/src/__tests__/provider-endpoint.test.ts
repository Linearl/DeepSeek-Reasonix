import {
  providerBaseURLForSave,
  providerRequestURLForFormatChange,
  providerBaseURLFromRequestURL,
  providerEndpointMismatchDetail,
  providerPathShapeDetail,
  providerRequestURLForCatalogFormatChange,
  providerRequestURLFromConfig,
} from "../lib/providerEndpoint";
let failed = 0;
function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) {
    process.stdout.write(`  PASS  ${label}\n`);
    return;
  }
  process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
  failed += 1;
}
console.log("\nprovider endpoint");

eq(providerRequestURLFromConfig("openai", "https://proxy.example.com/v1", ""), "https://proxy.example.com/v1/chat/completions", "legacy OpenAI base URLs expose their effective request URL");
eq(providerRequestURLFromConfig("anthropic", "https://proxy.example.com/v1", ""), "https://proxy.example.com/v1/messages", "legacy Anthropic base URLs expose their effective request URL");
eq(providerRequestURLFromConfig("responses", "https://proxy.example.com/v1", ""), "https://proxy.example.com/v1/responses", "legacy Responses base URLs expose their effective request URL");
eq(providerRequestURLFromConfig("openai", "https://proxy.example.com/v1", "", "https://legacy.example.com/chat/completions/"), "https://legacy.example.com/chat/completions", "legacy OpenAI chat URLs preserve historical trailing-slash normalization");
eq(providerRequestURLFromConfig("anthropic", "https://proxy.example.com/v1", "", "https://stale.example.com/chat/completions"), "https://proxy.example.com/v1/messages", "legacy Anthropic chat URLs remain ignored");
eq(providerRequestURLFromConfig("openai", "", "https://proxy.example.com/custom/chat/?token=1", "https://legacy.example.com/chat/completions"), "https://proxy.example.com/custom/chat/?token=1", "explicit request URLs remain unchanged and take priority");
eq(providerBaseURLFromRequestURL("openai", "https://proxy.example.com/v1/chat/completions?token=1"), "https://proxy.example.com/v1", "request URLs derive a query-free base URL for model discovery");

eq(providerBaseURLForSave({ kind: "openai", baseUrl: "https://api.deepseek.com/v1", chatUrl: "https://gateway.example/custom/chat/completions" }, "openai", "https://gateway.example/custom/chat/completions"), "https://api.deepseek.com/v1", "unchanged legacy providers preserve their independent base URL");
eq(providerBaseURLForSave({ kind: "anthropic", baseUrl: "https://models.example/v1/", requestUrl: "https://gateway.example/custom/messages?token=1" }, "anthropic", "https://gateway.example/custom/messages?token=1"), "https://models.example/v1/", "unchanged explicit request URLs preserve their independent base URL exactly");
eq(providerBaseURLForSave({ kind: "openai", baseUrl: "https://models.example/v1", requestUrl: "https://gateway.example/old/chat/completions" }, "openai", "https://gateway.example/new/chat/completions"), "https://gateway.example/new", "changing the request URL derives a new base URL");
eq(providerBaseURLForSave({ kind: "openai", baseUrl: "https://models.example/v1", requestUrl: "https://gateway.example/v1/chat/completions" }, "anthropic", "https://gateway.example/v1/chat/completions"), "https://gateway.example/v1/chat/completions", "changing protocol derives a new base URL under the new protocol");
eq(providerBaseURLForSave(undefined, "responses", "https://gateway.example/v1/responses"), "https://gateway.example/v1", "new providers derive their base URL from the exact request URL");

eq(providerRequestURLForFormatChange("openai", "responses", "https://gateway.example/v1/chat/completions"), "https://gateway.example/v1/responses", "explicit format switch changes a standard suffix");
eq(providerRequestURLForFormatChange("anthropic", "openai", "https://gateway.example/v1/messages"), "https://gateway.example/v1/chat/completions", "Anthropic v1 is preserved when switching to chat");
eq(providerRequestURLForFormatChange("openai", "responses", "https://gateway.example/custom?token=x"), "https://gateway.example/custom?token=x", "custom request paths and query values remain untouched");
eq(providerRequestURLForFormatChange("openai", "responses", "https://gateway.example/v1/chat/completions?version=1"), "https://gateway.example/v1/chat/completions?version=1", "query-bearing exact overrides are never rewritten");

const deepSeekCatalog = {
  brandId: "deepseek", brandLabel: "DeepSeek", region: "global", product: "api", format: "anthropic", baseUrl: "https://api.deepseek.com/anthropic",
  protocols: {
    openai: { baseUrl: "https://api.deepseek.com/v1", source: "fixture", checkedOn: "2026-09-08" },
    responses: { baseUrl: "https://api.deepseek.com", source: "fixture", checkedOn: "2026-09-08" },
    anthropic: { baseUrl: "https://api.deepseek.com/anthropic", source: "fixture", checkedOn: "2026-09-08" },
  },
};
const mimoCatalog = {
  ...deepSeekCatalog,
  brandId: "mimo",
  protocols: {
    ...deepSeekCatalog.protocols,
    openai: { ...deepSeekCatalog.protocols.openai, baseUrl: "https://api.xiaomimimo.com/v1" },
    responses: { ...deepSeekCatalog.protocols.responses, baseUrl: "https://api.xiaomimimo.com/v1" },
  },
};
eq(providerRequestURLForCatalogFormatChange("anthropic", "openai", "https://api.deepseek.com/anthropic/v1/messages", deepSeekCatalog), "https://api.deepseek.com/v1/chat/completions", "official Anthropic route switches to the registered Chat route");
eq(providerRequestURLForCatalogFormatChange("anthropic", "responses", "https://api.deepseek.com/anthropic/v1/messages", deepSeekCatalog), "https://api.deepseek.com/responses", "official Anthropic route switches to the registered Responses route");
eq(providerRequestURLForCatalogFormatChange("anthropic", "openai", "https://gateway.example/custom/v1/messages", deepSeekCatalog), "https://gateway.example/custom/v1/messages", "custom gateway remains byte-for-byte unchanged");
eq(providerRequestURLForCatalogFormatChange("anthropic", "openai", "https://api.deepseek.com/anthropic/v1/messages?token=x", deepSeekCatalog), "https://api.deepseek.com/anthropic/v1/messages?token=x", "query-bearing official host override stays untouched");
eq(providerEndpointMismatchDetail("openai", "https://api.deepseek.com/anthropic/v1/chat/completions", deepSeekCatalog).mismatch, true, "mixed DeepSeek path is blocked");
eq(providerEndpointMismatchDetail("openai", "https://api.deepseek.com/anthropic/v1/chat/completions", deepSeekCatalog).recommendedUrl, "https://api.deepseek.com/v1/chat/completions", "mixed DeepSeek path recommends the catalog route");
eq(providerEndpointMismatchDetail("openai", "https://api.deepseek.com/chat/completions", deepSeekCatalog).mismatch, false, "working DeepSeek root alias remains valid");
eq(providerEndpointMismatchDetail("openai", "https://api.xiaomimimo.com/v1/chat/completions", mimoCatalog).mismatch, false, "shared Chat and Responses base remains valid");
eq(providerEndpointMismatchDetail("openai", "https://gateway.example/anthropic/v1/chat/completions", deepSeekCatalog).mismatch, false, "custom host is not catalog-rewritten");
eq(providerEndpointMismatchDetail("openai", "https://api.deepseek.com/anthropic/custom/chat/completions", deepSeekCatalog).mismatch, false, "unknown custom path on the official host stays user-owned");

console.log("\nprovider path shape (tl1774)");
const shape = (kind: string, url: string) => providerPathShapeDetail(kind, url);
eq(shape("openai", "http://192.168.1.50:8080").missingProtocolPath, true, "bare LocalAI-style host warns before the 404 does");
eq(shape("openai", "http://192.168.1.50:8080").suggestedUrl, "http://192.168.1.50:8080/chat/completions", "bare host suggestion appends the chat path");
eq(shape("openai", "http://host:8080/v1").missingProtocolPath, true, "base+/v1 still lacks the chat path");
eq(shape("openai", "http://host:8080/v1").suggestedUrl, "http://host:8080/v1/chat/completions", "v1 base suggestion appends /chat/completions");
eq(shape("openai", "https://api.openai.com/v1/chat/completions").missingProtocolPath, false, "correct openai suffix stays silent");
eq(shape("openai", "https://gw.example/chat/completions").missingProtocolPath, false, "root chat suffix stays silent");
eq(shape("openai", "http://host:8080/").missingProtocolPath, true, "trailing-slash bare host warns");
eq(shape("anthropic", "https://gw.example").missingProtocolPath, true, "bare anthropic base warns");
eq(shape("anthropic", "https://gw.example").suggestedUrl, "https://gw.example/v1/messages", "bare anthropic suggestion builds the v1 messages path");
eq(shape("anthropic", "https://gw.example/v1").suggestedUrl, "https://gw.example/v1/messages", "v1 anthropic base reuses the existing version segment");
eq(shape("anthropic", "https://gw.example/v1/messages").missingProtocolPath, false, "correct anthropic suffix stays silent");
eq(shape("anthropic", "https://gw.example/messages").missingProtocolPath, false, "short anthropic suffix stays silent");
eq(shape("responses", "https://gw.example").missingProtocolPath, true, "bare responses base warns");
eq(shape("responses", "https://gw.example").suggestedUrl, "https://gw.example/responses", "bare responses suggestion appends /responses");
eq(shape("responses", "https://gw.example/v1/responses").missingProtocolPath, false, "correct responses suffix stays silent");
eq(shape("dashscope-responses", "https://gw.example").suggestedUrl, "https://gw.example/responses", "dashscope alias normalizes to the responses shape");
eq(shape("openai", "https://gw.example/api?token=1").missingProtocolPath, false, "query-bearing addresses stay user-owned and silent");
eq(shape("openai", "https://user:pass@gw.example/api").missingProtocolPath, false, "userinfo addresses stay user-owned and silent");
eq(shape("openai", "https://gw.example/api%2Fpath").missingProtocolPath, false, "percent-encoded custom paths stay silent");
eq(shape("openai", "not a url").missingProtocolPath, false, "unparseable drafts stay silent");
eq(shape("openai", "").missingProtocolPath, false, "empty drafts stay silent");
eq(shape("unknown-kind", "http://gw.example").missingProtocolPath, false, "unregistered protocols stay silent");
eq(shape("openai", "https://tokenrhythm.studio/v1").missingProtocolPath, false, "Token Rhythm official base is runtime-rewritten and stays silent");
eq(shape("openai", "https://api.stepfun.com/step_plan").missingProtocolPath, false, "StepFun documented step_plan base stays silent");
eq(shape("openai", "https://tokenrhythm.studio/v1/models").missingProtocolPath, false, "Token Rhythm models-path base is runtime-rewritten and stays silent");
eq(shape("openai", "https://gw.example/api/v2/ask").missingProtocolPath, true, "custom gateway paths warn while confirm-to-save stays available");
eq(shape("openai", "https://api.deepseek.com/anthropic/v1/chat/completions").missingProtocolPath, false, "cross-protocol suffixes defer to the mismatch banner");

if (failed > 0) process.exit(1);
