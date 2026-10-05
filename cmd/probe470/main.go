// Task 470 diagnostic probe (kept: reusable for any "model never sees the
// image" report). Replicates the desktop provider construction chain for a
// configured model with the real user config, then captures the actual wire
// request body for a user-image and a view_image-style tool image against a
// local capture server. Point Path C's URLs at the real endpoint (leave the
// clone's BaseURL/ChatURL/RequestURL untouched) to reproduce endpoint-side
// rejections directly — that is how task 470 proved MiMo rejects data-URL
// images (400) while accepting the same payload as an http(s) URL (200).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"reasonix/internal/boot"
	"reasonix/internal/config"
	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

const tinyPNG = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAoAAAAKCAYAAACNMs+9AAAAFUlEQVR42mP8z8AARIQBEwMDAwMDAwAkBgMBjXK3EAAAAABJRU5ErkJggg=="

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Println("config.Load error:", err)
		os.Exit(1)
	}
	entry, ok := cfg.ResolveModel("mimo-api/mimo-v2.6-flash")
	if !ok {
		fmt.Println("ResolveModel: not found")
		os.Exit(1)
	}
	fmt.Printf("resolved: name=%s model=%s kind=%s base=%s\n", entry.Name, entry.Model, entry.Kind, entry.BaseURL)
	fmt.Printf("provider-level Vision=%v VisionModels=%v (len=%d)\n", entry.Vision, entry.VisionModels, len(entry.VisionModels))
	ov, hasOV := entry.ModelOverrides[entry.Model]
	fmt.Printf("model_overrides[%s]: present=%v vision_ptr_nil=%v\n", entry.Model, hasOV, ov.Vision == nil)
	if ov.Vision != nil {
		fmt.Printf("model_overrides[%s].vision = %v\n", entry.Model, *ov.Vision)
	}
	fmt.Printf("EffectiveVision=%v (capability=%s)\n", config.EffectiveVision(entry), config.VisionCapabilityForModel(entry))

	// Path A: exactly what LocalProviderResolver.Resolve does.
	capResolver := config.NewModelCapabilityResolver()
	resolved := capResolver.Resolve(entry)
	info := resolved.ModelInfo
	if info.ID == "" {
		info = provider.ModelInfo{ID: resolved.Model, InputModalities: resolved.InputModalities}
	}
	fmt.Printf("capability resolve: state=%s source=%s modalities=%v\n", resolved.State, resolved.Source, resolved.InputModalities)

	p, err := boot.NewProviderWithProxyAndModelInfo(entry, netclient.ProxySpec{Mode: netclient.ModeAuto}, &info)
	if err != nil {
		fmt.Println("NewProvider error:", err)
		os.Exit(1)
	}
	if mp, ok := p.(provider.ModelInfoProvider); ok {
		mi := mp.ModelInfo()
		fmt.Printf("provider ModelInfo: id=%s modalities=%v supportsImage=%v (supportsNativeImages=%v)\n",
			mi.ID, mi.InputModalities, mi.SupportsInput(provider.ModalityImage), mi.SupportsInput(provider.ModalityImage))
	} else {
		fmt.Println("provider does NOT implement ModelInfoProvider (supportsNativeImages=false)")
	}

	// Path B: wire capture — same entry cloned, URLs pointed at the capture server.
	captured := make(chan []byte, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		captured <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	wireEntry := *entry
	wireEntry.BaseURL = srv.URL
	wireEntry.ChatURL = srv.URL
	wireEntry.RequestURL = srv.URL
	wireP, err := boot.NewProviderWithProxyAndModelInfo(&wireEntry, netclient.ProxySpec{Mode: netclient.ModeOff}, &info)
	if err != nil {
		fmt.Println("wire provider error:", err)
		os.Exit(1)
	}

	msgs := []provider.Message{
		{Role: provider.RoleUser, Content: "看看这张用户粘贴的截图", Images: []string{tinyPNG}},
		{Role: provider.RoleAssistant, Content: "", ToolCalls: []provider.ToolCall{{
			ID: "call_1", Name: "view_image", Arguments: `{"path":"shot.png"}`,
		}}},
		{Role: provider.RoleTool, Content: "[image: image/png, 10x10] shot.png", Images: []string{tinyPNG}, ToolCallID: "call_1", Name: "view_image"},
	}
	ch, err := wireP.Stream(nil2ctx(), provider.Request{Messages: msgs})
	if err != nil {
		fmt.Println("Stream error:", err)
		os.Exit(1)
	}
	for c := range ch {
		if c.Type == provider.ChunkError {
			fmt.Println("stream chunk error:", c.Err)
		}
	}
	body := <-captured
	analyze("with model_overrides (real config)", body)

	// Path C: same wire capture but with the model_overrides stripped —
	// simulates a chain that ignored per-model vision overrides.
	noOv := *entry
	noOv.BaseURL = srv.URL
	noOv.ChatURL = srv.URL
	noOv.RequestURL = srv.URL
	noOv.ModelOverrides = nil
	noOvP, err := boot.NewProviderWithProxyAndModelInfo(&noOv, netclient.ProxySpec{Mode: netclient.ModeOff}, nil)
	if err != nil {
		fmt.Println("no-override provider error:", err)
		os.Exit(1)
	}
	ch2, err := noOvP.Stream(nil2ctx(), provider.Request{Messages: msgs})
	if err != nil {
		fmt.Println("Stream(2) error:", err)
		os.Exit(1)
	}
	for range ch2 {
	}
	body2 := <-captured
	analyze("without model_overrides (control)", body2)
}

func nil2ctx() context.Context { return context.Background() }

func analyze(label string, body []byte) {
	var req struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		fmt.Printf("[%s] body parse error: %v\n", label, err)
		return
	}
	fmt.Printf("[%s] model=%s messages=%d\n", label, req.Model, len(req.Messages))
	imageURLParts := strings.Count(string(body), `"image_url"`)
	fmt.Printf("[%s] image_url content parts on the wire: %d\n", label, imageURLParts)
	for i, m := range req.Messages {
		var parts []map[string]any
		if err := json.Unmarshal(m.Content, &parts); err == nil {
			kinds := make([]string, 0, len(parts))
			for _, p := range parts {
				switch {
				case p["image_url"] != nil:
					kinds = append(kinds, "image_url")
				case p["text"] != nil:
					kinds = append(kinds, "text")
				default:
					kinds = append(kinds, fmt.Sprintf("type=%v", p["type"]))
				}
			}
			fmt.Printf("  msg[%d] role=%s content=parts%v\n", i, m.Role, kinds)
		} else {
			s := string(m.Content)
			if len(s) > 80 {
				s = s[:80] + "…"
			}
			fmt.Printf("  msg[%d] role=%s content=%q\n", i, m.Role, s)
		}
	}
}
