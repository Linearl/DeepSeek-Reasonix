package provider

// 任务 tl1327 ③④ 连接复用基线实测。同一负载（10 顺序请求 + 2 波×8 并发突发）
// 下对比三种 transport 形态的新建 TCP 连接数（本地 httptest 服务，计数在门内侧
// 的真实拨号）：
//
//	default — tl1327 之前：net/http 默认池（MaxIdleConnsPerHost=2）
//	tuned   — tl1327 ③：ChatMaxIdleConnsPerHost=8
//	gated   — tl1327 ①②③ 完整形态：tuned + 进程拨号门
//
// 读数进交付报告（新建/复用比）。断言只钉不抖的性质：tuned 严格优于 default、
// 顺序请求恒复用单连接、突发段调优后近似全复用、gated 峰值不破进程预算。

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"reasonix/internal/netclient"
)

func buildBaselineClient(t *testing.T, name string, gate bool) (*http.Client, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	tr, err := netclient.NewTransport(netclient.ProxySpec{Mode: netclient.ModeOff}, netclient.TransportOptions{
		DialTimeout: 5 * time.Second, KeepAlive: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("%s: transport: %v", name, err)
	}
	dials := &atomic.Int64{}
	peak := &atomic.Int64{}
	inner := tr.DialContext
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		cur := dials.Add(1)
		for {
			p := peak.Load()
			if cur <= p || peak.CompareAndSwap(p, cur) {
				break
			}
		}
		defer peak.Add(-1)
		return inner(ctx, network, addr)
	}
	client := &http.Client{Transport: tr}
	if gate {
		GateHTTPClient(client)
	}
	return client, dials, peak
}

func TestConnectionReuseBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()

	measure := func(name string, gate bool) (res shapeResult) {
		client, dials, _ := buildBaselineClient(t, name, gate)
		do := func() {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s request: %v", name, err)
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		res.name = name
		mark := dials.Load()
		for i := 0; i < 10; i++ { // 顺序段：keep-alive 复用下界
			do()
		}
		time.Sleep(20 * time.Millisecond)
		res.sequential = dials.Load() - mark
		mark = dials.Load()
		for burst := 0; burst < 2; burst++ { // 突发段×2：并发 8 > 默认池保活位 2
			var wg sync.WaitGroup
			for i := 0; i < 8; i++ {
				wg.Add(1)
				go func() { defer wg.Done(); do() }()
			}
			wg.Wait()
			time.Sleep(20 * time.Millisecond)
		}
		res.bursts = dials.Load() - mark
		res.dials = dials.Load()
		res.requests = 26
		return res
	}

	def := measure("default(MaxIdleConnsPerHost=2,tl1327前)", false)
	tun := measure("tuned(ChatMaxIdleConnsPerHost=8)", false)
	gat := measure("gated(tuned+拨号门)", true)
	for _, r := range []shapeResult{def, tun, gat} {
		t.Logf("基线[%s]: 新建连接=%d/请求=%d (新建率=%.0f%%) | 顺序段新建=%d 突发段新建=%d",
			r.name, r.dials, r.requests, 100*float64(r.dials)/float64(r.requests), r.sequential, r.bursts)
	}

	if tun.dials >= def.dials {
		t.Fatalf("tuned pool must dial strictly less than default: %d vs %d", tun.dials, def.dials)
	}
	if tun.sequential != 1 || gat.sequential != 1 || def.sequential != 1 {
		t.Fatalf("sequential keep-alive must reuse one connection: default=%d tuned=%d gated=%d", def.sequential, tun.sequential, gat.sequential)
	}
	if tun.bursts > 9 {
		t.Fatalf("tuned burst dials %d, want <=9 (second burst should reuse)", tun.bursts)
	}
	if gat.bursts > 9 {
		t.Fatalf("gated burst dials %d, want <=9 (gate must not break reuse)", gat.bursts)
	}
	// gated 完整形态下拨号峰值不破进程预算（本测试用默认门 16）。
	if snap := DialGateSnapshotNow(); snap.PeakInFlight > DefaultDialGateConcurrent {
		t.Fatalf("gated peak in-flight %d exceed budget %d", snap.PeakInFlight, DefaultDialGateConcurrent)
	}
}

type shapeResult = baselineShape // 保持表格小类型名简短

type baselineShape struct {
	name       string
	dials      int64
	requests   int
	sequential int64
	bursts     int64
}
