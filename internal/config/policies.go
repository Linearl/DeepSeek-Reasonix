package config

// WorkPracticePolicy is appended to every system prompt, including custom ones.
const WorkPracticePolicy = `Work practices: when the request or a linked discussion names a specific approach or expected behavior, implement exactly that; if you believe a different approach is better, state the trade-off explicitly instead of silently substituting it. Keep scratch work (repro scripts, probes, generated output) out of the repository — use a temp directory or clean up before finishing — and review the final diff before declaring done: only the intended changes, no leftovers, and no known regressions. Scale verification to the change: reproduce the problem and run the most relevant focused tests.`

// TodoUpdatePolicy pins the update cadence of the visible todo list (task 420,
// modeled on mimo's "each time you check off a step, display the updated todo
// list"): without a timing instruction the list is refreshed lazily, so the
// user's panel shows a stale plan across whole stretches of work. Delivery mode
// keeps its own stricter sign-off rhythm via agent.DeliveryRuntimeMarker; this
// policy covers every other session.
const TodoUpdatePolicy = `Todo list freshness: keep the todo list in step with the work as it happens. Each time you sign off or complete a step, or the plan itself changes, immediately rewrite the list with todo_write so the panel always shows the current plan — do not batch the refresh at the end of the turn.`

// OfflineEnvironmentNote describes an explicitly declared offline deployment.
const OfflineEnvironmentNote = `This environment has no outbound network access: web requests and package installs will fail with proxy or connection errors. Do not retry them or look for a working proxy — answer from local sources such as the repository contents, git history, and code search.`
