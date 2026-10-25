import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, Search, X } from "lucide-react";
import { app } from "../../../lib/bridge";
import type { ProjectNode } from "../../../lib/types";
import { useHeartbeatT } from "./heartbeat.i18n";

// tl 1059: the "bind a conversation" picker on the heartbeat task create form.
// Two ways to pick the topic a new task binds to (the first run then injects
// the prompt there instead of spawning a fresh conversation):
//   1. hierarchical browsing — the project tree's project/global groups with
//      their conversations, the same grouping the sidebar shows;
//   2. keyword search — the session directory (ListAddressableSessions)
//      filtered by title / purpose / topic id.
// No selection keeps today's behaviour: the first run creates and binds a
// conversation of its own ("explicit binding beats first-run binding").

interface TopicEntry {
  topicId: string;
  label: string;
  group: string; // top-level grouping label (project name / global)
  purpose?: string;
}

interface SessionHit {
  topicId: string;
  title: string;
  purpose?: string;
  scope?: string;
  workspaceRoot?: string;
}

function collectTopicEntries(nodes: ProjectNode[], group: string, depth: number, out: TopicEntry[]): void {
  for (const node of nodes ?? []) {
    if (!node) continue;
    // Only real conversations: recovery branch rows (session/global_session)
    // would offer dead or duplicate targets.
    const isTopic = node.kind === "topic" || node.kind === "global_topic";
    if (isTopic && node.topicId) {
      out.push({ topicId: node.topicId, label: node.label || node.topicId, group, purpose: node.preview });
    }
    if (node.children?.length) {
      collectTopicEntries(node.children, group, depth + 1, out);
    }
  }
}

function match(hit: { title?: string; purpose?: string; topicId?: string }, query: string): boolean {
  return [hit.title, hit.purpose, hit.topicId].some((field) => (field ?? "").toLowerCase().includes(query));
}

