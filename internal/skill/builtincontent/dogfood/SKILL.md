---
name: dogfood
description: Systematically explore and test a web application to find bugs, UX issues, and other problems. Use when asked to "dogfood", "QA", "exploratory test", "find issues", "bug hunt", "test this app/site/platform", or review the quality of a web application. Produces a structured report with full reproduction evidence -- step-by-step screenshots, repro videos, and detailed repro steps for every issue -- so findings can be handed directly to the responsible teams.
runas: inline
---

<!--
Derived from vercel-labs/agent-browser (skills/dogfood).
Copyright 2025 Vercel Inc. Licensed under Apache-2.0 (see the upstream repository for license text).
Adapted for Reasonix: browser automation runs through Playwright driven by the bash tool
(replacing the upstream agent-browser CLI), and the black-box testing methodology is absorbed
from the web-gui-tester skill.
-->

# Dogfood

Systematically explore a web application, find issues, and produce a report with full reproduction evidence for every finding.

## Execution Path (Reasonix)

There is no `agent-browser` CLI here. All browser automation goes through the **`bash` tool driving Playwright from Node scripts**:

- Write small `.mjs` scripts and run them with `node`. Playwright resolves from the cwd's `node_modules`; the known-good location in this repo is `desktop/frontend` (playwright dependency + cached browsers via `PLAYWRIGHT_BROWSERS_PATH`, which the bench scripts set themselves). For a scratch target outside the repo, create a scratch dir and add playwright once during environment preparation.
- **Session = persistent browser profile.** `chromium.launchPersistentContext("{OUTPUT_DIR}/session-profile", ...)` keeps cookies/localStorage across separate script runs, replacing the upstream named-session + `state save` model. Each phase/step is a self-contained script that reopens the same profile.
- **Screenshots** land on disk (`page.screenshot({ path })`); review them with the builtin **`view_image`** tool. Capturing without viewing is not observation.
- **Video** comes from `browser.newContext({ recordVideo: { dir } })` (or persistent-context equivalent): use a **dedicated recording context per interactive issue** so each issue gets its own webm.
- **Console/errors**: register read-only listeners at script start — `page.on("pageerror")`, `page.on("console")` (error level), `page.on("requestfailed")` — and collect them for the whole session.
- **Snapshot equivalent** (`snapshot -i` upstream) = read-only DOM inspection: enumerate interactive elements and texts via locators/`textContent`/aria snapshots. Never dispatch events or mutate the DOM from `evaluate`.

## Setup

Only the **Target URL** is required. Everything else has sensible defaults — use them unless the user explicitly provides an override.

| Parameter            | Default                          | Example override            |
| -------------------- | -------------------------------- | --------------------------- |
| **Target URL**       | _(required)_                     | `https://example.com`, `http://localhost:5173` |
| **Output directory** | `./dogfood-output/`              | `Output directory: /tmp/qa` |
| **Scope**            | Full app                         | `Focus on the billing page` |
| **Authentication**   | None                             | `Sign in to user@example.com` |

If the user says something like "dogfood https://example.com", start immediately with defaults. Do not ask clarifying questions unless authentication is mentioned but credentials are missing.

## Black-Box Methodology (non-negotiable)

1. **Pure GUI black-box testing.** Interact only with elements that are visible and operable on the page. Screenshots and read-only DOM inspection are allowed; JavaScript injection that modifies page state, triggers interactions, or bypasses frontend logic is prohibited (side-effect-free reads only).
2. **Faithful to the actual page.** All conclusions come from observed behavior — never guess. If a normal GUI operation fails, record the issue and move on; do not force progress through alternative mechanisms.
3. **Separate testing from fixing.** Do not modify the code under test. If a bug blocks a path, record it, skip that path, continue testing other points. Fixes start only after testing is declared complete and the user asks for changes.
4. **Cross-validate code and visuals.** Every conclusion needs both a read-only DOM check and a viewed screenshot; they corroborate, never replace each other.
5. **Environment preparation is a separate phase.** Starting servers, seeding accounts, or injecting cookies/storage is allowed *before* testing begins — announce "environment preparation complete, formal testing begins", after which the black-box rules apply. Setup must make the feature reachable, never pre-trigger it, and every setup action is reported in the final summary.

## Workflow

