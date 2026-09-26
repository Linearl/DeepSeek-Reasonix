#!/usr/bin/env node
// Task 335: screenshot capture for the "界面自验" (UI self-verification) chain.
// Opens the preview build in en-US, walks EVERY settings nav item (labels come
// from the build itself, so renames cannot silently skip a tab), opens the lab
// detail cards (incl. task 280's optimistic-parallel entry), and drops PNGs
// into tasks/ui-shots/ for view_image review.
//
// Rework (dispatch 20260927), two classes of fixes baked in:
//   1) boot splash & stale compositor frames: StartupSplash must detach AND
//      the compositor must serve a fresh frame before the main-surface shots.
//      Waiting/rAF/input/opacity do not flip it; a documentElement zoom
//      micro-change does (probe matrix _diag3/_diag4/_diag5). But running that
//      nudge inside the settings walk degrades the overlay shots (v6/v7
//      measured), so the capture runs as TWO isolated browser passes:
//      pass=settings (no zoom, 24 overlay shots) and pass=main (zoom, 2 main
//      shots). Fresh page per pass = no cross-contamination.
//   2) settings-load-error banner: keep ONE shot as diag evidence (dispatch
//      ask #2), try the in-product Retry, fail hard only if it cannot recover;
//      lab detail shots assert the rail selection really switched (the old
//      script shipped a pane that still showed the previous card).
//
// Usage:  node bench/screenshot-settings.mjs [outputDir]
// Output: <dir>/app-main.png, settings-<n>-<tab>.png, lab-detail-*.png …
//         <dir>/app-main-after-settings.png
//         <dir>/diag/settings-load-failed.png (only when the error fires)
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
const diagDir = path.join(outDir, "diag");
await fs.mkdir(outDir, { recursive: true });
await fs.mkdir(diagDir, { recursive: true });

const port = Number(process.env.REASONIX_SHOT_PORT ?? 4691);
const preview = await startPreviewServer(root, port);
const shots = [];
let diagShotTaken = false;

const slug = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
const settle = (page, frames = 3) => page.evaluate((k) => new Promise((r) => {
  let i = 0;
  const tick = () => (++i >= k ? r() : requestAnimationFrame(tick));
  requestAnimationFrame(tick);
}), frames);

async function zoomNudge(page) {
  await page.evaluate(() => { document.documentElement.style.zoom = "1.001"; });
  await settle(page, 2);
  await page.evaluate(() => { document.documentElement.style.zoom = "1"; });
  await settle(page, 3);
  await page.waitForTimeout(250);
}

async function shot(page, name, dir = outDir) {
  // Frame-forcing, modeled on the verified v3 walk (its every settings shot
  // was preceded by setViewportSize changes inside verifySaveBars). The
  // compositor only commits the overlay frame on a viewport-size change;
  // waits, rAF, input, zoom and warm-up shots all left the overlay shot on
  // the stale main-surface frame (probe matrix v6-v9).
  await page.setViewportSize({ width: 1598, height: 1000 });
  await page.setViewportSize({ width: 1600, height: 1000 });
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  await page.waitForTimeout(300);
  const file = path.join(dir, `${name}.png`);
  await page.screenshot({ path: file });
  shots.push(file);
  console.log(`SHOT ${file}`);
}

async function openSettings(page) {
  await page.locator('button:has(svg.lucide-settings)').last().click();
  await page.locator(".settings-screen").waitFor();
}

async function ensureSettingsHealthy(page, context) {
  if ((await page.locator(".settings-load-error").count()) === 0) return;
  if (!diagShotTaken) {
    await shot(page, "settings-load-failed", diagDir);
    diagShotTaken = true;
    console.log(`DIAG settings-load-error captured -> ${diagDir}`);
  } else {
    console.log(`DIAG settings-load-error again during ${context} (already captured)`);
  }
  await page.locator(".settings-load-error button").click();
  await page.waitForTimeout(2000);
  if ((await page.locator(".settings-load-error").count()) > 0) {
    throw new Error(`settings load failed and Retry did not recover during ${context}`);
  }
  console.log(`RECOVERED settings load during ${context} (Retry succeeded)`);
}

