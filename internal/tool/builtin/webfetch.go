package builtin

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	nethtml "golang.org/x/net/html"
	"golang.org/x/net/proxy"

	"reasonix/internal/netclient"
	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(webFetch{}) }

type webFetch struct {
	proxySpec netclient.ProxySpec
}

const (
	webFetchTimeout = 15 * time.Second
	webFetchMaxRead = 1 << 20 // 1 MiB cap before extraction
)

func (webFetch) Name() string { return "web_fetch" }

func (webFetch) Description() string {
	return "Fetch a URL over HTTPS/HTTP and return its text content. HTML pages are reduced to readable text (scripts, styles, tags stripped, whitespace collapsed); JSON / plain text / markdown bodies come back verbatim. Use to read documentation pages, API responses, or source files hosted somewhere the local filesystem can't reach."
}

func (webFetch) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "url":{"type":"string","description":"Absolute URL beginning with http:// or https://"}
},
"required":["url"]
}`)
}

func (webFetch) ReadOnly() bool { return true }

// SnipHint front-loads fetched page content like a file read: keep a generous
// head and a short tail.
func (webFetch) SnipHint() tool.SnipHint {
	return tool.SnipHint{Head: 120, Tail: 12, HeadChars: 12000, TailChars: 2000}
}

// ipResolver is the slice of *net.Resolver the SSRF dial path needs; a test
// can stub it to pin resolver edge cases without touching the network.
type ipResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// resolveVettedIPs resolves host and refuses every answer the SSRF blocklist
// rejects, so the dial paths share one vetting gate.
// tl1323: a resolver can legitimately return zero addresses with a nil error;
// indexing ips[0] on that used to panic inside the http.Client dial goroutine
// (no recover in Execute), piercing through to the turn executor. It is a
// normal "no addresses" error now.
func resolveVettedIPs(ctx context.Context, resolver ipResolver, host string) ([]net.IPAddr, error) {
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, &net.DNSError{Err: "no addresses returned", Name: host, IsNotFound: true}
	}
	for _, ip := range ips {
		if blockedFetchIP(ip.IP) {
			return nil, fmt.Errorf("refusing to fetch internal address %s (resolves to %s)", host, ip.IP)
		}
	}
	return ips, nil
}

// ssrfGuardedTransport refuses to connect to private, link-local, or unspecified
// addresses — the SSRF surface a prompt-injected fetch would aim at (cloud
// metadata at 169.254.169.254, RFC1918 internal services). Loopback is allowed:
// the agent can already reach localhost via bash, so a local dev server stays
// fetchable. The check runs at dial time on the resolved IP, so a public host
// that redirects or DNS-rebinds to an internal address is caught too.
func ssrfGuardedTransport(proxyURL string) *http.Transport {
	dialer := &net.Dialer{Timeout: webFetchTimeout}

	// directDialContext handles SSRF-protected direct connection (no proxy).
	// It resolves DNS locally, checks resolved IPs against the SSRF blocklist,
	// then dials the vetted IP directly to prevent DNS rebinding.
	directDialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := resolveVettedIPs(ctx, net.DefaultResolver, host)
		if err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}

	tr := &http.Transport{
		DialContext: directDialContext,
	}

	if proxyURL != "" {
		pu, err := url.Parse(proxyURL)
		if err == nil && pu.Host != "" {
			switch pu.Scheme {
			case "http", "https":
				// HTTP CONNECT: dial proxy → send CONNECT with the ORIGINAL
				// hostname (not a locally-resolved IP) so the proxy handles DNS.
				// This is essential for users whose local DNS is blocked (GFW).
				// SSRF protection (audit-medium#9, plan A): IP literals are
				// checked directly; hostnames get a best-effort local
				// resolution before being handed to the trusted proxy.
				proxyDialer := dialer
				tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
					host, port, err := net.SplitHostPort(addr)
					if err != nil {
						return nil, err
					}
					// SSRF check on the target BEFORE handing it to the proxy
					// (best-effort for hostnames — see checkProxyTargetSSRF).
					if err := checkProxyTargetSSRF(ctx, host); err != nil {
						return nil, err
					}
					// Dial the proxy (proxy address is never an SSRF target — the
					// user configured it, and it's almost certainly an IP or a
					// resolvable hostname reachable from the local network).
					proxyConn, err := proxyDialer.DialContext(ctx, "tcp", pu.Host)
					if err != nil {
						return nil, fmt.Errorf("connect to proxy %s: %w", pu.Host, err)
					}
					// CONNECT the ORIGINAL hostname through the proxy, letting
					// the proxy resolve DNS on the remote side. If this is an IP
					// literal we already vetted it above.
					targetAddr := net.JoinHostPort(host, port)
					connectReq := &http.Request{
						Method: http.MethodConnect,
						URL:    &url.URL{Host: targetAddr},
						Host:   targetAddr,
						Header: make(http.Header),
					}
					if pu.User != nil {
						user := pu.User.Username()
						pass, _ := pu.User.Password()
						auth := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
						connectReq.Header.Set("Proxy-Authorization", "Basic "+auth)
					}
					if err := connectReq.Write(proxyConn); err != nil {
						proxyConn.Close()
						return nil, fmt.Errorf("write CONNECT to proxy: %w", err)
					}
					br := bufio.NewReader(proxyConn)
					resp, err := http.ReadResponse(br, connectReq)
					if err != nil {
						proxyConn.Close()
						return nil, fmt.Errorf("read CONNECT response: %w", err)
					}
					if resp.StatusCode != http.StatusOK {
						proxyConn.Close()
						return nil, fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
					}
					return proxyConn, nil
				}
				tr.Proxy = nil

			case "socks5", "socks5h":
				// Tunnel through SOCKS5. Dial the trusted proxy with a plain
				// dialer (a proxy on a private/LAN address must not be rejected
				// by the SSRF guard), then route the target through it. The
				// target host gets the same best-effort SSRF pre-check as the
				// HTTP CONNECT path above (audit-medium#9, plan A).
				var auth *proxy.Auth
				if pu.User != nil {
					pass, _ := pu.User.Password()
					auth = &proxy.Auth{User: pu.User.Username(), Password: pass}
				}
				if sd, err := proxy.SOCKS5("tcp", pu.Host, auth, dialer); err == nil {
					if cd, ok := sd.(proxy.ContextDialer); ok {
						tr.Proxy = nil
						tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
							host, _, err := net.SplitHostPort(addr)
							if err != nil {
								return nil, err
							}
							if err := checkProxyTargetSSRF(ctx, host); err != nil {
								return nil, err
							}
							return cd.DialContext(ctx, network, addr)
						}
					}
				}
			}
		}
	}

	return tr
}

// Transport reuse: ssrfGuardedTransport is built once per proxyURL and cached,
// so keep-alive connections survive across web_fetch calls. Building a fresh
// Transport per request (the previous behavior) leaked resources: an idle
// persistConn back-references its Transport, so every fetch left a socket plus
// its readLoop/writeLoop goroutines pinned until the peer timed out. Two
// fallbacks bound the cost of transports that leave the cache (audit-high#2):
// IdleConnTimeout recycles idle conns on a timer, and eviction calls
// CloseIdleConnections so a replaced proxy's sockets drop immediately.
const (
	webfetchMaxCachedTransports = 8                // FIFO bound: distinct proxy configs are few
	webfetchIdleConnTimeout     = 90 * time.Second // matches http.DefaultTransport
)

var (
	transportCacheMu    sync.Mutex
	transportCache      = map[string]*http.Transport{}
	transportCacheOrder []string
)

// cachedTransport returns the shared SSRF-guarded transport for proxyURL,
// creating and caching it on first use. When the FIFO bound is exceeded the
// oldest entry is evicted and its idle connections are closed.
func cachedTransport(proxyURL string) *http.Transport {
	transportCacheMu.Lock()
	defer transportCacheMu.Unlock()
	if tr, ok := transportCache[proxyURL]; ok {
		return tr
	}
	tr := ssrfGuardedTransport(proxyURL)
	tr.IdleConnTimeout = webfetchIdleConnTimeout
	transportCache[proxyURL] = tr
	transportCacheOrder = append(transportCacheOrder, proxyURL)
	if len(transportCacheOrder) > webfetchMaxCachedTransports {
		evicted := transportCacheOrder[0]
		transportCacheOrder = transportCacheOrder[1:]
		if old, ok := transportCache[evicted]; ok {
			old.CloseIdleConnections()
			delete(transportCache, evicted)
		}
	}
	return tr
}

type webFetchRoundTripper struct {
	proxyURLFor func(*http.Request) (string, error)
}

func (rt webFetchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	proxyURL, err := rt.proxyURLFor(req)
	if err != nil {
		return nil, fmt.Errorf("resolve proxy: %w", err)
	}
	return cachedTransport(proxyURL).RoundTrip(req)
}

func ssrfGuardedClient(proxyURLFor func(*http.Request) (string, error)) *http.Client {
	return &http.Client{
		Timeout:   webFetchTimeout,
		Transport: webFetchRoundTripper{proxyURLFor: proxyURLFor},
	}
}

// cgnatRange is RFC 6598 shared address space (100.64.0.0/10). Go's IsPrivate
// doesn't cover it, yet some clouds host instance metadata there (Alibaba Cloud
// at 100.100.100.200), so it's an SSRF target web_fetch must refuse too.
var cgnatRange = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// blockedFetchIP reports whether ip is an address web_fetch must not reach.
func blockedFetchIP(ip net.IP) bool {
	return ip.IsPrivate() || // RFC1918 + IPv6 unique-local (fc00::/7)
		ip.IsLinkLocalUnicast() || // 169.254.0.0/16 (incl. cloud metadata) + fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || // 0.0.0.0 / ::
		cgnatRange.Contains(ip) // 100.64.0.0/10 (incl. Alibaba Cloud metadata)
}

// webfetchResolveTimeout bounds the best-effort local DNS pre-check in the
// proxy branches, so a blackholed or censored resolver cannot stall the fetch
// for the whole request timeout. Var (not const) as a test seam.
var webfetchResolveTimeout = 2 * time.Second

// webfetchLookupIPAddr is the DNS seam used by checkProxyTargetSSRF; tests
// swap it to control resolution outcomes.
var webfetchLookupIPAddr = func(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// checkProxyTargetSSRF vets a CONNECT/SOCKS5 target host before it is handed
// to the trusted proxy (audit-medium#9). IP literals are checked directly.
// For hostnames, local resolution is BEST-EFFORT (plan A, deliberate policy):
// if it succeeds, every resolved IP is checked against the blocklist; if it
// fails, the fetch is still allowed through the proxy, leaving a slog.Warn so
// the unchecked path is searchable instead of silent. Local resolution must
// never be mandatory here: users whose local DNS is censored/blocked (GFW)
// depend on the proxy resolving remotely, and a hard local lookup would fail
// every fetch for them. A TOCTOU gap remains by design — the proxy resolves
// and connects independently — and that residual risk is what the warning
// marks.
func checkProxyTargetSSRF(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if blockedFetchIP(ip) {
			return fmt.Errorf("refusing to fetch internal address %s (resolves to %s)", host, ip)
		}
		return nil
	}
	rctx, cancel := context.WithTimeout(ctx, webfetchResolveTimeout)
	defer cancel()
	ips, err := webfetchLookupIPAddr(rctx, host)
	if err != nil {
		slog.Warn("web_fetch ssrf pre-check could not resolve host locally; allowing through proxy unchecked",
			"host", host, "err", err)
		return nil
	}
	for _, ip := range ips {
		if blockedFetchIP(ip.IP) {
			return fmt.Errorf("refusing to fetch internal address %s (resolves to %s)", host, ip.IP)
		}
	}
	return nil
}

func (wf webFetch) proxyURLFor(req *http.Request) (string, error) {
	pf, err := netclient.ProxyFunc(wf.proxySpec)
	if err != nil {
		return "", err
	}
	if pf == nil {
		return "", nil
	}
	u, err := pf(req)
	if err != nil || u == nil {
		return "", err
	}
	return u.String(), nil
}

func (wf webFetch) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.URL == "" {
		return "", fmt.Errorf("url is required")
	}
	u, err := sanitizeFetchURL(p.URL)
	if err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	// A plain UA + Accept tip the server toward returning text/HTML rather
	// than minified asset bundles or binary content.
	req.Header.Set("User-Agent", "reasonix-web-fetch/1.0")
	req.Header.Set("Accept", "text/html,text/plain,text/markdown,application/json,*/*;q=0.5")

	resp, err := ssrfGuardedClient(wf.proxyURLFor).Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", p.URL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxRead))
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	out := string(body)
	if strings.Contains(ct, "text/html") || looksLikeHTML(out) {
		out = htmlToText(out)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return fmt.Sprintf("(empty body — status %s)", resp.Status), nil
	}
	header := fmt.Sprintf("status %s · %s · %d bytes\n\n", resp.Status, contentTypeShort(ct), len(body))
	return header + out, nil
}

// looksLikeHTML lets servers that misreport Content-Type still hit the HTML
// reducer — GitHub raw pages and many docs sites lie about content type.
func looksLikeHTML(s string) bool {
	head := s
	if len(head) > 512 {
		head = head[:512]
	}
	low := strings.ToLower(head)
	return strings.Contains(low, "<!doctype html") || strings.Contains(low, "<html")
}

var (
	multiBlank = regexp.MustCompile(`\n[\t ]*\n([\t ]*\n)+`)
	trailingWS = regexp.MustCompile(`[\t ]+\n`)
)

// htmlToText tokenizes HTML, drops script/style content, unescapes entities, and
// inserts lightweight block boundaries. It is intentionally lossy: we want to
// give the model readable text rather than preserve structure for re-rendering.
func htmlToText(s string) string {
	w := &htmlTextWriter{}
	tokenizer := nethtml.NewTokenizer(strings.NewReader(s))
	skipDepth := 0
	preDepth := 0
	for {
		tt := tokenizer.Next()
		switch tt {
		case nethtml.ErrorToken:
			return normalizeHTMLText(w.String())
		case nethtml.TextToken:
			if skipDepth == 0 {
				w.Text(string(tokenizer.Text()), preDepth > 0)
			}
		case nethtml.StartTagToken:
			name, hasAttr := tokenizer.TagName()
			tag := strings.ToLower(string(name))
			if tag == "script" || tag == "style" {
				skipDepth++
				continue
			}
			if skipDepth > 0 {
				continue
			}
			if tag == "a" {
				w.StartLink(htmlAttr(tokenizer, hasAttr, "href"))
				continue
			}
			w.StartTag(tag)
			if tag == "pre" {
				preDepth++
			}
		case nethtml.SelfClosingTagToken:
			name, _ := tokenizer.TagName()
			tag := strings.ToLower(string(name))
			w.SelfClosingTag(tag)
		case nethtml.EndTagToken:
			name, _ := tokenizer.TagName()
			tag := strings.ToLower(string(name))
			if skipDepth > 0 {
				if tag == "script" || tag == "style" {
					skipDepth--
				}
				continue
			}
			if tag == "pre" && preDepth > 0 {
				preDepth--
			}
			w.EndTag(tag)
		}
	}
}

type htmlTextWriter struct {
	b     strings.Builder
	links []string
}

func (w *htmlTextWriter) String() string {
	return w.b.String()
}

func (w *htmlTextWriter) StartTag(tag string) {
	switch tag {
	case "title":
		w.ensureBlankLine()
		w.b.WriteString("# ")
	case "h1":
		w.ensureBlankLine()
		w.b.WriteString("# ")
	case "h2":
		w.ensureBlankLine()
		w.b.WriteString("## ")
	case "h3":
		w.ensureBlankLine()
		w.b.WriteString("### ")
	case "h4", "h5", "h6":
		w.ensureBlankLine()
		w.b.WriteString("#### ")
	case "li":
		w.ensureNewline()
		w.b.WriteString("- ")
	case "pre":
		w.ensureBlankLine()
		w.b.WriteString("```\n")
	case "blockquote":
		w.ensureBlankLine()
		w.b.WriteString("> ")
	case "tr":
		w.ensureNewline()
	case "td", "th":
		w.ensureCellBoundary()
	default:
		if htmlBreakTag(tag) || htmlBlockTag(tag) {
			w.ensureNewline()
		}
	}
}

