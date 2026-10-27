// Run: tsx src/__tests__/resume-click-contract.test.ts
//
// tl 1189 回归锁：会话跳转点击不允许被静默吞掉。
//
// 背景：onResumeSession 曾带 running 闸——`state.running && !singleSurfaceLayout
// && !splitTabId` 时 `return Promise.resolve()`，图墙卡片/命令面板/历史面板的
// 跳转点击在有 turn 运行时毫无反馈（用户两轮确认与目标 tab 是否已开无关）。
// 修复 = 删除静默早退，无条件放行幂等导航（后端已有 tab 原地激活、缺 tab 新建；
// 失败路径由导航队列 catch 的 toast 兜底）。
//
// App 组件没有可直接挂 harness 的入口，本契约以源级断言锁住修复不被静默加回：
// 1) onResumeSession 函数体内不得出现静默早退（Promise.resolve() / bare return）；
// 2) 它必须无条件转发 enqueueNavigation 的 resume-session；
// 3) 四个消费端调用点仍在（历史面板 / 恢复版本宿主 / 命令面板 / 会话图墙）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

let passed = 0;
let failed = 0;

function ok(cond: boolean, label: string) {
  if (cond) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

const here = dirname(fileURLToPath(import.meta.url));
const appPath = resolve(here, "../App.tsx");
const appSource = readFileSync(appPath, "utf8");
const tree = ts.createSourceFile(appPath, appSource, ts.ScriptTarget.Latest, true);

// 定位 `const onResumeSession = useCallback(<arrow>, [deps])`。
let gateBody: ts.ConciseBody | undefined;
let gateDeps: string[] | undefined;
ts.forEachChild(tree, function visit(node: ts.Node): void {
  if (ts.isVariableStatement(node)) {
    for (const decl of node.declarationList.declarations) {
      if (ts.isIdentifier(decl.name) && decl.name.text === "onResumeSession" && decl.initializer
        && ts.isCallExpression(decl.initializer) && decl.initializer.expression.getText(tree) === "useCallback"
        && decl.initializer.arguments.length > 0) {
        gateBody = decl.initializer.arguments[0] as ts.ConciseBody;
        const deps = decl.initializer.arguments[1];
        if (deps && ts.isArrayLiteralExpression(deps)) {
          gateDeps = deps.elements.map((el) => el.getText(tree));
        }
      }
    }
  }
  ts.forEachChild(node, visit);
});

ok(Boolean(gateBody), "onResumeSession is defined via useCallback in App.tsx");
if (!gateBody) {
  console.log(`\n${passed} passed, ${failed} failed`);
  process.exit(1);
}

const bodyText = gateBody.getText(tree);
ok(!bodyText.includes("Promise.resolve()"), "onResumeSession has no silent Promise.resolve() early return");
ok(!/return\s*;/.test(bodyText), "onResumeSession has no bare silent return");
ok(ts.isArrowFunction(gateBody) && ts.isBlock(gateBody.body) && gateBody.body.statements.length === 1
  && ts.isReturnStatement(gateBody.body.statements[0]),
  "onResumeSession body is a single unconditional return");
ok(bodyText.includes("enqueueNavigation") && bodyText.includes('"resume-session"'),
  "onResumeSession unconditionally forwards the resume-session navigation");
ok(Array.isArray(gateDeps) && gateDeps.length === 1 && gateDeps[0] === "enqueueNavigation",
  "onResumeSession depends only on enqueueNavigation (no running/split/single-surface gate inputs)");

// 消费端调用点：删守卫不许误伤入口本身。
ok(appSource.includes("onResume={onResumeSession}"), "HistoryPanel still routes resume through onResumeSession");
ok(appSource.includes("onResumeSession={onResumeSession}"), "SessionRecoveryVersionsHost still routes resume through onResumeSession");
ok(appSource.includes("run: () => void onResumeSession(s)"), "command palette sessions still route resume through onResumeSession");
ok(appSource.includes("void onResumeSession(session)"), "session wall cards still route resume through onResumeSession");

console.log(`\n${passed} passed, ${failed} failed`);
if (failed > 0) process.exit(1);