export function HeartbeatSessionPicker({
  value,
  disabled,
  onChange,
  onOpenTopic,
  taskScope,
  taskRoot,
}: {
  value?: string;
  disabled?: boolean;
  onChange: (topicId: string | undefined) => void;
  onOpenTopic?: (scope: string, workspaceRoot: string, topicId: string) => void;
  taskScope?: string;
  taskRoot?: string;
}) {
  const t = useHeartbeatT();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [tree, setTree] = useState<ProjectNode[] | null>(null);
  const [sessions, setSessions] = useState<SessionHit[] | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    void Promise.all([
      app.ListProjectTree().catch(() => []),
      app.ListAddressableSessions().catch(() => []),
    ]).then(([projectTree, addressable]) => {
      if (cancelled) return;
      setTree(projectTree ?? []);
      setSessions((addressable ?? []).filter((item) => item.topicId).map((item) => ({
        topicId: item.topicId as string,
        title: item.title || (item.topicId as string),
        purpose: item.purpose,
        scope: item.scope,
        workspaceRoot: item.workspaceRoot,
      })));
    });
    return () => { cancelled = true; };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("click", close);
    return () => document.removeEventListener("click", close);
  }, [open]);

  // Tree view: conversations grouped under their project / global folder.
  const groups = useMemo(() => {
    const out: { group: string; entries: TopicEntry[] }[] = [];
    for (const node of tree ?? []) {
      if (!node || (node.kind !== "project" && node.kind !== "global_folder")) continue;
      const entries: TopicEntry[] = [];
      collectTopicEntries(node.children ?? [], node.label || node.root || "", 1, entries);
      if (entries.length > 0) out.push({ group: node.label || t("heartbeat.bindTopicGlobal"), entries });
    }
    return out;
  }, [tree, t]);

  // Search view: the session directory filtered by title / purpose / topic id.
  // Sessions were already narrowed to entries with a topic id at fetch time.
  const hits = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    return (sessions ?? []).filter((item) => match(item, q));
  }, [query, sessions]);

  const loading = tree === null || sessions === null;
  const normalizedQuery = query.trim();
  const listEmpty = loading ? false : normalizedQuery ? hits.length === 0 : groups.length === 0;

  const pick = useCallback((topicId: string) => {
    onChange(topicId);
    setOpen(false);
    setQuery("");
  }, [onChange]);

  const selectedLabel = useMemo(() => {
    if (!value) return "";
    for (const group of groups) {
      const hit = group.entries.find((entry) => entry.topicId === value);
      if (hit) return hit.label;
    }
    for (const item of sessions ?? []) {
      if (item.topicId === value) return item.title || value;
    }
    return value;
  }, [value, groups, sessions]);

  return (
    <div className="heartbeat-bind-topic" data-testid="heartbeat-bind-topic" ref={wrapRef}>
      {value ? (
        <div className="heartbeat-editor__bound-topic" data-testid="heartbeat-bind-topic-selected">
          <button
            className="heartbeat-editor__bound-id"
            type="button"
            title={t("heartbeat.openTopic")}
            disabled={disabled}
            onClick={() => {
              if (onOpenTopic) onOpenTopic(taskScope || "global", taskRoot || "", value);
            }}
          >
            {selectedLabel}
          </button>
          <span className="heartbeat-editor__bound-unbound">{value}</span>
          {!disabled && (
            <>
              <button
                className="heartbeat-bind-topic__clear"
                type="button"
                data-testid="heartbeat-bind-topic-change"
                onClick={() => setOpen((current) => !current)}
              >
                {t("heartbeat.bindTopicChange")}
              </button>
              <button
                className="heartbeat-bind-topic__clear"
                type="button"
                title={t("heartbeat.bindTopicClear")}
                data-testid="heartbeat-bind-topic-clear"
                onClick={() => onChange(undefined)}
              >
                <X size={12} />
                {t("heartbeat.bindTopicClear")}
              </button>
            </>
          )}
        </div>
      ) : (
        <button
          className="heartbeat-scope-select"
          type="button"
          disabled={disabled}
          data-testid="heartbeat-bind-topic-toggle"
          onClick={() => setOpen((current) => !current)}
        >
          {t("heartbeat.bindTopicUnbound")}
        </button>
      )}
      {open && !disabled && (
        <div className="heartbeat-project-menu heartbeat-bind-topic__menu" data-testid="heartbeat-bind-topic-menu">
          <div className="heartbeat-bind-topic__search">
            <Search size={12} />
            <input
              value={query}
              placeholder={t("heartbeat.bindTopicSearch")}
              aria-label={t("heartbeat.bindTopicSearch")}
              data-testid="heartbeat-bind-topic-search"
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {loading && <div className="heartbeat-project-menu__empty">…</div>}
          {!loading && normalizedQuery && (
            hits.length === 0 ? (
              <div className="heartbeat-project-menu__empty">{t("heartbeat.bindTopicNoMatch")}</div>
            ) : (
              hits.map((item) => (
                <button
                  key={item.topicId}
                  type="button"
                  className={`heartbeat-project-menu__item${item.topicId === value ? " heartbeat-project-menu__item--active" : ""}`}
                  data-testid="heartbeat-bind-topic-hit"
                  onClick={() => pick(item.topicId)}
                >
                  <span className="heartbeat-bind-topic__label">{item.title}</span>
                  {item.purpose && <span className="heartbeat-bind-topic__purpose">{item.purpose}</span>}
                  {item.topicId === value && <Check size={12} className="heartbeat-filter-menu__check" />}
                </button>
              ))
            )
          )}
          {!loading && !normalizedQuery && groups.map((group) => (
            <div key={group.group} className="heartbeat-bind-topic__group">
              <div className="heartbeat-bind-topic__group-title">{group.group}</div>
              {group.entries.map((entry) => (
                <button
                  key={entry.topicId}
                  type="button"
                  className={`heartbeat-project-menu__item heartbeat-bind-topic__item${entry.topicId === value ? " heartbeat-project-menu__item--active" : ""}`}
                  data-testid="heartbeat-bind-topic-entry"
                  title={entry.purpose || entry.topicId}
                  onClick={() => pick(entry.topicId)}
                >
                  <span className="heartbeat-bind-topic__label">{entry.label}</span>
                  {entry.topicId === value && <Check size={12} className="heartbeat-filter-menu__check" />}
                </button>
              ))}
            </div>
          ))}
          {listEmpty && <div className="heartbeat-project-menu__empty">{t("heartbeat.bindTopicEmpty")}</div>}
        </div>
      )}
    </div>
  );
}
