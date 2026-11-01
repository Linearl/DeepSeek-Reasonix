package responses

// 任务 tl1327 接线测试：responses 聊天主力链路的 http.Client 必须装上进程拨号门
// （新 TCP 连接计入进程预算、失败喂熔断）并调高 keep-alive 复用参数。

import (
	"context"
	"net/http"
	"testing"

	"reasonix/internal/netclient"
	"reasonix/internal/provider"
)

func TestNewInstallsDialGate(t *testing.T) {
	p := New(Config{
		Name:     "responses-gate-test",
		Protocol: "responses",
		BaseURL:  "http://127.0.0.1:1", // 不真正请求；只查 transport
		Model:    "test-model",
		Proxy:    netclient.ProxySpec{Mode: netclient.ModeOff},
	})
	c, ok := p.(*client)
	if !ok {
		t.Fatalf("New returned %T, want *client", p)
	}
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport %T is not *http.Transport", c.http.Transport)
	}
	// ③ keep-alive 复用：空闲连接保活从默认 2 提到 8。
	if tr.MaxIdleConnsPerHost != provider.ChatMaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConnsPerHost=%d, want %d (connection reuse tuning)", tr.MaxIdleConnsPerHost, provider.ChatMaxIdleConnsPerHost)
	}
	// ①② 门在拨号路径上：一次被拒拨号必须喂进进程熔断的证据计数。
	provider.ConfigureDialGate(provider.DialGateSettings{}) // 全局默认门
	defer provider.ConfigureDialGate(provider.DialGateSettings{})
	before := provider.DialGateSnapshotNow().ConsecutiveFails
	if _, derr := tr.DialContext(context.Background(), "tcp", "127.0.0.1:1"); derr == nil {
		t.Fatal("dial to closed port unexpectedly succeeded")
	}
	if after := provider.DialGateSnapshotNow().ConsecutiveFails; after != before+1 {
		t.Fatalf("dial did not flow through the gate: consecutive fails %d -> %d", before, after)
	}
}
