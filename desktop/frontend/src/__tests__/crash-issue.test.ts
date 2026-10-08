// Run: tsx src/__tests__/crash-issue.test.ts
// Task 617 route A: the crash overlay Copy button must yield a paste-ready
// GitHub issue skeleton (title + sectioned body + repo link + labels).

import { buildCrashIssueSkeleton, CRASH_ISSUE_NEW_URL, CRASH_ISSUE_REPO } from "../lib/crashIssue";
import { buildCrashPayload, buildPerformancePayload, type PerformanceSnapshot } from "../lib/crash";

let passed = 0;
let failed = 0;

function ok(value: unknown, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\ncrash issue skeleton (task 617 route A)");

const crashPayload = buildCrashPayload("react", new Error("boom at render"), "in App\n    at div");
crashPayload.stack = "Error: boom at render\n    at App.tsx:12:3";

const skeleton = buildCrashIssueSkeleton(crashPayload);

ok(skeleton.includes(CRASH_ISSUE_NEW_URL), "skeleton carries the new-issue URL");
ok(skeleton.includes(CRASH_ISSUE_REPO), "skeleton names the target repository");
ok(/Suggested labels: bug, crash/.test(skeleton), "skeleton suggests kind label");
ok(/Suggested title: \[crash\] .*boom at render/.test(skeleton), "skeleton suggests a titled headline");
ok(skeleton.includes("## Summary"), "skeleton has a Summary section");
ok(skeleton.includes("boom at render"), "Summary carries the error message");
ok(skeleton.includes("## Environment"), "skeleton has an Environment section");
ok(skeleton.includes("build: dev"), "Environment carries the build commit");
ok(skeleton.includes("## Stack"), "skeleton has a Stack section");
ok(skeleton.includes("at App.tsx:12:3"), "Stack section embeds the stack frames");
ok(skeleton.includes("## Raw diagnostic payload"), "skeleton embeds the raw payload");
ok(skeleton.includes('"schemaVersion": 2'), "raw payload is rendered as JSON");
ok(skeleton.includes("```text"), "stack is fenced as text");
ok(!skeleton.includes("crash.title"), "no localized UI strings leak into the skeleton");

// A stack containing ``` must not break the surrounding fenced block.
const hostilePayload = buildCrashPayload("window.error", "hostile", "");
hostilePayload.stack = "Error: hostile\n```ts\nnot-a-real-fence\n```";
const hostileSkeleton = buildCrashIssueSkeleton(hostilePayload);
ok(hostileSkeleton.includes("``\u200b`"), "nested fences are neutralized");

// Performance prompts copy the same skeleton shape.
const snapshot: PerformanceSnapshot = {
  reason: "long task 900ms",
  uptimeMs: 1234,
  visibility: "visible",
  focused: true,
  online: true,
  hardwareConcurrency: 8,
};
const perfPayload = buildPerformancePayload(snapshot);
const perfSkeleton = buildCrashIssueSkeleton(perfPayload);
ok(/Suggested title: \[performance\]/.test(perfSkeleton), "performance skeleton suggests its kind title");
ok(/Suggested labels: bug, performance/.test(perfSkeleton), "performance skeleton suggests the performance label");
ok(perfSkeleton.includes(CRASH_ISSUE_NEW_URL), "performance skeleton carries the repo URL");

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
