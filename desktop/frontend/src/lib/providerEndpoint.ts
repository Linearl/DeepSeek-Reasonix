import type { ProviderCatalog } from "./providerCatalogTypes";

export interface ProviderEndpointConfig {
  kind: string;
  baseUrl: string;
  requestUrl?: string;
  chatUrl?: string;
}

export function trimmedBaseURL(value: string): string {
  return value.trim().replace(/\/+$/, "");
}

export function providerRequestURLFromConfig(
  kind: string,
  baseUrl: string,
  requestUrl: string,
  legacyChatUrl = "",
): string {
  const exactRequestURL = requestUrl.trim();
  if (exactRequestURL) return exactRequestURL;
  if (kind.trim().toLowerCase() === "openai") {
    const legacyOpenAIRequestURL = legacyChatUrl.trim().replace(/\/+$/, "");
    if (legacyOpenAIRequestURL) return legacyOpenAIRequestURL;
  }
  const base = trimmedBaseURL(baseUrl);
  if (!base) return "";
  switch (kind.trim().toLowerCase()) {
    case "anthropic":
      return base.endsWith("/v1") ? `${base}/messages` : `${base}/v1/messages`;
    case "responses":
    case "dashscope-responses":
      return `${base}/responses`;
    default:
      return `${base}/chat/completions`;
  }
}

export function providerBaseURLFromRequestURL(kind: string, requestUrl: string): string {
  const exactRequestURL = requestUrl.trim();
  if (!exactRequestURL) return "";
  const normalizedKind = kind.trim().toLowerCase();
  const suffixes = normalizedKind === "anthropic"
    ? ["/v1/messages"]
    : normalizedKind === "responses" || normalizedKind === "dashscope-responses"
      ? ["/responses"]
      : ["/chat/completions"];
  try {
    const parsed = new URL(exactRequestURL);
    const pathname = parsed.pathname.replace(/\/+$/, "");
    const suffix = suffixes.find((candidate) => pathname.endsWith(candidate));
    parsed.pathname = suffix ? pathname.slice(0, -suffix.length) || "/" : pathname || "/";
    parsed.search = "";
    parsed.hash = "";
    return trimmedBaseURL(parsed.toString());
  } catch {
    const suffix = suffixes.find((candidate) => exactRequestURL.endsWith(candidate));
    if (suffix) return trimmedBaseURL(exactRequestURL.slice(0, -suffix.length));
  }
  return trimmedBaseURL(exactRequestURL);
}

export function providerBaseURLForSave(
  initial: ProviderEndpointConfig | undefined,
  effectiveKind: string,
  effectiveRequestUrl: string,
): string {
  const requestUrl = effectiveRequestUrl.trim();
  if (initial) {
    const initialRequestUrl = providerRequestURLFromConfig(
      initial.kind,
      initial.baseUrl,
      initial.requestUrl ?? "",
      initial.chatUrl ?? "",
    );
    const kindUnchanged = initial.kind.trim().toLowerCase() === effectiveKind.trim().toLowerCase();
    if (kindUnchanged && initialRequestUrl === requestUrl) {
      return initial.baseUrl.trim();
    }
  }
  return providerBaseURLFromRequestURL(effectiveKind, requestUrl);
}

// Only rewrite a standard API suffix on an explicit protocol selection. Custom
// paths, queries and fragments are user-owned and must stay byte-for-byte intact.
export function providerRequestURLForFormatChange(previousKind: string, nextKind: string, requestUrl: string): string {
  if (previousKind === nextKind || !requestUrl) return requestUrl;
  try {
    const url = new URL(requestUrl);
    if (url.search || url.hash) return requestUrl;
    const previousSuffix = previousKind === "anthropic" ? "/messages"
      : previousKind === "responses" ? "/responses" : previousKind === "openai" ? "/chat/completions" : "";
    if (!previousSuffix || !url.pathname.endsWith(previousSuffix)) return requestUrl;
    const nextSuffix = nextKind === "anthropic" ? "/messages"
      : nextKind === "responses" ? "/responses" : nextKind === "openai" ? "/chat/completions" : "";
    if (!nextSuffix) return requestUrl;
    url.pathname = url.pathname.slice(0, -previousSuffix.length) + nextSuffix;
    return url.toString();
  } catch { return requestUrl; }
}

function normalizedProtocol(kind: string): string {
  const normalized = kind.trim().toLowerCase();
  return normalized === "dashscope-responses" ? "responses" : normalized;
}

