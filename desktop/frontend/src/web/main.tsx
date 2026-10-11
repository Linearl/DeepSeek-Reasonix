import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import WebApp from "./WebApp";
import "./web.css";

// 任务 701（Web Remote）：Web 壳入口。独立于桌面主入口（src/main.tsx）：
// 桌面 Wails 宿主不引用本文件，壳经 serve 的 /web/ 前缀托管运行。
// 样式也在此引入（WebApp 组件保持无 CSS 依赖，纯逻辑可测）。

const rootEl = document.getElementById("root");
if (!rootEl) {
	throw new Error("web shell: #root missing");
}

createRoot(rootEl).render(
	<StrictMode>
		<WebApp />
	</StrictMode>,
);
