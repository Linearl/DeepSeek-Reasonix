// Task-card parsing for the in-conversation collaboration card (task 19 / 145).
//
// The card is authored by the agent as a fenced `taskcard` block holding the
// JSON the create_task_card / update_task_card tools return. Parsing is total:
// a malformed block falls back to the plain code viewer rather than rendering a
// half-empty card, so a model slip never produces a misleading surface.

export type CollabCardStatus = "pending" | "running" | "blocked" | "done" | "failed";

export type CollabCardNode = {
  contactId?: string;
  sessionPath?: string;
  role?: string;
  at?: number;
  note?: string;
};

export type CollabTaskCard = {
  id: string;
  title: string;
  status: CollabCardStatus;
  initiatorContactId?: string;
  assigneeContactId?: string;
  body?: string;
  result?: string;
  error?: string;
  hop?: number;
  nodes: CollabCardNode[];
  createdAt?: number;
  updatedAt?: number;
};

const STATUSES: readonly CollabCardStatus[] = ["pending", "running", "blocked", "done", "failed"];

function normalizeStatus(value: unknown): CollabCardStatus {
  const text = String(value ?? "").trim().toLowerCase();
  return (STATUSES as readonly string[]).includes(text) ? (text as CollabCardStatus) : "pending";
}

function normalizeNodes(value: unknown): CollabCardNode[] {
  if (!Array.isArray(value)) return [];
  return value
    .filter((node): node is Record<string, unknown> => Boolean(node) && typeof node === "object")
    .map((node) => ({
      contactId: typeof node.contactId === "string" ? node.contactId : undefined,
      sessionPath: typeof node.sessionPath === "string" ? node.sessionPath : undefined,
      role: typeof node.role === "string" ? node.role : undefined,
      at: typeof node.at === "number" ? node.at : undefined,
      note: typeof node.note === "string" ? node.note : undefined,
    }));
}

/** Returns null when the block is not a usable card. */
export function parseCollabTaskCard(source: string): CollabTaskCard | null {
  const text = source.trim();
  if (text === "") return null;
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return null;
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const record = raw as Record<string, unknown>;
  const id = typeof record.id === "string" ? record.id.trim() : "";
  const title = typeof record.title === "string" ? record.title.trim() : "";
  // A card without an id or a title carries no meaning; fail to the code view.
  if (id === "" || title === "") return null;
  return {
    id,
    title,
    status: normalizeStatus(record.status),
    initiatorContactId: typeof record.initiatorContactId === "string" ? record.initiatorContactId : undefined,
    assigneeContactId: typeof record.assigneeContactId === "string" ? record.assigneeContactId : undefined,
    body: typeof record.body === "string" ? record.body : undefined,
    result: typeof record.result === "string" ? record.result : undefined,
    error: typeof record.error === "string" ? record.error : undefined,
    hop: typeof record.hop === "number" ? record.hop : undefined,
    nodes: normalizeNodes(record.nodes),
    createdAt: typeof record.createdAt === "number" ? record.createdAt : undefined,
    updatedAt: typeof record.updatedAt === "number" ? record.updatedAt : undefined,
  };
}

/** A terminal failure state has to be visually distinct from ordinary progress. */
export function collabCardIsFailed(card: CollabTaskCard): boolean {
  return card.status === "failed" || Boolean(card.error && card.error.trim() !== "");
}

export function collabCardNodeLabel(node: CollabCardNode): string {
  const who = node.contactId || node.sessionPath || node.role || "";
  return who;
}
