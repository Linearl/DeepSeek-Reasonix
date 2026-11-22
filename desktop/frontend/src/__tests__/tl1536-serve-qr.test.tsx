// Run: npx tsx --tsconfig tsconfig.tsx-runner.json --import ./scripts/css-stub-register.mjs src/__tests__/tl1536-serve-qr.test.tsx
//
// tl1536 acceptance pins — desktop serve connect QR for the GC mobile client:
// ① the QR value IS a fillable serve URL with auth in it:
//    http://<lan-host>:<port>?token=<gateway-token> (buildServeConnectURL is
//    the single constructor; the rendered <code> shows the same string);
// ② the pre-existing QR surface does not regress: SettingsPanel's bot-install
//    QR keeps its value source (install.result?.url), the botMobileRemote
//    locale keys survive in all three dictionaries, and the bridge mock keeps
//    carrying lan_hosts;
// ③ entry visibility: the QR block renders only when the gateway is enabled
//    + running + token + lan_hosts are all present (settings → 集成与连接 →
//    本地服务器服务 — no new surface, reinforcement of the existing panel).
//
// tl1539 additions — custom connect base (tunnel/公网中转):
// ④ buildServeConnectURLFromBase emits <base>?token=<token> and shares the
//    lan constructor's token encoding (encodeURIComponent appears exactly
//    once in the component source; both constructors delegate to one helper);
// ⑤ base_override (config key serve_connect_base_override) switches the QR:
//    non-empty → <base>?token=<token> even with no lan_hosts; empty → the
//    verbatim tl1536 lan URL (fallback default);
// ⑥ the custom base is an explicit <details> expand item inside the existing
//    QR block; typing a base and saving persists through the bridge and
//    re-encodes the QR (trimmed, scheme-defaulted https://).

import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

import type { AppBindings } from "../lib/bridge";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ntl1536 serve connect QR");

// ── source-level pins (② and lazy-import contract) ──
const frontendSrc = fileURLToPath(new URL("..", import.meta.url));
const localServerSource = readFileSync(new URL("../components/LocalServerPage.tsx", import.meta.url), "utf8");
const settingsPanelSource = readFileSync(new URL("../components/SettingsPanel.tsx", import.meta.url), "utf8");
const bridgeSource = readFileSync(new URL("../lib/bridge.ts", import.meta.url), "utf8");
const servepoolHostSource = readFileSync(new URL("../../../servepool_host.go", import.meta.url), "utf8");
const zhSource = readFileSync(new URL("../locales/zh.ts", import.meta.url), "utf8");
const enSource = readFileSync(new URL("../locales/en.ts", import.meta.url), "utf8");
const zhTWSource = readFileSync(new URL("../locales/zh-TW.ts", import.meta.url), "utf8");

ok(
  localServerSource.includes('lazy(() => import("qrcode.react")'),
  "LocalServerPage lazy-loads qrcode.react (no static chunk bloat, same shape as SettingsPanel)",
);
ok(
  localServerSource.includes("installQrURL") === false && settingsPanelSource.includes('const installQrURL = install.result?.url ?? ""'),
  "bot-install QR value source in SettingsPanel is untouched (原功能不回退)",
);
ok(
  settingsPanelSource.includes('value={installQrURL}'),
  "bot-install QRCodeSVG still renders value={installQrURL}",
);
for (const [name, src] of [["zh", zhSource], ["en", enSource], ["zh-TW", zhTWSource]] as const) {
  ok(src.includes('"settings.botMobileRemoteTitle"'), `${name} keeps the botMobileRemote locale keys (pre-existing surface intact)`);
}
ok(
  bridgeSource.includes('lan_hosts: ["192.168.1.10"]'),
  "browser mock ServePoolStatus carries lan_hosts (preview parity)",
);
ok(
  localServerSource.includes('export function buildServeConnectURL(host: string, port: number, token: string): string'),
  "buildServeConnectURL keeps the tl1536 lan constructor signature (default path verbatim)",
);
// tl1539 adaptation of the "single exported URL constructor" pin: two
// constructor shapes (lan / custom base) are allowed, but token encoding
// stays a single point — encodeURIComponent(token) appears exactly once and
// both constructors delegate to the shared serveConnectTokenQuery helper.
ok(
  localServerSource.split("encodeURIComponent(token)").length - 1 === 1,
  "token encoding lives in exactly one place (encodeURIComponent(token) x1) — the single-encoder contract holds",
);
ok(
  localServerSource.includes('export function buildServeConnectURLFromBase(base: string, token: string): string')
    && localServerSource.split("serveConnectTokenQuery(token)").length - 1 === 2,
  "buildServeConnectURLFromBase delegates to the shared token query helper (no second encoder)",
);
ok(
  servepoolHostSource.includes('json:"serve_connect_base_override') && servepoolHostSource.includes("SetServePoolConnectBase"),
  "Go side persists the override under serve_connect_base_override + SetServePoolConnectBase binding",
);
ok(
  bridgeSource.includes("SetServePoolConnectBase") && bridgeSource.includes('base_override: ""'),
  "bridge carries SetServePoolConnectBase and the mock status keeps base_override (preview parity)",
);
for (const [name, src] of [["zh", zhSource], ["en", enSource], ["zh-TW", zhTWSource]] as const) {
  ok(src.includes('"localserver.qrCustomToggle"') && src.includes('"localserver.qrCustomHint"'), `${name} keeps the tl1539 qrCustom locale keys`);
}

