import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
	StreamKind,
	webServe,
	UnauthorizedError,
	type WebHistoryMessage,
	type WebSessionEntry,
	type WebStreamEvent,
} from "./serveClient";

// 样式在入口 main.tsx 引入（组件保持无 CSS 依赖，便于纯逻辑测试）。
// 任务 701：移动优先降级布局见 web.css。

// 任务 701（Web Remote）：移动端简化 Web 壳。
//
// 用户定调：Reasonix 桌面界面复杂，网页几乎必然降级——本壳是基础体验而
// 非全量投影。四件能力：token 登录、会话列表（侧栏，窄视口折叠为抽屉）、
// 收发消息（POST /submit + /events SSE 流式）、转录查看（GET /history，
// turn 完成后重载自愈）。复杂面板（项目树/多 tab/设置）不投影。

// ─── 流式 turn 聚合 ───

interface LiveBlock {
	type: "reasoning" | "text" | "tool" | "notice" | "error";
	text: string;
	toolName?: string;
	toolDone?: boolean;
}

/** 把 SSE 原始帧聚合进当前 turn 的块列表（append 语义，turn_done 后整体
 * 由 GET /history 重载替换——自愈式对账，避免帧合并 bug）。kind 为
 * eventwire 字符串枚举；runtime_state 等未列 kind 忽略。导出供定向测试。 */
export function foldEvent(blocks: LiveBlock[], e: WebStreamEvent): LiveBlock[] {
	switch (e.kind) {
		case StreamKind.TurnStarted:
			return [];
		case StreamKind.Reasoning: {
			const last = blocks[blocks.length - 1];
			if (last?.type === "reasoning") {
				return [...blocks.slice(0, -1), { ...last, text: last.text + (e.text ?? "") }];
			}
			return [...blocks, { type: "reasoning", text: e.text ?? "" }];
		}
		case StreamKind.Text: {
			const last = blocks[blocks.length - 1];
			if (last?.type === "text") {
				return [...blocks.slice(0, -1), { ...last, text: last.text + (e.text ?? "") }];
			}
			return [...blocks, { type: "text", text: e.text ?? "" }];
		}
		case StreamKind.Message: {
			// Message 是整帧文本：替换最近 text 块（流式增量已由 Text 叠出）。
			const last = blocks[blocks.length - 1];
			if (last?.type === "text") {
				return [...blocks.slice(0, -1), { ...last, text: e.text ?? "" }];
			}
			return [...blocks, { type: "text", text: e.text ?? "" }];
		}
		case StreamKind.ToolDispatch:
		case StreamKind.ToolStarted:
			return [...blocks, { type: "tool", text: e.tool?.args ?? "", toolName: e.tool?.name ?? "tool" }];
		case StreamKind.ToolResultPreview:
		case StreamKind.ToolResult: {
			// ES2023 findLastIndex 不在当前 tsconfig lib 内，手写反向查找。
			let idx = -1;
			for (let i = blocks.length - 1; i >= 0; i--) {
				if (blocks[i].type === "tool" && !blocks[i].toolDone) {
					idx = i;
					break;
				}
			}
			if (idx >= 0) {
				const next = blocks.slice();
				next[idx] = {
					...next[idx],
					toolDone: true,
					text: e.tool?.err ? `错误：${e.tool.err}` : (e.tool?.output ?? next[idx].text),
				};
				return next;
			}
			return blocks;
		}
		case StreamKind.Notice:
			return [...blocks, { type: "notice", text: e.text ?? "" }];
		case StreamKind.TurnDone:
			if (e.err) return [...blocks, { type: "error", text: e.err }];
			return blocks;
		default:
			return blocks;
	}
}

// ─── 壳组件 ───

type AuthState = "checking" | "needed" | "ready";