```
1. Initialize    Set up session profile, output dirs, report file, error listeners
2. Prepare       Environment only if needed (server, accounts, login); then announce
3. Authenticate  Sign in if needed via GUI, reuse profile
4. Orient        Navigate to starting point, initial screenshot + DOM map
5. Explore       Systematically visit pages and test features (P0 → P3)
6. Document      Screenshot + record each issue as found
7. Wrap up       Update summary counts, verify report, summarize to user
```

### 1. Initialize

```bash
mkdir -p {OUTPUT_DIR}/screenshots {OUTPUT_DIR}/videos {OUTPUT_DIR}/steps
```

Copy the report template into the output directory and fill in the header fields:

```bash
cp {SKILL_DIR}/templates/dogfood-report-template.md {OUTPUT_DIR}/report.md
```

Create `{OUTPUT_DIR}/helpers.mjs` (reused by every step script):

```js
import { chromium } from "playwright";
export const OUT = process.env.DOGFOOD_OUT;            // absolute path, set per invocation
export async function open() {
  const ctx = await chromium.launchPersistentContext(`${OUT}/session-profile`, {
    viewport: { width: 1440, height: 900 },
  });
  const page = ctx.pages()[0] ?? await ctx.newPage();
  const errors = [];
  page.on("pageerror", (e) => errors.push(`pageerror: ${e.message}`));
  page.on("console", (m) => { if (m.type() === "error") errors.push(`console: ${m.text()}`); });
  page.on("requestfailed", (r) => errors.push(`requestfailed: ${r.url()} ${r.failure()?.errorText ?? ""}`));
  return { ctx, page, errors };
}
export const shot = (page, name) => page.screenshot({ path: `${OUT}/screenshots/${name}.png`, fullPage: true });
export async function close(ctx) { await ctx.close(); }   // persists profile + flushes video
```

Each workflow step runs as `DOGFOOD_OUT=... node {OUTPUT_DIR}/steps/step-NN.mjs` via one bash call; keep the script for the record.

### 2. Prepare (only when needed)

If the app needs a dev server, test accounts, or seeded data, set that up first (bash, filesystem, backend tools). This is the only phase where non-GUI mechanisms are allowed. Then state explicitly: environment preparation is complete; formal testing is beginning.

### 3. Authenticate

If the app requires login, do it through the GUI in a step script (fill the form, submit, wait for `load state: "networkidle"`). For OTP/email codes: ask the user, wait for their response, then enter the code. The persistent profile keeps the session for later steps — no separate state file to manage.

### 4. Orient

Take an initial screenshot and map the page structure with read-only DOM inspection:

```js
const { ctx, page, errors } = await open();
await page.goto(TARGET, { waitUntil: "networkidle" });
await shot(page, "initial");
// enumerate interactive elements read-only:
const controls = await page.locator("a, button, input, select, textarea, [role=button], [role=tab]").allTextContents();
```

Identify the main navigation elements and map out the sections to visit. If the user gave only a URL, plan here: P0 main flow → P1 interaction feedback → P2 input boundaries → P3 layout/styling. Show the plan, then start with P0 immediately.

### 5. Explore

Read [references/issue-taxonomy.md](references/issue-taxonomy.md) for the full list of what to look for and the exploration checklist.

**Strategy — work through the app systematically:**

- Start from the main navigation. Visit each top-level section.
- Within each section, test interactive elements: click buttons, fill forms, open dropdowns/modals.
- Check edge cases: empty states, error handling, boundary inputs.
- Try realistic end-to-end workflows (create, edit, delete flows).
- Check the collected console/network error log periodically.

**At each page:** take a screenshot, enumerate interactive elements read-only, and note any new errors from the session log. Use your judgment on depth: core features first. A cluster of issues in one area means investigate deeper.

When an element locator fails, do not retry unchanged — re-observe (fresh screenshot + DOM read) to distinguish a page bug (record and skip) from a stale locator (rebuild from the new facts).

### 6. Document Issues (Repro-First)

Exploration and documentation happen in a single pass — when you find an issue, stop and document it immediately. Every issue must be reproducible: prove it with evidence, don't just note it.

**Choose the right level of evidence for the issue:**

#### Interactive / behavioral issues (functional, ux, console errors on action)

1. Launch a **dedicated recording context** for this issue: `launchPersistentContext(profile, { recordVideo: { dir: OUT + "/videos" } })`.
2. Walk through the steps **at human pace** — `await page.waitForTimeout(1000)` between actions — and screenshot each step:

```js
await shot(page, "issue-001-step-1");
await page.waitForTimeout(1000);
await page.getByRole("button", { name: "Save" }).click();
await page.waitForTimeout(1000);
await shot(page, "issue-001-step-2");
```