func (w *htmlTextWriter) SelfClosingTag(tag string) {
	if htmlBreakTag(tag) || htmlBlockTag(tag) {
		w.ensureNewline()
	}
}

func (w *htmlTextWriter) EndTag(tag string) {
	switch tag {
	case "a":
		w.EndLink()
	case "title", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote":
		w.ensureBlankLine()
	case "pre":
		w.ensureNewline()
		w.b.WriteString("```\n")
		w.ensureBlankLine()
	case "li", "p", "tr":
		w.ensureNewline()
	case "td", "th":
		return
	default:
		if htmlBlockTag(tag) {
			w.ensureNewline()
		}
	}
}

func (w *htmlTextWriter) StartLink(href string) {
	w.links = append(w.links, strings.TrimSpace(href))
}

func (w *htmlTextWriter) EndLink() {
	if len(w.links) == 0 {
		return
	}
	href := w.links[len(w.links)-1]
	w.links = w.links[:len(w.links)-1]
	if href != "" {
		w.b.WriteString(" (")
		w.b.WriteString(href)
		w.b.WriteByte(')')
	}
}

func (w *htmlTextWriter) Text(text string, pre bool) {
	text = stdhtml.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	if !pre {
		text = collapseHTMLInlineText(text)
	}
	if strings.TrimSpace(text) == "" {
		if !w.lastIsSpace() {
			w.b.WriteByte(' ')
		}
		return
	}
	if !pre && w.b.Len() > 0 && !w.lastIsSpace() && !startsWithSpaceOrPunct(text) {
		w.b.WriteByte(' ')
	}
	w.b.WriteString(text)
}

