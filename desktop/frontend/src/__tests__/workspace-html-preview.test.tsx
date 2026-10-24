// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/workspace-html-preview.test.tsx

import { act } from "react";
import { flushPromises, renderFilesWorkspace, waitFor } from "./workspace-panel-test-harness";

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

function htmlFrame(): HTMLIFrameElement | null {
  return document.querySelector<HTMLIFrameElement>("iframe.workspace-media__frame");
}

function toggleInput(): HTMLInputElement | null {
  return document.querySelector<HTMLInputElement>("input[type=checkbox].workspace-media__html-toggle, .workspace-media__html-toggle input");
}

console.log("\nworkspace html preview (tl 1110)");

const { dom, root } = await renderFilesWorkspace({
  ListDirForTab: async (_tabId, dir) => (dir === "" ? [{ name: "index.html", isDir: false }] : []),
  ReadFileForTab: async (_tabId, path) => ({
    path,
    body: "",
    size: 40,
    truncated: false,
    binary: false,
    kind: "html",
    mime: "text/html",
    url: "/__reasonix_workspace_media/token-a/index.html",
  }),
});

await waitFor("files view entry", () => document.body.textContent?.includes("index.html") === true);
await act(async () => {
  document.querySelector<HTMLButtonElement>('[data-workspace-path="index.html"]')
    ?.dispatchEvent(new window.MouseEvent("click", { bubbles: true }));
  await flushPromises();
});
await waitFor("html preview iframe", () => htmlFrame() !== null);

const frame = htmlFrame();
ok(frame !== null, "html preview renders an iframe");
ok(frame?.getAttribute("src") === "/__reasonix_workspace_media/token-a/index.html", "iframe src uses the token media URL");
// SAFETY HARD ASSERTION: default sandbox must be allow-same-origin WITHOUT allow-scripts.
ok(frame?.getAttribute("sandbox") === "allow-same-origin", "default sandbox is exactly allow-same-origin (scripts withheld)");
ok(!frame?.getAttribute("sandbox")?.includes("allow-scripts"), "default sandbox never contains allow-scripts");

const toggle = toggleInput();
ok(toggle !== null, "allow-scripts toggle is present for html preview");
ok(toggle?.checked === false, "allow-scripts toggle starts unchecked");

await act(async () => {
  const input = toggleInput();
  if (!input) throw new Error("missing toggle input");
  // React attaches a synthetic change listener at the root container; the
  // harness wires it so a bubbling "change" event reaches the onChange prop.
  input.click();
  await flushPromises();
});
await waitFor("sandbox switched", () => htmlFrame()?.getAttribute("sandbox") === "allow-scripts");

// SAFETY HARD ASSERTION: enabling scripts must drop allow-same-origin entirely.
const enabledSandbox = htmlFrame()?.getAttribute("sandbox");
ok(enabledSandbox === "allow-scripts", "enabled sandbox is exactly allow-scripts");
ok(!enabledSandbox?.includes("allow-same-origin"), "enabled sandbox never contains allow-same-origin (escape prevention)");

await act(async () => {
  root.unmount();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
