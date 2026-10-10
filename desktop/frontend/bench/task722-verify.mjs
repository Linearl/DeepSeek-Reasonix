#!/usr/bin/env node
// 任务 722/724/727 — 实验室重排六点 + yaml 机制 + 子开关的 UI 截图对照。
// 打开 preview 构建（zh 主视角 + zh-TW/en 三语各一张详情卡），走实验室 rail
// 六点位置断言 + 邮箱化行布局断言 + 安全成本子开关卡截图。PNG 落
// tasks/ui-shots/task722/。
// Usage: PLAYWRIGHT_BROWSERS_PATH=<dir> node bench/task722-verify.mjs [outDir]
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { startPreviewServer } from "./vite-preview-server.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
if (!process.env.PLAYWRIGHT_BROWSERS_PATH) {
  process.env.PLAYWRIGHT_BROWSERS_PATH = path.join(root, ".pw-browsers");
}
const { chromium } = await import("playwright");

const outDir = path.resolve(process.argv[2] ?? path.join(root, "..", "..", "..", "..", "..", "..", "tasks", "ui-shots", "task722"));
await fs.mkdir(outDir, { recursive: true });
const port = Number(process.env.REASONIX_722_PORT ?? 4693);
const preview = await startPreviewServer(root, port);

const failures = [];
const ok = (cond, label) => {
  process.stdout.write(`  ${cond ? "PASS" : "FAIL"}  ${label}\n`);
  if (!cond) failures.push(label);
};
const settle = (page, frames = 3) => page.evaluate((k) => new Promise((r) => {
  let i = 0; const tick = () => (i++ >= k ? r() : requestAnimationFrame(tick)); tick();
}), frames);

// 本机 ms-playwright 只有 chromium-1228（playwright 1.63 要 1243，下载被
// 网络重置）——显式 executablePath 走旧版二进制（协议近邻兼容，截图够用）。
const executablePath = process.env.CHROMIUM_722_EXE
  ?? "C:/Users/yinji/AppData/Local/ms-playwright/chromium-1228/chrome-win64/chrome.exe";
