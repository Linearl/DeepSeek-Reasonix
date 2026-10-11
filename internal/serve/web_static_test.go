package serve

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// newWebDistTestServer 构造一个带 web_dist 的最小 Server（hostguard/auth
// 测试同款模式）：control.New 空工程控制器足够驱动 handler() 装配。
func newWebDistTestServer(t *testing.T, cfg config.ServeConfig) *Server {
	t.Helper()
	return New(control.New(control.Options{}), NewBroadcaster(), cfg)
}

func writeWebDistFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.html":           "<!doctype html><html><body>web shell</body></html>",
		"assets/app-abc123.js": "console.log('shell')",
		"favicon.ico":          "icon",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// 任务 701 硬验收：web_dist 未配置（默认）时零行为——GET /web/ 落到既有
// `GET /` 通配（内嵌 UI），与改动前逐字节一致，绝不是 Web 壳。
func TestWebDistNotConfiguredRoutesAbsent(t *testing.T) {
	s := newWebDistTestServer(t, config.ServeConfig{})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/web/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	// 零行为语义：未配置时 GET /web/ 落到既有 `GET /` 通配（内嵌 UI），
	// 与改动前一致；绝不返回 Web 壳。
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /web/ status = %d, want 200 (pre-existing GET / wildcard behavior)", resp.StatusCode)
	}
	if strings.Contains(string(body), "web shell") {
		t.Errorf("GET /web/ served the web shell without web_dist configured (zero-behavior violation)")
	}
}

func TestWebDistServesShellIndexAndAssets(t *testing.T) {
	dir := writeWebDistFixture(t)
	s := newWebDistTestServer(t, config.ServeConfig{WebDist: dir})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// GET /web 301 → /web/（保留查询串）。
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/web?x=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("GET /web status = %d, want 301", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "/web/?") {
		t.Errorf("Location = %q, want /web/?x=1 (query preserved)", loc)
	}

	// GET /web/ → 壳 index.html，no-cache。
	resp2, err := http.Get(ts.URL + "/web/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("GET /web/ status = %d, want 200", resp2.StatusCode)
	}
	if !strings.Contains(string(body), "web shell") {
		t.Errorf("GET /web/ body = %q, want shell index.html", string(body))
	}
	if cc := resp2.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	// GET /web/assets/… → 带 hash 资产直出 + immutable 长缓存。
	resp3, err := http.Get(ts.URL + "/web/assets/app-abc123.js")
	if err != nil {
		t.Fatal(err)
	}
	body3, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusOK {
		t.Errorf("GET asset status = %d, want 200", resp3.StatusCode)
	}
	if string(body3) != "console.log('shell')" {
		t.Errorf("asset body = %q", string(body3))
	}
	if cc := resp3.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("asset Cache-Control = %q, want immutable", cc)
	}
}

func TestWebDistSPAFallbackAndHonest404(t *testing.T) {
	dir := writeWebDistFixture(t)
	s := newWebDistTestServer(t, config.ServeConfig{WebDist: dir})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// 无扩展名未命中 → SPA 回落壳入口。
	resp, err := http.Get(ts.URL + "/web/sessions/abc")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "web shell") {
		t.Errorf("SPA fallback status = %d body = %q, want 200 + shell index", resp.StatusCode, string(body))
	}

	// 带扩展名未命中 → 诚实 404（不把 HTML 冒充成图片）。
	resp2, err := http.Get(ts.URL + "/web/missing.png")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("missing.png status = %d, want 404", resp2.StatusCode)
	}
}

// 任务 701 硬验收：token 鉴权全程生效。
// 豁免面 = 壳入口 + assets（惰性静态资源）；API 照常 401，错 token 401。
func TestWebDistTokenModeShellPublicAPINot(t *testing.T) {
	dir := writeWebDistFixture(t)
	s := newWebDistTestServer(t, config.ServeConfig{AuthMode: "token", Token: "secret", WebDist: dir})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	// 壳入口与资产豁免（惰性壳，fragment 引导的前置）。
	for _, path := range []string{"/web", "/web/", "/web/assets/app-abc123.js"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMovedPermanently {
			t.Errorf("GET %s status = %d, want 200/301 (inert shell exempt)", path, resp.StatusCode)
		}
	}

	// API 无 token → 401（负向）。
	resp, err := http.Get(ts.URL + "/sessions")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /sessions (no token) status = %d, want 401", resp.StatusCode)
	}

	// API 错 token → 401（负向）。
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/sessions", nil)
	req.AddCookie(&http.Cookie{Name: cookieToken, Value: "wrong"})
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /sessions (wrong token) status = %d, want 401", resp2.StatusCode)
	}

	// 非 assets 的 /web/ 未命中路径不豁免。
	resp3, err := http.Get(ts.URL + "/web/some/other")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /web/some/other (no token) status = %d, want 401", resp3.StatusCode)
	}
}

// ?token= 既有 legacy 路径对 /web/ 同样可用（复用机制验收）。
func TestWebDistQueryTokenBootstrap(t *testing.T) {
	dir := writeWebDistFixture(t)
	s := newWebDistTestServer(t, config.ServeConfig{AuthMode: "token", Token: "secret", WebDist: dir})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.URL + "/web/?token=secret")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("status = %d, want 302 (query token bootstrap)", resp.StatusCode)
	}
	if c := findCookie(resp.Cookies(), cookieToken); c == nil {
		t.Fatal("token cookie missing after ?token= bootstrap")
	}
}
