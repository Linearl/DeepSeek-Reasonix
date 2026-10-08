import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { DiagnosticsSettingsPage } from "../components/DiagnosticsSettingsPage";
import type { AppBindings } from "../lib/bridge";
import { LocaleProvider } from "../lib/i18n";
import type { CapabilityDiagnosticsReport, SettingsTab } from "../lib/types";

function ok(value: unknown, message: string) {
  if (!value) throw new Error(message);
}

function flush(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

async function waitFor(label: string, predicate: () => boolean) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    await act(async () => {
      await flush();
    });
    if (predicate()) return;
  }
  throw new Error(`timed out waiting for ${label}: ${document.body?.textContent?.slice(0, 400) ?? ""}`);
}

function installDom() {
  const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
    pretendToBeVisual: true,
    url: "http://localhost/",
  });
  (globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  globalThis.window = dom.window as unknown as Window & typeof globalThis;
  globalThis.document = dom.window.document;
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
  globalThis.Node = dom.window.Node;
  globalThis.HTMLElement = dom.window.HTMLElement;
  globalThis.HTMLButtonElement = dom.window.HTMLButtonElement;
  globalThis.HTMLInputElement = dom.window.HTMLInputElement;
  globalThis.Event = dom.window.Event;
  globalThis.MouseEvent = dom.window.MouseEvent;
  globalThis.localStorage = dom.window.localStorage;
  globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
  globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: () => ({
      matches: true,
      media: "(prefers-reduced-motion: reduce)",
      onchange: null,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
      dispatchEvent: () => false,
    }),
  });
  return dom;
}

function baseReport(runtime: boolean): CapabilityDiagnosticsReport {
  return {
    schema_version: 1,
    root: "<workspace>",
    live: false,
    summary: {
      errors: 1,
      warnings: 1,
      infos: runtime ? 1 : 0,
      instructions: 1,
      skills: 1,
      commands: 0,
      hooks: 0,
      plugins: 0,
      mcp_servers: 1,
    },
    instructions: { docs: [{ path: "<workspace>/AGENTS.md", scope: "project", directory: "<workspace>", depth: 0, order: 1 }] },
    skills: {
      roots: [{ path: "<workspace>/.reasonix/skills", scope: "project", status: "ok" }],
      entries: [{ name: "demo", path: "<workspace>/.reasonix/skills/demo/SKILL.md", status: "winner" }],
      winners: 1,
      shadowed: 0,
    },
    commands: { roots: [], entries: [], winners: 0, shadowed: 0 },
    hooks: { trusted_project: false, project_defines_hooks: true, sources: [], entries: [] },
    plugins: { packages: [] },
    mcp: {
      servers: [{
        name: "demo-mcp",
        transport: "stdio",
        start_intent: "automatic",
        source: "toml",
        runtime_status: runtime ? "connected" : undefined,
        tool_count: runtime ? 2 : undefined,
      }],
    },
    issues: [
      {
        severity: "error",
        code: "mcp.command_not_found",
        subsystem: "mcp",
        name: "demo-mcp",
        message: "command missing",
        remediation: "install binary",
        settings_tab: "mcp",
      },
      {
        severity: "warning",
        code: "skill.missing_description",
        subsystem: "skills",
        name: "nodesc",
        message: "no description",
        settings_tab: "skills",
      },
    ],
  };
}

console.log("diagnostics settings page");

