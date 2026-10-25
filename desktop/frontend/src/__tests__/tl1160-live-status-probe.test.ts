// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1160-live-status-probe.test.ts
//
// tl 1160 第一阶段诊断探针的行为钉：
//   ① 门签名变化（布尔门/指针/腿命中/行数）即时上报；
//   ② 纯内容变化（流式文本增长）不做迁移，5s 心跳窗口内不重复上报；
//   ③ 上报走 reportFrontendLog 通道（内存环可取证，格式含协调线指定字段）。
// 探针为诊断件（定因后随修复一并摘除），本测试保证其在插桩窗口内的行为
// 可预期、不刷爆日志。

import { JSDOM } from "jsdom";

const dom = new JSDOM("<!doctype html><html><body></body></html>");
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;

const { probeLiveStatus, head40ForProbe } = await import("../lib/liveStatusDebug");
const { peekFrontendLogRing, drainFrontendLogRing } = await import("../lib/frontendLog");

let passed = 0;
let failed = 0;
function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const base = {
  seg: "seg@1",
  exp: "concise",
  open: false,
  run: true,
  concise: true,
  n: 3,
  liveId: "a1",
  hasLive: true as const,
  tail: "a1",
  tool: "t1:grep",
  snap: "snapshot reasoning head",
  liveRLen: 42,
  liveRDone: false as const,
  eff: "live reasoning head",
  act: "grep · pattern",
  shown: true,
};

drainFrontendLogRing();

// ① 首次评估：即时上报
probeLiveStatus(base);
const first = drainFrontendLogRing();
ok(first.length === 1, "首次评估即时上报 1 条");
ok(first[0]?.includes("[frontend:live-status]"), "通道标签 live-status（grep 可达）");
ok(first[0]?.includes("liveId=a1"), "字段含 liveId");
ok(first[0]?.includes("hasLive=true"), "字段含 hasLive");
ok(first[0]?.includes('snap="snapshot reasoning head"'), "字段含 snap 首 40 采样");
ok(first[0]?.includes("shown=1"), "字段含 showLiveStatus");

// ① 门签名变化：即时上报（用累积对象保持门态，内容变化不再意外翻门）
const current = { ...base };
probeLiveStatus({ ...current, tool: "-" });
current.tool = "-";
const gateFlip = drainFrontendLogRing();
ok(gateFlip.length === 1, "腿A 从命中变未命中=门签名变化，即时上报");
probeLiveStatus({ ...current, open: true });
current.open = true;
const openFlip = drainFrontendLogRing();
ok(openFlip.length === 1, "门② open 翻转即时上报");
ok(openFlip[0]?.includes("open=1"), "open 字段值正确");

// ② 同签名同窗口：内容变化不重复上报
probeLiveStatus({ ...current, eff: "完全不同的流式内容增长文本" });
probeLiveStatus({ ...current, snap: "另一段快照文本" });
const quiet = drainFrontendLogRing();
ok(quiet.length === 0, "纯内容变化（5s 心跳窗口内）不上报");

// ③ 同签名跨心跳窗口：放行一条
await new Promise((resolve) => setTimeout(resolve, 5100));
probeLiveStatus({ ...current, eff: "心跳窗口后的流式内容" });
const heartbeat = drainFrontendLogRing();
ok(heartbeat.length === 1, "跨 5s 心跳窗口放行 1 条");
// 心跳后的同签名内容变化继续静默（窗口重置）
probeLiveStatus({ ...current, eff: "又一段内容" });
ok(drainFrontendLogRing().length === 0, "心跳后窗口重置，同签名继续静默");

// head40 采样口径
ok(head40ForProbe("x".repeat(40)).length === 40, "40 字原样");
ok(head40ForProbe("x".repeat(41)).length === 40, "41 字截到 40");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