function protocolSuffix(kind: string): string {
  switch (normalizedProtocol(kind)) {
    case "anthropic": return "/messages";
    case "responses": return "/responses";
    case "openai": return "/chat/completions";
    default: return "";
  }
}

export function providerCatalogRequestURL(catalog: ProviderCatalog | undefined, kind: string): string {
  const route = catalog?.protocols?.[normalizedProtocol(kind)];
  return route?.baseUrl ? providerRequestURLFromConfig(kind, route.baseUrl, "") : "";
}

function normalizedExactURL(value: string): string {
  try {
    const parsed = new URL(value.trim());
    if (parsed.search || parsed.hash || parsed.username || parsed.password) return "";
    parsed.pathname = parsed.pathname.replace(/\/+$/, "") || "/";
    return parsed.toString();
  } catch {
    return "";
  }
}

// Switch to a catalog route only when the current URL is one of that catalog's
// registered official routes. Custom gateways and query-bearing routes keep the
// conservative suffix-only behavior.
export function providerRequestURLForCatalogFormatChange(
  previousKind: string,
  nextKind: string,
  requestUrl: string,
  catalog?: ProviderCatalog,
): string {
  const current = normalizedExactURL(requestUrl);
  if (current && catalog?.protocols) {
    const matchesOfficialRoute = Object.entries(catalog.protocols).some(([kind, route]) =>
      normalizedExactURL(providerRequestURLFromConfig(kind, route.baseUrl, "")) === current,
    );
    if (matchesOfficialRoute) {
      const next = providerCatalogRequestURL(catalog, nextKind);
      if (next) return next;
    }
  }
  if (catalog?.protocols) return requestUrl;
  return providerRequestURLForFormatChange(previousKind, nextKind, requestUrl);
}

export interface ProviderPathShapeDetail {
  // true when the typed address carries no protocol chat path at all (a bare
  // base or base+/v1), so the runtime would POST to exactly this URL and a
  // 404 is the likely first symptom. Deliberately narrower than a rewrite:
  // warning only, the address stays user-owned until the user applies the
  // suggestion or confirms the custom path by saving.
  missingProtocolPath: boolean;
  suggestedUrl: string;
}

// tl1774: expected terminal chat-path suffixes per protocol. openai accepts
// both /v1/chat/completions and /chat/completions endings; anthropic accepts
// /v1/messages and the shorter /messages form the runtime builds for /v1 bases.
function protocolPathSuffixes(kind: string): string[] {
  switch (normalizedProtocol(kind)) {
    case "openai": return ["/chat/completions"];
    case "anthropic": return ["/v1/messages", "/messages"];
    case "responses": return ["/responses"];
    default: return [];
  }
}

// Mirrors Go config.ProviderRequestURL: the request URL the contract layer
// would build from a base URL, so the editor's suggestion matches what the
// runtime actually does with a base-only URL.
function suggestedProtocolRequestURL(kind: string, base: string): string {
  switch (normalizedProtocol(kind)) {
    case "anthropic":
      return base.endsWith("/v1") ? `${base}/messages` : `${base}/v1/messages`;
    case "responses":
      return `${base}/responses`;
    default:
      return `${base}/chat/completions`;
  }
}

// Mirrors Go openai.canonicalKnownVendorChatURL: official vendor bases the
// runtime rewrites to their canonical chat path, so they work exactly as
// typed and must never trigger the shape warning. Hosts, ports, schemes and
// known path sets follow the Go authority (tokenrhythm.go / stepfun.go);
// paths ending in a chat leaf are already covered by the suffix check, the
// bare and /models leaves are the ones that would otherwise false-alarm.
function isKnownVendorCanonicalChatForm(value: string): boolean {
  let parsed: URL;
  try { parsed = new URL(value); } catch { return false; }
  if (parsed.protocol !== "https:" || parsed.username || parsed.password) return false;
  if (parsed.search || parsed.hash || (parsed.port && parsed.port !== "443")) return false;
  const host = parsed.hostname.toLowerCase();
  let path = parsed.pathname;
  if (path !== "/") path = path.replace(/\/+$/, "");
  const chatLeaves = ["/chat/completions", "/models"];
  const rootPrefixes = ["", "/v1", "/v1/v1"];
  if (host === "tokenrhythm.studio") {
    if (path === "" || path === "/" || rootPrefixes.includes(path)) return true;
    return chatLeaves.some(leaf => rootPrefixes.some(prefix => path === prefix + leaf || path === prefix + leaf + leaf));
  }
  const stepPrefixes = ["/step_plan", "/step_plan/v1", "/step_plan/v1/v1"];
  if (host === "api.stepfun.com" || host === "api.stepfun.ai") {
    if (stepPrefixes.includes(path)) return true;
    return chatLeaves.some(leaf => stepPrefixes.some(prefix => path === prefix + leaf || path === prefix + leaf + leaf));
  }
  return false;
}