func (w *htmlTextWriter) ensureNewline() {
	if w.b.Len() == 0 || w.lastByte() == '\n' {
		return
	}
	w.b.WriteByte('\n')
}

func (w *htmlTextWriter) ensureBlankLine() {
	if w.b.Len() == 0 {
		return
	}
	if strings.HasSuffix(w.b.String(), "\n\n") {
		return
	}
	w.ensureNewline()
	w.b.WriteByte('\n')
}

func (w *htmlTextWriter) ensureCellBoundary() {
	if w.b.Len() == 0 || w.lastByte() == '\n' {
		return
	}
	if !strings.HasSuffix(w.b.String(), " | ") {
		w.b.WriteString(" | ")
	}
}

func (w *htmlTextWriter) lastByte() byte {
	if w.b.Len() == 0 {
		return 0
	}
	s := w.b.String()
	return s[len(s)-1]
}

func (w *htmlTextWriter) lastIsSpace() bool {
	if w.b.Len() == 0 {
		return false
	}
	return unicode.IsSpace(rune(w.lastByte()))
}

func normalizeHTMLText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = trailingWS.ReplaceAllString(s, "\n")
	s = multiBlank.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func collapseHTMLInlineText(s string) string {
	if s == "" {
		return ""
	}
	leading := unicode.IsSpace([]rune(s)[0])
	trailing := unicode.IsSpace([]rune(s)[len([]rune(s))-1])
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return " "
	}
	out := strings.Join(fields, " ")
	if leading {
		out = " " + out
	}
	if trailing {
		out += " "
	}
	return out
}

func startsWithSpaceOrPunct(s string) bool {
	for _, r := range s {
		return unicode.IsSpace(r) || strings.ContainsRune(".,;:!?)]}", r)
	}
	return false
}

func htmlAttr(tokenizer *nethtml.Tokenizer, hasAttr bool, name string) string {
	for hasAttr {
		key, val, more := tokenizer.TagAttr()
		if strings.EqualFold(string(key), name) {
			return stdhtml.UnescapeString(string(val))
		}
		hasAttr = more
	}
	return ""
}

func htmlBreakTag(tag string) bool {
	return tag == "br" || tag == "hr"
}

func htmlBlockTag(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "body", "caption", "dd", "details",
		"dialog", "div", "dl", "dt", "fieldset", "figcaption", "figure", "footer", "form",
		"h1", "h2", "h3", "h4", "h5", "h6", "head", "header", "html", "li", "main", "nav",
		"ol", "p", "pre", "section", "table", "tbody", "td", "tfoot", "th", "thead", "tr", "ul":
		return true
	default:
		return false
	}
}

func contentTypeShort(ct string) string {
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return strings.TrimSpace(ct)
}
