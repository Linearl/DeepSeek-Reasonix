package control

import (
	"testing"
	"time"

	"reasonix/internal/agent"
)

// tl1500 刀③ seam 钉（与实现同 commit；行为钉在 in_flight_turn_stale_age_test.go）：
// 超龄判定函数的边界——nil marker、零 StartedAt（老 sidecar，年龄未知）都不判
// 超龄，保持既有行为；新鲜标记不判超龄；3 小时旧标记判超龄。
func TestStaleInFlightMarkerNilAndZeroStartedAtNotStale(t *testing.T) {
	if staleInFlightTurnMarkerStale(nil) {
		t.Fatal("nil marker must not read as stale")
	}
	if staleInFlightTurnMarkerStale(&agent.InFlightTurnMeta{ID: "legacy"}) {
		t.Fatal("zero StartedAt (legacy sidecar) must not read as stale — unknown age keeps existing behaviour")
	}
	if !staleInFlightTurnMarkerStale(&agent.InFlightTurnMeta{ID: "old", StartedAt: time.Now().Add(-3 * time.Hour)}) {
		t.Fatal("a 3h-old marker must read as stale")
	}
	if staleInFlightTurnMarkerStale(&agent.InFlightTurnMeta{ID: "fresh", StartedAt: time.Now().Add(-time.Minute)}) {
		t.Fatal("a fresh marker must not read as stale")
	}
}

// 编译期防漂移：cause 常量必须保持行为钉与 notice 所依赖的字面量。
func TestStaleInFlightTurnCauseConstant(t *testing.T) {
	if staleInFlightTurnRecoveredCause != "stale_in_flight_turn_recovered" {
		t.Fatalf("cause constant drifted: %q", staleInFlightTurnRecoveredCause)
	}
}
