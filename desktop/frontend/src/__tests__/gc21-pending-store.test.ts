// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/gc21-pending-store.test.ts
// GC-21（⭐21 跨端输入队列）：pendingStore 纯逻辑定向测试。
//
// 覆盖：FIFO 登记/ACK 移除、24h 过期分类与显式丢弃（含过期项可删——
// removePending 曾漏 expired 的回归钉）、坏数据容错、clear 全清。

import assert from "node:assert";
import process from "node:process";
import {
	addPending,
	clearPending,
	loadPending,
	removePending,
} from "../web/pendingStore";

let passed = 0;
let failed = 0;
function ok(cond: boolean, label: string) {
	if (cond) {
		process.stdout.write(`  PASS  ${label}\n`);
		passed += 1;
	} else {
		process.stdout.write(`  FAIL  ${label}\n`);
		failed += 1;
		process.exitCode = 1;
	}
}

// localStorage stub（node 环境无浏览器存储）
const mem = new Map<string, string>();
(globalThis as { localStorage?: unknown }).localStorage = {
	getItem: (k: string) => mem.get(k) ?? null,
	setItem: (k: string, v: string) => void mem.set(k, v),
	removeItem: (k: string) => void mem.delete(k),
};

const T0 = 1_800_000_000_000; // 固定基准时刻
const HOUR = 3_600_000;

console.log("gc21 pendingStore：");

// 1. 登记 + FIFO 顺序
mem.clear();
addPending("第一", T0);
addPending("第二", T0 + 1);
{
	const { alive, expired } = loadPending(T0 + 2);
	ok(alive.length === 2 && expired.length === 0, "登记 2 条全存活");
	ok(alive[0].input === "第一" && alive[1].input === "第二", "FIFO 顺序（新在尾部）");
}

// 2. ACK 移除
{
	const first = loadPending(T0 + 2).alive[0];
	removePending(first.id, T0 + 3);
	const { alive } = loadPending(T0 + 3);
	ok(alive.length === 1 && alive[0].input === "第二", "removePending 按 id 移除");
}

// 3. 24h 过期分类（第 1 条到 25h 已过期、第 2 条仅 2h——注意 addPending
//    会顺带 GC 登记时刻已过期的存量，故第 2 条在旧消息过期前登记）
mem.clear();
addPending("旧消息", T0);
addPending("新消息", T0 + 23 * HOUR);
{
	const { alive, expired } = loadPending(T0 + 25 * HOUR);
	ok(alive.length === 1 && alive[0].input === "新消息", "过期分类：存活仅新消息");
	ok(expired.length === 1 && expired[0].input === "旧消息", "过期分类：超 24h 入 expired");
}

// 4. 过期项可删（removePending 回归钉：曾只清 alive 致过期残留反复提示）
{
	const expiredId = loadPending(T0 + 25 * HOUR).expired[0].id;
	removePending(expiredId, T0 + 25 * HOUR);
	const after = loadPending(T0 + 25 * HOUR);
	ok(after.expired.length === 0, "过期项可被 removePending 删除（不残留）");
}

// 5. clear 全清
mem.clear();
addPending("a", T0);
addPending("b", T0);
clearPending();
{
	const { alive, expired } = loadPending(T0);
	ok(alive.length === 0 && expired.length === 0, "clearPending 清空全部");
}

// 6. 坏数据容错（坏 JSON / 非数组 / 缺字段）
mem.set("reasonix_web_pending", "{not json");
ok(loadPending(T0).alive.length === 0, "坏 JSON 容错为空");
mem.set("reasonix_web_pending", '{"a":1}');
ok(loadPending(T0).alive.length === 0, "非数组容错为空");
mem.set("reasonix_web_pending", '[{"id":1,"input":"x"}]');
ok(loadPending(T0).alive.length === 0, "缺字段（id 非 string）容错为空");
mem.clear();

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
