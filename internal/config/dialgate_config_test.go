package config

import "testing"

func TestNetworkDialGateResolution(t *testing.T) {
	// 默认（零值）：门开、全默认参数——disabled=false 是缺省，不是显式关闭。
	var c Config
	d := c.NetworkDialGate()
	if d.Disabled || d.MaxConcurrent != 0 || d.FailureThreshold != 0 || d.Cooldown != 0 {
		t.Fatalf("zero config must resolve to all-defaults, got %+v", d)
	}
	// nil config（旧装配器）同口径。
	var nilCfg *Config
	if d := nilCfg.NetworkDialGate(); d.Disabled {
		t.Fatal("nil config must resolve to all-defaults")
	}

	c.Network.DialGate = DialGateConfig{Disabled: true}
	if !c.NetworkDialGate().Disabled {
		t.Fatal("disabled=true must resolve through")
	}

	if err := c.SetNetworkDialGate(DialGateConfig{MaxConcurrentDials: 8, FailureThreshold: 5, CooldownSeconds: 30}); err != nil {
		t.Fatalf("SetNetworkDialGate: %v", err)
	}
	d = c.NetworkDialGate()
	if d.Disabled || d.MaxConcurrent != 8 || d.FailureThreshold != 5 || d.Cooldown == 0 {
		t.Fatalf("knobs not resolved: %+v", d)
	}

	// 越界拒绝（防呆：错误配置不能解除预算也不能饿死正常流量）。
	for _, bad := range []DialGateConfig{
		{MaxConcurrentDials: -1},
		{MaxConcurrentDials: 999},
		{FailureThreshold: -3},
		{FailureThreshold: 99},
		{CooldownSeconds: -1},
		{CooldownSeconds: 601},
	} {
		if err := c.SetNetworkDialGate(bad); err == nil {
			t.Fatalf("out-of-range %+v accepted", bad)
		}
	}
}
