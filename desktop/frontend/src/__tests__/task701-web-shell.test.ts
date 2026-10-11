// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task701-web-shell.test.ts
// 任务 701（Web Remote）：Web 壳定向测试。
//
// 三层：
//   1. foldEvent 流式聚合契约（eventwire 字符串 kind → live turn 块）；
//   2. serveClient 请求契约（fetch stub：token 引导 / 列表 / 提交 / 401 归一）；
//   3. 隔离与降级布局契约（桌面主壳零耦合、web 壳独立入口、移动端媒体查询）。
//
// 浏览器渲染验证受环境限制（无浏览器工具），布局层以 CSS 契约断言 +
// 真机清单（交付报告）兜底。

import assert from "node:assert";
import fs from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";
import { StreamKind, webServe, type WebStreamEvent } from "../web/serveClient";
import { foldEvent } from "../web/WebApp";
import type { LiveBlock } from "../web/WebApp";

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

function ev(partial: Partial<WebStreamEvent>): WebStreamEvent {
	return { kind: "", ...partial } as WebStreamEvent;
}

const here = path.dirname(fileURLToPath(import.meta.url));
const frontendRoot = path.resolve(here, "..", "..");

console.log("\n任务 701 Web 壳：foldEvent 流式聚合");

// reasoning 增量叠进同一块。
{
	const b1 = foldEvent([], ev({ kind: StreamKind.Reasoning, text: "思考" }));
	const b2 = foldEvent(b1, ev({ kind: StreamKind.Reasoning, text: "中" }));
	ok(b2.length === 1 && b2[0].type === "reasoning" && b2[0].text === "思考中", "reasoning 增量叠块");
}
// text 增量叠块；message 整帧替换最近 text 块。
{
	let b = foldEvent([], ev({ kind: StreamKind.Text, text: "你好" }));
	b = foldEvent(b, ev({ kind: StreamKind.Text, text: "，世界" }));
	ok(b.length === 1 && b[0].text === "你好，世界", "text 增量叠块");
	b = foldEvent(b, ev({ kind: StreamKind.Message, text: "整帧替换" }));
	ok(b.length === 1 && b[0].text === "整帧替换", "message 整帧替换 text 块");
}
// turn_started 重置（新 turn 清空上一轮 live 块）。
{
	const b = foldEvent(
		[{ type: "text", text: "旧内容" }],
		ev({ kind: StreamKind.TurnStarted }),
	);
	ok(b.length === 0, "turn_started 重置 live 块");
}
// tool dispatch → result 按未完成工具行回填；err 优先展示。
{
	let b = foldEvent([], ev({ kind: StreamKind.ToolDispatch, tool: { name: "bash", args: "ls" } }));
	ok(b.length === 1 && b[0].type === "tool" && b[0].toolName === "bash" && !b[0].toolDone, "tool 建行");
	b = foldEvent(b, ev({ kind: StreamKind.ToolResult, tool: { output: "file.txt" } }));
	ok(b[0].toolDone === true && b[0].text === "file.txt", "tool result 回填输出");
	b = foldEvent(b, ev({ kind: StreamKind.ToolDispatch, tool: { name: "edit", args: "x" } }));
	b = foldEvent(b, ev({ kind: StreamKind.ToolResult, tool: { err: "blocked" } }));
	ok(b[1].text === "错误：blocked", "tool 错误优先展示");
}
// notice 追加；turn_done 携带 err 追加错误块；未知 kind 忽略。
{
	let b = foldEvent([], ev({ kind: StreamKind.Notice, text: "提示" }));
	ok(b.length === 1 && b[0].type === "notice", "notice 追加");
	b = foldEvent(b, ev({ kind: StreamKind.TurnDone, err: "超时" }));
	ok(b.length === 2 && b[1].type === "error" && b[1].text === "超时", "turn_done 错误块");
	b = foldEvent(b, ev({ kind: "runtime_state" }));
	ok(b.length === 2, "未知 kind 忽略");
}

console.log("\n任务 701 Web 壳：serveClient 请求契约");

