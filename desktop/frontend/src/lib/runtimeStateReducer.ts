import type { RuntimeProjection, RuntimeState, createRuntimeStateStore } from "./runtimeStateStore";
import type { ProjectRuntimeTopic } from "./types";

function validState(state: RuntimeState) {
  if (!state || state.schemaVersion !== 1) return Boolean(state);
  return Boolean(state.runtimeEpoch) && Number.isSafeInteger(state.revision) && state.revision > 0
    && ["idle", "executing", "finishing", "closed"].includes(state.phase)
    && [state.running, state.pendingPrompt, state.cancelRequested, state.cancellable].every(value => typeof value === "boolean")
    && Number.isSafeInteger(state.backgroundJobs) && state.backgroundJobs >= 0;
}

export function acceptRuntimeState(store: ReturnType<typeof createRuntimeStateStore>, next: RuntimeProjection, authoritative = false): "accepted" | "duplicate" | "stale" | "conflict" {
  const snapshot = store.getSnapshot();
  if (!next?.epoch || !Number.isSafeInteger(next.revision) || !Array.isArray(next.sessions) || !Array.isArray(next.topics)) return "stale";
  if (snapshot && snapshot.epoch !== next.epoch && !authoritative) return "conflict";
  if (snapshot?.epoch === next.epoch) {
    if (next.revision < snapshot.revision) return "stale";
    if (next.revision === snapshot.revision) {
      if (JSON.stringify(next) !== JSON.stringify(snapshot)) return "conflict";
      store.commit(snapshot);
      return "duplicate";
    }
  }
  if (next.sessions.some(session => !validState(session?.state))) return "conflict";
  // Preserve per-session identity across unrelated updates.
  const old = new Map(snapshot?.sessions.map(session => [`${session.tabId}\0${session.sessionPath}`, session]));
  // 981 二轮（候选2）：topics 同样保引用——未变 topic 回交旧对象（仍经
  // structuredClone 兜底的对象），让 store 的内容等价浅比较（逐元素 ===）
  // 能判出「无渲染差异」并跳过 notify（残余放大链 B：每次翻转的全量广播里
  // 大量 topic 内容其实没变）。JSON 判等与 sessions 同一保守口径：字段顺序
  // 不同只会放弃复用（多 clone 一次），不会错误判等。
  const oldTopics = new Map(snapshot?.topics.map(topic => [runtimeTopicIdentity(topic), topic]));
  const committed = { ...next, topics: next.topics.map(topic => {
    const previous = oldTopics.get(runtimeTopicIdentity(topic));
    return previous && JSON.stringify(previous) === JSON.stringify(topic) ? previous : structuredClone(topic);
  }), sessions: next.sessions.map(session => {
    const previous = old.get(`${session.tabId}\0${session.sessionPath}`);
    return previous && JSON.stringify(previous) === JSON.stringify(session) ? previous : Object.freeze({ ...session, state: Object.freeze({ ...session.state }) });
  }) };
  store.commit(committed);
  return "accepted";
}

function runtimeTopicIdentity(topic: ProjectRuntimeTopic): string {
  return `${topic.scope}\0${topic.workspaceRoot ?? ""}\0${topic.node?.topicId ?? topic.node?.key ?? ""}`;
}
