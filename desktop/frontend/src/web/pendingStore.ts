// GC-21（⭐21 跨端输入队列 ACK/查询对账，web 壳侧落地）：
// 发送前登记 pending → 202 响应即 ACK 移除；失败保留供重试/对账。
// 持久化 localStorage（跨刷新存活），24h 过期收口为显式丢弃（对齐 zcode
// discarded 语义）；对账（内容比对 /history）在 WebApp 重试路径执行，
// 免 serve 协议改动——残余窗口：响应已达但转录未落时的重发双发，
// HTTP 同步 202 使该窗口极小（见 reconcile 注释）。

export type PendingMsg = {
	id: string;
	input: string;
	ts: number;
};

const STORAGE_KEY = "reasonix_web_pending";
const TTL_MS = 24 * 60 * 60 * 1000; // 24h（zcode pending-command registry 同窗）

/** 载入存活 pending；过期项一并返回由调用方显式提示后丢弃 */
export function loadPending(now: number = Date.now()): {
	alive: PendingMsg[];
	expired: PendingMsg[];
} {
	let raw: string;
	try {
		raw = globalThis.localStorage?.getItem(STORAGE_KEY) ?? "";
	} catch {
		return { alive: [], expired: [] };
	}
	if (!raw) return { alive: [], expired: [] };
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return { alive: [], expired: [] };
	}
	if (!Array.isArray(parsed)) return { alive: [], expired: [] };
	const all = parsed.filter(
		(m): m is PendingMsg =>
			typeof m === "object" && m !== null &&
			typeof (m as PendingMsg).id === "string" &&
			typeof (m as PendingMsg).input === "string" &&
			typeof (m as PendingMsg).ts === "number",
	);
	return {
		alive: all.filter((m) => now - m.ts < TTL_MS),
		expired: all.filter((m) => now - m.ts >= TTL_MS),
	};
}

/** 登记待发送消息（新在尾部，发送序保持 FIFO） */
export function addPending(input: string, now: number = Date.now()): PendingMsg {
	const { alive } = loadPending(now);
	const msg: PendingMsg = {
		id: `p-${now}-${Math.random().toString(36).slice(2, 8)}`,
		input,
		ts: now,
	};
	save(alive.concat(msg));
	return msg;
}

/** 移除（ACK 到达或显式丢弃；过期项一并可删，避免残留反复提示） */
export function removePending(id: string, now: number = Date.now()): void {
	const { alive, expired } = loadPending(now);
	save(alive.concat(expired).filter((m) => m.id !== id));
}

/** 一次性清空（UI「全部丢弃」） */
export function clearPending(): void {
	try {
		globalThis.localStorage?.removeItem(STORAGE_KEY);
	} catch {
		/* 私有模式等：忽略 */
	}
}

function save(list: PendingMsg[]): void {
	try {
		globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(list));
	} catch {
		/* 存储失败静默：pending 仅影响重试能力，不阻塞发送主链 */
	}
}