// ── render-level pins (① and ③) ──
const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });

const [{ createRoot }, { LocalServerPage }, { LocaleProvider }, { buildServeConnectURL, buildServeConnectURLFromBase }] = await Promise.all([
  import("react-dom/client"),
  import("../components/LocalServerPage"),
  import("../lib/i18n"),
  import("../components/LocalServerPage"),
]);

// ① pure constructor: auth rides inside the URL as ?token= — the field the
// gateway checkToken accepts and GrandCouncil's HttpClientFactory injects.
ok(
  buildServeConnectURL("192.168.1.5", 18789, "tok-abc") === "http://192.168.1.5:18789?token=tok-abc",
  "buildServeConnectURL emits http://host:port?token=<token>",
);
ok(
  buildServeConnectURL("10.0.0.2", 1234, "a/b?c") === "http://10.0.0.2:1234?token=a%2Fb%3Fc",
  "token is URL-encoded so special characters survive the QR roundtrip",
);

// ④ tl1539: the custom-base constructor swaps only the Base; the query string
// is byte-identical to the lan constructor's for the same token.
ok(
  buildServeConnectURLFromBase("https://tunnel.example.com", "tok-abc") === "https://tunnel.example.com?token=tok-abc",
  "buildServeConnectURLFromBase emits <base>?token=<token>",
);
ok(
  buildServeConnectURL("h", 1, "a/b?c").split("?")[1] === buildServeConnectURLFromBase("https://tunnel.example.com", "a/b?c").split("?")[1],
  "lan and custom-base constructors emit identical ?token= query strings (encoding consistency)",
);
ok(
  buildServeConnectURLFromBase("https://tunnel.example.com/", "t") === "https://tunnel.example.com?token=t",
  "trailing slash on the custom base is stripped",
);
ok(
  buildServeConnectURLFromBase("tunnel.example.com", "t") === "https://tunnel.example.com?token=t",
  "scheme-less custom base defaults to https://",
);
ok(
  buildServeConnectURLFromBase("   ", "t") === "",
  "blank custom base yields an empty URL (lan fallback, nothing scannable)",
);

const serveStatusCalls: number[] = [];
window.go = { main: { App: {
  async ServePoolStatus() {
    serveStatusCalls.push(1);
    return { enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789, token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: ["192.168.1.5", "100.100.2.3"] };
  },
  async SetServePoolEnabled() {},
  async SetServePoolPort() {},
} as Partial<AppBindings> as AppBindings } };

const { act } = await import("react");

// Each renderWithStatus mounts a fresh root on a fresh container: the page
// fetches ServePoolStatus only on mount, so a new status needs a new mount.
let mounted: { root: ReturnType<typeof createRoot>; el: HTMLDivElement } | null = null;
async function renderWithStatus(status: Record<string, unknown>) {
  if (mounted) {
    const prev = mounted;
    mounted = null;
    await act(async () => { prev.root.unmount(); });
    prev.el.remove();
  }
  (window.go!.main.App as { ServePoolStatus(): Promise<unknown> }).ServePoolStatus = async () => status;
  const el = document.createElement("div");
  document.body.appendChild(el);
  const r = createRoot(el);
  mounted = { root: r, el };
  // First act: mount + status fetch. The lazy qrcode.react chunk settles
  // outside this act (module loader macrotask), so a second act flushes the
  // Suspense recovery — one combined act never sees the svg.
  await act(async () => {
    r.render(
      <LocaleProvider>
        <LocalServerPage />
      </LocaleProvider>,
    );
    await new Promise((resolve) => setTimeout(resolve, 20));
  });
  await act(async () => {
    for (let i = 0; i < 6; i++) {
      await Promise.resolve();
      await new Promise((resolve) => setTimeout(resolve, 10));
    }
  });
}

const qrBlock = () => document.querySelector('[data-testid="serve-qr"]');
const qrCodeText = () => qrBlock()?.querySelector("code")?.textContent ?? "";
const qrSvg = () => qrBlock()?.querySelector("svg");

// ③ visibility: enabled + running + token + hosts → block with an svg.
await renderWithStatus({ enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789, token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: ["192.168.1.5", "100.100.2.3"] });
ok(qrBlock() !== null, "QR entry renders on the existing 本地服务 panel when enabled+running");
ok(qrSvg() !== null, "the QR renders as an svg (qrcode.react resolved through Suspense)");

// ① the value: same string the constructor builds, token inside the URL.
ok(
  qrCodeText() === "http://192.168.1.5:18789?token=tl1536-test-token",
  `QR URL carries host+port+token (got "${qrCodeText()}")`,
);
ok(qrCodeText().includes("token="), "auth parameter rides inside the URL (?token=)");