{
  const calls: boolean[] = [];
  const navigations: SettingsTab[] = [];
  installDom();
  // Prefer English labels for stable button text assertions.
  window.localStorage.setItem("reasonix-lang", "en");

  window.go = {
    main: {
      App: {
        CapabilityDiagnostics: async (includeSessionRuntime: boolean) => {
          calls.push(includeSessionRuntime);
          return baseReport(includeSessionRuntime);
        },
      } as Partial<AppBindings> as AppBindings,
    },
  };

  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);

  await act(async () => {
    root.render(
      React.createElement(
        LocaleProvider,
        null,
        React.createElement(DiagnosticsSettingsPage, {
          onNavigate: (tab: SettingsTab) => {
            navigations.push(tab);
          },
        }),
      ),
    );
    await flush();
  });

  await waitFor("static report", () => (rootEl.textContent || "").includes("mcp.command_not_found"));
  ok(calls[0] === false, "initial load must request static report (includeSessionRuntime=false)");
  ok((rootEl.textContent || "").includes("skill.missing_description"), "warnings must render");
  ok(rootEl.querySelector(".diag-summary"), "health summary must render");
  const frontendToggle = rootEl.querySelector('[data-testid="frontend-diagnostics-settings"] [role="switch"]');
  ok(frontendToggle, "frontend diagnostics switch must be visible in the production diagnostics settings page");
  ok(frontendToggle?.getAttribute("aria-checked") === "false", "frontend diagnostics switch starts off");

  const runtimeToggle = rootEl.querySelector('input[type="checkbox"]') as HTMLInputElement | null;
  ok(runtimeToggle, "runtime toggle must exist");
  ok(runtimeToggle!.checked === false, "runtime toggle starts unchecked");
  await act(async () => {
    // Use click so React's controlled onChange sees the flipped checked state.
    runtimeToggle!.click();
    await flush();
  });
  await waitFor("runtime reload", () => calls.length >= 2 && calls[calls.length - 1] === true);

  const gotoBtn = Array.from(rootEl.querySelectorAll("button")).find((b) =>
    (b.textContent || "").includes("Open settings"),
  );
  ok(gotoBtn, "goto settings button must exist on issue with settings_tab");
  await act(async () => {
    gotoBtn!.dispatchEvent(new MouseEvent("click", { bubbles: true }));
    await flush();
  });
  ok(navigations.includes("mcp"), `goto settings must navigate to mcp, got ${JSON.stringify(navigations)}`);

  const paths = rootEl.querySelectorAll(".diag-path");
  ok(paths.length > 0, "redacted paths must render");

  await act(async () => {
    root.unmount();
  });
}

{
  installDom();
  window.localStorage.setItem("reasonix-lang", "en");

  const nullArrays = baseReport(false) as unknown as Record<string, unknown>;
  nullArrays.summary = {
    errors: 0,
    warnings: 0,
    infos: 0,
    instructions: 0,
    skills: 0,
    commands: 0,
    hooks: 0,
    plugins: 0,
    mcp_servers: 0,
  };
  nullArrays.issues = null;
  nullArrays.instructions = { docs: null };
  nullArrays.skills = { roots: null, entries: null, winners: 0, shadowed: 0 };
  nullArrays.commands = { roots: null, entries: null, winners: 0, shadowed: 0 };
  nullArrays.hooks = { trusted_project: false, project_defines_hooks: false, sources: null, entries: null };
  nullArrays.plugins = { packages: null };
  nullArrays.mcp = { servers: null };

  window.go = {
    main: {
      App: {
        CapabilityDiagnostics: async () => nullArrays as unknown as CapabilityDiagnosticsReport,
      } as Partial<AppBindings> as AppBindings,
    },
  };

  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);

  await act(async () => {
    root.render(
      React.createElement(
        LocaleProvider,
        null,
        React.createElement(DiagnosticsSettingsPage),
      ),
    );
    await flush();
  });

  await waitFor("null arrays to render as empty", () => (rootEl.textContent || "").includes("No issues found"));
  ok(rootEl.querySelector(".diag-summary"), "null arrays must not crash the diagnostics page");

  await act(async () => {
    root.unmount();
  });
}

