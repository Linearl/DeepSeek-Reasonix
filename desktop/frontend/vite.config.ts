import { createRequire } from "node:module";
import { defineConfig, searchForWorkspaceRoot, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import reactSwc from "@vitejs/plugin-react-swc";
import { execSync } from "node:child_process";
import { mkdir, readdir, rename, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const devPort = Number(process.env.REASONIX_DESKTOP_VITE_PORT || "5173");
// Test-only opt-in (see transcript-dom-harness): the harness preloads the whole
// markdown graph, and Babel transforming it can exceed vite's transport timeout.
const useSwcTransform = process.env.REASONIX_TEST_TRANSFORM === "swc";
const configDir = dirname(fileURLToPath(import.meta.url));

// Stamps the build commit into the bundle so a minified crash stack can be mapped
// back to the sourcemap of the exact build. Falls back to "dev" off a git checkout.
function buildCommit(): string {
  if (process.env.REASONIX_COMMIT) return process.env.REASONIX_COMMIT;
  try {
    return execSync("git rev-parse --short HEAD", { cwd: configDir }).toString().trim();
  } catch {
    return "dev";
  }
}

function buildChannel(): string {
  return process.env.REASONIX_CHANNEL || "stable";
}

// 493 sourcemap 归档准入（2026-10-05）：sourcemaps/<commit>/ 是崩溃反解历史库，
// 只对「主线内容的构建」入库存量——判据 = 当前 HEAD 是 main-v2-stable 的祖先
// （主仓主线构建、已合并内容的最终构建 ⇒ 归档；worktree 任务分支的未合并内容 ⇒ 跳过，
// map 留在 dist 随该树生命周期自然消亡，从源头止住任务树归档库无限增长，
// 实测 2026-10-05 wt 侧已积 1.9GB）。git 不可用 / 非 git 环境 ⇒ 保守跳过（宁少归档）。
// 显式覆盖：REASONIX_ARCHIVE_SOURCEMAPS=1 强制归档 / =0 强制跳过。
function shouldArchiveSourcemaps(): boolean {
  const forced = process.env.REASONIX_ARCHIVE_SOURCEMAPS;
  if (forced === "1") return true;
  if (forced === "0") return false;
  try {
    execSync("git rev-parse HEAD", { cwd: configDir, stdio: ["ignore", "ignore", "ignore"] });
    execSync("git merge-base --is-ancestor HEAD main-v2-stable", {
      cwd: configDir,
      stdio: ["ignore", "ignore", "ignore"],
    });
    return true;
  } catch {
    return false;
  }
}

// On macOS ≤ 12 (Safari 15 WebKit) a crossorigin module/stylesheet fetched over the
// wails:// scheme is CORS-blocked (no Access-Control-Allow-Origin from the handler),
// so the bundle never loads and the window paints blank; newer WebKit tolerates it.
function stripCrossorigin(): Plugin {
  return {
    name: "strip-crossorigin",
    enforce: "post",
    transformIndexHtml: (html) => html.replace(/\s+crossorigin(?==["']|[\s/>])/g, ""),
  };
}

function archiveHiddenSourcemaps(commit: string): Plugin {
  async function collectMapFiles(dir: string): Promise<string[]> {
    const entries = await readdir(dir, { withFileTypes: true }).catch(() => []);
    const files: string[] = [];
    for (const entry of entries) {
      const p = resolve(dir, entry.name);
      if (entry.isDirectory()) files.push(...(await collectMapFiles(p)));
      else if (entry.isFile() && entry.name.endsWith(".map")) files.push(p);
    }
    return files;
  }

  return {
    name: "archive-hidden-sourcemaps",
    apply: "build",
    closeBundle: async () => {
      // 493：非主线内容的构建跳过归档（见 shouldArchiveSourcemaps 注释）。
      if (!shouldArchiveSourcemaps()) return;
      const distDir = resolve(configDir, "dist");
      const maps = await collectMapFiles(distDir);
      if (!maps.length) return;

      const archiveDir = resolve(configDir, "sourcemaps", commit);
      await mkdir(archiveDir, { recursive: true });
      await Promise.all(
        maps.map(async (mapPath) => {
          const rel = mapPath.slice(distDir.length + 1).replace(/[\\/]+/g, "__");
          await rename(mapPath, resolve(archiveDir, rel));
        }),
      );
      await writeFile(
        resolve(archiveDir, "manifest.json"),
        JSON.stringify({ commit, channel: buildChannel(), archivedAt: new Date().toISOString() }, null, 2) + "\n",
      );
    },
  };
}

// Vite must empty dist before production builds so stale hashed assets disappear.
// Recreate the tracked placeholder afterwards so git status stays clean and
// Go's //go:embed all:frontend/dist still works on a fresh checkout.
function keepDistPlaceholder(): Plugin {
  return {
    name: "keep-dist-placeholder",
    apply: "build",
    closeBundle: async () => {
      const distDir = resolve(configDir, "dist");
      await mkdir(distDir, { recursive: true });
      await writeFile(resolve(distDir, ".gitkeep"), "\n");
    },
  };
}

const commit = buildCommit();
const channel = buildChannel();

const nodeModulePath = String.raw`[\\/]node_modules[\\/](?:\.pnpm[\\/][^\\/]+[\\/]node_modules[\\/])?`;
const vendorReact = new RegExp(`${nodeModulePath}(?:react|react-dom)(?:[\\/]|$)`);
const vendorMarkdown = new RegExp(
  `${nodeModulePath}(?:react-markdown|remark-gfm|remark-math|remark-parse|remark-rehype|rehype-katex|katex|unified|vfile|hast-util-to-jsx-runtime|html-url-attributes)(?:[\\/]|$)`,
);
const vendorHighlight = new RegExp(`${nodeModulePath}highlight\\.js(?:[\\/]|$)`);

// base: "./" so built asset URLs are relative. Wails serves the embedded dist from
// the app root over the wails:// scheme, where absolute "/assets/..." URLs 404.
export default defineConfig({
  // errorRecovery tells lightningcss to skip unparseable rules instead of
  // failing the whole build. Vite 8 + lightningcss 1.32.0 can reject valid
  // @keyframes in concatenated CSS bundles (heartbeat.css + styles.css).
  css: {
    lightningcss: { errorRecovery: true },
  },
  // The DOM harness SSR-transforms the transcript + the preloaded markdown graph;
  // SWC renders that graph fast enough to stay inside vite's transport timeout
  // (module-runner: `transport.timeout ?? 6e4`). Production builds keep Babel
  // so the shipped bundle is byte-for-byte what it was.
  plugins: [useSwcTransform ? reactSwc() : react(), stripCrossorigin(), archiveHiddenSourcemaps(commit), keepDistPlaceholder()],
  base: "./",
  define: { __BUILD_COMMIT__: JSON.stringify(commit), __BUILD_CHANNEL__: JSON.stringify(channel) },
  resolve: {
    alias: {
      // decode-named-character-reference (micromark/remark dependency) ships a
      // browser condition (index.dom.js) that calls document.createElement at
      // module scope. That explodes inside markdown.worker.ts (WorkerGlobalScope
      // has no document), killing the off-main-thread parse on first use. The
      // default entry is DOM-free and works in both window and worker, so pin
      // it for every bundle. The package is a direct devDependency so this
      // resolve works under pnpm's non-hoisted layout.
      "decode-named-character-reference": createRequire(import.meta.url).resolve("decode-named-character-reference"),
      // hast-util-from-html-isomorphic (rehype-katex dependency) has the same
      // shape: its browser entry constructs a DOMParser at module scope, which
      // WorkerGlobalScope lacks. Pin the isomorphic default (parse5) entry.
      "hast-util-from-html-isomorphic": createRequire(import.meta.url).resolve("hast-util-from-html-isomorphic"),
    },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: "hidden",
    target: "es2021",
    // Use terser for smaller output (esbuild is faster to build but produces
    // larger bundles). Disabled for dev builds via the default.
    minify: "terser",
    terserOptions: {
      compress: {
        // Keep warn/error so crash breadcrumbs still capture them; drop the noise.
        drop_console: ["log", "debug", "info", "trace"],
        passes: 2,
      },
      // Preserve names so minified crash stacks stay readable.
      keep_classnames: true,
      keep_fnames: true,
    },
    rolldownOptions: {
      output: {
        // Manual chunk splitting: keep the heavy markdown/math/code pipeline
        // in a separate chunk so it can be cached independently from the
        // app shell. The vendor chunk splits react+react-dom (stable, rarely
        // changes) from the markdown stack (changes more often).
        codeSplitting: {
          groups: [
            { name: "vendor-react", test: vendorReact },
            { name: "vendor-markdown", test: vendorMarkdown },
            { name: "vendor-highlight", test: vendorHighlight },
          ],
        },
      },
    },
    // Raise the warning limit — the markdown vendor chunk is legitimately large
    // (katex alone is ~300KB). The manual split ensures it's cached separately.
    chunkSizeWarningLimit: 600,
  },
  server: {
    // Bind IPv4 — unset host listens on ::1, and the Wails dev proxy's [::1]
    // dial fails on Windows hosts where IPv6 loopback is filtered.
    host: "127.0.0.1",
    port: devPort,
    strictPort: true,
    fs: {
      // Browser-dev theme mocks use the same embedded source assets as Wails.
      // Keep the allow-list narrow while retaining Vite's workspace root.
      allow: [searchForWorkspaceRoot(configDir), resolve(configDir, "../themes/official")],
    },
  },
});
