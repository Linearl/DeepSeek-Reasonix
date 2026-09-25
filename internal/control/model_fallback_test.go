package control

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

type fallbackSwitchSink struct {
	mu     sync.Mutex
	events []event.Event
}

func (s *fallbackSwitchSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *fallbackSwitchSink) hasCode(code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.events {
		if e.Code == code {
			return true
		}
	}
	return false
}

// armFallback writes an isolated home config with the switch on and a target,
// mirroring config's own tests (REASONIX_HOME + SaveTo — Save() would leak a
// reasonix.toml into the package dir, task 225's finding).
func armFallback(t *testing.T, on bool, target string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.SetExperimentalFallbackModel(on); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if err := cfg.SetFallbackModel(target); err != nil {
		t.Fatalf("target: %v", err)
	}
	if err := cfg.SaveTo(filepath.Join(home, "config.toml")); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = os.Stdout
}

// Task 242: absorbQuotaForFallback must swap-and-recover ONLY on a qualifying
// quota error; every other outcome surfaces the original error unchanged
// (never a silent swallow), and a quota error while already on the fallback
// must NOT switch again — that is the no-loop guarantee.
func TestAbsorbQuotaForFallbackQualification(t *testing.T) {
	quotaErr := &provider.QuotaError{Status: 429, Code: "quota"}
	otherErr := errors.New("connection reset")

	c := &Controller{modelRef: "primary/model-a"}
	sink := &fallbackSwitchSink{}
	c.sink = sink
	switched := 0
	c.onFallbackSwitch = func(selfPath, target string) error {
		switched++
		if target != "backup/model-b" {
			return errors.New("unexpected target " + target)
		}
		return nil
	}

	// Off / unconfigured: quota surfaces untouched, no callback.
	if got := absorbQuotaForFallback(c, quotaErr); !errors.Is(got, quotaErr) {
		t.Fatalf("switch off must keep the quota error, got %v", got)
	}
	if switched != 0 {
		t.Fatalf("switch off called the callback %d times", switched)
	}

	// Not a quota error: untouched even with the switch armed.
	armFallback(t, true, "backup/model-b")
	if got := absorbQuotaForFallback(c, otherErr); !errors.Is(got, otherErr) {
		t.Fatalf("transient errors must keep their A4 lane, got %v", got)
	}
	if switched != 0 {
		t.Fatalf("non-quota error called the callback %d times", switched)
	}

	// Qualifying quota error: swapped, notice emitted, turn recovered.
	if got := absorbQuotaForFallback(c, quotaErr); got != nil {
		t.Fatalf("qualifying quota must be absorbed, got %v", got)
	}
	if switched != 1 {
		t.Fatalf("callback calls = %d, want 1", switched)
	}
	if !sink.hasCode(event.NoticeCodeFallbackModelSwitched) {
		t.Fatal("no fallback_model_switched notice — the swap was silent")
	}

	// Already on the fallback: quota surfaces again (stop condition, no loop).
	c.modelRef = "backup/model-b"
	if got := absorbQuotaForFallback(c, quotaErr); !errors.Is(got, quotaErr) {
		t.Fatalf("quota on the fallback must surface, got %v", got)
	}
	if switched != 1 {
		t.Fatalf("second switch attempt happened: calls = %d", switched)
	}

	// Switch callback fails: original error kept (nothing swallowed).
	c.modelRef = "primary/model-a"
	failing := &Controller{modelRef: "primary/model-a", sink: sink}
	failing.onFallbackSwitch = func(_, _ string) error { return errors.New("rebuild refused") }
	if got := absorbQuotaForFallback(failing, quotaErr); !errors.Is(got, quotaErr) {
		t.Fatalf("failed switch must surface the quota error, got %v", got)
	}
}

// The notice copy promises no auto-return: a manual switch back re-arms the
// fallback (next quota error switches again) — the initial-version recovery
// semantics (task 242 point 4).
func TestManualSwitchBackRearmsFallback(t *testing.T) {
	armFallback(t, true, "backup/model-b")
	quotaErr := &provider.QuotaError{Status: 429}
	switched := 0
	c := &Controller{modelRef: "primary/model-a"}
	c.onFallbackSwitch = func(_, _ string) error { switched++; return nil }
	if got := absorbQuotaForFallback(c, quotaErr); got != nil {
		t.Fatalf("first switch: %v", got)
	}
	// Simulate the user manually switching back: modelRef returns to primary.
	c.modelRef = "primary/model-a"
	if got := absorbQuotaForFallback(c, quotaErr); got != nil {
		t.Fatalf("re-armed switch: %v", got)
	}
	if switched != 2 {
		t.Fatalf("switches = %d, want 2 (re-armed after manual return)", switched)
	}
	if !strings.Contains(fallbackModelSwitchedNotice(), "manually") {
		t.Fatal("notice must say the return is manual")
	}
}
