#!/usr/bin/env node
// Task 627 repro bench: drive the REAL bundled frontend in a REAL Chromium
// through the exact user flow — Ctrl+K → type 跳转会话 → Enter — and report
// whether the session wall mounts, plus every page error / rejection.
// Uses the app's own browser-mode dev bridge, with a LOCAL diagnostic patch
// (bridge.ts mock experimentalSessionWall:true, never commit) standing in for
// the user's config so every React/lazy/CSS layer is production-identical.

import { spawn } from "node:child_process";
import http from "node:http";
import path from "node:path";
import { fileURLToPath } from "node:url";

const frontendDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(frontendDir, ".pw-browsers");
const { chromium } = await import("playwright");
const port = Number(process.env.REASONIX_627_REPRO_PORT ?? 4627);
const url = `http://127.0.0.1:${port}/`;

function assert(condition, message) {
  if (!condition) throw new Error(message);
  process.stdout.write(`  PASS  ${message}\n`);
}

async function waitForServer() {
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    const ready = await new Promise((resolve) => {
      const request = http.get(url, (response) => {
        response.resume();
        resolve((response.statusCode ?? 500) < 500);
      });
      request.on("error", () => resolve(false));
    });
    if (ready) return;
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error("627 repro preview did not become ready");
}

const preview = spawn("pnpm", ["exec", "vite", "preview", "--port", String(port), "--strictPort", "--host", "127.0.0.1"], {
  cwd: frontendDir,
  stdio: "ignore",
  shell: process.platform === "win32",
});

let browser;
let failed = false;
let consoleMsgs = [];
let pageErrors = [];
try {
  await waitForServer();
  browser = await chromium.launch({ headless: true, channel: "msedge" });
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const chunkFailures = [];
  page.on("pageerror", (error) => pageErrors.push(String(error)));
  page.on("console", (msg) => {
    if (msg.type() === "error" || msg.type() === "warning") consoleMsgs.push(`[${msg.type()}] ${msg.text().slice(0, 240)}`);
    if (msg.type() === "error" && /SessionWallPanel|preload/i.test(msg.text())) chunkFailures.push(msg.text());
  });
  await page.addInitScript(() => {
    window.__rejections = [];
    window.addEventListener("unhandledrejection", (e) => window.__rejections.push(String(e.reason)));
  });
  await page.goto(url);
  await page.waitForLoadState("domcontentloaded");
  await page.waitForTimeout(3000);

  const shell = await page.evaluate(() => ({
    root: document.getElementById("root")?.children.length ?? 0,
    ariaModal: document.querySelectorAll('[aria-modal="true"]').length,
  }));
  console.log("shell:", JSON.stringify(shell));
  assert(shell.root > 0, "app shell mounted in plain browser mode");

  // 1. palette opens on Ctrl+K
  await page.keyboard.press("Control+k");
  await page.waitForTimeout(800);
  const paletteVisible = await page.locator(".palette").count();
  assert(paletteVisible > 0, "palette opens on Ctrl+K");

  // 2. type the wall query
  await page.keyboard.type("跳转会话");
  await page.waitForTimeout(400);
  const wallEntry = await page.getByText("跳转会话", { exact: true }).count();
  assert(wallEntry > 0, `wall entry 跳转会话 present in palette (${wallEntry})`);

  // 3. Enter runs the highlighted (first) item — the wall entry
  await page.keyboard.press("Enter");
  await page.waitForTimeout(1500);

  const wallCount = await page.locator(".session-wall").count();
  const wallState = wallCount > 0 ? await page.locator(".session-wall").getAttribute("data-state") : "(absent)";
  const cardCount = wallCount > 0 ? await page.locator(".session-wall__card").count() : 0;
  assert(wallCount > 0 && wallState === "open", `session wall mounted with data-state=open (got count=${wallCount} state=${wallState})`);
  assert(cardCount > 0, `wall renders session cards (${cardCount})`);

  const visibility = wallCount > 0 ? await page.locator(".session-wall").evaluate((el) => {
    const style = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    return { display: style.display, visibility: style.visibility, opacity: style.opacity, width: rect.width, height: rect.height, zIndex: style.zIndex };
  }) : null;
  assert(Boolean(visibility) && visibility.width > 0 && visibility.height > 0 && visibility.opacity !== "0", `wall is actually visible: ${JSON.stringify(visibility)}`);

  if (pageErrors.length) console.log("pageerrors:", pageErrors.slice(0, 6));
  if (consoleMsgs.length) console.log("console:", consoleMsgs.slice(0, 10));
  console.log("rejections:", await page.evaluate(() => window.__rejections));
  if (chunkFailures.length) console.log("chunk failures:", chunkFailures);
  console.log(`RESULT: wall=${wallCount > 0 ? "RENDERED" : "ZERO-RENDER"} cards=${cardCount}`);
} catch (error) {
  failed = true;
  console.error("REPRO FAILED:", error instanceof Error ? error.message : error);
  if (consoleMsgs.length) console.log("console-tail:", consoleMsgs.slice(0, 12));
  if (pageErrors.length) console.log("pageerrors:", pageErrors.slice(0, 5));
} finally {
  if (browser) await browser.close().catch(() => {});
  preview.kill();
}
process.exit(failed ? 1 : 0);
