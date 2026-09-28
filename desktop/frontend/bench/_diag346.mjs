#!/usr/bin/env node
// Task 346 acceptance screenshot (temporary diag - archive after capture per
// the 界面自验操作卡 §1): walks the settings nav, finds the OpenCode Go usage
// card, switches it On, and captures the three-window layout at the same
// angle as the 0928 feedback shot. Requires the TEMPORARY three-tier mock
// fixture in bridge.ts (GetOpenCodeGoUsage) so the windows render at all -
// the preview mock has no real usage endpoint.
//
// Usage: node bench/_diag346.mjs [outputPng]
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { startPreviewServer } from "./vite-preview-server.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(root, ".pw-browsers");
const { chromium } = await import("playwright");

const defaultOut = path.resolve(root, "..", "..", "..", "..", "..", "..", "tasks", "ui-shots", "opencode-usage-346.png");
const outFile = path.resolve(process.argv[2] ?? defaultOut);
await fs.mkdir(path.dirname(outFile), { recursive: true });

const port = Number(process.env.REASONIX_SHOT_PORT ?? 4693);
const preview = await startPreviewServer(root, port);

const settle = (page, frames = 3) => page.evaluate((k) => new Promise((r) => {
  let i = 0;
  const tick = () => (++i >= k ? r() : requestAnimationFrame(tick));
  requestAnimationFrame(tick);
}), frames);

async function shot(page, file) {
  await page.setViewportSize({ width: 1598, height: 1000 });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  await page.waitForTimeout(300);
  await page.screenshot({ path: file });
  console.log(`SHOT ${file}`);
}

const browser = await chromium.launch({ headless: process.env.REASONIX_SHOT_HEADED !== "1" });
let exitCode = 0;
try {
  const page = await browser.newPage({ locale: "en-US", viewport: { width: 1600, height: 1000 } });
  await page.goto(`http://127.0.0.1:${port}/?mock=deepseek_upgrade&bench=1&platform=windows`, { waitUntil: "domcontentloaded" });
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor({ timeout: 30000 });
  await page.locator(".startup-splash").waitFor({ state: "detached", timeout: 20000 });
  await page.waitForTimeout(12000);

  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.locator(".settings-screen").waitFor();

  const nav = page.getByRole("navigation", { name: "Settings", exact: true });
  const navButtons = nav.locator("button.settings-center__navitem");
  const navCount = await navButtons.count();
  let found = false;
  for (let i = 0; i < navCount && !found; i++) {
    await navButtons.nth(i).click();
    await page.waitForTimeout(600);
    if ((await page.getByText("OpenCode Go usage (experimental)").count()) > 0) {
      found = true;
      console.log(`FOUND on nav item ${i + 1}: ${(await navButtons.nth(i).innerText()).split("\n")[0].trim()}`);
    }
  }
  if (!found) throw new Error("OpenCode Go usage card not found in any settings tab");

  // The text found above is the Lab rail entry; click it so the detail card
  // (field + Off/On segment) actually mounts in the right pane.
  await page.getByText("OpenCode Go usage (experimental)").first().click();
  await page.locator(".settings-field--opencode-usage").waitFor({ timeout: 10000 });
  await page.waitForTimeout(400);
  // Switch it On: the segmented Off/On control inside the field.
  const onBtn = page.locator(".settings-field--opencode-usage .set-seg__btn", { hasText: /^On$/ });
  await onBtn.click();
  await page.locator('[data-testid="opencode-go-usage-card"]').waitFor({ timeout: 10000 });
  await page.waitForTimeout(800);
  const rows = await page.locator(".opencode-go-usage__row").count();
  console.log(`WINDOW ROWS: ${rows}`);
  if (rows !== 3) throw new Error(`expected 3 window rows, got ${rows}`);
  await settle(page, 3);
  await shot(page, outFile);
  console.log("DONE 1 screenshot ->", outFile);
} catch (err) {
  console.error("DIAG FAILED:", err);
  exitCode = 1;
} finally {
  await browser.close();
  await preview.close?.();
  process.exit(exitCode);
}
