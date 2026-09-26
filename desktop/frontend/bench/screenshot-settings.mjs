#!/usr/bin/env node
// Task 335: screenshot capture for the "界面自验" (UI self-verification) chain.
// Opens the preview build in en-US, walks EVERY settings nav item (labels come
// from the build itself, so renames cannot silently skip a tab), opens the lab
// detail cards (incl. task 280's optimistic-parallel entry), and drops PNGs
// into tasks/ui-shots/ for view_image review.
//
// Usage:  node bench/screenshot-settings.mjs [outputDir]
// Output: <dir>/app-main.png, settings-<n>-<tab>.png, lab-detail-*.png …
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { startPreviewServer } from "./vite-preview-server.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(root, ".pw-browsers");
const { chromium } = await import("playwright");

// global-workspace/tasks/ui-shots: frontend -> desktop -> wt-335 -> reasonix
// -> worktrees -> github-repo -> global-workspace = six levels up.
const defaultOut = path.resolve(root, "..", "..", "..", "..", "..", "..", "tasks", "ui-shots");
const outDir = path.resolve(process.argv[2] ?? defaultOut);
await fs.mkdir(outDir, { recursive: true });

const port = Number(process.env.REASONIX_SHOT_PORT ?? 4691);
const preview = await startPreviewServer(root, port);
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ locale: "en-US", viewport: { width: 1600, height: 1000 } });
const shots = [];

const slug = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");

async function shot(name) {
  const file = path.join(outDir, `${name}.png`);
  await page.screenshot({ path: file });
  shots.push(file);
  console.log(`SHOT ${file}`);
}

try {
  await page.goto(`http://127.0.0.1:${port}/?mock=deepseek_upgrade&bench=1&platform=windows`, { waitUntil: "domcontentloaded" });
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor({ timeout: 30000 });
  await shot("app-main");

  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.locator(".settings-screen").waitFor();
  const nav = page.getByRole("navigation", { name: "Settings", exact: true });
  const navButtons = nav.locator("button.settings-center__navitem");
  const navCount = await navButtons.count();
  console.log(`NAV items: ${navCount}`);
  for (let i = 0; i < navCount; i++) {
    const btn = navButtons.nth(i);
    const label = (await btn.innerText()).split("\n")[0].trim();
    try {
      await btn.click();
      await page.waitForTimeout(600);
      await shot(`settings-${String(i + 1).padStart(2, "0")}-${slug(label)}`);
    } catch (e) {
      console.log(`SKIP tab ${JSON.stringify(label)}: ${String(e).slice(0, 120)}`);
    }
  }

  // Lab detail cards: the efficiency group plus task 280's optimistic-parallel
  // entry (upright checkbox binding) and reference controls. Labels come from
  // the build's own feature list.
  const labBtn = nav.getByRole("button", { name: /^Lab$/ });
  if ((await labBtn.count()) > 0) {
    await labBtn.first().click();
    await page.waitForTimeout(500);
    const railLabels = await page.locator(".experimental-rail__label").allTextContents();
    console.log("LAB features:", JSON.stringify(railLabels));
    for (const card of ["Optimistic parallel writes", "Autopilot", "Dream"]) {
      const item = page.locator(".experimental-rail__item").filter({ hasText: card });
      if ((await item.count()) === 0) {
        console.log(`SKIP lab card ${card} (feature not present)`);
        continue;
      }
      await item.first().click();
      await page.waitForTimeout(600);
      await shot(`lab-detail-${slug(card)}`);
    }
  } else {
    console.log("SKIP lab: nav item ^Lab$ not found");
  }

  await page.locator(".settings-screen .management-screen__back").click();
  await page.locator(".settings-screen").waitFor({ state: "detached" });
  await shot("app-main-after-settings");

  console.log(`DONE ${shots.length} screenshots -> ${outDir}`);
} finally {
  await browser.close();
  await preview.close();
}
