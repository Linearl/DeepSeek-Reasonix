import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// 任务 701（Web Remote）：Web 壳独立构建配置。
//
// 与桌面主构建（vite.config.ts，Wails 宿主、base=/、bundle budget 棘轮）
// 完全隔离：
//   - base=/web/ 对齐 serve 的 /web/ 前缀静态托管（[serve] web_dist）；
//   - outDir=dist-web 不触碰桌面 dist，桌面 initial chunk 棘轮零影响
//     （任务书停测令：新 Vite 壳走独立入口独立产物）；
//   - dev 模式经 proxy 把 API/SSE 转发到本机 reasonix serve，壳源码零
//     分支。
//
// 壳只打入口 web.html + src/web/，不引入 desktop 主壳组件树——桌面
// 主入口（index.html / src/main.tsx）对此文件无任何引用。

const HERE = fileURLToPath(new URL(".", import.meta.url));

// serve API 前缀清单：dev 代理与生产同源使用同一组根级路径（见
// src/web/serveClient.ts）。新增端点时同步这里。
const serveApiPrefixes = [
  "/sessions",
  "/events",
  "/submit",
  "/history",
  "/resume",
  "/new",
  "/cancel",
  "/approve",
  "/answer",
  "/auth",
  "/context",
  "/capabilities",
];

// reasonixServeOrigin：dev 代理目标，默认本机 serve 默认地址。
const serveOrigin = process.env.REASONIX_SERVE_ORIGIN || "http://127.0.0.1:8787";

function logServeTarget(): Plugin {
  return {
    name: "web-shell-serve-target",
    configureServer(server) {
      server.config.logger.info(`web shell dev proxy → ${serveOrigin}`);
    },
  };
}

// serve 端 /web/ 托管契约以 index.html 为壳入口（与通用静态托管器一致）。
// Vite 会保留输入文件名 web.html，这里在产物层归一为 index.html；资产
// 引用路径已由 Vite 基于 base=/web/ 写入 html 内容，改名只影响壳文件名。
function renameShellHtmlToIndex(): Plugin {
	return {
		name: "web-shell-rename-html",
		enforce: "post",
		generateBundle(_opts, bundle) {
			const entry = bundle["web.html"];
			if (entry && entry.type === "asset") {
				entry.fileName = "index.html";
			}
		},
	};
}

export default defineConfig({
  plugins: [react(), logServeTarget(), renameShellHtmlToIndex()],
  base: "/web/",
  build: {
    outDir: "dist-web",
    emptyOutDir: true,
    sourcemap: false,
    rollupOptions: {
      input: resolve(HERE, "web.html"),
    },
  },
  server: {
    port: 5174,
    proxy: Object.fromEntries(
      serveApiPrefixes.map((prefix) => [prefix, { target: serveOrigin, changeOrigin: true }]),
    ),
  },
});
