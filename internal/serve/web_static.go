package serve

import (
	"net/http"
	"path"
	"path/filepath"
	"strings"
)

// 任务 701（Web Remote 基础体验）：serve 托管桌面前端 Web 壳的构建产物。
//
// [serve] web_dist 指向 desktop/frontend 以 web.html 为入口构建出的 dist
// 目录。配置后：
//
//	GET /web        301 → /web/（保留查询串，方便 ?token= 链接）
//	GET /web/       壳 index.html（no-cache，SPA 入口）
//	GET /web/<path> dist 内静态文件（assets/ 带 hash 长缓存）；未命中的
//	                无扩展名路径回落 index.html（SPA 客户端路由）；带扩
//	                展名的未命中路径诚实 404
//
// 未配置（零值）时不挂载任何路由——/web* 保持 404，逐字节零行为。
//
// 鉴权：全部 /web 路由都在 authGate 之内。token 模式下仅壳入口与
// /web/assets/ 前缀豁免于 token 检查（见 auth.go tokenBootstrapPublicPath：
// 惰性静态资源，与既有 / 与 /sessions/{id} 豁免同构），壳 JS 负责用 URL
// fragment 经既有 POST /auth/token 换取 HttpOnly cookie；其余 /web/ 路径
// 与全部 API 照常要求鉴权，无/错 token 一律 401。

const (
	// webShellPrefix 是 Web 壳的挂载前缀。选 /web/ 而不是替换 /：serve
	// 内嵌 UI 的既有路由（GET /、GET /sessions/{id}）保持原样，零回归。
	webShellPrefix = "/web/"
	webShellExact  = "/web"
	// webAssetsPrefix 是 Vite 构建产物的带 hash 资产目录；豁免面与长缓存
	// 都锚定它。
	webAssetsPrefix = "assets/"
)

// mountWebDist 在 webDist 非空时把 /web 静态托管挂到 mux 上。由 handler()
// 在路由表装配末尾调用；空配置是 no-op。
func (s *Server) mountWebDist(mux *http.ServeMux) {
	if s.webDist == "" {
		return
	}
	files := http.Dir(filepath.Clean(s.webDist))
	mux.HandleFunc("GET /web", func(w http.ResponseWriter, r *http.Request) {
		// 保留查询串：?token=…/web 这类链接经 302 落到 /web/ 后，checkToken
		// 的 query 分支仍能看到 token 并完成 cookie 引导。
		target := webShellPrefix
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
	mux.HandleFunc("GET /web/", func(w http.ResponseWriter, r *http.Request) {
		s.serveWebDistFile(w, r, files)
	})
}

// serveWebDistFile 处理 /web/ 前缀下的单个请求：命中文件则按扩展名回
// Content-Type 直出；未命中时无扩展名路径回落壳 index.html（SPA 路由），
// 有扩展名路径 404。
func (s *Server) serveWebDistFile(w http.ResponseWriter, r *http.Request, files http.Dir) {
	name := strings.TrimPrefix(r.URL.Path, webShellPrefix)
	if name == "" {
		// /web/ 本身就是壳入口。
		s.serveWebShellIndex(w, r, files)
		return
	}
	name = path.Clean(name)
	if name == "." || strings.HasPrefix(name, "..") {
		http.NotFound(w, r)
		return
	}
	if !strings.HasPrefix(name, webAssetsPrefix) {
		// 非 assets 的具名文件（favicon、manifest…）：存在才直出，存在性
		// 探测失败一律按 SPA fallback 规则走。
		if f, err := files.Open(name); err == nil {
			defer f.Close()
			if fi, err := f.Stat(); err == nil && !fi.IsDir() {
				w.Header().Set("Cache-Control", "no-cache")
				http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
				return
			}
		}
	}
	if strings.HasPrefix(name, webAssetsPrefix) {
		if f, err := files.Open(name); err == nil {
			defer f.Close()
			if fi, err := f.Stat(); err == nil && !fi.IsDir() {
				// 文件名带内容 hash，可以缓存一年（immutable）。
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
				return
			}
		}
		http.NotFound(w, r)
		return
	}
	if path.Ext(name) != "" {
		// 带扩展名的未命中：宁可诚实 404，也不把 HTML 冒充成图片/脚本。
		http.NotFound(w, r)
		return
	}
	// 无扩展名未命中：SPA 客户端路由回落壳入口。
	s.serveWebShellIndex(w, r, files)
}

// serveWebShellIndex 回落壳 index.html。缺构建产物时 404 并给出可执行的
// 下一步（构建命令），不留静默空白页。
func (s *Server) serveWebShellIndex(w http.ResponseWriter, r *http.Request, files http.Dir) {
	f, err := files.Open("index.html")
	if err != nil {
		http.Error(w, "web shell is not built: index.html missing under [serve] web_dist (build desktop/frontend with the web entry first)", http.StatusNotFound)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.Error(w, "web shell is not built: index.html missing under [serve] web_dist", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", fi.ModTime(), f)
}