// Multi-NIC switch: picking the second host re-encodes the URL.
const select = qrBlock()?.querySelector("select");
ok(select !== null, "multi-NIC shows a host picker");
await act(async () => {
  select!.value = "1";
  select!.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await Promise.resolve();
});
ok(
  qrCodeText() === "http://100.100.2.3:18789?token=tl1536-test-token",
  `picking another host re-encodes the QR URL (got "${qrCodeText()}")`,
);

// ③ visibility: gateway off → no QR entry at all.
await renderWithStatus({ enabled: false, running: false, bind: "", addr: "", port: 18789, token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: ["192.168.1.5"] });
ok(qrBlock() === null && document.querySelector("svg") === null, "gateway disabled: no QR block renders");

// ③ visibility: running but no LAN hosts (e.g. no non-loopback IPv4) → no QR.
await renderWithStatus({ enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789, token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: [] });
ok(qrBlock() === null, "no reachable hosts: no QR block renders (nothing scannable to show)");

// ── tl1539: base_override switches the QR's Base source ──

// Default view keeps an explicit expand item (the custom-base entry), while
// the QR itself stays the verbatim tl1536 lan URL.
await renderWithStatus({ enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789, token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: ["192.168.1.5"], base_override: "" });
ok(qrBlock()?.querySelector("details") !== null, "custom base is an explicit expand item inside the existing QR block (default lan URL untouched)");
ok(qrCodeText() === "http://192.168.1.5:18789?token=tl1536-test-token", `empty base_override keeps the verbatim lan URL (got "${qrCodeText()}")`);

// ⑤ custom mode: a non-empty base_override wins over lan hosts, even with
// several of them present (picker hidden — the base source is the override).
await renderWithStatus({ enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789, token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: ["192.168.1.5", "100.100.2.3"], base_override: "https://tl1539-tunnel.example.com" });
ok(
  qrCodeText() === "https://tl1539-tunnel.example.com?token=tl1536-test-token",
  `base_override set: QR encodes <base>?token=<token>, lan hosts ignored (got "${qrCodeText()}")`,
);
ok(qrBlock()?.querySelector("select") === null, "custom mode hides the lan host picker");

// ⑤ tunnel-only visibility: no lan_hosts at all, the custom base still
// renders a scannable QR (the whole point of a tunnel address).
await renderWithStatus({ enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789, token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: [], base_override: "https://tl1539-tunnel.example.com" });
ok(qrBlock() !== null && qrSvg() !== null, "custom mode renders the QR even with no lan hosts (tunnel-only setup)");

// ⑥ save flow: expand → type a padded base → save → bridge receives the
// trimmed value and the QR re-encodes from the refreshed override.
let savedBase: string | null = null;
const appMock = window.go!.main.App as unknown as Record<string, (...a: unknown[]) => Promise<unknown>>;
appMock.SetServePoolConnectBase = async (b: unknown) => { savedBase = String(b); };
const liveStatus: Record<string, unknown> = {
  enabled: true, running: true, bind: "0.0.0.0:18789", addr: "0.0.0.0:18789", port: 18789,
  token: "tl1536-test-token", listen: "0.0.0.0:18789", lan_hosts: ["192.168.1.5"],
  get base_override() { return savedBase ?? ""; },
};
await renderWithStatus(liveStatus);
const details = qrBlock()?.querySelector("details");
ok(details !== null, "expand item present before any override is configured");
if (details) {
  (details as unknown as { open?: boolean }).open = true;
  const input = details.querySelector("input");
  const saveBtn = details.querySelector("button");
  ok(input !== null && saveBtn !== null, "expanded item exposes the base input and save button");
  if (input && saveBtn) {
    const setValue = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
    await act(async () => {
      setValue?.call(input, "  https://typed.example.com  ");
      input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
      await Promise.resolve();
    });
    await act(async () => {
      saveBtn.dispatchEvent(new dom.window.Event("click", { bubbles: true }));
      for (let i = 0; i < 8; i++) {
        await Promise.resolve();
        await new Promise((resolve) => setTimeout(resolve, 10));
      }
    });
    ok(savedBase === "https://typed.example.com", `save persists the trimmed base through the bridge (got "${savedBase}")`);
    ok(
      qrCodeText() === "https://typed.example.com?token=tl1536-test-token",
      `QR re-encodes from the refreshed override (got "${qrCodeText()}")`,
    );
  }
}

// ⑤ fallback: clearing the override returns to the verbatim lan URL.
savedBase = "";
await renderWithStatus(liveStatus);
ok(
  qrCodeText() === "http://192.168.1.5:18789?token=tl1536-test-token",
  `cleared override falls back to the verbatim tl1536 lan URL (got "${qrCodeText()}")`,
);

if (mounted) {
  const cur = mounted;
  mounted = null;
  await act(async () => { cur.root.unmount(); });
  cur.el.remove();
}

dom.window.close();
console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
