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
  "buildServeConnectURL is the single exported URL constructor",
);

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

const [{ createRoot }, { LocalServerPage }, { LocaleProvider }, { buildServeConnectURL }] = await Promise.all([
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

if (mounted) {
  const cur = mounted;
  mounted = null;
  await act(async () => { cur.root.unmount(); });
  cur.el.remove();
}

dom.window.close();
console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