export default function WebApp() {
	const [auth, setAuth] = useState<AuthState>("checking");
	const [hashToken, setHashToken] = useState("");

	// 启动：读 #token= 引导参数（token 不进 URL 日志，换取 cookie 后立即清除）。
	useEffect(() => {
		const m = location.hash.match(/^#token=([^&]+)$/);
		if (m) setHashToken(decodeURIComponent(m[1]));
		webServe
			.authenticated()
			.then((ok) => setAuth(ok ? "ready" : "needed"))
			.catch(() => setAuth("needed"));
	}, []);

	// 带 #token= 到站：直接引导，成功后清 hash 刷新鉴权态。
	useEffect(() => {
		if (!hashToken || auth === "ready") return;
		let cancelled = false;
		webServe.bootstrapToken(hashToken).then((ok) => {
			if (cancelled) return;
			if (ok) {
				history.replaceState(null, "", location.pathname + location.search);
				setAuth("ready");
			} else {
				setHashToken("");
				setAuth("needed");
			}
		});
		return () => {
			cancelled = true;
		};
	}, [hashToken, auth]);

	if (auth === "checking") {
		return (
			<div className="web-root">
				<p className="web-loading">连接中…</p>
			</div>
		);
	}
	if (auth === "needed") {
		return <TokenGate onReady={() => setAuth("ready")} />;
	}
	return <Shell />;
}

function TokenGate({ onReady }: { onReady: () => void }) {
	const [token, setToken] = useState("");
	const [error, setError] = useState("");
	const busy = useRef(false);

	const submit = useCallback(
		async (value: string) => {
			if (busy.current || !value.trim()) return;
			busy.current = true;
			setError("");
			try {
				const ok = await webServe.bootstrapToken(value.trim());
				if (ok) onReady();
				else setError("token 不正确");
			} catch (err) {
				setError(err instanceof Error ? err.message : String(err));
			} finally {
				busy.current = false;
			}
		},
		[onReady],
	);

	return (
		<div className="web-root web-token-gate">
			<h1>Reasonix Web</h1>
			<p>输入访问 token（PC 端 reasonix serve 启动时打印）</p>
			<form
				onSubmit={(e) => {
					e.preventDefault();
					void submit(token);
				}}
			>
				<input
					type="password"
					value={token}
					onChange={(e) => setToken(e.target.value)}
					placeholder="token"
					autoFocus
				/>
				<button type="submit">登录</button>
			</form>
			{error && <p className="web-error">{error}</p>}
		</div>
	);
}

function Shell() {
	const [sessions, setSessions] = useState<WebSessionEntry[]>([]);
	const [history, setHistory] = useState<WebHistoryMessage[]>([]);
	const [live, setLive] = useState<LiveBlock[]>([]);
	const [turnActive, setTurnActive] = useState(false);
	const [drawerOpen, setDrawerOpen] = useState(false);
	const [error, setError] = useState("");

	const reloadSessions = useCallback(async () => {
		try {
			setSessions(await webServe.listSessions());
			setError("");
		} catch (err) {
			if (err instanceof UnauthorizedError) location.reload();
			else setError(err instanceof Error ? err.message : String(err));
		}
	}, []);

	const reloadHistory = useCallback(async () => {
		try {
			setHistory(await webServe.history());
			setError("");
		} catch (err) {
			if (!(err instanceof UnauthorizedError)) {
				setError(err instanceof Error ? err.message : String(err));
			}
		}
	}, []);

	// 事件流：聚合 live turn；turn_done / session_changed 后重载转录与列表。
	useEffect(() => {
		const blocks: LiveBlock[] = [];
		let raf = 0;
		const close = webServe.openEvents((e) => {
			if (e.kind === StreamKind.SessionChanged) {
				setLive([]);
				setTurnActive(false);
				void reloadHistory();
				void reloadSessions();
				return;
			}
			if (e.kind === StreamKind.TurnStarted) setTurnActive(true);
			const folded = foldEvent(blocks, e);
			blocks.length = 0;
			blocks.push(...folded);
			if (e.kind === StreamKind.TurnDone) {
				setTurnActive(false);
				void reloadHistory();
				void reloadSessions();
				setLive([]);
				return;
			}
			cancelAnimationFrame(raf);
			raf = requestAnimationFrame(() => setLive([...blocks]));
		});
		return () => {
			cancelAnimationFrame(raf);
			close();
		};
	}, [reloadHistory, reloadSessions]);

	// 初次加载 + 轮询会话列表（running/current 标记有滞后成本，30s 足够）。
	useEffect(() => {
		void reloadSessions();
		void reloadHistory();
		const timer = setInterval(() => void reloadSessions(), 30_000);
		return () => clearInterval(timer);
	}, [reloadSessions, reloadHistory]);

	const openSession = useCallback(
		async (path: string) => {
			setDrawerOpen(false);
			try {
				await webServe.resume(path);
				setLive([]);
				await reloadHistory();
			} catch (err) {
				setError(err instanceof Error ? err.message : String(err));
			}
		},
		[reloadHistory],
	);

	const newSession = useCallback(async () => {
		setDrawerOpen(false);
		try {
			await webServe.newSession();
			setLive([]);
			await Promise.all([reloadHistory(), reloadSessions()]);
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		}
	}, [reloadHistory, reloadSessions]);

	const send = useCallback(
		async (input: string) => {
			setTurnActive(true);
			try {
				await webServe.submit(input);
				// 用户消息立即可见：本地乐观行，转录以 turn_done 后重载为准。
				setHistory((prev) => [...prev, { role: "user", content: input }]);
			} catch (err) {
				setTurnActive(false);
				setError(err instanceof Error ? err.message : String(err));
			}
		},
		[],
	);

	const stop = useCallback(async () => {
		try {
			await webServe.cancel();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		}
	}, []);

	const current = useMemo(() => sessions.find((s) => s.current), [sessions]);

	return (
		<div className="web-root web-shell">
			<header className="web-topbar">
				<button
					className="web-icon-btn"
					aria-label="会话列表"
					onClick={() => setDrawerOpen((v) => !v)}
				>
					☰
				</button>
				<span className="web-topbar-title">{current?.title || current?.name || "Reasonix"}</span>
				{turnActive && <span className="web-turn-dot" title="运行中" />}
			</header>
			{error && (
				<p className="web-error web-banner" role="alert">
					{error}
					<button onClick={() => setError("")}>×</button>
				</p>
			)}
			<div className="web-body">
				<aside className={`web-sidebar${drawerOpen ? " open" : ""}`}>
					<button className="web-new-btn" onClick={() => void newSession()}>
						＋ 新会话
					</button>
					<ul>
						{sessions.map((s) => (
							<li key={s.path}>
								<button
									className={s.current ? "active" : ""}
									onClick={() => void openSession(s.path)}
									title={s.heldBy === "other" ? "被其他运行时占用（只读）" : s.path}
								>
									<span className="web-session-title">{s.title || s.name}</span>
									<span className="web-session-meta">
										{s.turns ?? 0} 轮{s.running ? " · 运行中" : ""}
										{s.heldBy === "other" ? " · 被占用" : ""}
									</span>
								</button>
							</li>
						))}
					</ul>
				</aside>
				{drawerOpen && (
					<div className="web-scrim" onClick={() => setDrawerOpen(false)} />
				)}
				<main className="web-main">
					<Transcript history={history} live={live} turnActive={turnActive} />
					<Composer onSend={send} onStop={stop} turnActive={turnActive} />
				</main>
			</div>
		</div>
	);
}

function Transcript({
	history,
	live,
	turnActive,
}: {
	history: WebHistoryMessage[];
	live: LiveBlock[];
	turnActive: boolean;
}) {
	const endRef = useRef<HTMLDivElement>(null);
	useEffect(() => {
		endRef.current?.scrollIntoView({ block: "end" });
	}, [history.length, live.length]);

	return (
		<div className="web-transcript">
			{history.map((m, i) => (
				<MessageRow key={i} role={m.role} content={m.content} reasoning={m.reasoning} toolName={m.toolName} />
			))}
			{live.length > 0 && (
				<div className="web-msg assistant live">
					{live.map((b, i) => (
						<LiveRow key={i} block={b} />
					))}
				</div>
			)}
			{turnActive && live.length === 0 && <p className="web-loading">思考中…</p>}
			<div ref={endRef} />
		</div>
	);
}

function MessageRow({
	role,
	content,
	reasoning,
	toolName,
}: {
	role: string;
	content: string;
	reasoning?: string;
	toolName?: string;
}) {
	if (role === "tool") {
		return (
			<div className="web-msg tool">
				<span className="web-tool-name">{toolName || "tool"}</span>
				<pre>{content}</pre>
			</div>
		);
	}
	if (role === "notice") {
		return <p className="web-msg notice">{content}</p>;
	}
	const isUser = role === "user";
	return (
		<div className={`web-msg ${isUser ? "user" : "assistant"}`}>
			{reasoning && <details className="web-reasoning">
				<summary>思考过程</summary>
				<pre>{reasoning}</pre>
			</details>}
			<div className="web-content">{content}</div>
		</div>
	);
}

function LiveRow({ block }: { block: LiveBlock }) {
	if (block.type === "tool") {
		return (
			<div className="web-msg tool">
				<span className="web-tool-name">
					{block.toolName}
					{block.toolDone ? " ✓" : " …"}
				</span>
				<pre>{block.text}</pre>
			</div>
		);
	}
	if (block.type === "notice") {
		return <p className="web-msg notice">{block.text}</p>;
	}
	if (block.type === "error") {
		return <p className="web-msg error">{block.text}</p>;
	}
	if (block.type === "reasoning") {
		return (
			<details className="web-reasoning" open>
				<summary>思考中</summary>
				<pre>{block.text}</pre>
			</details>
		);
	}
	return <div className="web-content">{block.text}</div>;
}

function Composer({
	onSend,
	onStop,
	turnActive,
}: {
	onSend: (input: string) => void;
	onStop: () => void;
	turnActive: boolean;
}) {
	const [input, setInput] = useState("");

	const submit = useCallback(() => {
		const trimmed = input.trim();
		if (!trimmed) return;
		setInput("");
		onSend(trimmed);
	}, [input, onSend]);

	return (
		<form
			className="web-composer"
			onSubmit={(e) => {
				e.preventDefault();
				submit();
			}}
		>
			<textarea
				value={input}
				onChange={(e) => setInput(e.target.value)}
				placeholder={turnActive ? "运行中…（可输入排队内容）" : "输入消息"}
				rows={1}
				enterKeyHint="send"
				onKeyDown={(e) => {
					// 手机软键盘没有 Ctrl：回车直接发送，Shift+Enter 换行。
					if (e.key === "Enter" && !e.shiftKey) {
						e.preventDefault();
						submit();
					}
				}}
			/>
			{turnActive ? (
				<button type="button" className="web-stop" onClick={onStop}>
					停止
				</button>
			) : (
				<button type="submit" disabled={!input.trim()}>
					发送
				</button>
			)}
		</form>
	);
}
