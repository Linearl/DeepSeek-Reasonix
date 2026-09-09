package agent

import "testing"

// #26 A-c: a sleep is a stall, not work. The loop guard has to recognize it so
// the third sleep in a turn is blocked instead of hiding behind a new duration.
func TestSleepOnlyCommandRecognizesStalls(t *testing.T) {
	for _, command := range []string{"sleep 115", "sleep 110", "sleep 2.5", "sleep 90s", "sleep 5m"} {
		if !isSleepOnlyCommand(command) {
			t.Errorf("%q is a pure stall and must be recognized", command)
		}
	}
	for _, command := range []string{"sleep 2 && echo hi", "sleep", "sleepy 5", "echo sleep 5", "sleep abc"} {
		if isSleepOnlyCommand(command) {
			t.Errorf("%q is real work and must not be treated as a stall", command)
		}
	}
}
