package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tl1393: the frontend degrade chain classifies running-turn refusals by these
// exact message forms (desktop/frontend/src/lib/controllerNotices.ts
// isTurnAlreadyRunningRejection / isNotActiveTurnRejection). The forms are
// load-bearing contract, not prose: a rewording silently breaks the takeover
// and the user's message bounces with the raw Go error again. This pin turns
// any drift (or a NEW unclassified rejection site) red on the Go side first;
// extend the FE predicates AND the FE mocks together when the forms change.

const tl1393NotActiveTurnFormat = `turn %q is not the active turn for tab %q`

func TestTL1393TurnRefusalFormsStayPinned(t *testing.T) {
	// ErrTurnRunning — the admission-raced form the 461-P6 degrade gate takes
	// over ("turn already running").
	controllerSrc, err := os.ReadFile(filepath.Join("..", "internal", "control", "controller.go"))
	if err != nil {
		t.Fatalf("read controller.go: %v", err)
	}
	if !strings.Contains(string(controllerSrc), `errors.New("turn already running")`) {
		t.Fatalf("internal/control/controller.go no longer defines ErrTurnRunning as %q; update the FE predicate isTurnAlreadyRunningRejection and the tl1393/tl1303 FE mocks together", "turn already running")
	}

	// The exact-turn fence form — exactly two sites per file today:
	//   turn_runtime_api.go: validatePromptIdentity, AnswerPromptForTab
	//   inbox_app.go:        EnqueueInboxSteerForTurn, SteerInboxItemForTurn
	// A new emitter of the same form must be checked against the FE predicate
	// (this count going up is the signal); a count going down means a
	// rewording — the FE takeover is broken until both move together.
	for _, name := range []string{"turn_runtime_api.go", "inbox_app.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if got := strings.Count(string(src), tl1393NotActiveTurnFormat); got != 2 {
			t.Fatalf("%s emits %q %d times, want exactly 2; any change here must move the FE predicate isNotActiveTurnRejection and the tl1393 FE mocks in the same commit", name, tl1393NotActiveTurnFormat, got)
		}
	}
}
