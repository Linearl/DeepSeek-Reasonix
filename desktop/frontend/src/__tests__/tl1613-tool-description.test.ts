// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1613-tool-description.test.ts
// tl1613 工具 description 参数 → 卡片两层结构（人话标题+命令原文收起区）：
// ① subjectOf 对 bash/grep/glob/read_file/edit_file/write_file/multi_edit
//   description 优先、无/空白回退原 subject（旧会话零回退）；
// ② task 家族既有 description 语义不被本件改动（回归钉）；
// ③ shellCommandOf 永远出命令原文（描述不顶替命令块）；
// ④ 流式半截 JSON：description 键优先、未到时回退命令键；
// ⑤ 桌面端 historyToolSubject 同链（读 Go 源码钉双侧一致，task603 同款跨检）。

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { shellCommandOf, streamingToolSubject, subjectOf } from "../lib/tools";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
function eq(actual: unknown, expected: unknown, label: string) {
  const same = actual === expected;
  if (!same) process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
  ok(same, label);
}

// ① subjectOf：description 优先 + 回退链
eq(subjectOf("bash", JSON.stringify({ command: "git status", description: "Show working tree status" })), "Show working tree status", "① bash description 优先");
eq(subjectOf("bash", JSON.stringify({ command: "git status" })), "git status", "① bash 无 description 回退命令");
eq(subjectOf("bash", JSON.stringify({ command: "git status", description: "   " })), "git status", "① bash 空白 description 回退命令");
eq(subjectOf("grep", JSON.stringify({ pattern: "retry.*deadline", path: "internal/agent", description: "Find retry deadline handling" })), "Find retry deadline handling", "① grep description 优先");
eq(subjectOf("grep", JSON.stringify({ pattern: "retry.*deadline", path: "internal/agent" })), "retry.*deadline", "① grep 无 description 回退 pattern");
eq(subjectOf("glob", JSON.stringify({ pattern: "**/*.test.ts", description: "Locate frontend test files" })), "Locate frontend test files", "① glob description 优先");
eq(subjectOf("read_file", JSON.stringify({ path: "a.go", description: "Check arg validation" })), "Check arg validation", "① read_file description 优先");
eq(subjectOf("read_file", JSON.stringify({ path: "a.go" })), "a.go", "① read_file 无 description 回退 path");
eq(subjectOf("edit_file", JSON.stringify({ path: "a.go", description: "Fix off-by-one" })), "Fix off-by-one", "① edit_file description 优先");
eq(subjectOf("edit_file", JSON.stringify({ file_path: "b.ts" })), "b.ts", "① edit_file 回退 file_path");
eq(subjectOf("write_file", JSON.stringify({ path: "n.md", content: "x", description: "Draft note" })), "Draft note", "① write_file description 优先");
eq(subjectOf("multi_edit", JSON.stringify({ path: "c.tsx", edits: [], description: "Rename across card" })), "Rename across card", "① multi_edit description 优先");
eq(subjectOf("bash", "not json at all"), "", "① 半截 JSON 不猜值（空 subject，诚实态由渲染方分层）");

// ② task 既有语义回归钉：description||prompt 不受影响
eq(subjectOf("task", JSON.stringify({ prompt: "do research", description: "Survey compact tests" })), "Survey compact tests", "② task description 优先（既有）");
eq(subjectOf("task", JSON.stringify({ prompt: "do research" })), "do research", "② task 无 description 回退 prompt（既有）");

// ③ 命令块永出命令原文
eq(shellCommandOf(JSON.stringify({ command: "git status", description: "Show working tree status" })), "git status", "③ shellCommandOf 忽略 description");
eq(shellCommandOf(JSON.stringify({ description: "only description" })), "", "③ shellCommandOf 无命令为空");

// ④ 流式：description 键优先；未到时回退命令键；半截值出前缀
eq(streamingToolSubject("bash", `{"command":"git st`), "git st", "④ 流式 description 未到回退命令（半截值出前缀）");
eq(streamingToolSubject("bash", `{"description":"Show working tree status","command":"git status"}`), "Show working tree status", "④ 流式 description 完整优先");
eq(streamingToolSubject("bash", `{"description":"Show working tre`), "Show working tre", "④ 流式 description 半截出已到前缀");
eq(streamingToolSubject("read_file", `{"path":"a.go","offset":0}`), "a.go", "④ 流式 read_file 回退 path");

// ⑤ 双侧一致：桌面端 historyToolSubject 也走 description 优先回退链
const repoRoot = fileURLToPath(new URL("../../../../", import.meta.url));
const goSource = readFileSync(`${repoRoot}desktop/app_history_tools.go`, "utf8");
ok(goSource.includes('firstNonEmpty(desc, historyArgString(a, "command"))'), "⑤ 桌面端 bash: desc->command 链在位");
ok(goSource.includes('firstNonEmpty(desc, historyArgString(a, "pattern")'), "⑤ 桌面端 grep/glob: desc->pattern 链在位");
ok(goSource.includes('"read_file", "edit_file", "write_file", "multi_edit"'), "⑤ 桌面端文件工具四件套同链");

process.stdout.write(`\ntl1613-tool-description: ${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
