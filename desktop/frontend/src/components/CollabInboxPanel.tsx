// 跨会话收件箱面板 (task 320): the one place that shows every cross-session
// mail — five-bucket aggregation (views, not storage), sender/recipient/date
// filtering with an asc/desc date toggle (a), revision-stamped dismiss
// (contract ①), retention picker, approval sub-states with their decider
// recorded (task 320 d), and the thread-chain view (task 320 g). The panel is
// a pure consumer of the Go index: everything
// it shows already sits in a recipient inbox — a queued send is not an entry
// (contract ②), and dismissed/decided/retention state lives on disk, so a
// restart keeps all of it (contract f).

import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";

export type CollabMailEntry = {
  id: string;
  from: string;
  fromSession?: string;
  to: string;
  toTitle?: string;
  at: number;
  threadId: string;
  hop?: number;
  cardId?: string;
  approver?: string;
  requireReply?: boolean;
  delivery?: string;
  bucket: string;
  preview: string;
  delivered: boolean;
  read: boolean;
  dismissed?: boolean;
  decidedBy?: string;
  decidedAt?: number;
  pendingMe?: boolean;
  mine?: boolean;
};

export type CollabMailSettings = { retention: string };

export type CollabMailSnapshot = {
  revision: string;
  settings: CollabMailSettings;
  total: number;
  returned: number;
  truncated: boolean;
  entries: CollabMailEntry[];
};

export type CollabMailChain = {
  threadId: string;
  participants: string[];
  count: number;
  firstAt: number;
  lastAt: number;
  preview: string;
  entries: CollabMailEntry[];
};

export type CollabMailChains = {
  revision: string;
  settings: CollabMailSettings;
  total: number;
  chains: CollabMailChain[];
};

export type CollabInboxBindings = {
  ListCollabMail(bucket: string, from: string, to: string, state: string, limit: number, includeDismissed: boolean, order: string): Promise<CollabMailSnapshot>;
  ListCollabMailChains(bucket: string, limit: number): Promise<CollabMailChains>;
  DismissCollabMail(ids: string[]): Promise<CollabMailSnapshot>;
  UndismissCollabMail(ids: string[]): Promise<CollabMailSnapshot>;
  MarkCollabMailDecided(messageID: string, by: string): Promise<CollabMailSnapshot>;
  SetCollabMailRetention(retention: string): Promise<CollabMailSnapshot>;
};

let panelOpen = false;
const openListeners = new Set<(open: boolean) => void>();

export function isCollabInboxOpen(): boolean {
  return panelOpen;
}

/** Opens/closes the panel (settings entry point, task 320). */
export function setCollabInboxOpen(next: boolean): void {
  if (panelOpen === next) return;
  panelOpen = next;
  for (const listener of openListeners) listener(next);
}

export function onCollabInboxOpenChange(cb: (open: boolean) => void): () => void {
  openListeners.add(cb);
  return () => openListeners.delete(cb);
}

const BUCKETS = ["all", "approval", "mention", "automation", "system"] as const;
const STATES = ["all", "pendingMe", "mine", "decided"] as const;
const RETENTIONS = ["7d", "30d", "90d", "forever"] as const;
const ORDERS = ["desc", "asc"] as const;

type Bucket = (typeof BUCKETS)[number];
type StateFilter = (typeof STATES)[number];
type OrderFilter = (typeof ORDERS)[number];

function formatTime(ms: number): string {
  if (!ms) return "";
  return new Date(ms).toLocaleString();
}

