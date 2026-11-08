// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/poll-bound-sameface.test.tsx
//
// tl1496b 验收钉——provider-poll 同类面 6 项全扩（用户 2026-11-08 两裁定：
// 失败有界 + 无活跃不查，逐项落）。本文件覆盖前端三面，Go 侧 detached
// idle 档位缓存由 desktop 包内 TestDetachedIdleReleaseMinutesServesCachedTier
// 直接钉（读数零 config 重载 + 写后一次重载），此处只做源码包含对照：
//  ⑤ 刀①·MCP 状态 store（mcpStatus）：MCPServers() 连续失败达
//     MCP_STATUS_FAILURE_LIMIT(3) 即 paused；paused 不拦单发路径（焦点/
//     页面发布/重挂载语义），任一次成功读数（publish 路径）清零恢复；
//     无 MCP 配置（空快照 → summarize=hidden）芯片不挂载即零调用。
//  ⑥ 刀①·SettingsPanel 两状态面（model application 2s / bot runtime 5s）：
//     组件级钉以源码包含钉承载（tsx-runner 挂全量 SettingsPanel 需桩整个
//     settings 视图，收益不成比例；tl1413 源码钉判例）——失败上限常量、
//     定时器 paused 门、横幅/状态行暂停标记、bot 渠道面零调用门。

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act } from "react";
import { JSDOM } from "jsdom";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; process.exitCode = 1; }
}

console.log("\ntl1496b provider-poll same-face expansion: bounded retry + no-active-no-query");

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  CustomEvent: dom.window.CustomEvent,
  Event: dom.window.Event,
  IS_REACT_ACT_ENVIRONMENT: true,
});

// ── 桩桥：MCPServers 计数器 ──
type ServerViewList = Array<Record<string, unknown>>;
let mcpCalls = 0;
let mcpPayload: ServerViewList | Error = [];
const appStub = {
  MCPServers: async (): Promise<ServerViewList> => {
    mcpCalls += 1;
    if (mcpPayload instanceof Error) throw mcpPayload;
    return mcpPayload;
  },
};
Object.assign(window, { go: { main: { App: appStub } } });

// ════ ⑤ MCP 状态 store：失败有界 + 恢复对照 ════
const { useMcpStatusStore, publishMcpServers, MCP_STATUS_FAILURE_LIMIT, bypassMcpRefreshFloorForTest, resetMcpStatusStoreForTest } = await import("../store/mcpStatus");
const summarizeMcpServersModule = await import("../lib/mcpStatus");
const { summarizeMcpServers } = summarizeMcpServersModule;
const asServers = (v: unknown): Parameters<typeof summarizeMcpServers>[0] => v as Parameters<typeof summarizeMcpServers>[0];

const refreshOnce = async () => {
  bypassMcpRefreshFloorForTest(); // 2s 节流地板只护生产节拍；钉内逐拍旁路
  await act(async () => { await useMcpStatusStore.getState().refresh(); });
};

// ── ⑤ 刀②·无活跃面：无 MCP 配置（空快照）→ summarize=hidden（芯片不挂载） ──
{
  resetMcpStatusStoreForTest();
  ok(summarizeMcpServers([]).state === "hidden", "无 MCP 配置：空快照 summarize=hidden（芯片不挂载，零调用）");
  ok(summarizeMcpServers(asServers([{ name: "a", status: "connected" }])).state === "hidden",
    "无 MCP 配置对照：全 connected 同样 hidden（steady 态不值得噪音）");
}

// ── ⑤ 刀①·失败有界：连续失败 3 次 → paused；暂停中 tick 由芯片门控零发 ──
{
  resetMcpStatusStoreForTest();
  mcpCalls = 0;
  mcpPayload = new Error("boom");
  await refreshOnce();
  await refreshOnce();
  ok(useMcpStatusStore.getState().paused === false, "刀①铺垫：失败 2 次未达上限，暂停未触发");
  await refreshOnce();
  const st = useMcpStatusStore.getState();
  ok(st.paused === true, `刀①红→绿：连续失败 3 次 store.paused=true（实测 ${String(st.paused)}）`);
  ok(st.failStreak === MCP_STATUS_FAILURE_LIMIT, `刀①：failStreak 记满（${st.failStreak}）`);
  ok(mcpCalls === 3, `刀①：恰好 3 次桥调用（实测 ${mcpCalls}）`);
  // 单发路径不被 paused 拦截（焦点重查/手动语义）：仍失败则暂停维持。
  await refreshOnce();
  ok(useMcpStatusStore.getState().paused === true, "刀①：暂停中单发仍可发起（失败 → 暂停维持）");
  ok(mcpCalls === 4, `刀①：单发计第 4 次调用（实测 ${mcpCalls}）`);
}