// tl1774 save-time local shape check. Conservative like every endpoint check
// in this file: query/hash/userinfo addresses and percent-encoded custom paths
// are user-owned escape hatches and stay silent; unparseable values stay
// silent (the URL input already tolerates drafts mid-typing).
export function providerPathShapeDetail(kind: string, requestUrl: string): ProviderPathShapeDetail {
  const empty: ProviderPathShapeDetail = { missingProtocolPath: false, suggestedUrl: "" };
  const suffixes = protocolPathSuffixes(kind);
  const value = requestUrl.trim();
  if (suffixes.length === 0 || !value) return empty;
  let parsed: URL;
  try { parsed = new URL(value); } catch { return empty; }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") return empty;
  if (parsed.search || parsed.hash || parsed.username || parsed.password) return empty;
  if (!parsed.hostname) return empty;
  if (parsed.pathname.includes("%")) return empty;
  const path = parsed.pathname.replace(/\/+$/, "");
  if (suffixes.some(suffix => path.endsWith(suffix))) return empty;
  if (isKnownVendorCanonicalChatForm(value)) return empty;
  return { missingProtocolPath: true, suggestedUrl: suggestedProtocolRequestURL(kind, trimmedBaseURL(value)) };
}

export interface ProviderEndpointMismatchDetail {
  mismatch: boolean;
  recommendedUrl: string;
}

export function providerEndpointMismatchDetail(
  kind: string,
  address: string,
  catalog?: ProviderCatalog,
): ProviderEndpointMismatchDetail {
  const recommendedUrl = providerCatalogRequestURL(catalog, kind);
  let parsed: URL;
  try { parsed = new URL(address); } catch { return { mismatch: false, recommendedUrl }; }
  if (parsed.search || parsed.hash || parsed.username || parsed.password) return { mismatch: false, recommendedUrl };
  const path = parsed.pathname.replace(/\/+$/, "");
  const expected = protocolSuffix(kind);
  if (!expected) return { mismatch: false, recommendedUrl };
  if (recommendedUrl && normalizedExactURL(address) === normalizedExactURL(recommendedUrl)) return { mismatch: false, recommendedUrl };
  const knownSuffix = ["/messages", "/chat/completions", "/responses"].find(suffix => path.endsWith(suffix));
  if (knownSuffix && !path.endsWith(expected)) return { mismatch: true, recommendedUrl };
  if (!catalog?.protocols || !path.endsWith(expected)) return { mismatch: false, recommendedUrl };

  const selectedRoute = catalog.protocols[normalizedProtocol(kind)];
  if (!selectedRoute) return { mismatch: false, recommendedUrl };
  let selectedBase: URL;
  try { selectedBase = new URL(selectedRoute.baseUrl); } catch { return { mismatch: false, recommendedUrl }; }
  if (selectedBase.host.toLowerCase() !== parsed.host.toLowerCase()) return { mismatch: false, recommendedUrl };
  for (const [otherKind, route] of Object.entries(catalog.protocols)) {
    if (normalizedProtocol(otherKind) === normalizedProtocol(kind)) continue;
    let foreignBase: URL;
    try { foreignBase = new URL(providerRequestURLFromConfig(otherKind, route.baseUrl, "")); } catch { continue; }
    if (foreignBase.host.toLowerCase() !== parsed.host.toLowerCase()) continue;
    const otherSuffix = protocolSuffix(otherKind);
    const foreignRequestPath = foreignBase.pathname.replace(/\/+$/, "");
    const foreignRoot = foreignRequestPath.slice(0, -otherSuffix.length);
    if (!foreignRoot || foreignRoot === "/") continue;
    if (path === `${foreignRoot}${expected}`) {
      return { mismatch: true, recommendedUrl };
    }
  }
  return { mismatch: false, recommendedUrl };
}
