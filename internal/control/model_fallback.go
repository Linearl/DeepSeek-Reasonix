package control

import (
	"fmt"
	"log/slog"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Task 242: absorbQuotaForFallback is the single decision point that swaps the
// session's model after the sampling retry loop exhausts on a quota-class
// error. It runs where every orchestrated turn's error surfaces, so goal and
// non-goal turns share one path.
//
// Qualification, all required:
//   - the error classifies as quota (transient connect/headers failures keep
//     their A4 retry lane and never reach here);
//   - the experimental switch is on with a configured target (Live reads the
//     config per call — settings apply without a restart);
//   - the session is not already on the fallback (modelRef check) — that is
//     the no-state anti-loop rule: a quota error AFTER the swap surfaces
//     instead of cycling, and a manual switch back to the primary re-arms it;
//   - the host injected a switch callback.
//
// On success it emits the visible in-session notice, logs the swap for
// telemetry, and returns nil so the caller treats the turn as recovered (the
// next sampling run already uses the fallback model). Any failure to switch
// returns the original error unchanged — never a silent swallow.
func absorbQuotaForFallback(c *Controller, err error) error {
	if err == nil {
		return nil
	}
	if provider.ClassifyRecovery(err).Phase != "quota" {
		return err
	}
	target := config.FallbackModelLive()
	if target == "" || c.onFallbackSwitch == nil {
		return err
	}
	if c.ModelRef() == target {
		// Already on the fallback: this is the stop condition, not another
		// switch. The error surfaces (verification failure, not a loop).
		return err
	}
	if switchErr := c.onFallbackSwitch(c.SessionPath(), target); switchErr != nil {
		slog.Warn("task242: fallback model switch failed",
			"target", target, "from", c.ModelRef(), "path", c.SessionPath(), "err", switchErr)
		return err
	}
	slog.Warn("task242: switched to fallback model after quota exhaustion",
		"target", target, "quota_err", err)
	if c.sink != nil {
		c.sink.Emit(event.Event{
			Kind:   event.Notice,
			Level:  event.LevelWarn,
			Code:   event.NoticeCodeFallbackModelSwitched,
			Text:   fallbackModelSwitchedNotice(),
			Detail: fmt.Sprintf("primary %q quota-exhausted; now using fallback %q (switch back manually in Settings — no auto-return)", c.ModelRef(), target),
		})
	}
	return nil
}

func fallbackModelSwitchedNotice() string {
	return "The primary model hit its quota limit; Reasonix switched this session to the fallback model. Switch back manually in Settings once the quota window resets — sessions do not auto-return."
}
