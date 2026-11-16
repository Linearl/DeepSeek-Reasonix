// Test-only ESM load hook: neutralizes non-JS asset imports (CSS, YAML, fonts,
// images) for component tests run through tsx. SettingsPanel and friends import
// CSS statically, which Node cannot load. Register this hook from the test file
// (after tsx has bootstrapped, before any dynamic component imports) with
// node:module register — the last-registered hook runs first in the chain.
export async function load(url, context, next) {
  const clean = url.split("?")[0];
  if (/\.(ya?ml|md|txt)$/i.test(clean)) {
    // consumed as raw text (vite ?raw / yaml plugin)
    return { format: "module", source: 'export default "";', shortCircuit: true };
  }
  if (/\.(css|svg|png|jpe?g|gif|woff2?|ttf)$/i.test(clean)) {
    return { format: "module", source: "export default {};", shortCircuit: true };
  }
  return next(url, context);
}
