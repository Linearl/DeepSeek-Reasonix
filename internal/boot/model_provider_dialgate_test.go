package boot

// 任务 tl1327 接线测试：[network.dial_gate] 家族经 boot 构造路径下发进程拨号门。
// 全默认（零值家族）不下发——门的熔断状态跨 provider 重建保持；显式覆盖才换门。

import (
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/provider"
)

func TestApplyDialGateFromConfig(t *testing.T) {
	restore := func() { provider.ConfigureDialGate(provider.DialGateSettings{}) }
	restore()
	defer restore()

	// nil / 全默认：门保持默认构造（capacity 16、开启），状态不被换掉。
	applyDialGateFromConfig(nil)
	applyDialGateFromConfig(&config.Config{})
	if snap := provider.DialGateSnapshotNow(); snap.Disabled || snap.Capacity != provider.DefaultDialGateConcurrent {
		t.Fatalf("all-default config must not touch the gate: %+v", snap)
	}

	// 显式关闭：disabled = true 是完整回退路径。
	cfg := &config.Config{}
	cfg.Network.DialGate = config.DialGateConfig{Disabled: true}
	applyDialGateFromConfig(cfg)
	if snap := provider.DialGateSnapshotNow(); !snap.Disabled {
		t.Fatalf("disabled=true did not reach the gate: %+v", snap)
	}

	// 显式调参：预算换到 5。
	cfg2 := &config.Config{}
	cfg2.Network.DialGate = config.DialGateConfig{MaxConcurrentDials: 5}
	applyDialGateFromConfig(cfg2)
	if snap := provider.DialGateSnapshotNow(); snap.Disabled || snap.Capacity != 5 {
		t.Fatalf("max_concurrent_dials=5 did not reach the gate: %+v", snap)
	}
}