3. Pause ~2s at the broken state, take the result screenshot, then `await ctx.close()` — the webm flushes to the videos dir; rename it `issue-001-repro.webm`.
4. View every screenshot with `view_image` before citing it. Write numbered repro steps in the report, each referencing its screenshot.

#### Static / visible-on-load issues (typos, placeholder text, clipped text, misalignment, console errors on load)

Visible without interaction — a single screenshot suffices, no video, no multi-step repro. Write a brief description and reference the screenshot in the report. Set **Repro Video** to `N/A`.

**For all issues:**

1. **Append to the report immediately** — never batch. Increment the issue counter (ISSUE-001, ISSUE-002, ...).
2. **Transient feedback** (toasts, tooltips, loading spinners): capture before → action → after **inside the same script run**, waiting on the target element/state (fixed delay only as fallback), then view both frames.
3. **Page-load failures**: screenshot the failed state, record it as an issue, skip dependent test points.

### 7. Wrap Up

Aim to find **5-10 well-documented issues**, then wrap up. Depth of evidence beats count — 5 issues with full repro beats 20 vague ones.

1. Re-read the report and reconcile the summary severity counts with the actual `### ISSUE-` blocks.
2. Summarize the session error log (JS exceptions, failed requests) in the report with the step where each occurred; if the console could not be captured, say so honestly and fall back to visible error manifestations.
3. Tell the user the report is ready: total issues, breakdown by severity, most critical items, and every environment-preparation action taken.

## Testing Reasonix Itself (fork-specific ladder)

When the target is this fork's own desktop app, climb the verification ladder instead of a plain web session (details in workspace `tasks/界面自验操作卡.md` and the builtin `ll-iteration-dev` skill's `references/gui-self-verify.md`):

1. **Preview level**: Playwright scripts against the vite preview build of `desktop/frontend` (bench scripts in `desktop/frontend/bench/`, mock scenarios via `?mock=...&bench=1`, pin `locale: en-US`).
2. **Installed level**: the lab switch `[desktop] experimental_cdp_debug_port` → read `logs/desktop/cdp-endpoint.txt` → `chromium.connectOverCDP(...)`. Isolated instances only (own REASONIX_HOME/APPDATA copies); never touch the user's instance.
3. **Live window level**: the builtin `screenshot` + `ui_interact` tools drive a real desktop window directly (requires `experimental_ui_driver`).

Wrap-up framing follows the 装机验证四层法 (four-layer installed verification): version calibration, effect evidence (before/after on the same metric), artifact self-proof (grep the feature's string anchor inside the shipped binary), and stability (runtime + error counts).

## Guidance

- **Repro is everything.** Every issue needs proof — match the evidence to the issue: interactive bugs get video + step-by-step screenshots; static bugs get one screenshot.
- **Verify reproducibility before collecting evidence.** Reproduce at least once before recording; an irreproducible glitch is not a valid issue.
- **Write repro steps that map to screenshots.** A reader should be able to follow the steps visually without touching a browser.
- **View every screenshot you cite** with `view_image`. Capture-without-view is not evidence.
- **Be thorough but use judgment.** You are exploring like a real user, not following a test script. If something feels off, investigate.
- **Write findings incrementally.** Append each issue as you discover it; an interrupted session keeps its findings. Never batch issues for the end.
- **Never delete output files.** Do not remove screenshots, videos, or the report mid-session, and do not tear down and restart the session profile. Work forward, not backward.
- **Do not audit the target's source code during testing.** Test as a user; all findings come from observed browser behavior (environment preparation may read configs/scripts to make the app reachable).
- **Check the console.** Many issues are invisible in the UI but show up as JS errors or failed requests.
- **Test like a user, not a robot.** Common workflows end-to-end, realistic data, clicks a human would click.
- **Pace repro videos for humans.** ~1s between actions so a reviewer can watch at 1x speed.
- **Batch independent work.** Multiple step scripts or screenshot calls can run in one bash call when they are independent.

## References

| Reference | When to Read |
| --------- | ------------ |
| [references/issue-taxonomy.md](references/issue-taxonomy.md) | Start of session — calibrate what to look for, severity levels, exploration checklist |

## Templates

| Template | Purpose |
| -------- | ------- |
| [templates/dogfood-report-template.md](templates/dogfood-report-template.md) | Copy into output directory as the report file |
