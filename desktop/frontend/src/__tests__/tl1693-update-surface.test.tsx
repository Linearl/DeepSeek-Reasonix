// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1693-update-surface.test.tsx
//
// tl1693 pins for the two new update surfaces:
//   - SidebarUpdateDot: renders only while the updater context holds an
//     available release (zero-distraction default), click opens the updates
//     tab, and a failed check stays silent (no dot, no noise).
//   - ForkReleasesList: tl1737 tightened the contract — the list renders the
//     newest release ONLY (one row, however many the channel returns); when
//     the running version is the newest the row collapses to a single
//     "up to date, running vX" line with the installer link kept; the full
//     history stays behind the "view online" link. A failed/empty lookup
//     renders nothing at all (availability first — never an error banner).

import { JSDOM } from "jsdom";

import { act } from "react";
import { useEffect } from "react";
import { createRoot } from "react-dom/client";
import { ForkReleasesList } from "../components/ForkReleasesList";
import { SidebarUpdateDot } from "../components/SidebarUpdateDot";
import { FORK_RELEASES_URL } from "../lib/forkLinks";
import type { ForkReleaseList, UpdateInfo } from "../lib/types";
import { LocaleProvider } from "../lib/i18n";
import { UpdaterProvider, useUpdater } from "../lib/useUpdater";

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