{
  installDom();
  window.localStorage.setItem("reasonix-lang", "en");

  window.go = {
    main: {
      App: {
        CapabilityDiagnostics: async () => baseReport(false),
        CrashPendingDiagnostics: async () => ({
          count: 10,
          capacity: 10,
          retentionDays: 14,
          oldestAt: "2026-10-07T01:25:00Z",
          newestAt: "2026-10-08T01:32:00Z",
          atCapacity: true,
        }),
      } as Partial<AppBindings> as AppBindings,
    },
  };

  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);

  await act(async () => {
    root.render(
      React.createElement(
        LocaleProvider,
        null,
        React.createElement(DiagnosticsSettingsPage),
      ),
    );
    await flush();
  });

  // Task 618: the crash-pending queue must be visible in the diagnostics page,
  // with the cap warning when uploads stay failing.
  await waitFor("crash pending section", () => (rootEl.textContent || "").includes("Crash report local queue"));
  const text = rootEl.textContent || "";
  ok(text.includes("10 pending"), "pending count renders with capacity context");
  ok(text.includes("queue is full"), "at-capacity warning renders");
  ok(Boolean(rootEl.querySelector('[data-testid="crash-pending-diagnostics"]')), "crash pending section is testable");

  await act(async () => {
    root.unmount();
  });
}

// Task 624: zh locale renders localized issue sentences while the issue code
// stays English, and the runtime-doctor section labels localize while the raw
// backend dump is kept verbatim as an annotated technical value.
{
  installDom();
  // Pref persistence lives in desktop config; detectLocale falls back to
  // navigator.language, so emulate a Chinese UI environment here.
  Object.defineProperty(window.navigator, "language", { configurable: true, value: "zh-CN" });

  const zhReport = baseReport(false);
  zhReport.issues = [
    {
      severity: "warning",
      code: "skill.missing_description",
      subsystem: "skills",
      name: "nodesc",
      message: "skill has no description frontmatter; index quality is reduced",
      remediation: "Add a one-line description: field to the skill frontmatter",
      settings_tab: "skills",
    },
  ];

  const doctorReport = {
    text: "runtime status: unavailable\n",
    publishedGeneration: 7,
    allowResume: true,
    cleanRollback: false,
    hasIrreversible: false,
    noOpRebuilds: 1,
    fullRebuilds: 2,
    subgraphRebuilds: 3,
    staleDrops: 4,
    admissionRejected: 5,
    runtimeOwnerFallbacks: 6,
  };

  window.go = {
    main: {
      App: {
        CapabilityDiagnostics: async () => zhReport,
        RuntimeDoctor: async () => doctorReport,
        CrashPendingDiagnostics: async () => ({ count: 0, capacity: 50, retentionDays: 14, atCapacity: false }),
      } as Partial<AppBindings> as AppBindings,
    },
  };

  const rootEl = document.getElementById("root");
  if (!rootEl) throw new Error("missing root");
  const root = createRoot(rootEl);

  await act(async () => {
    root.render(
      React.createElement(
        LocaleProvider,
        null,
        React.createElement(DiagnosticsSettingsPage),
      ),
    );
    await flush();
  });

  // Localized message + remediation under zh, with the English issue code intact.
  await waitFor("zh issue copy", () => (rootEl.textContent || "").includes("技能缺少 description frontmatter"));
  const zhText = rootEl.textContent || "";
  ok(zhText.includes("skill.missing_description"), "issue code must stay English and copyable under zh");
  ok(zhText.includes("在技能 frontmatter 中补一行"), "remediation must localize under zh");
  ok(!zhText.includes("index quality is reduced"), "unlocalized backend sentence must not render under zh");

  // Runtime doctor section: localized labels, verbatim raw dump below the caption.
  await waitFor("zh runtime section", () => zhText.includes("扩展运行时（v2）"));
  ok(zhText.includes("允许恢复"), "allow-resume label must localize under zh");
  ok(zhText.includes("属主回退=6"), "runtime metric labels must localize under zh");
  ok(zhText.includes("技术值，保留英文"), "raw dump caption must render under zh");
  ok(zhText.includes("runtime status: unavailable"), "raw backend dump must stay verbatim (technical value)");

  await act(async () => {
    root.unmount();
  });
}

console.log("diagnostics-settings: ok");
