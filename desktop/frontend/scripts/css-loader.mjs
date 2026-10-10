// Loader hook for tsx-based tests: `.css` imports resolve to an empty module
// so component modules (e.g. HeartbeatPanel) can statically import their CSS
// without failing under node. Vite handles the real CSS in browser builds.
// `.yaml` imports mirror Vite's `?raw` semantics (default export = file
// contents as a string) — SettingsPanel's lab layout imports
// `../lab/lab-layout.yaml?raw`, which vite resolves in the browser build but
// node/tsx cannot load natively.
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
export async function load(url, context, nextLoad) {
  const pathname = new URL(url, "file://").pathname;
  if (pathname.endsWith(".css")) {
    return { format: "module", source: "export default \"\";", shortCircuit: true };
  }
  if (pathname.endsWith(".yaml")) {
    const source = `export default ${JSON.stringify(readFileSync(fileURLToPath(url), "utf8"))};`;
    return { format: "module", source, shortCircuit: true };
  }
  return nextLoad(url, context);
}
