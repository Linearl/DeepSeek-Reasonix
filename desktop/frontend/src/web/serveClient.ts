// 任务 701（Web Remote）：Web 壳数据层适配器——reasonix serve HTTP/SSE 实现。
//
// 壳不依赖 desktop 桥接面（bridge.ts 的 window.go.main.App / Wails
// runtime）：那里是桌面 App 的数百方法族，浏览器壳按任务书只走 serve 的
// 既有 HTTP 端点。本文件显式声明壳消费的方法族（webServe），未覆盖的
// 桌面能力在壳中不存在调用点——「未支持 = 不暴露」，不静默失败。
//
// 端点契约（internal/serve）：
//   GET  /sessions        → sessionListEntry[]
//   GET  /history         → historyMessage[]（当前会话转录）
//   POST /submit {input}  → 202（输出经 /events SSE）
//   POST /resume {path}   → 204 切换会话
//   POST /new {}          → 新会话
//   POST /cancel          → 204 打断当前 turn
//   GET  /events          → SSE，data: eventwire.Event JSON（kind 为字符串，
//                            小写驼峰字段；与 desktop 前端 EventKind 同名）
//   POST /auth/token {token} → 204 + Set-Cookie（fragment 引导，token 不进
//                               URL/日志；见 internal/serve/auth.go）
//
// 鉴权全部依赖 HttpOnly cookie（reasonix_token）；无/错 token 一律 401，
// 壳据此引导登录。

// ─── 数据类型（与 internal/serve 的 JSON 形态一一对应） ───

export interface WebSessionEntry {
	name: string;
	path: string;
	title?: string;
	turns?: number;
	current?: boolean;
	running?: boolean;
	takenOver?: boolean;
	mtimeMilli: number;
	heldBy?: "me" | "other" | "";
}

export interface WebHistoryMessage {
	role: "user" | "assistant" | "tool" | "notice" | string;
	content: string;
	reasoning?: string;
	toolName?: string;
	toolCallId?: string;
}

/**
 * eventwire.Event 的壳侧子集（internal/eventwire/event.go）：kind 是字符串
 * 枚举（internal/eventwire/wire.go kindNames），与 desktop 前端 EventKind
 * 同名；另有 kind:"runtime_state" 状态注入帧，壳忽略。
 */
export interface WebStreamEvent {
	kind: string;
	turnId?: string;
	seq?: number;
	status?: string;
	text?: string;
	detail?: string;
	code?: string;
	reasoning?: string;
	err?: string;
	tool?: {
		id?: string;
		name?: string;
		args?: string;
		output?: string;
		err?: string;
		runState?: string;
	};
	promptKind?: string;
}

// 壳消费的事件种类（internal/eventwire/wire.go kindNames 子集）；未列出的
// kind 壳忽略（基础体验而非全量投影）。
export const StreamKind = {
	TurnStarted: "turn_started",
	Reasoning: "reasoning",
	Text: "text",
	Message: "message",
	ToolDispatch: "tool_dispatch",
	ToolResult: "tool_result",
	Notice: "notice",
	TurnDone: "turn_done",
	ToolResultPreview: "tool_result_preview",
	SessionChanged: "session_changed",
	ToolStarted: "tool_started",
} as const;

// ─── 适配器 ───

async function serveJSON<T>(input: string, init?: RequestInit): Promise<T> {
	const resp = await fetch(input, init);
	if (resp.status === 401) {
		throw new UnauthorizedError();
	}
	if (!resp.ok) {
		const text = await resp.text().catch(() => "");
		throw new Error(`${resp.status} ${text.trim()}`.trim());
	}
	if (resp.status === 204) {
		// resume/cancel 等成功应答无 body。
		return undefined as T;
	}
	return (await resp.json()) as T;
}

export class UnauthorizedError extends Error {
	constructor() {
		super("unauthorized");
		this.name = "UnauthorizedError";
	}
}

function post(body?: unknown): RequestInit {
	return {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify(body ?? {}),
	};
}

export const webServe = {
	/** 探测鉴权态：401 = 未登录。 */
	async authenticated(): Promise<boolean> {
		try {
			await fetch("/sessions");
			return true;
		} catch {
			// 网络错误与 401 都按未就绪处理；区分交给调用方重试。
			return false;
		}
	},

	/** 用 token 换 HttpOnly cookie（POST /auth/token）。 */
	async bootstrapToken(token: string): Promise<boolean> {
		const resp = await fetch("/auth/token", post({ token }));
		return resp.ok;
	},

	async listSessions(): Promise<WebSessionEntry[]> {
		return serveJSON<WebSessionEntry[]>("/sessions");
	},

	async history(): Promise<WebHistoryMessage[]> {
		return serveJSON<WebHistoryMessage[]>("/history");
	},

	async resume(path: string): Promise<void> {
		await serveJSON<void>("/resume", post({ path }));
	},

	async newSession(): Promise<void> {
		await fetch("/new", post({}));
	},

	async submit(input: string): Promise<void> {
		const resp = await fetch("/submit", post({ input }));
		if (resp.status === 401) throw new UnauthorizedError();
		if (!resp.ok) {
			const text = await resp.text().catch(() => "");
			throw new Error(text.trim() || `submit failed (${resp.status})`);
		}
	},

	async cancel(): Promise<void> {
		await fetch("/cancel", post({}));
	},

	/** 订阅事件流（EventSource 自动带 cookie，断线自动重连）。返回退订函数。 */
	openEvents(onEvent: (e: WebStreamEvent) => void): () => void {
		const es = new EventSource("/events");
		es.onmessage = (msg) => {
			try {
				onEvent(JSON.parse(msg.data) as WebStreamEvent);
			} catch {
				// 忽略坏帧：SSE 注释行（: ping）不会进 onmessage。
			}
		};
		return () => es.close();
	},
};
