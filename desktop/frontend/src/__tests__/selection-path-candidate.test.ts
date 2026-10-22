// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/selection-path-candidate.test.ts
//
// 任务 1060：转录选中文字 → 路径候选的形态识别矩阵（纯函数）。
// 只测形态层：接受=交给后端探针的候选；拒绝=连探针都不必问（零误报第一道闸）。
// 存在性/可开性判定归后端探针（desktop/resolve_openable_path.go），此处不重复。

import { selectionPathCandidate } from "../lib/selectionPathCandidate";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  if (actual === expected) ok(true, label);
  else ok(false, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

console.log("\nselection path candidate (task 1060)");

// ── 接受：绝对形 ──
eq(selectionPathCandidate("D:\\out\\weekly.md")?.path, "D:\\out\\weekly.md", "Windows 盘符绝对路径接受");
eq(selectionPathCandidate("D:/out/weekly.md")?.path, "D:/out/weekly.md", "正斜杠盘符绝对路径接受");
eq(selectionPathCandidate("\\\\nas\\share\\docs\\report.md")?.path, "\\\\nas\\share\\docs\\report.md", "UNC 路径接受");
eq(selectionPathCandidate("//nas/share/docs/report.md")?.path, "//nas/share/docs/report.md", "斜杠形式 UNC 接受");
eq(selectionPathCandidate("/etc/hosts")?.path, "/etc/hosts", "POSIX 开头斜杠绝对路径接受");

// ── 接受：相对形 ──
eq(
  selectionPathCandidate("docs/task/lens_dirty/镜头脏污-周会讲稿.md")?.path,
  "docs/task/lens_dirty/镜头脏污-周会讲稿.md",
  "任务书实例：项目内相对路径接受",
);
eq(selectionPathCandidate("a/b")?.path, "a/b", "最短相对候选接受（存在性交给探针）");
eq(selectionPathCandidate("2026/10/22")?.path, "2026/10/22", "日期形斜杠文本按候选放行（存在性裁决）");
eq(selectionPathCandidate("C:\\Program Files\\x y.md")?.path, "C:\\Program Files\\x y.md", "含空格路径接受");

// ── 接受：清洗（剥包裹与句尾噪声）──
eq(selectionPathCandidate("D:\\out\\weekly.md。")?.path, "D:\\out\\weekly.md", "中文句号收尾剥除");
eq(selectionPathCandidate("docs/a.md,")?.path, "docs/a.md", "英文逗号收尾剥除");
eq(selectionPathCandidate('"D:\\out\\weekly.md"')?.path, "D:\\out\\weekly.md", "成对双引号剥除");
eq(selectionPathCandidate("`docs/task/x.md`")?.path, "docs/task/x.md", "成对反引号剥除");
eq(selectionPathCandidate("**docs/a.md**")?.path, "docs/a.md", "成对 markdown 强调剥除");
eq(selectionPathCandidate("  docs/a.md  ")?.path, "docs/a.md", "首尾空白剥除");
eq(selectionPathCandidate("D:\\x\\y.md)。")?.path, "D:\\x\\y.md", "不配平收尾括号+句号剥除");
eq(
  selectionPathCandidate("C:\\Program Files (x86)")?.path,
  "C:\\Program Files (x86)",
  "配平括号属于路径本身（不剥收尾括号）",
);

// ── 拒绝：非路径文字与明确出围的形态（零误报第一道闸）──
ok(selectionPathCandidate("assistant reply text") === null, "普通句子（无分隔符）拒绝");
ok(selectionPathCandidate("README") === null, "无分隔符单词拒绝");
ok(selectionPathCandidate("note: this") === null, "带冒号无分隔符文本拒绝");
ok(selectionPathCandidate("https://example.com/a") === null, "http URL 拒绝（不在本件范围）");
ok(selectionPathCandidate("file:///D:/x.md") === null, "file URL 拒绝（另有链路面）");
ok(selectionPathCandidate("~/docs/x.md") === null, "家目录缩写拒绝（前端不猜 HOME）");
ok(selectionPathCandidate("D:\\a.md\nD:\\b.md") === null, "多行选区拒绝");
ok(selectionPathCandidate("") === null, "空串拒绝");
ok(selectionPathCandidate("   ") === null, "纯空白拒绝");
ok(selectionPathCandidate("。") === null, "裸标点拒绝");
ok(selectionPathCandidate("a/".repeat(600)) === null, "超长文本拒绝（护栏）");
eq(selectionPathCandidate("（docs/a.md）")?.path, "docs/a.md", "成对全角括号剥除");
eq(selectionPathCandidate("未配对（docs/a.md")?.path, "未配对（docs/a.md", "未配对包裹符号不剥（保持候选原样交探针）");

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
