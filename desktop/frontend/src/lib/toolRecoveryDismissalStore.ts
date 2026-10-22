// 任务 1068（1061 返修）：忽略集合从组件内 state 提升到模块级 per-session
// store。1061 把 dismissed 挂在 ToolRecoveryPanel 组件 state 里、靠 Transcript
// 的 key={sessionKey} 按会话重挂载隔离——但同会话内切 tab / turn 结束 running
// 翻转（卸载→重挂）都会把集合清零，用户看到「切走再切回记录重现」。
//
// 记账维度：per-session（快照的 sessionPath，非空时）；快照无 sessionPath
// （探查失败/极端时序）退化为 per-tab（`tab:<tabId>`），退化键不会跨 tab 泄漏
// ——tab 键按 tabId 隔离，session 键按 sessionPath 隔离，两者互不相交。
//
// 语义边界（沿 1061 铁律，一条不破）：
// - 只在内存：不写 localStorage、不调后端（零 binding 调用，不触发新 turn、
//   不改转录数据、不写会话事件日志）；进程重启后记录重现（任务书明示可接受）；
// - per-session：以 snapshot.sessionPath 为主键（tab 与会话是多对一，同一会话
//   的多个 tab 共享忽略集合才是正确语义）。
//
// 键与 1061 一致：attempt_id 为键，无 id 记录以 #idx-<index> 兜底（索引兜底
// 只在同一快照的记录顺序内稳定——这是 1061 已接受的语义，本件不扩）。

const dismissedByScope = new Map<string, Set<string>>();
let revision = 0;
const listeners = new Set<() => void>();

export function toolRecoveryDismissalScope(tabId: string, sessionPath?: string | null): string {
  const path = String(sessionPath ?? "").trim();
  // 快照未到达（sessionPath 空）时按 tab 键记账——记到本 tabId 下；同会话后续
  // 快照到达后过滤统一用 session 键（见 rekeyToolRecoveryDismissals）。
  return path ? `session:${path}` : `tab:${String(tabId ?? "").trim()}`;
}

// 面板用 useSyncExternalStore 订阅本 store：点忽略 → commit → 订阅方重渲染，
// 记录行即时摘除（替代 1061 的 setState 路径）。
export function subscribeToolRecoveryDismissals(listener: () => void): () => void {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}

export function toolRecoveryDismissalRevision(): number {
  return revision;
}

function commit(): void {
  revision += 1;
  listeners.forEach((listener) => listener());
}

export function isToolRecoveryDismissed(scope: string, key: string): boolean {
  return dismissedByScope.get(scope)?.has(key) ?? false;
}

export function dismissToolRecoveryCall(scope: string, key: string): void {
  let set = dismissedByScope.get(scope);
  if (!set) {
    set = new Set();
    dismissedByScope.set(scope, set);
  }
  if (set.has(key)) return;
  set.add(key);
  commit();
}

// 已忽略键并入指定 scope（不 commit，调用方负责在自己的渲染批次里用）。用于
// 快照到达后即时并入 tab 键下的旧账（rekeyToolRecoveryDismissals 的异步窗口
// 里点击忽略的极端时序），避免「点击后一次渲染闪现再消失」。
export function mergeToolRecoveryDismissals(scope: string, keys: ReadonlySet<string>): void {
  if (keys.size === 0) return;
  let set = dismissedByScope.get(scope);
  if (!set) {
    set = new Set();
    dismissedByScope.set(scope, set);
  }
  keys.forEach((key) => set!.add(key));
}

// 快照 scope 下某键是否被忽略（含 legacyTabScope 下的旧账并入）——纯判定不
// commit，供渲染批次内使用（rekey 未 commit 的竞态窗口）。
export function legacyTabDismissedKeys(scope: string, legacyTabScope: string): ReadonlySet<string> | undefined {
  if (scope === legacyTabScope) return undefined;
  return dismissedByScope.get(legacyTabScope);
}

// 快照到达时统一 scope：tab 键下的旧账（快照到达前点击忽略的极端时序）搬到
// session 键，避免同一会话两套键各记一半。搬迁即 commit（订阅方重渲染）。
export function rekeyToolRecoveryDismissals(fromTabId: string, sessionPath?: string): void {
  const path = String(sessionPath ?? "").trim();
  if (!path) return;
  const from = `tab:${String(fromTabId ?? "").trim()}`;
  const to = `session:${path}`;
  if (from === to) return;
  const legacy = dismissedByScope.get(from);
  if (!legacy || legacy.size === 0) return;
  dismissedByScope.delete(from);
  let set = dismissedByScope.get(to);
  if (!set) {
    set = new Set();
    dismissedByScope.set(to, set);
  }
  let changed = false;
  legacy.forEach((key) => { if (!set!.has(key)) { set!.add(key); changed = true; } });
  if (changed) commit();
}

// 本 store 只被 harness 隔离用：生产代码不得调用（sessionPath 主键已天然隔离，
// 无需清理；harness 串行跑同一模块实例才需要）。
export function __resetToolRecoveryDismissalsForTests(): void {
  dismissedByScope.clear();
  commit();
}
