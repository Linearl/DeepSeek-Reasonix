// Run: tsx src/__tests__/tl1387-sidebar-two-segments.test.ts
// tl1387 D (用户痛点回归钉) + C (窄宽度不挤压): the sidebar session entry is
// title+time TWO segments — no provider/model name renders inline, and the
// title column carries a min-width floor so narrow widths cannot squeeze it
// toward one character. Source-level assertions per the task brief (源码级断言).

import { readFileSync } from "node:fs";

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

console.log("\ntl1387 sidebar two segments (D pin + C guard)");

const tree = readFileSync(new URL("../components/ProjectTree.tsx", import.meta.url), "utf8");
const styles = readFileSync(new URL("../styles.css", import.meta.url), "utf8");
const classic = readFileSync(new URL("../styles/mode-classic.css", import.meta.url), "utf8");
const workbench = readFileSync(new URL("../styles/mode-workbench.css", import.meta.url), "utf8");

// D①: the inline provider/modelRef render block is gone (不出现模型名).
ok(!/node\.modelRef \? \(/.test(tree), "D: no conditional modelRef inline render in the row");
ok(!/provider \|\| node\.modelRef/.test(tree), "D: no provider||modelRef expression anywhere in the row render");
ok(!/data-model=\{node\.modelRef\}/.test(tree), "D: no data-model stamp on an inline span");

// D②: the two segments still render — heading label + side time.
ok(tree.includes('className="project-tree__topic-label"'), "D: title segment (topic-label) present");
ok(tree.includes("project-tree__topic-side") && tree.includes("timeLabel"), "D: time segment (topic-side + timeLabel) present");
// The heading block sits between the label span and the recovery badge — the
// first thing after the label span must NOT be a model span (comments are
// stripped first: the tl1387 comment mentions the retired tag by name).
{
  const start = tree.indexOf('className="project-tree__topic-label"');
  const afterLabel = tree.slice(start, start + 600).replace(/\/\*[\s\S]*?\*\//g, "");
  ok(!afterLabel.includes("modelRef") && !afterLabel.includes("node.provider"), "D: nothing model-bearing renders directly after the title segment");
}

// C: the title column floor (narrow-width anti-squeeze).
{
  const m = styles.match(/\.project-tree__topic--session \.project-tree__topic-label \{[^}]*\}/);
  ok(m !== null, "C: session label rule exists");
  ok(m ? /min-width:\s*72px/.test(m[0]!) : false, "C: title column carries the min-width:72px floor");
  // The base label rule keeps the ellipsis trim so the floor cannot cause a
  // horizontal blowout.
  const base = styles.match(/\.project-tree__topic-label \{[^}]*\}/);
  ok(base ? /text-overflow:\s*ellipsis/.test(base[0]!) : false, "C: base label keeps ellipsis trim alongside the floor");
}

// 38a82c458 protections intact (time column nowrap in both classic and workbench).
ok(/project-tree__topic-time \{[^}]*white-space:\s*nowrap/s.test(classic), "C: classic time column nowrap intact (38a82c458)");
ok(/project-tree__topic-time \{[^}]*white-space:\s*nowrap/s.test(workbench), "C: workbench time column nowrap intact (38a82c458)");

// The hover chain is the only model surface on the row (B) — source pins.
ok(tree.includes("sessionModelHover(modelHoverKey"), "B: row hover reads the three-level model chain");
ok(tree.includes("rememberLiveSessionModel(modelHoverKey, node.modelRef)"), "B: live values feed the last-known memory");

let code = 0;
if (failed > 0) {
  process.stdout.write(`\n${failed} failed, ${passed} passed\n`);
  code = 1;
} else {
  process.stdout.write(`\n${passed} passed, 0 failed\n`);
}
process.exit(code);