// ── ⑤ 刀①·恢复对照：成功读数（publish 路径）清零并恢复 ──
{
  mcpPayload = [{ name: "a", status: "failed", error: "boom" }];
  await refreshOnce();
  const st = useMcpStatusStore.getState();
  ok(st.paused === false && st.failStreak === 0, `刀①红→绿：成功读数清零恢复（paused=${String(st.paused)} streak=${st.failStreak}）`);
  // 管理页发布路径同样清零（用户动作即恢复证据）。
  useMcpStatusStore.setState({ failStreak: 2, paused: true });
  publishMcpServers([{ name: "a", status: "connected" }]);
  const st2 = useMcpStatusStore.getState();
  ok(st2.paused === false && st2.failStreak === 0, "刀①：页面 publish 同样清零（快照相同也清——门描述查询健康非载荷）");
}

// ── ⑤ 对照：成功读数从不触发暂停 ──
{
  resetMcpStatusStoreForTest();
  mcpPayload = [];
  await refreshOnce();
  await refreshOnce();
  const st = useMcpStatusStore.getState();
  ok(st.paused === false && st.failStreak === 0 && mcpCalls >= 2, "对照：连续成功读数零暂停");
}

// ════ ⑥ SettingsPanel 两状态面：源码包含钉（tl1413 判例） ════
{
  const frontendRoot = fileURLToPath(new URL("../", import.meta.url));
  const panelSrc = readFileSync(`${frontendRoot}/components/SettingsPanel.tsx`, "utf8");
  ok(panelSrc.includes("MODEL_APPLICATION_FAILURE_LIMIT"), "model application 面：源码携带失败上限常量（有界重试）");
  ok(panelSrc.includes("BOT_RUNTIME_FAILURE_LIMIT"), "bot runtime 面：源码携带失败上限常量（有界重试）");
  ok(panelSrc.includes("if (modelApplicationPaused) return;"), "model application 面：2s 定时器 paused 门（tick 零发）");
  ok(panelSrc.includes("hasBotRuntimeSurface"), "bot runtime 面：渠道面零调用门（无 QQ/钉钉/连接即不轮询）");
  ok(panelSrc.includes('t("settings.models.applyPaused")'), "model application 面：横幅携带暂停标记键");
  ok(panelSrc.includes('t("settings.botRuntimePaused")'), "bot runtime 面：状态行携带暂停标记键");
  ok(panelSrc.includes("modelApplicationFailStreak.current = 0"), "model application 面：成功读数清零恢复（reload+单发双路径）");

  const statusBarSrc = readFileSync(`${frontendRoot}/components/StatusBar.tsx`, "utf8");
  ok(statusBarSrc.includes("if (mcpPaused) return undefined;"), "MCP 芯片：节拍 paused 门（暂停后零定时调用）");
  ok(statusBarSrc.includes('t("status.mcpPaused")'), "MCP 芯片：tooltip 携带暂停标记键");
  ok(statusBarSrc.includes('mcpSummary.state === "hidden" ? null'), "MCP 芯片：无 MCP 配置 hidden 态零挂载（渲染层零调用门）");

  const storeSrc = readFileSync(`${frontendRoot}/store/mcpStatus.ts`, "utf8");
  ok(storeSrc.includes("MCP_STATUS_FAILURE_LIMIT = 3"), "MCP store：失败上限常量与 planUsage 刀①同档");

  const goSrc = readFileSync(`${frontendRoot}/../../detached_idle_release.go`, "utf8");
  ok(goSrc.includes("cachedConfigForRoot"), "Go detached idle：档位改读 task-609 config 快照（热拍零 config 读）");
  const goTestSrc = readFileSync(`${frontendRoot}/../../detached_idle_release_test.go`, "utf8");
  ok(goTestSrc.includes("TestDetachedIdleReleaseMinutesServesCachedTier"), "Go detached idle：热拍零重载+写后一次重载行为钉在包内");

  // 三语 locale 携带三类暂停文案键。
  const localesRoot = fileURLToPath(new URL("../locales/", import.meta.url));
  const keys = ["status.mcpPaused", "settings.models.applyPaused", "settings.botRuntimePaused"];
  for (const locale of ["zh", "en", "zh-TW"]) {
    const dict = readFileSync(`${localesRoot}${locale}.ts`, "utf8");
    const missing = keys.filter((k) => !dict.includes(`"${k}"`));
    ok(missing.length === 0, `${locale} 携带暂停标记三语键（missing: ${JSON.stringify(missing)}）`);
  }
}

resetMcpStatusStoreForTest();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
