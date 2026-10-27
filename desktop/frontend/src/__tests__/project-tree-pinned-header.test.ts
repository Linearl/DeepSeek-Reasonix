import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const projectTreeSource = readFileSync(new URL("../components/ProjectTree.tsx", import.meta.url), "utf8");
// tl1191: mode-scoped rules (sidebar--workbench variants) live in src/styles/mode-*.css now.
const styles = [
  readFileSync(new URL("../styles.css", import.meta.url), "utf8"),
  readFileSync(new URL("../styles/mode-classic.css", import.meta.url), "utf8"),
  readFileSync(new URL("../styles/mode-workbench.css", import.meta.url), "utf8"),
  readFileSync(new URL("../styles/mode-creation.css", import.meta.url), "utf8"),
].join("\n");

assert.ok(
  projectTreeSource.includes('className="project-tree__section-title project-tree__section-title--pinned"'),
  "pinned heading uses a non-interactive section-title class",
);
assert.ok(
  !projectTreeSource.includes('className="project-tree__folder" style={{ cursor'),
  "pinned heading does not inherit interactive folder hover styles",
);
assert.ok(
  projectTreeSource.includes('className="project-tree__section-title-icon" aria-hidden="true"'),
  "pinned heading marks its decorative icon as hidden from assistive technology",
);
assert.match(
  styles,
  /\.sidebar--workbench \.project-tree__section-title--pinned,[^{]*\{[^}]*display:\s*flex;[^}]*height:\s*32px;/s,
  "pinned heading keeps the folder-aligned layout without folder interaction styles",
);

console.log("  PASS  project tree pinned heading contract");