async function bootToShell(browser, { waitForSplash = true } = {}) {
  const page = await browser.newPage({ locale: "en-US", viewport: { width: 1600, height: 1000 } });
  await page.goto(`http://127.0.0.1:${port}/?mock=deepseek_upgrade&bench=1&platform=windows`, { waitUntil: "domcontentloaded" });
  await page.locator("textarea.composer__input:not([aria-hidden=true])").waitFor({ timeout: 30000 });
  if (waitForSplash) await page.locator(".startup-splash").waitFor({ state: "detached", timeout: 20000 });
  return page;
}

// PASS 1 — settings overlay shots (no zoom here: the zoom nudge measured as
// destructive to overlay frames in v6/v7).
async function passSettings() {
  // REASONIX_SHOT_HEADED=1 opens a real window: on this Windows box the
  // compositor's stale-frame race (non-deterministic in headless, v2/v3
  // luck vs v6-v9 failures with identical code) does not apply when there
  // is a real surface to present to.
  const browser = await chromium.launch({ headless: process.env.REASONIX_SHOT_HEADED !== "1" });
  try {
    // Sequence matters and it is empirical: opening settings too early after
    // boot leaves every overlay shot on the main-surface frame (v6/v7/v8
    // measured at 300ms-2s), while the verified v3 run — which idled ~15s
    // after the composer mounted before touching settings — produced correct
    // overlay frames. Wait out the settle window first, then walk.
    const page = await bootToShell(browser, { waitForSplash: true });
    await page.waitForTimeout(12000);
    await openSettings(page);
    await ensureSettingsHealthy(page, "first open");
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
        await ensureSettingsHealthy(page, `tab ${label}`);
        await shot(page, `settings-${String(i + 1).padStart(2, "0")}-${slug(label)}`);
      } catch (e) {
        console.log(`SKIP tab ${JSON.stringify(label)}: ${String(e).slice(0, 120)}`);
      }
    }

    const labBtn = nav.getByRole("button", { name: /^Lab$/ });
    if ((await labBtn.count()) > 0) {
      await labBtn.first().click();
      await page.waitForTimeout(500);
      await ensureSettingsHealthy(page, "lab open");
      const railLabels = await page.locator(".experimental-rail__label").allTextContents();
      console.log("LAB features:", JSON.stringify(railLabels));
      for (const card of ["Optimistic parallel writes", "Autopilot", "Dream"]) {
        const item = page.locator(".experimental-rail__item").filter({ hasText: card });
        if ((await item.count()) === 0) {
          console.log(`SKIP lab card ${card} (feature not present)`);
          continue;
        }
        await item.first().click();
        // Assert the selection really moved: the rework round-0 shipped a shot
        // whose pane still showed the previous card. Timeout = loud failure.
        await page.waitForFunction((want) => [...document.querySelectorAll(".experimental-rail__item--active")]
          .some((el) => (el.textContent || "").includes(want)), card, { timeout: 10000 });
        await page.waitForTimeout(500);
        await ensureSettingsHealthy(page, `lab card ${card}`);
        await shot(page, `lab-detail-${slug(card)}`);
      }
    } else {
      console.log("SKIP lab: nav item ^Lab$ not found");
    }
    console.log("PASS settings done");
  } finally {
    await browser.close();
  }
}

// PASS 2 — main-surface shots only (fresh page, zoom nudge flips the stale
// splash frame; measured strat-G).
async function passMain() {
  const browser = await chromium.launch({ headless: process.env.REASONIX_SHOT_HEADED !== "1" });
  try {
    const page = await bootToShell(browser);
    await zoomNudge(page);
    await shot(page, "app-main");

    await openSettings(page);
    await ensureSettingsHealthy(page, "main-pass open");
    await page.locator(".settings-screen .management-screen__back").click();
    await page.locator(".settings-screen").waitFor({ state: "detached" });
    await zoomNudge(page);
    await shot(page, "app-main-after-settings");
    console.log("PASS main done");
  } finally {
    await browser.close();
  }
}

try {
  const only = process.env.REASONIX_SHOT_GROUP ?? "all";
  if (only !== "main") await passSettings();
  if (only !== "settings") await passMain();
  console.log(`DONE ${shots.length} screenshots -> ${outDir}`);
  console.log(diagShotTaken ? "DIAG settings-load-failed captured once" : "DIAG not triggered this run (no settings-load-error)");
} finally {
  await preview.close();
}
