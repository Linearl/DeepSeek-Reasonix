package main

import "time"

// tl1830: reapStaleTopicActivityStatus shipped as an unwired backstop — its
// only caller was a test, so an orphaned thinking/streaming spinner (TurnDone
// lost while detached) never expired in production. This loop is the wiring.
//
// Scheduling choice — a standalone tick, NOT an event/refresh hook: the orphan
// case is by definition "no further events arrive", so a reap hooked into the
// event or refresh path would skip exactly the pass that matters most. This
// follows the detached-idle-release watchdog pattern (detached_idle_release.go)
// in both shape (goSafe + Ticker in startupStartBackgroundLoops) and cadence.
//
// Cost when idle: one activityAt map copy + one lock per tick; no broadcast
// unless a status was actually reaped (reapStaleTopicActivityStatus returns
// early otherwise). The 10-minute TTL itself is unchanged — the tick only adds
// detection latency (cleared within [TTL, TTL+tick]).
const topicActivityReapTick = 5 * time.Minute

// startTopicActivityReapLoop arms the TTL watchdog. Ungated on purpose: the
// user accepted the visible change (stale spinners now clear after ~10-15min
// instead of spinning forever), and the idle cost is negligible — a gate here
// would only re-add the forever-spinner defect it fixes.
func (a *App) startTopicActivityReapLoop() {
	a.goSafe("topicActivityReapLoop", func() {
		ticker := time.NewTicker(topicActivityReapTick)
		defer ticker.Stop()
		for now := range ticker.C {
			a.reapStaleTopicActivityStatus(now)
		}
	})
}
