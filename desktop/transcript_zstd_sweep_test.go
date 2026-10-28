package main

import (
	"testing"

	"reasonix/internal/config"
)

// tl 1174 验收④（开关门纯函数，921 TestEventsIdleSweepFromConfig 同款）：
// 开关关（默认/nil/err）sweep 零行为；开=允许冷压缩。每 tick 现读，翻转免重启。
func TestTranscriptZstdSweepFromConfig(t *testing.T) {
	if got := transcriptZstdSweepFromConfig(nil); got.enabled {
		t.Fatalf("nil config must keep the sweep off: %+v", got)
	}
	off := &config.Config{}
	if got := transcriptZstdSweepFromConfig(off); got.enabled {
		t.Fatalf("default (off) must keep the sweep off: %+v", got)
	}
	on := &config.Config{}
	on.Desktop.ExperimentalJsonlZstd = true
	if got := transcriptZstdSweepFromConfig(on); !got.enabled {
		t.Fatalf("experimental_jsonl_zstd on must enable the sweep: %+v", got)
	}
}
