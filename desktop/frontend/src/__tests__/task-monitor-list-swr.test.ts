// tl1483: task list refresh semantics — SWR (stale rows survive every refresh
// path) + refresh reason classification + deterministic recency sort. Source
// contract pins the component wiring; unit tests pin the pure matrix.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  applyTaskMonitorRefresh,
  sortTaskMonitorRows,
  taskMonitorRecencyCompare,
  taskMonitorRowsEqual,
  type TaskListRow,
} from "../lib/taskMonitorList";

const testDir = dirname(fileURLToPath(import.meta.url));
const panel = readFileSync(resolve(testDir, "../components/TaskMonitorPanel.tsx"), "utf8");

// --- source contracts on the component ---

assert.doesNotMatch(
  panel,
  /setLoading\(true\)/,
  "no refresh path may blank a painted list behind the loading branch (clear→loading→backfill is forbidden)",
);
assert.match(
  panel,
  /loading && sorted\.length === 0/,
  "the loading spinner renders only when there is no stale data to paint",
);
assert.doesNotMatch(
  panel,
  /!loading &&\s*\n\s*sorted\.map/,
  "painted rows render independently of the loading flag (stale-while-revalidate)",
);
assert.match(
  panel,
  /fetchTasks\("", "manual"\)/,
  "the refresh button classifies as a manual refresh",
);
assert.match(
  panel,
  /fetchTasks\("", "poll"\)/,
  "the interval classifies as a poll refresh",
);
assert.match(
  panel,
  /fetchTasks\("", "action"\)/,
  "the post-mutation refetch classifies as an action refresh",
);
assert.match(
  panel,
  /inputsChanged \? "filter" : "mount"/,
  "the mount effect classifies input changes as filter refreshes",
);
assert.match(
  panel,
  /applyTaskMonitorRefresh\(current,/,
  "every non-cursor page write routes through the refresh-reason matrix",
);
assert.doesNotMatch(
  panel,
  /new Date\(b\.updated_at\)\.getTime\(\)/,
  "the recency sort lives in the deterministic compare, not an inline updated_at diff",
);
assert.match(
  panel,
  /useMemo\(\(\) => sortTaskMonitorRows\(tasks\), \[tasks\]\)/,
  "row order derives from the stable compare via memo",
);

// --- unit tests: deterministic recency compare ---

function row(overrides: Partial<TaskListRow> = {}): TaskListRow {
  return {
    schema_version: 1,
    task_id: "task-1",
    session_id: "sess-1",
    state: "running",
    version: 1,
    created_at: "2025-01-01T00:00:00Z",
    updated_at: "2025-01-01T01:00:00Z",
    __projectKey: "",
    __projectLabel: "",
    __catalogKey: "task-1",
    ...overrides,
  };
}

assert.equal(
  taskMonitorRecencyCompare(row({ __catalogKey: "a" }), row({ __catalogKey: "b" })),
  -1,
  "equal-recency rows break ties by catalog key (total order: a before b)",
);
assert.equal(
  taskMonitorRecencyCompare(
    row({ __catalogKey: "a", updated_at: "2025-01-01T02:00:00Z" }),
    row({ __catalogKey: "b", updated_at: "2025-01-01T01:00:00Z" }),
  ) < 0,
  true,
  "newer updated_at sorts before older (comparator returns raw ms delta; sign is the contract)",
);
assert.equal(
  taskMonitorRecencyCompare(
    row({ __catalogKey: "a", updated_at: "2025-01-01T01:00:00Z", created_at: "2025-01-01T01:00:00Z" }),
    row({ __catalogKey: "b", updated_at: "2025-01-01T01:00:00Z", created_at: "2025-01-01T00:00:00Z" }),
  ) < 0,
  true,
  "equal updated_at falls back to created_at desc (raw ms delta; sign is the contract)",
);
assert.equal(
  taskMonitorRecencyCompare(
    row({ __catalogKey: "b", updated_at: "", created_at: "" }),
    row({ __catalogKey: "a", updated_at: "not-a-date", created_at: "not-a-date" }),
  ) > 0,
  true,
  "invalid dates degrade to 0 and the catalog key tie-break decides (raw ms delta; sign is the contract)",
);

// Sort-stability pin: the same row set in any input order produces one
// identical output order (backend row order cannot flip-flop ties).
const stableSet = [
  row({ __catalogKey: "t-1", task_id: "t-1", updated_at: "2025-01-01T01:00:00Z", created_at: "2025-01-01T00:00:00Z" }),
  row({ __catalogKey: "t-2", task_id: "t-2", updated_at: "2025-01-01T01:00:00Z", created_at: "2025-01-01T00:00:00Z" }),
  row({ __catalogKey: "t-3", task_id: "t-3", updated_at: "2025-01-01T03:00:00Z", created_at: "2025-01-01T00:00:00Z" }),
  row({ __catalogKey: "t-4", task_id: "t-4", updated_at: "2025-01-01T01:00:00Z", created_at: "2025-01-01T02:00:00Z" }),
];
const expectedOrder = ["t-3", "t-4", "t-1", "t-2"];
for (const input of [stableSet, [...stableSet].reverse(), [stableSet[2], stableSet[0], stableSet[3], stableSet[1]]]) {
  assert.deepEqual(
    sortTaskMonitorRows(input).map((entry) => entry.__catalogKey),
    expectedOrder,
    "shuffled inputs sort to the same deterministic order",
  );
}

// --- unit tests: row equality ---

assert.equal(taskMonitorRowsEqual([row()], [row()]), true, "identical row sets are equal");
assert.equal(
  taskMonitorRowsEqual([row({ __catalogKey: "a", task_id: "a" }), row({ __catalogKey: "b", task_id: "b" })], [row({ __catalogKey: "b", task_id: "b" }), row({ __catalogKey: "a", task_id: "a" })]),
  true,
  "equality is order-independent",
);
assert.equal(taskMonitorRowsEqual([row()], []), false, "a length change is a content change");
assert.equal(
  taskMonitorRowsEqual([row()], [row({ version: 2 })]),
  false,
  "a version bump must repaint (version feeds expectedVersion in stop/requeue)",
);
assert.equal(
  taskMonitorRowsEqual([row()], [row({ updated_at: "2025-01-01T02:00:00Z" })]),
  false,
  "an updated_at bump is a content change",
);
assert.equal(
  taskMonitorRowsEqual([row()], [row({ __catalogKey: "task-2", task_id: "task-2" })]),
  false,
  "a key-set change is a content change",
);
assert.equal(
  taskMonitorRowsEqual([row({ error_code: "E1" })], [row({ error_code: undefined })]),
  false,
  "an error-code change is a content change",
);

// --- unit tests: refresh-reason matrix ---

const painted = [row()];
const samePage = [row()];
const changedPage = [row({ state: "succeeded", version: 2 })];

assert.equal(
  applyTaskMonitorRefresh(painted, samePage, "poll"),
  painted,
  "an idle poll keeps the previous array reference (zero repaint)",
);
assert.equal(
  applyTaskMonitorRefresh(painted, changedPage, "poll"),
  changedPage,
  "a poll with changed content applies the new page",
);
for (const reason of ["mount", "manual", "action", "filter"] as const) {
  assert.equal(
    applyTaskMonitorRefresh(painted, samePage, reason),
    samePage,
    `a ${reason} refresh applies its page eagerly even when content is unchanged`,
  );
}

console.log("  PASS  task monitor list SWR + refresh reason classification contract");
