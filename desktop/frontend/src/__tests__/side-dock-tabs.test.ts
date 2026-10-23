// Run: tsx src/__tests__/side-dock-tabs.test.ts
// tl 1108/1109: 方案 E 浮动标签组纯逻辑（文件面板与子代理面板共用）——
// 预览/钉住形态、单击选中=预览、再单击取消选中=关闭、批量关闭（全部/其他/
// 右侧）、拖拽排序、超时自动折叠抑制判据。

import {
  activateSideDockTab,
  closeAllSideDockTabs,
  closeOtherSideDockTabs,
  closeRightSideDockTabs,
  closeSideDockTab,
  pinSideDockTab,
  reorderSideDockTabs,
  sideDockHasActiveTabs,
  type SideDockTabsState,
} from "../lib/sideDockTabs";

let passed = 0;
let failed = 0;

function eq(label: string, actual: unknown, expected: unknown) {
  if (JSON.stringify(actual) === JSON.stringify(expected)) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}\n`);
    failed += 1;
  }
}

console.log("\nside dock tabs (tl1108/1109 方案 E)");

const empty: SideDockTabsState<string> = { tabs: [], activeId: null };

// activate: 不存在 ⇒ 追加为 preview 并选中；已存在 ⇒ 只选中。
{
  const s1 = activateSideDockTab(empty, "a.ts");
  eq("activate: first file becomes preview + active", s1, { tabs: [{ id: "a.ts", kind: "preview" }], activeId: "a.ts" });
  const s2 = activateSideDockTab(s1, "b.ts");
  eq("activate: second file becomes preview + active", s2, {
    tabs: [{ id: "a.ts", kind: "preview" }, { id: "b.ts", kind: "preview" }],
    activeId: "b.ts",
  });
  const s3 = activateSideDockTab(s2, "a.ts");
  eq("activate: re-click an existing file only switches selection", s3.activeId, "a.ts");
  eq("activate: re-click never silently pins", s3.tabs.find((t) => t.id === "a.ts")?.kind, "preview");
}

// pin: 双击 ⇒ pinned，可与其他 pinned 并存。
{
  const s = pinSideDockTab(activateSideDockTab(activateSideDockTab(empty, "a.ts"), "b.ts"), "a.ts");
  eq("pin: double-click turns the tab pinned", s.tabs.find((t) => t.id === "a.ts")?.kind, "pinned");
  const s2 = pinSideDockTab(s, "b.ts");
  eq("pin: two pinned tabs coexist", s2.tabs.filter((t) => t.kind === "pinned").length, 2);
  eq("pin: active stays on the just-pinned tab", s2.activeId, "b.ts");
}

// close: 落回最近打开的标签；关最后一个 ⇒ 空态（宽度复位判据）。
{
  const s = activateSideDockTab(activateSideDockTab(empty, "a.ts"), "b.ts");
  const closedB = closeSideDockTab(s, "b.ts");
  eq("close: closing the active falls back to the previous tab", closedB.activeId, "a.ts");
  const closedA = closeSideDockTab(closedB, "a.ts");
  eq("close: the last tab closes to the empty state", closedA, { tabs: [], activeId: null });
  eq("close: an unknown id is a no-op", closeSideDockTab(s, "zzz.ts").tabs.length, 2);
  eq("close: closing a background tab keeps the active", closeSideDockTab(s, "a.ts").activeId, "b.ts");
}

// batch close: all / others / right.
{
  const s: SideDockTabsState<string> = {
    tabs: [
      { id: "a.ts", kind: "pinned" },
      { id: "b.ts", kind: "preview" },
      { id: "c.ts", kind: "pinned" },
    ],
    activeId: "b.ts",
  };
  eq("closeAll: empties everything", closeAllSideDockTabs(s), { tabs: [], activeId: null });
  eq("closeOthers: keeps the target only", closeOtherSideDockTabs(s, "c.ts"), {
    tabs: [{ id: "c.ts", kind: "pinned" }],
    activeId: null,
  });
  eq("closeOthers: keeps the target active when it was active", closeOtherSideDockTabs(s, "b.ts"), {
    tabs: [{ id: "b.ts", kind: "preview" }],
    activeId: "b.ts",
  });
  eq("closeRight: keeps the target and its left side", closeRightSideDockTabs(s, "b.ts").tabs.map((t) => t.id), ["a.ts", "b.ts"]);
  eq("closeRight: active falling in the removed tail moves to the target", closeRightSideDockTabs(s, "a.ts").activeId, "a.ts");
  eq("closeRight: an unknown target is a no-op", closeRightSideDockTabs(s, "zzz.ts").tabs.length, 3);
}

// reorder (拖拽排序).
{
  const s: SideDockTabsState<string> = {
    tabs: [
      { id: "a.ts", kind: "pinned" },
      { id: "b.ts", kind: "preview" },
      { id: "c.ts", kind: "pinned" },
    ],
    activeId: "b.ts",
  };
  eq("reorder: move a before c", reorderSideDockTabs(s, "a.ts", "c.ts", true).tabs.map((t) => t.id), ["b.ts", "a.ts", "c.ts"]);
  eq("reorder: move a after c", reorderSideDockTabs(s, "a.ts", "c.ts", false).tabs.map((t) => t.id), ["b.ts", "c.ts", "a.ts"]);
  eq("reorder: same target is a no-op", reorderSideDockTabs(s, "a.ts", "a.ts", true).tabs.map((t) => t.id), ["a.ts", "b.ts", "c.ts"]);
  eq("reorder: unknown id is a no-op", reorderSideDockTabs(s, "zzz.ts", "a.ts", true).tabs.map((t) => t.id), ["a.ts", "b.ts", "c.ts"]);
  eq("reorder: active selection survives", reorderSideDockTabs(s, "c.ts", "a.ts", true).activeId, "b.ts");
}

// 超时自动折叠抑制判据（1108/1109 共用，勿各自另写）。
{
  eq("folding: no tabs ⇒ folding allowed", sideDockHasActiveTabs([]), false);
  eq("folding: a preview tab suppresses folding", sideDockHasActiveTabs([{ id: "a.ts", kind: "preview" }]), true);
  eq("folding: a pinned tab suppresses folding", sideDockHasActiveTabs([{ id: "a.ts", kind: "pinned" }]), true);
}

console.log(`\n${passed} passed, ${failed} failed`);
process.exit(failed === 0 ? 0 : 1);
