// Run: tsx src/__tests__/remote-navigation-provider-contract.test.ts
//
// B3 remote product fix guard: the mounted App tree must supply the
// RemoteNavigationContext value (Provider + remote-project queue branch) and
// the Composer finishing-followup inbox props. Before the fix the only
// production Provider lived in AppRuntimeView — a tree no root mounts since
// a141c4aa1 — so remote topic clicks resolved notReady -> cancelled silently.
// Source-level contract: enumerate every production Provider site, assert the
// App tree wiring, and keep the a141 composition half in place (fence 8:
// wire the split, never revert it).

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else failed += 1;
}

const srcRoot = join(import.meta.dirname, "..");
const appSource = readFileSync(join(srcRoot, "App.tsx"), "utf8");

console.log("\nremote navigation provider contract");

// Dynamic enumeration: walk every production .tsx (tests excluded) and count
// RemoteNavigationContext.Provider sites. Lower bound guards against a broken
// scan silently passing; exact membership names the two legitimate sites.
function collectTsx(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      if (entry === "__tests__") continue;
      collectTsx(full, out);
    } else if (entry.endsWith(".tsx")) out.push(full);
  }
  return out;
}
const providerFiles = collectTsx(srcRoot)
  .filter((file) => readFileSync(file, "utf8").includes("RemoteNavigationContext.Provider"))
  .map((file) => file.replace(/\\/g, "/"));
ok(providerFiles.length >= 2, `provider scan is non-trivial (found ${providerFiles.length}, lower bound 2)`);
ok(providerFiles.some((file) => file.endsWith("/App.tsx")), "the mounted App tree supplies a RemoteNavigationContext.Provider");
ok(providerFiles.some((file) => file.endsWith("/app-shell/AppRuntimeView.tsx")), "the a141 AppRuntimeView composition half stays wired (not reverted)");

// App tree Provider opens around the root and resolves the queue-backed command.
ok(appSource.includes('<RemoteNavigationContext.Provider value={openRemoteProjectCommand}>'),
  "App Provider value is the queue-backed openRemoteProjectCommand");
ok(appSource.includes("</RemoteNavigationContext.Provider>"), "App Provider closes its element");

// The command routes through the shared navigation queue with an outcome slot.
ok(appSource.includes("openRemoteProjectCommand = useCallback<RemoteNavigationCommand>"),
  "openRemoteProjectCommand is typed as RemoteNavigationCommand");
ok(appSource.includes('kind: "remote-project"') && appSource.includes("remoteOutcome"),
  "the command enqueues a remote-project intent carrying the outcome slot");
ok(appSource.includes('remoteOutcome.value ?? { status: "cancelled", reason: "superseded" }'),
  "a request coalesced away or superseded resolves as cancelled (silent-return contract)");

// Queue branch mirrors desktopNavigationOwner's remote-project sequence.
const branchAnchor = 'if (request.kind === "remote-project") {';
const branchAt = appSource.indexOf(branchAnchor);
ok(branchAt >= 0, "runNavigationRequest implements the remote-project branch");
if (branchAt >= 0) {
  const branchSlice = appSource.slice(branchAt, branchAt + 1600);
  const steps = [
    ["await registeredNavigationIntent(", "fences on the registered navigation intent"],
    ["app.OpenRemoteProjectTab(", "opens the remote tab through the bridge"],
    ["seedActiveTabMeta(openedTab)", "seeds the opened tab as active"],
    ["await switchRemoteTab(openedTab", "switches through the remote-aware controller path"],
    ["setTabRevealSignal(", "reveals the transcript region"],
    ["await refreshLatestTabMetas()", "refreshes the tab list after adoption"],
  ] as const;
  let cursor = -1;
  for (const [needle, label] of steps) {
    const at = branchSlice.indexOf(needle);
    ok(at > cursor, `remote-project sequence: ${label}`);
    cursor = Math.max(cursor, at);
  }
  ok(branchSlice.includes('outcome.value = { status: "failed", error: err }'),
    "remote-project failures report a failed outcome (consumers toast)");
}

// Finishing-followup: Composer must receive the inbox identity props from the
// active tab (mirror of decisionFooterBuilders in the unmounted tree).
ok(appSource.includes("inboxSessionPath={activeTab?.sessionPath}"), "Composer receives inboxSessionPath from the active tab");
ok(appSource.includes("inboxHostId={activeTab?.remote?.hostId}"), "Composer receives inboxHostId from the active tab");
ok(appSource.includes("inboxWorkspace={activeTab?.remote?.workspace}"), "Composer receives inboxWorkspace from the active tab");

// Main-pane wiring: remote tabs must mount RemoteSessionSurface (27fa47f76
// moved it into the unmounted tree; without it the remote topic opens with a
// topicbar change but the local Transcript keeps stale content).
ok(appSource.includes('const RemoteSessionSurface = lazy(() => import("./components/RemoteSessionSurface")'),
  "App lazily imports the remote surface");
const mountAt = appSource.indexOf("<RemoteSessionSurface tab={activeTab} session={remoteSession} />");
ok(mountAt >= 0, "App mounts RemoteSessionSurface for the active remote tab");
ok(mountAt > appSource.indexOf("noticePreviewMockEnabled() ?"), "the remote surface branch sits in the main-pane selection chain");

// New Session ownership: while a remote tab is active the quick action must
// open a new session on that remote workspace (mirror of
// useSessionNavigationCommands.handleNewTab), not a local blank.
const newTabAt = appSource.indexOf("const handleNewTab = useCallback");
ok(newTabAt >= 0, "App defines handleNewTab");
if (newTabAt >= 0) {
  const newTabSlice = appSource.slice(newTabAt, newTabAt + 900);
  ok(newTabSlice.includes("if (activeTab?.remote)") && newTabSlice.includes("openRemoteProjectCommand(activeTab.remote, { newSession: true })"),
    "New Session while a remote tab is active routes through the remote command");
}

// Consumers stay on the context — no prop-drilled bypass (fence 8 keeps the
// command-only dependency in place).
for (const consumer of ["components/ProjectTreeRemoteGroups.tsx", "components/RemoteConnectWizard.tsx", "components/RemoteSessionSurface.tsx"]) {
  const source = readFileSync(join(srcRoot, consumer), "utf8");
  ok(source.includes("useRemoteNavigationCommand()"), `${consumer} still resolves the command from context`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