const browser = await chromium.launch({ executablePath });
try {
  for (const [localeTag, locale, navLabel, tabLabel] of [
    ["zh", "zh-CN", "设置", "实验室"],
    ["zh-TW", "zh-TW", "設定", "實驗室"],
    ["en", "en-US", "Settings", "Lab"],
  ]) {
    const context = await browser.newContext({ viewport: { width: 1600, height: 1200 }, locale });
    const page = await context.newPage();
    await page.goto(`http://127.0.0.1:${port}/`, { waitUntil: "domcontentloaded" });
    await page.waitForSelector("button", { timeout: 30000 });
    await settle(page, 6);
    // 进设置 → 实验室。
    await page.getByRole("navigation", { name: "Settings", exact: true }).or(page.getByRole("navigation", { name: navLabel, exact: true })).first().waitFor({ timeout: 15000 }).catch(() => {});
    const settingsNav = page.getByRole("button", { name: navLabel, exact: true }).first();
    await settingsNav.click({ timeout: 20000 });
    await settle(page);
    await page.getByRole("button", { name: tabLabel, exact: true }).first().click({ timeout: 20000 });
    await settle(page);
    await page.screenshot({ path: path.join(outDir, `lab-rail-${localeTag}.png`) });

    if (localeTag !== "zh") {
      // 三语只要一张整页对照，其余断言跑 zh 一遍。
      await context.close();
      continue;
    }

    // 展开自动化 + 提效两组。
    for (const g of ["自动化", "提效"]) {
      await page.getByRole("button", { name: new RegExp(g) }).filter({ has: page.locator(".experimental-lab__group-arrow") }).first().click();
      await settle(page);
    }
    await page.screenshot({ path: path.join(outDir, "lab-rail-expanded.png") });

    // 点1/点2：模型策略卡在提效组，卡内含备用模型行、无「已退役」徽章。
    await page.getByRole("button", { name: /模型策略/ }).first().click();
    await settle(page);
    const paneText = await page.locator(".experimental-pane").innerText();
    ok(paneText.includes("备用模型"), "点1 备用模型行出现在模型策略卡");
    ok(await page.locator(".experimental-pane .lab-tier-badge--retired").count() === 0,
      "点2 模型策略卡无「已退役」档位徽章（键只读提示文本保留，564 口径）");
    await page.screenshot({ path: path.join(outDir, "card-model-strategy.png") });

    // 点6：安全/成本控制卡（子开关 + 压缩模型 + 消息合并 + 心跳轮换）。
    await page.getByRole("button", { name: /安全 \/ 成本控制/ }).first().click();
    await settle(page);
    const safetyText = await page.locator(".experimental-pane").innerText();
    ok(safetyText.includes("心跳空转自终止") && safetyText.includes("循环中性继续注记") && safetyText.includes("等待返回前复查"),
      "点6 三个子开关行在安全/成本控制卡");
    ok(safetyText.includes("允许指定压缩模型"), "点5 允许指定压缩模型在安全/成本控制卡");
    ok(safetyText.includes("收件队列合并注入") && safetyText.includes("引导「合并下条」按钮"),
      "点6 消息合并两行（收件队列合并注入+引导「合并下条」按钮）在安全/成本控制卡");
    ok(safetyText.includes("心跳会话轮换"), "727 心跳会话轮换在安全/成本控制卡");
    await page.screenshot({ path: path.join(outDir, "card-safety-cost.png"), fullPage: false });

    // 点3/点4：跨会话协作卡（自动折叠子项 + 邮箱化宽行）。
    await page.getByRole("button", { name: /跨会话协作/ }).first().click();
    await settle(page);
    const collabText = await page.locator(".experimental-pane").innerText();
    ok(collabText.includes("自动折叠跨会话消息"), "点3 自动折叠跨会话消息为跨会话协作子项");
    ok(collabText.includes("邮箱化默认"), "邮箱化默认块在跨会话协作卡");
    // 点4 布局断言：投递通道标签在左、下拉框在右、标签无截断。
    const wide = page.locator(".set-gates__item--wide");
    ok(await wide.count() === 1, "点4 投递通道行是 --wide 整行");
    const box = await wide.boundingBox();
    const labelBox = await wide.locator(".set-gates__label").boundingBox();
    const selectBox = await wide.locator("select").boundingBox();
    const overflow = await wide.locator(".set-gates__label").evaluate((el) => el.scrollWidth > el.clientWidth + 1);
    ok(labelBox && selectBox && labelBox.x < selectBox.x && labelBox.x + labelBox.width <= selectBox.x + 4,
      "点4 标签在左、下拉框在右");
    ok(box && labelBox && selectBox && Math.abs(box.x - labelBox.x) < 8 && Math.abs(box.x + box.width - selectBox.x - selectBox.width) < 8,
      "点4 行首行尾对齐（整行跨双列）");
    ok(!overflow, "点4 标签文本无截断");
    // 备用模型行不在跨会话协作卡。
    ok(!collabText.includes("备用模型"), "点1 跨会话协作卡不再含备用模型行");
    await page.locator(".set-gates__item--wide").scrollIntoViewIfNeeded();
    await settle(page);
    await page.screenshot({ path: path.join(outDir, "card-session-collab-mail.png") });

    // 组序：安全/成本控制在提效组内（rail 目录断言）。
    const effBlock = await page.locator(".experimental-lab__group", { hasText: "提效" }).first().innerText();
    ok(effBlock.includes("安全 / 成本控制"), "点6 安全/成本控制卡在提效组");
    await context.close();
  }
} finally {
  await browser.close();
  try { preview?.httpServer?.close(); } catch { /* already gone */ }
}

process.stdout.write(`\n${failures.length === 0 ? "ALL PASS" : `${failures.length} FAILURE(S)`}\n`);
process.exit(failures.length === 0 ? 0 : 1);