export function CollabInboxPanel({ bindings }: { bindings?: CollabInboxBindings } = {}) {
  const t = useT();
  const b: CollabInboxBindings = bindings ?? app;
  const [open, setOpen] = useState(panelOpen);
  const [bucket, setBucket] = useState<Bucket>("all");
  const [state, setState] = useState<StateFilter>("all");
  const [order, setOrder] = useState<OrderFilter>("desc");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [view, setView] = useState<"list" | "chains">("list");
  const [showDismissed, setShowDismissed] = useState(false);
  const [snapshot, setSnapshot] = useState<CollabMailSnapshot | null>(null);
  const [chains, setChains] = useState<CollabMailChains | null>(null);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => onCollabInboxOpenChange((next) => setOpen(next)), []);

  const refresh = useCallback(async () => {
    try {
      if (view === "chains") {
        setChains(await b.ListCollabMailChains(bucket, 100));
      } else {
        setSnapshot(await b.ListCollabMail(bucket, from.trim(), to.trim(), state, 100, showDismissed, order));
      }
    } catch {
      // A closed gateway must not crash the panel — it simply shows no data.
    }
  }, [b, bucket, from, to, state, order, showDismissed, view]);

  useEffect(() => {
    if (!open) return;
    void refresh();
  }, [open, refresh]);

  if (!open) return null;

  const act = async (fn: () => Promise<CollabMailSnapshot>) => {
    setBusy(true);
    try {
      setSnapshot(await fn());
      setView("list");
    } catch {
      // keep the previous snapshot on failure
    } finally {
      setBusy(false);
    }
  };

  const rows: CollabMailEntry[] = snapshot?.entries ?? [];

  return createPortal(
    <div className="collab-inbox-panel" role="dialog" aria-label={t("collabInbox.title")}>
      <div className="collab-inbox-panel__head">
        <span className="collab-inbox-panel__title">{t("collabInbox.title")}</span>
        <div className="collab-inbox-panel__actions">
          <label className="collab-inbox-panel__retention">
            <span>{t("collabInbox.retention")}</span>
            <select
              value={snapshot?.settings?.retention ?? "7d"}
              disabled={busy}
              onChange={(event) => void act(() => b.SetCollabMailRetention(event.target.value))}
            >
              {RETENTIONS.map((r) => (
                <option key={r} value={r}>{t(`collabInbox.retention.${r}` as "collabInbox.retention.7d")}</option>
              ))}
            </select>
          </label>
          <button type="button" className="btn btn--small" onClick={() => setCollabInboxOpen(false)}>
            {t("collabInbox.close")}
          </button>
        </div>
      </div>

      <div className="collab-inbox-panel__buckets" role="tablist">
        {BUCKETS.map((name) => (
          <button
            key={name}
            type="button"
            role="tab"
            aria-selected={bucket === name}
            className={`collab-inbox-panel__bucket${bucket === name ? " collab-inbox-panel__bucket--on" : ""}`}
            onClick={() => setBucket(name)}
          >
            {t(`collabInbox.bucket.${name}` as "collabInbox.bucket.all")}
          </button>
        ))}
      </div>

      {bucket === "approval" && (
        <div className="collab-inbox-panel__states">
          {STATES.map((name) => (
            <button
              key={name}
              type="button"
              className={`collab-inbox-panel__state${state === name ? " collab-inbox-panel__state--on" : ""}`}
              onClick={() => setState(name)}
            >
              {t(`collabInbox.state.${name}` as "collabInbox.state.all")}
            </button>
          ))}
        </div>
      )}

      <div className="collab-inbox-panel__filters">
        <input
          className="collab-inbox-panel__filter"
          placeholder={t("collabInbox.from")}
          value={from}
          onChange={(event) => setFrom(event.target.value)}
        />
        <input
          className="collab-inbox-panel__filter"
          placeholder={t("collabInbox.to")}
          value={to}
          onChange={(event) => setTo(event.target.value)}
        />
        <div className="collab-inbox-panel__ordertoggle" role="group" aria-label={t("collabInbox.sortByDate")}>
          {ORDERS.map((name) => (
            <button
              key={name}
              type="button"
              aria-pressed={order === name}
              className={`collab-inbox-panel__state${order === name ? " collab-inbox-panel__state--on" : ""}`}
              onClick={() => setOrder(name)}
            >
              {t(`collabInbox.order.${name}` as "collabInbox.order.desc")}
            </button>
          ))}
        </div>
        <div className="collab-inbox-panel__viewtoggle">
          <button
            type="button"
            className={`collab-inbox-panel__state${view === "list" ? " collab-inbox-panel__state--on" : ""}`}
            onClick={() => setView("list")}
          >
            {t("collabInbox.view.list")}
          </button>
          <button
            type="button"
            className={`collab-inbox-panel__state${view === "chains" ? " collab-inbox-panel__state--on" : ""}`}
            onClick={() => setView("chains")}
          >
            {t("collabInbox.view.chains")}
          </button>
        </div>
        <label className="collab-inbox-panel__showdismissed">
          <input
            type="checkbox"
            checked={showDismissed}
            onChange={(event) => setShowDismissed(event.target.checked)}
          />
          {t("collabInbox.showDismissed")}
        </label>
      </div>

      <div className="collab-inbox-panel__rows">
        {view === "list" && rows.length === 0 && (
          <div className="collab-inbox-panel__empty">{t("collabInbox.empty")}</div>
        )}
        {view === "list" &&
          rows.map((entry) => (
            <div key={entry.id} className={`collab-inbox-panel__row${entry.dismissed ? " collab-inbox-panel__row--dismissed" : ""}`}>
              <div className="collab-inbox-panel__meta">
                <span className={`collab-inbox-panel__bucketbadge collab-inbox-panel__bucketbadge--${entry.bucket}`}>
                  {t(`collabInbox.bucket.${entry.bucket as Bucket}` as "collabInbox.bucket.all")}
                </span>
                <span className="collab-inbox-panel__route">
                  {entry.from} → {entry.toTitle || entry.to}
                </span>
                <span className="collab-inbox-panel__time">{formatTime(entry.at)}</span>
                <span className={`collab-inbox-panel__read${entry.read ? " collab-inbox-panel__read--on" : ""}`}>
                  {entry.read ? t("collabInbox.read") : t("collabInbox.unread")}
                </span>
                {entry.decidedBy && (
                  <span className="collab-inbox-panel__decided">
                    {t("collabInbox.decidedBy", { by: entry.decidedBy })}
                  </span>
                )}
                {entry.pendingMe && !entry.decidedBy && (
                  <span className="collab-inbox-panel__pending">{t("collabInbox.state.pendingMe")}</span>
                )}
              </div>
              <div className="collab-inbox-panel__preview">{entry.preview}</div>
              <div className="collab-inbox-panel__rowactions">
                {entry.bucket === "approval" && !entry.decidedBy && !entry.dismissed && (
                  <button
                    type="button"
                    className="btn btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.MarkCollabMailDecided(entry.id, "human"))}
                  >
                    {t("collabInbox.decide")}
                  </button>
                )}
                {!entry.dismissed ? (
                  <button
                    type="button"
                    className="btn btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.DismissCollabMail([entry.id]))}
                  >
                    {t("collabInbox.dismiss")}
                  </button>
                ) : (
                  <button
                    type="button"
                    className="btn btn--small"
                    disabled={busy}
                    onClick={() => void act(() => b.UndismissCollabMail([entry.id]))}
                  >
                    {t("collabInbox.restore")}
                  </button>
                )}
              </div>
            </div>
          ))}

        {view === "chains" && (chains?.chains?.length ?? 0) === 0 && (
          <div className="collab-inbox-panel__empty">{t("collabInbox.empty")}</div>
        )}
        {view === "chains" &&
          (chains?.chains ?? []).map((chain) => (
            <div key={chain.threadId} className="collab-inbox-panel__chain">
              <button
                type="button"
                className="collab-inbox-panel__chainhead"
                onClick={() => setExpanded(expanded === chain.threadId ? null : chain.threadId)}
              >
                <span className="collab-inbox-panel__chaincount">
                  {t("collabInbox.rounds", { n: chain.count })}
                </span>
                <span className="collab-inbox-panel__chainroute">{chain.participants.join(" ↔ ")}</span>
                <span className="collab-inbox-panel__time">{formatTime(chain.lastAt)}</span>
              </button>
              {expanded === chain.threadId && (
                <div className="collab-inbox-panel__chainbody">
                  {chain.entries.map((entry) => (
                    <div key={entry.id} className="collab-inbox-panel__chainentry">
                      <span className="collab-inbox-panel__route">{entry.from}</span>
                      <span className="collab-inbox-panel__preview">{entry.preview}</span>
                      <span className="collab-inbox-panel__time">{formatTime(entry.at)}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}
      </div>

      <div className="collab-inbox-panel__foot">
        <span className="collab-inbox-panel__revision">
          {t("collabInbox.revision", { rev: snapshot?.revision ?? chains?.revision ?? "-" })}
        </span>
        <span className="collab-inbox-panel__note">{t("collabInbox.note")}</span>
      </div>
    </div>,
    document.body,
  );
}
