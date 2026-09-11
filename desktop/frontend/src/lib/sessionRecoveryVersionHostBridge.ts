import type { ProjectTopicKey, RecoveryLineageView, SessionMeta } from "./types";

type SessionVersionInspector = (session: SessionMeta, view: RecoveryLineageView) => void;

let inspector: SessionVersionInspector | undefined;
let queued: Parameters<SessionVersionInspector> | undefined;

export function requestSessionVersions(session: SessionMeta, view: RecoveryLineageView) {
  if (inspector) inspector(session, view);
  else queued = [session, view];
}

export function bindSessionVersionInspector(next: SessionVersionInspector): () => void {
  inspector = next;
  if (queued) {
    const request = queued;
    queued = undefined;
    next(...request);
  }
  return () => {
    if (inspector === next) inspector = undefined;
  };
}

// A topic-level request: the caller knows which topic to inspect but not which
// session represents it, so the host fills the session fields in itself. This is
// what gives version management a fixed entry point - a transient notification
// used to be the only place the dialog could be opened from, so missing it meant
// having no way back in.
// view is optional: a caller that only knows the topic (a tree row, say) has no
// lineage data of its own and lets the host fetch it.
type TopicVersionInspector = (topic: ProjectTopicKey, view?: RecoveryLineageView) => void;

let topicInspector: TopicVersionInspector | undefined;
let queuedTopic: Parameters<TopicVersionInspector> | undefined;

export function requestTopicVersions(topic: ProjectTopicKey, view?: RecoveryLineageView) {
  if (topicInspector) topicInspector(topic, view);
  else queuedTopic = [topic, view];
}

export function bindTopicVersionInspector(next: TopicVersionInspector): () => void {
  topicInspector = next;
  if (queuedTopic) {
    const request = queuedTopic;
    queuedTopic = undefined;
    next(...request);
  }
  return () => {
    if (topicInspector === next) topicInspector = undefined;
  };
}