// fetch stub 记录调用并返回可编程应答。
type Call = { url: string; init?: RequestInit };
const calls: Call[] = [];
let responder: (url: string, init?: RequestInit) => Response = () => new Response(null, { status: 401 });
(globalThis as { fetch?: typeof fetch }).fetch = ((url: string | URL, init?: RequestInit) => {
	const u = String(url);
	calls.push({ url: u, init });
	return Promise.resolve(responder(u, init));
}) as typeof fetch;

async function asyncOk(cond: boolean, label: string) {
	ok(cond, label);
}

{
	// 引导成功应答（serve 端 handleTokenBootstrap 成功 = 204 + Set-Cookie）。
	responder = () => new Response(null, { status: 204 });
	const okBoot = await webServe.bootstrapToken("secret-token");
	asyncOk(okBoot && calls[0].url === "/auth/token", "bootstrapToken → POST /auth/token");
	const body = JSON.parse(String(calls[0].init?.body));
	asyncOk(body.token === "secret-token", "token 走 JSON body（不进 URL）");
	asyncOk(calls[0].init?.method === "POST", "bootstrapToken 是 POST");
}
{
	responder = (_u, init) =>
		new Response(init?.method === "POST" ? null : JSON.stringify([{ name: "a", path: "/p", mtimeMilli: 1 }]), {
			status: init?.method === "POST" ? 204 : 200,
		});
	calls.length = 0;
	const sessions = await webServe.listSessions();
	asyncOk(sessions.length === 1 && sessions[0].name === "a", "listSessions 解析 JSON");
	await webServe.resume("/p/x.jsonl");
	asyncOk(calls.at(-1)?.url === "/resume", "resume → POST /resume");
	asyncOk(calls.at(-1)?.init?.method === "POST", "resume 是 POST（204 无 body 不抛）");
}
{
	responder = (u) =>
		new Response(u.includes("/sessions") ? "Unauthorized\n" : "no", {
			status: 401,
		});
	calls.length = 0;
	let caught = false;
	try {
		await webServe.listSessions();
	} catch (err) {
		caught = err instanceof Error && err.name === "UnauthorizedError";
	}
	asyncOk(caught, "401 归一为 UnauthorizedError");
}

console.log("\n任务 701 Web 壳：隔离与降级布局契约");

const mainEntry = fs.readFileSync(path.join(frontendRoot, "src/main.tsx"), "utf8");
const webEntry = fs.readFileSync(path.join(frontendRoot, "src/web/main.tsx"), "utf8");
const webAppSrc = fs.readFileSync(path.join(frontendRoot, "src/web/WebApp.tsx"), "utf8");
const webCss = fs.readFileSync(path.join(frontendRoot, "src/web/web.css"), "utf8");
const viteWebCfg = fs.readFileSync(path.join(frontendRoot, "vite.web.config.ts"), "utf8");
const indexHtml = fs.readFileSync(path.join(frontendRoot, "index.html"), "utf8");
const webHtml = fs.readFileSync(path.join(frontendRoot, "web.html"), "utf8");

ok(!mainEntry.includes("/web/") && !mainEntry.includes("web/WebApp"), "桌面主入口不引用 web 壳");
ok(!webEntry.includes("bridge") && !webAppSrc.includes('from "../lib/bridge"'), "web 壳不依赖桌面桥接面");
ok(indexHtml.includes("src/main.tsx") && webHtml.includes("src/web/main.tsx"), "双入口各自独立");
ok(viteWebCfg.includes('base: "/web/"') && viteWebCfg.includes('outDir: "dist-web"'), "web 构建独立产物目录");
ok(!viteWebCfg.includes("stripCrossorigin"), "web 构建不携带桌面 Wails 专用插件");

// 移动端降级：窄视口抽屉 + 宽屏常驻 + 安全区。
ok(webCss.includes("transform: translateX(-100%)") && webCss.includes(".web-sidebar.open"), "窄视口侧栏抽屉化");
ok(webCss.includes("@media (min-width: 768px)") && webCss.includes("position: static"), "宽屏侧栏常驻");
ok(webCss.includes("env(safe-area-inset-bottom)"), "composer 安全区适配");
ok(!webCss.includes("viewport-fit") && webHtml.includes("viewport-fit=cover"), "viewport-fit 声明在 web.html");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