const dom = new JSDOM('<!doctype html><html><body><div id="root"></div></body></html>', {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
globalThis.Node = dom.window.Node;
globalThis.Element = dom.window.Element;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;

// The tests inject their own binding stubs through the same seam the browser
// uses (window.go.main.App), so both components resolve the real app face.
function injectApp(stub: Partial<Record<string, (...args: unknown[]) => unknown>>) {
  const w = window as unknown as {
    go?: { main?: { App?: Record<string, unknown> } };
  };
  w.go = { main: { App: { ...stub } } };
}

const availableInfo: UpdateInfo = {
  available: true,
  current: "v1.38.3-20261115-0841",
  latest: "v1.38.3-20261117-1800",
  notes: "",
  channel: "stable",
  canSelfUpdate: true,
  downloaded: false,
  downloadUrl: "",
  assetSize: 0,
};

const twoReleases: ForkReleaseList = {
  failed: false,
  releases: [
    {
      tag: "desktop-v1.38.3-20261117-1800",
      version: "v1.38.3-20261117-1800",
      notes: "newest",
      published: "2026-11-17T18:00:00Z",
      current: false,
      assets: [{ name: "Reasonix-windows-amd64.zip", url: "https://example.com/win.zip", size: 1 }],
    },
    {
      tag: "desktop-v1.38.3-20261115-0841",
      version: "v1.38.3-20261115-0841",
      notes: "running",
      published: "",
      current: true,
      assets: [],
    },
  ],
};

// tl1737: the up-to-date face — the newest release IS the running one.
const upToDateReleases: ForkReleaseList = {
  failed: false,
  releases: [
    {
      tag: "desktop-v1.38.3-20261119-0930",
      version: "v1.38.3-20261119-0930",
      notes: "latest and running",
      published: "2026-11-19T09:30:00Z",
      current: true,
      assets: [{ name: "Reasonix-windows-amd64.zip", url: "https://example.com/latest-win.zip", size: 2 }],
    },
  ],
};

let root = createRoot(document.getElementById("root")!);

// Each scenario mounts a fresh tree: UpdaterProvider and the components run
// their mount effects against the stub injected for that scenario (a reused
// instance would keep the previous scenario's state and never re-check).
async function render(children: React.ReactNode) {
  await act(async () => {
    root.unmount();
  });
  root = createRoot(document.getElementById("root")!);
  const wrapped = <LocaleProvider>{children}</LocaleProvider>;
  await act(async () => {
    root.render(wrapped);
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

// ── SidebarUpdateDot ─────────────────────────────────────────────────────────

let openCalls = 0;

// In production the startup check runs from UpdateBanner's mount effect; the
// dot itself adds no network (that is the design under test). This consumer
// plays UpdateBanner's role so the context reaches "available".
function CheckTrigger() {
  const { check } = useUpdater();
  useEffect(() => {
    void check();
  }, [check]);
  return null;
}

// Idle context (no check has run): the dot renders nothing.
await render(
  <UpdaterProvider>
    <SidebarUpdateDot onOpenUpdates={() => { openCalls += 1; }} />
  </UpdaterProvider>,
);
ok(!document.querySelector(".sidebar__utility-button--update, .sidebar__utility-button--active"),
  "sidebar dot stays hidden while no release is known (zero-distraction default)");

// An available release lights the dot; clicking it opens the updates tab.
injectApp({
  CheckUpdate: async () => availableInfo,
  Version: async () => availableInfo.current,
});
await render(
  <UpdaterProvider>
    <CheckTrigger />
    <SidebarUpdateDot onOpenUpdates={() => { openCalls += 1; }} />
  </UpdaterProvider>,
);
// Locale-independent: the dot reuses the shared --active accent face; the
// label comes from the dict (en or zh depending on the test host's locale).
const dot = document.querySelector(".sidebar__utility-button--active");
ok(dot !== null && dot.getAttribute("aria-label"), "sidebar dot lights up while a release is available");
if (dot) {
  await act(async () => {
    dot!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  ok(openCalls === 1, "clicking the dot opens the updates settings tab");
} else {
  failed += 1;
}

// ── ForkReleasesList ─────────────────────────────────────────────────────────

// Multi-release channel: the user (tl1737) asked for the newest release only,
// not the full history — exactly one row must render.
injectApp({
  ForkListReleases: async () => twoReleases,
});
await render(<ForkReleasesList />);
const rows = document.querySelectorAll(".fork-releases__row");
ok(rows.length === 1, `release list renders exactly ONE row from a multi-release channel (got ${rows.length})`);
ok(document.querySelector(".fork-releases__tag")?.textContent === "desktop-v1.38.3-20261117-1800",
  "the single row is the newest release (its tag renders)");
ok(!document.querySelector(".fork-releases__current"),
  "no per-row current badge: the running row is no longer listed (tl1737)");
const download = document.querySelector(".fork-releases__download") as HTMLButtonElement | null;
ok(download !== null && download.title === "Reasonix-windows-amd64.zip",
  "the newest release's installer link points at the platform artifact");

// The "view online" link opens the fork's releases page in the browser — the
// full history lives behind it now. openExternal falls back to window.open
// when window.runtime is absent (this DOM), so stubbing window.open pins it.
const openedUrls: string[] = [];
const realOpen = window.open;
window.open = ((url: string) => {
  openedUrls.push(url);
  return null;
}) as typeof window.open;
const online = document.querySelector(".fork-releases__online") as HTMLButtonElement | null;
ok(online !== null, "the view-online link is present beside the title");
if (online) {
  await act(async () => {
    online!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
  ok(openedUrls.length === 1 && openedUrls[0] === FORK_RELEASES_URL,
    `clicking view-online opens the fork releases page (got ${JSON.stringify(openedUrls)})`);
} else {
  failed += 1;
}
window.open = realOpen;

// Up-to-date face: the newest release IS the running one — one row collapsing
// to the "已是最新，当前 vX" line, installer link kept, no bare tag row.
injectApp({
  ForkListReleases: async () => upToDateReleases,
});
await render(<ForkReleasesList />);
const upRows = document.querySelectorAll(".fork-releases__row");
ok(upRows.length === 1, `the up-to-date face renders exactly one row (got ${upRows.length})`);
const upLine = document.querySelector(".fork-releases__uptodate");
ok(upLine !== null && upLine.textContent!.includes("已是最新")
    && upLine.textContent!.includes("v1.38.3-20261119-0930"),
  `the row reads the up-to-date line with the running version (got ${upLine?.textContent ?? "none"})`);
const upDownload = document.querySelector(".fork-releases__download") as HTMLButtonElement | null;
ok(upDownload !== null && upDownload.title === "Reasonix-windows-amd64.zip",
  "the up-to-date row keeps the installer link");
ok(!document.querySelector(".fork-releases__tag"),
  "the up-to-date face hides the bare tag row");

// A failed channel lookup renders nothing — the same silent face as the check.
injectApp({
  ForkListReleases: async () => ({ releases: [], failed: true } satisfies ForkReleaseList),
});
await render(<ForkReleasesList />);
ok(!document.querySelector(".fork-releases"), "a failed lookup hides the release list entirely");

// A rejected transport promise renders nothing too.
injectApp({
  ForkListReleases: async () => {
    throw new Error("transport down");
  },
});
await render(<ForkReleasesList />);
ok(!document.querySelector(".fork-releases"), "a transport rejection hides the release list (no error UI)");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
