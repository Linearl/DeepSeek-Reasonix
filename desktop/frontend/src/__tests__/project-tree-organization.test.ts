import assert from "node:assert/strict";
import { manualTopicOrder, moveTopicInRoster, projectTreeFolderHasActiveRuntime, reorderedTopicIDs } from "../components/ProjectTreeOrganization";
import type { ProjectNode } from "../lib/types";

const tree: ProjectNode[] = [
  { key: "a", kind: "project", label: "A", root: "/a", children: [
    { key: "a-1", kind: "topic", label: "A1", root: "/a", topicId: "same", children: [] },
    { key: "a-2", kind: "topic", label: "A2", root: "/a", topicId: "a2", children: [] },
  ] },
  { key: "b", kind: "project", label: "B", root: "/b", children: [
    { key: "b-1", kind: "topic", label: "B1", root: "/b", topicId: "same", children: [] },
    { key: "b-2", kind: "topic", label: "B2", root: "/b", topicId: "b2", children: [] },
  ] },
];

assert.deepEqual(reorderedTopicIDs(tree, "project", "/b", "b2", "same", "before"), ["b2", "same"]);
assert.equal(reorderedTopicIDs(tree, "project", "/a", "b2", "same", "before"), null, "duplicate ids cannot cross project scope");
assert.equal(reorderedTopicIDs([{ ...tree[0]!, children: [...tree[0]!.children!, {
  key: "runtime", kind: "topic", label: "Runtime", root: "/a", topicId: "runtime", runtimeOnly: true, children: [],
}] }], "project", "/a", "runtime", "same", "before"), null, "runtime-only topics are not persisted into manual order");

assert.equal(manualTopicOrder({ key: "a", kind: "topic", label: "A", sortOrder: -1 }, { key: "b", kind: "topic", label: "B" }), 0);
assert.ok(manualTopicOrder({ key: "a", kind: "topic", label: "A", sortOrder: 1 }, { key: "b", kind: "topic", label: "B", sortOrder: 2 }) < 0);

// Task 720: the shared move closure — the topic leaves every group it is
// listed in and joins the target (single-group constraint, task 170).
assert.deepEqual(
  moveTopicInRoster([
    { id: "a", title: "A", topicIds: ["t"] },
    { id: "b", title: "B", topicIds: ["other", "t"] },
    { id: "c", title: "C", topicIds: [] },
  ], "t", "c"),
  [
    { id: "a", title: "A", topicIds: [] },
    { id: "b", title: "B", topicIds: ["other"] },
    { id: "c", title: "C", topicIds: ["t"] },
  ],
);
assert.equal(projectTreeFolderHasActiveRuntime({ key: "folder", kind: "project", label: "Folder", children: [
  { key: "active", kind: "topic", label: "Active", status: "thinking" },
]}), true);
assert.equal(projectTreeFolderHasActiveRuntime({ key: "folder", kind: "project", label: "Folder", children: [
  { key: "idle", kind: "topic", label: "Idle", status: "paused" },
]}), false);

console.log("  PASS  project tree organization");
