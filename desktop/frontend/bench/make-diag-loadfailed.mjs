#!/usr/bin/env node
// Task 335, dispatch ruling ④: construct the settings-load-failed evidence
// on demand (no gambling on the natural race).
//
// Construction path: serve the SOURCE tree with a vite dev server (the
// runtime-state bench already does) so the dynamic import of
// providerCatalog.generated.json stays an independent network request — in
// the preview build it is compiled into a chunk whose dependency map is
// preloaded together with the settings entry, so aborting it there kills the
// panel itself (measured: ABORT -> "Failed to fetch dynamically imported
// module: SettingsPanelEntry-*.js" -> panel never opens). Abort the .json
// request, open settings, capture the "Settings failed to load / Retry"
// banner into diag/settings-load-failed.png, record whether the in-product
// Retry recovers (the memoized mockCatalogLoad ??= keeps the rejected
// promise — evidence for the 163 follow-up), then restore and reload clean.
//
// Usage: node bench/make-diag-loadfailed.mjs [outputDir]
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(root, ".pw-browsers");
const { chromium } = await import("playwright");

const defaultOut = path.resolve(root, "..", "..", "..", "..", "..", "..", "tasks", "ui-shots");
const outDir = path.resolve(process.argv[2] ?? defaultOut);
const diagDir = path.join(outDir, "diag");
await fs.mkdir(diagDir, { recursive: true });

const port = Number(process.env.REASONIX_SHOT_PORT ?? 4691);
const server = await createServer({ root, logLevel: "error", server: { host: "127.0.0.1", port, strictPort: true } });
await server.listen();
const browser = await chromium.launch({ headless: process.env.REASONIX_SHOT_HEADED !== "1" });
const page = await browser.newPage({ locale: "en-US", viewport: { width: 1600, height: 1000 } });

try {
  await page.route("**/src/lib/providerCatalog.generated.json*", (route) => route.abort());
  await page.goto(`http://127.0.0.1:${port}/?mock=deepseek_upgrade&bench=1&platform=windows`, { waitUntil: "domcontentloaded" });
  const logs = [];
  page.on("pageerror", (e) => logs.push("ERR " + String(e).slice(0, 200)));
  page.on("console", (m) => { if (m.type() === "error") logs.push("CON " + m.text().slice(0, 200)); });
  page.on("requestfailed", (r) => { if (r.url().includes("providerCatalog")) logs.push("ABORT " + r.url().split("/").pop()); });
  // Dev server may run dependency pre-bundling on first hit; allow extra time.
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor({ timeout: 60000 });

  await page.locator('button:has(svg.lucide-settings)').last().click();
  try {
    await page.locator(".settings-screen").waitFor({ timeout: 15000 });
    console.log("settings-screen opened (panel entry loads independent of the JSON in dev mode)");
  } catch (e) {
    console.log("LOGS (" + logs.length + "):"); for (const l of logs.slice(0, 10)) console.log("  " + l);
    throw e;
  }
  const banner = page.locator(".settings-load-error");
  await banner.waitFor({ timeout: 15000 });
  console.log("BANNER visible:", (await banner.innerText()).replace(/\n/g, " | "));

  const diagFile = path.join(diagDir, "settings-load-failed.png");
  await page.screenshot({ path: diagFile });
  console.log("SHOT", diagFile);

  // Observe the product Retry under a memoized rejection (evidence, not a gate).
  await page.locator(".settings-load-error button").click();
  await page.waitForTimeout(2000);
  const stillFailing = (await page.locator(".settings-load-error").count()) > 0;
  console.log(stillFailing
    ? "RETRY-RESULT: banner persists after Retry (mockCatalogLoad ??= caches the rejected import — 163 follow-up evidence)"
    : "RETRY-RESULT: banner recovered after Retry");

  await page.unroute("**/src/lib/providerCatalog.generated.json*");
  await page.reload({ waitUntil: "domcontentloaded" });
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor({ timeout: 60000 });
  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.locator(".settings-screen").waitFor({ timeout: 15000 });
  await page.waitForTimeout(1500);
  const healthy = (await page.locator(".settings-load-error").count()) === 0;
  console.log(healthy ? "HEALTHY after reload: settings load clean" : "WARN still failing after reload");
  console.log("DONE diag ->", diagFile);
  for (const l of logs.slice(0, 6)) console.log("LOG " + l);
} catch (e) {
  console.log("FATAL", String(e).slice(0, 500));
  process.exitCode = 1;
} finally {
  await browser.close();
  await server.close();
}
