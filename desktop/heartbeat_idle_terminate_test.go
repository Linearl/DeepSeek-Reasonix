package main

import "testing"

// Task 244 B1 (experimental_autonomous_idle_terminate): three consecutive runs
// whose conversation never grew real history disable the task. The switch is
// read at call time; off it must be a hard no-op (zero regression).

func TestHeartbeatIdleStreakPure(t *testing.T) {
	// An idle run extends the streak; any productive run resets it.
	if got := heartbeatIdleStreakNext(2, true); got != 3 {
		t.Fatalf("idle extends: got %d, want 3", got)
	}
	if got := heartbeatIdleStreakNext(2, false); got != 0 {
		t.Fatalf("productive run resets: got %d, want 0", got)
	}
}

func TestEvaluateIdleStreakOffIsNoop(t *testing.T) {
	e := &HeartbeatEngine{idleTerminate: func() bool { return false }}
	task := HeartbeatTask{ID: "t1", Title: "probe", Enabled: true, IdleStreak: 2}
	out := e.evaluateIdleStreak(task, true)
	if out.IdleStreak != 2 {
		t.Fatalf("off state must not advance the streak, got %d", out.IdleStreak)
	}
	if !out.Enabled {
		t.Fatal("off state must never disable a task")
	}
	if len(out.RunHistory) != 0 {
		t.Fatal("off state must not backfill run history")
	}
	// Nil switch is equally off (engine without injection, e.g. tests).
	naked := &HeartbeatEngine{}
	if got := naked.evaluateIdleStreak(task, true); got.IdleStreak != 2 || !got.Enabled {
		t.Fatalf("nil switch must be a no-op, got streak=%d enabled=%v", got.IdleStreak, got.Enabled)
	}
}

func TestEvaluateIdleStreakOnDisablesAfterStrikes(t *testing.T) {
	e := &HeartbeatEngine{idleTerminate: func() bool { return true }}
	task := HeartbeatTask{ID: "t1", Title: "burner", Enabled: true}

	// Two strikes: still enabled, streak visible.
	task.IdleStreak = heartbeatIdleStreakNext(task.IdleStreak, true)
	task = e.evaluateIdleStreak(task, true)
	if task.IdleStreak != 2 || !task.Enabled {
		t.Fatalf("after 2 strikes: streak=%d enabled=%v, want 2/true", task.IdleStreak, task.Enabled)
	}
	// Third strike: disabled with the streak recorded.
	task = e.evaluateIdleStreak(task, true)
	if task.IdleStreak != heartbeatIdleTerminateStrikes {
		t.Fatalf("streak=%d, want %d", task.IdleStreak, heartbeatIdleTerminateStrikes)
	}
	if task.Enabled {
		t.Fatal("third consecutive idle run must disable the task (burn guard)")
	}
	// A productive run resets before any disable could fire again.
	reset := HeartbeatTask{ID: "t2", Enabled: true, IdleStreak: 2}
	reset = e.evaluateIdleStreak(reset, false)
	if reset.IdleStreak != 0 || !reset.Enabled {
		t.Fatalf("productive run must reset: streak=%d enabled=%v", reset.IdleStreak, reset.Enabled)
	}
}

func TestEvaluateIdleStreakBackfillsRunHistory(t *testing.T) {
	e := &HeartbeatEngine{idleTerminate: func() bool { return true }}
	task := HeartbeatTask{ID: "t1", Enabled: true,
		RunHistory: []HeartbeatRun{{At: 1, TopicID: "topic-x"}}}
	task = e.evaluateIdleStreak(task, true)
	if len(task.RunHistory) != 1 || !task.RunHistory[0].Idle {
		t.Fatalf("last run must be backfilled idle=true, got %+v", task.RunHistory)
	}
}
