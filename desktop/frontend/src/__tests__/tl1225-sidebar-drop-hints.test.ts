// Run: tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/tl1225-sidebar-drop-hints.test.ts
//
// tl 1225 验收钉（侧栏拖拽放置提示统一：分组/项目拖拽目标与对话拖拽手感一致）:
//   ① 视觉机制统一：项目行（.project-tree__folder--drop-*）与项目分组头
//      （.project-tree__pgroup-main--drop-*）的放置指示改用对话行同款
//      inset 实心条；旧的外置伪元素细线（画在行框外 top/bottom: -2px）清零
//      ——它会被相邻行 wrapper 的 contain:layout stacking context 盖住、被
//      滚动容器在首末行裁剪。（tl 1267：对话行参照自身已演进为 accent 插入
//      线，项目侧 inset 条范式保持不变。）
//   ② 「落入分组」高亮（pgroup-main--drop-into）描边对齐会话侧 nest-target
//      的 2px 强度。
//   ③ 状态链与落盘语义零改动：dropProject/dropGroup/projectDropInto 三个
//      状态、className 组装点、PROJECT_ROW_DRAG_TYPE 专用 MIME 与
//      handleMoveToGroup 落盘链原样（提示层只动 CSS，不碰 TSX）。

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  process.stdout.write(`  ${value ? "PASS" : "FAIL"}  ${label}\n`);
  if (value) passed += 1;
  else {
    failed += 1;
    process.exitCode = 1;
  }
}

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(resolve(here, "../styles.css"), "utf8");
const tree = readFileSync(resolve(here, "../components/ProjectTree.tsx"), "utf8");

// 取「单选择器规则」的声明体（本测试只钉单选择器规则）。
function ruleBody(selector: string): string {
  const index = css.indexOf(`${selector} {`);
  if (index < 0) return "";
  const open = css.indexOf("{", index);
  const close = css.indexOf("}", open);
  return css.slice(open + 1, close);
}

console.log("\ntl1225 ① project row hint = topic-row inset bar mechanism");

// ── ① 项目行：inset 实心条（与 .project-tree__topic--drop-* 同机制） ──────────
{
  // tl 1267 返修演进：topic 参照机制从 currentColor inset 条升级为 accent
  // 插入线（::after 车道 + --project-tree-drop-inset 缩进起笔，完整契约钉在
  // tl1267-quadrisector-drop-hints.test.tsx）；项目侧（folder/pgroup）保留
  // inset 条范式不变，下面的断言钉的仍是项目行自己。
  ok(
    !css.includes("box-shadow: inset 0 2px 0 currentColor;")
      && ruleBody(".project-tree__topic.project-tree__topic--drop-before::after").includes("top: 0;"),
    "the topic reference evolved to the tl1267 accent insertion line (old currentColor bar retired)",
  );
  ok(
    ruleBody(".project-tree__folder--drop-before").includes("box-shadow: inset 0 2px 0 var(--accent);"),
    "the project-row before-hint is an inset bar INSIDE the row box",
  );
  ok(
    ruleBody(".project-tree__folder--drop-after").includes("box-shadow: inset 0 -2px 0 var(--accent);"),
    "the project-row after-hint is an inset bar INSIDE the row box",
  );
  ok(
    ruleBody(".project-tree__pgroup-main--drop-before").includes("box-shadow: inset 0 2px 0 var(--accent);")
      && ruleBody(".project-tree__pgroup-main--drop-after").includes("box-shadow: inset 0 -2px 0 var(--accent);"),
    "the project-group header hints use the same inset bars",
  );
  ok(
    ruleBody(".project-tree__pgroup-main--drop-into").includes("box-shadow: inset 0 0 0 2px color-mix(in srgb, var(--accent) 55%, transparent);")
      && ruleBody(".project-tree__pgroup-main--drop-into").includes("background: color-mix(in srgb, var(--accent) 16%, transparent);"),
    "the drop-into landing keeps the accent wash and carries the 2px nest-target-weight ring",
  );
  // 上限 + 新增即红：项目树四个提示类的外置伪元素选择器不允许回来。
  const retired = [
    ".project-tree__folder--drop-before::before",
    ".project-tree__folder--drop-after::after",
    ".project-tree__pgroup-main--drop-before::before",
    ".project-tree__pgroup-main--drop-after::after",
  ];
  ok(
    retired.every((selector) => !css.includes(selector)),
    "the retired outset hairline selectors stay gone (0 occurrences, regression = red)",
  );
}

console.log("\ntl1225 ③ state chain and write semantics untouched (hint layer = CSS only)");

// ── ③ 类名组装点与状态链原样（TSX 零改动自证） ────────────────────────────────
{
  ok(
    tree.includes("${projectDropPosition ? ` project-tree__folder--drop-${projectDropPosition}` : \"\"}"),
    "the project row still composes the drop-position hint class",
  );
  ok(
    tree.includes("${groupDropPosition ? ` project-tree__pgroup-main--drop-${groupDropPosition}` : \"\"}"),
    "the group header still composes the group-reorder hint class",
  );
  ok(
    tree.includes("${projectLanding ? \" project-tree__pgroup-main--drop-into\" : \"\"}"),
    "the group header still composes the drop-into landing class",
  );
  ok(
    tree.includes("setDropProject(") && tree.includes("setDropGroup(") && tree.includes("setProjectDropInto("),
    "all three hint states are wired (dropProject / dropGroup / projectDropInto)",
  );
  ok(
    (tree.match(/event\.clientY < rect\.top \+ rect\.height \/ 2 \? "before" : "after"/g) ?? []).length >= 2,
    "the insertion-point split (pointer half) drives both the row and the header hints",
  );
  ok(
    tree.includes('event.dataTransfer.setData(PROJECT_ROW_DRAG_TYPE, projectDragKey)')
      && tree.includes("handleMoveToGroup(droppedProjectRoot, group.id)")
      && tree.includes("handleMoveToGroup(draggedRoot, targetGroupId)"),
    "the tl1199 dedicated MIME and the handleMoveToGroup write chain are untouched",
  );
}

console.log(`\ntl1225: ${passed} passed, ${failed} failed`);
if (failed > 0) process.exitCode = 1;
