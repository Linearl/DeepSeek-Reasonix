export const STATUS_BAR_ITEM_IDS = [
  "workspace",
  "cache",
  "cache_avg",
  "session_tokens",
  "turn_tokens",
  "turn_tps",
  "turn_output_tokens",
  "turn_cache_tokens",
  "turn_cost",
  "session_turns",
  "context",
  "compact",
  "cost",
  "balance",
  "plan",
  // Task 559: MCP connection state joins the managed items. Appended so saved
  // preference lists keep their order; the chip itself stays hidden while the
  // MCP fleet is fully settled, so a default-on entry adds no standing noise.
  "mcp",
  // tl1500: 对话请求数 / 运行时间 join the managed items — same append contract
  // as the task-559 entry (saved preference lists keep their order; defaults
  // follow the session_tokens/session_turns kind, i.e. default-visible). Both
  // chips read the SAME ContextPanel snapshot the right-dock overview card
  // shows, so bar and card can never disagree (see lib/sessionMetricsSnapshot).
  "session_requests",
  "session_elapsed",
] as const;

export type StatusBarItemId = typeof STATUS_BAR_ITEM_IDS[number];

export const DEFAULT_STATUS_BAR_ITEMS: StatusBarItemId[] = [...STATUS_BAR_ITEM_IDS];

const statusBarItemSet = new Set<string>(STATUS_BAR_ITEM_IDS);

export function normalizeStatusBarItems(items: readonly string[] | null | undefined): StatusBarItemId[] {
  const out: StatusBarItemId[] = [];
  const seen = new Set<string>();
  let removedModel = false;
  for (const raw of items ?? []) {
    const value = String(raw ?? "").trim();
    if (value === "model") removedModel = true;
    // Older preferences stored workspace and branch independently. Keep their
    // first configured position while rendering one combined workspace item.
    const id = value === "git_branch" ? "workspace" : value;
    if (!statusBarItemSet.has(id) || seen.has(id)) continue;
    out.push(id as StatusBarItemId);
    seen.add(id);
  }
  return out.length > 0 ? out : removedModel ? ["workspace"] : [...DEFAULT_STATUS_BAR_ITEMS];
}
