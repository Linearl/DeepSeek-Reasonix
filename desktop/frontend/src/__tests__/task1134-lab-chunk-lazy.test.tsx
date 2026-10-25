// 任务 1134 验收（实验室页加载偏慢——二级 chunk 拆分 + 惰性词表 + rail memo）：
//  ① chunk 门控：ExperimentalLabSection 只经 lazy(() => import(...)) 引用，
//     挂载点唯一且在 experimental tab 分支内（切 tab 前实验室模块零加载零挂载，
//     1127 判例：独立 chunk 用 void import() 挂载使 app-shell 预算零增量）；
//  ② chunk 独立性：实验室 chunk 不反向静态依赖主面板（防循环依赖 + 防
//     chunk 边界被共享引用悄悄打穿）；公共面经三个下沉模块取用；
//  ③ 搜索词表惰性：空查询短路（SettingsNavigation 在 getter 调用前返回），
//     词表 getter 只在真正过滤时构建一次（缓存于闭包），语言切换重建；
//  ④ 切换后全量可达：导出面 + pane 白名单单源（布局一致性由 764/562 盖）。
//
// Run: npx tsx --tsconfig tsconfig.tsx-runner.json src/__tests__/task1134-lab-chunk-lazy.test.tsx

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}

const panel = readFileSync(fileURLToPath(new URL("../components/SettingsPanel.tsx", import.meta.url)), "utf8");
const lab = readFileSync(fileURLToPath(new URL("../components/ExperimentalLabSection.tsx", import.meta.url)), "utf8");
const nav = readFileSync(fileURLToPath(new URL("../components/SettingsNavigation.tsx", import.meta.url)), "utf8");
const shell = readFileSync(fileURLToPath(new URL("../components/SettingsPageShell.tsx", import.meta.url)), "utf8");
const registry = readFileSync(fileURLToPath(new URL("../lab/labPaneRegistry.ts", import.meta.url)), "utf8");

console.log("\ntask 1134 lab chunk lazy gate");

// ── ① chunk 门控：tab 切换前实验室模块不挂载 ──
ok(!/^\s*import\s+\{[^}]*\}\s+from\s+"\.\/ExperimentalLabSection"/m.test(panel)
  && !new RegExp('import\\s+ExperimentalSection\\s+from\\s+"\\./ExperimentalLabSection"').test(panel),
  "SettingsPanel has NO static import of ExperimentalLabSection (chunk stays out of the main bundle)");
ok(panel.includes('const ExperimentalSection = lazy(() => import("./ExperimentalLabSection")'),
  "ExperimentalSection is referenced only via lazy(() => import(...))");
{
  const mounts = panel.match(/<ExperimentalSection\b/g) ?? [];
  ok(mounts.length === 1, `exactly ONE ExperimentalSection mount point (got ${mounts.length})`);
  const mountIdx = panel.indexOf("<ExperimentalSection");
  const branchIdx = panel.indexOf('{tab === "experimental"');
  const suspIdx = panel.lastIndexOf("<Suspense", mountIdx);
  ok(branchIdx >= 0 && panel.slice(branchIdx, mountIdx).includes("<Suspense"),
    "the lab mount sits inside the experimental-tab branch behind Suspense");
  ok(suspIdx >= 0 && suspIdx < mountIdx && panel.slice(suspIdx, mountIdx).includes("lazySettingsPageFallback"),
    "the lab mount renders the shared lazy-page fallback while the chunk loads");
}
// 主 chunk 其余 tab 分支不得引用实验室组件（门控不因重构漂移）。
{
  const nonLabBranches = panel.split(/\{tab === "/).slice(1).filter((chunk) => !chunk.startsWith('experimental"'));
  ok(nonLabBranches.every((chunk) => !chunk.includes("<ExperimentalSection")),
    "no other tab branch mounts the lab section");
}

// ── ② chunk 独立性：不反向依赖主面板 ──
ok(!/from\s+"\.\//.test(lab.match(/import[^;]*SettingsPanel[^;]*;/)?.[0] ?? "") && !lab.includes('from "./SettingsPanel"'),
  "ExperimentalLabSection does not import the main panel (no reverse dependency)");
for (const [file, label] of [
  [shell, "SettingsPageShell.tsx"],
  [registry, "lab/labPaneRegistry.ts"],
] as const) {
  ok(file.includes("tl 1134"), `${label} carries the tl-1134 sink provenance comment`);
  const modName = label.replace("lab/labPaneRegistry.ts", "labPaneRegistry").replace("SettingsPageShell.tsx", "SettingsPageShell");
  ok(new RegExp(`from "[^"]*${modName}"`).test(lab), `the lab chunk imports the shared ${label} module`);
}
ok(panel.includes('from "./SettingsPageShell"') && panel.includes('from "../lab/labPaneRegistry"'),
  "the main panel also uses the sunk shared modules (single source, no drift)");

// ── ③ 搜索词表惰性：空查询零成本，真过滤才构建 ──
ok(nav.includes("if (!normalized) return SETTINGS_TAB_GROUPS;"),
  "empty query short-circuits BEFORE any searchTerms getter runs (zero index cost at first paint)");
ok(nav.includes('typeof item.searchTerms === "function" ? item.searchTerms() : (item.searchTerms ?? "")'),
  "navigation resolves lazy getter searchTerms when a real filter runs");
ok(/const labSearchTerms = useMemo\(\(\) => \{\s*let cache: string \| null = null;\s*return \(\) => \{/.test(panel)
  && panel.includes("cache = parts.join(\" \");"),
  "lab search word list is a cached closure getter (built once per t, reused across keystrokes)");
ok(panel.includes(': id === "experimental" ? labSearchTerms : ""'),
  "the experimental nav item carries the lazy getter (not a prebuilt string)");

// ── ④ 切换后全量可达 ──
ok(lab.includes("export function ExperimentalSection("),
  "the lab chunk exports ExperimentalSection for the lazy boundary");
ok(registry.includes("export const LAB_PANE_IDS:") && lab.includes("LAB_PANE_IDS"),
  "LAB_PANE_IDS lives in the sunk registry and the lab chunk uses it for the resolve whitelist");
ok(/labLayoutResolved = useMemo\(\s*\(\) => resolveLabLayout\(labLayoutYaml, \{ allowedEntryIds: LAB_PANE_IDS \}\)/.test(lab),
  "the lab layout resolves against the same pane whitelist as navigation search (764 single source)");

// 量化读数（人工核对用，不做断言）：主 chunk 规模前后对比的当前侧。
process.stdout.write(
  `  [metrics] main panel now: ${panel.split("\n").length} lines, ${panel.match(/^import /gm)?.length ?? 0} static imports; ` +
  `lab chunk: ${lab.split("\n").length} lines (baseline panel @905daa434: 11969 lines / 86 imports)\n`,
);

process.stdout.write(`\n${passed} passed, ${failed} failed, ${passed + failed} total\n`);
if (failed > 0) process.exit(1);
