// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1792-banner-copy-error.test.tsx
//
// tl1792: the config-problem banner gains a "copy error details" button that
// puts the FULL untruncated diagnostics (path + toml line original text, all
// warnings joined) onto the clipboard, while the existing open/reload/dismiss
// buttons keep their exact wiring and the banner gains no other structure.

import { JSDOM } from "jsdom";

import { act } from "react";
import { createRoot } from "react-dom/client";
import { SessionStatusBanners } from "../app-shell/SessionStatusBanners";
import { LocaleProvider } from "../lib/i18n";
import { UpdaterProvider } from "../lib/useUpdater";
import type { Translator } from "../lib/i18n";

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

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', { url: "http://localhost/" });
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;

// Deterministic clipboard channel: navigator.clipboard is unavailable under
// the Node/jsdom test runtime, so the three-tier writer lands on the Wails
// runtime branch — exactly the branch a real desktop user exercises too.
const clipboardWrites: string[] = [];
window.runtime = {
  EventsOn: () => () => {},
  ClipboardSetText: (text: string) => {
    clipboardWrites.push(text);
    return Promise.resolve(true);
  },
} as unknown as Window["runtime"];

// The fake translator returns keys verbatim, so tests locate buttons by their
// locale key instead of any one language's copy.
const t = ((key: string) => key) as unknown as Translator;

const calls = { open: 0, reload: 0, dismiss: 0 };

const warnings = [
  "user config C:\\Users\\demo\\AppData\\Roaming\\reasonix\\config.toml is invalid (toml: line 12 (last key \"models\"): expected a table key but got a newline); using last-known-good snapshot in memory without modifying the original file",
  "project reasonix.toml is invalid (toml: line 3: unexpected key \"sandbx\")",
];

function bannerProps(configWarnings: readonly string[]) {
  return {
    t,
    takenOver: false,
    reclaimTabId: "",
    reclaimBusyTabId: null,
    onReclaim: () => {},
    leaseBlocked: null,
    startupError: undefined,
    takeoverDialogTabId: null,
    onOpenTakeover: () => {},
    onCloseTakeover: () => {},
    configWarnings,
    onOpenConfigFile: () => {
      calls.open += 1;
    },
    onReloadConfigFile: () => {
      calls.reload += 1;
    },
    onDismissConfigWarnings: () => {
      calls.dismiss += 1;
    },
    providerSetupNeeded: false,
    needsOnboarding: false,
    onConfigureProvider: () => {},
    updateChecksEnabled: false,
    onShowReleaseNotes: () => {},
  };
}

type BannerProps = Parameters<typeof SessionStatusBanners>[0];

function findButton(label: string): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll("button")).find((b) => b.textContent === label);
}

async function flush(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing test root");
const root = createRoot(rootElement);

async function renderBanners(configWarnings: readonly string[]) {
  await act(async () => {
    root.render(
      <LocaleProvider>
        <UpdaterProvider>
          <SessionStatusBanners {...(bannerProps(configWarnings) as BannerProps)} />
        </UpdaterProvider>
      </LocaleProvider>,
    );
  });
  await flush();
}

console.log("\ntl1792 config banner copy-error button");

await renderBanners(warnings);

eq(
  [findButton("config.openConfig"), findButton("config.copyError"), findButton("config.reloadConfig"), findButton("updater.dismiss")].every(Boolean),
  true,
  "banner shows open / copy / reload / dismiss buttons",
);

const copyButton = findButton("config.copyError");
if (!copyButton) throw new Error("missing copy button");
await act(async () => {
  copyButton.click();
});
await flush();

eq(clipboardWrites.length, 1, "exactly one clipboard write per copy click");
const payload = clipboardWrites[0] ?? "";
eq(payload, warnings.join("\n"), "clipboard receives all warnings joined verbatim");
ok(payload.includes('C:\\Users\\demo\\AppData\\Roaming\\reasonix\\config.toml'), "payload keeps the full config path");
ok(payload.includes('toml: line 12 (last key "models"): expected a table key but got a newline'), "payload keeps the toml line original text");
ok(!payload.includes("…") && !payload.endsWith("…"), "payload is not ellipsised");

ok(findButton("msg.copied") !== undefined, "copy button flips to the copied acknowledgement");
await act(async () => {
  await new Promise((resolve) => setTimeout(resolve, 1400));
});
await flush();
ok(findButton("msg.copied") === undefined && findButton("config.copyError") !== undefined, "copied acknowledgement reverts after 1.2s");

const openButton = findButton("config.openConfig");
const reloadButton = findButton("config.reloadConfig");
const dismissButton = findButton("updater.dismiss");
if (!openButton || !reloadButton || !dismissButton) throw new Error("missing existing banner buttons");
await act(async () => {
  openButton.click();
  reloadButton.click();
  dismissButton.click();
});
await flush();

eq(JSON.stringify(calls), JSON.stringify({ open: 1, reload: 1, dismiss: 1 }), "open / reload / dismiss fire their original handlers once each");
eq(clipboardWrites.length, 1, "existing banner buttons never touch the clipboard");

await renderBanners([]);
ok(findButton("config.copyError") === undefined, "no copy button without config warnings");

await act(async () => {
  root.unmount();
});

if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  process.exit(1);
}
process.stdout.write(`\n${passed} passed\n`);
