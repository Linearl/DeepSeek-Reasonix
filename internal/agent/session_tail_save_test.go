package agent

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// TestLoadSessionTailMatchesFullLoadForSmallSessions is the no-regression half of
// the first-paint loader: below the size threshold it must behave exactly like
// LoadSession, including not claiming the transcript is truncated.
func TestLoadSessionTailMatchesFullLoadForSmallSessions(t *testing.T) {
	path := dagTestSession(t)
	_, _ = dagWideLog(t, path, 8)

	full, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := LoadSessionTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if tail.TailTruncated() {
		t.Fatal("a small session must not be marked as a first-paint tail")
	}
	if full.TailTruncated() {
		t.Fatal("LoadSession never returns a truncated transcript")
	}
	if strings.Join(dagContents(tail.Messages), ",") != strings.Join(dagContents(full.Messages), ",") {
		t.Fatalf("tail load differs from the full load for a small session")
	}
}

// TestUpgradeTruncatedTranscriptForWriteRestoresFullHistory is the data-safety
// property behind wiring the tail into hydrate: a first-paint Session that is
// about to be written must be replaced by the complete transcript first.
func TestUpgradeTruncatedTranscriptForWriteRestoresFullHistory(t *testing.T) {
	path := dagTestSession(t)
	_, _ = dagWideLog(t, path, 12)

	full, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	whole := dagContents(full.Messages)
	if len(whole) < 4 {
		t.Fatalf("fixture too small: %d messages", len(whole))
	}

	// Stand in for a Session that hydrate obtained from the trailing window.
	partial := &Session{}
	partial.Messages = append([]provider.Message(nil), full.Messages[len(whole)/2:]...)
	partial.tailTruncated = true
	if !partial.TailTruncated() {
		t.Fatal("fixture is not marked as truncated")
	}

	if err := partial.upgradeTruncatedTranscriptForWrite(path); err != nil {
		t.Fatal(err)
	}
	if partial.TailTruncated() {
		t.Fatal("the upgrade must clear the truncated flag")
	}
	if got := strings.Join(dagContents(partial.Messages), ","); got != strings.Join(whole, ",") {
		t.Fatalf("upgraded transcript is not the full one:\n got %s\nwant %s", got, strings.Join(whole, ","))
	}
	// Upgrading a complete Session is a no-op, so the guard costs nothing on the
	// ordinary path.
	if err := partial.upgradeTruncatedTranscriptForWrite(path); err != nil {
		t.Fatal(err)
	}
}

// TestSaveUpgradesTruncatedTranscriptBeforeWriting is the end-to-end form: a
// truncated Session must never reach disk, because a save is a full rewrite.
func TestSaveUpgradesTruncatedTranscriptBeforeWriting(t *testing.T) {
	path := dagTestSession(t)
	_, _ = dagWideLog(t, path, 12)

	full, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	whole := dagContents(full.Messages)

	truncated := &Session{}
	truncated.Messages = append([]provider.Message(nil), full.Messages[len(whole)/2:]...)
	truncated.tailTruncated = true

	if err := truncated.Save(path); err != nil {
		t.Fatalf("save of a truncated session: %v", err)
	}
	reloaded, err := LoadSession(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(dagContents(reloaded.Messages), ","); got != strings.Join(whole, ",") {
		t.Fatalf("saving a truncated session dropped history:\n got %s\nwant %s", got, strings.Join(whole, ","))
	}
}

// TestSessionReplayRefusalCacheIsKeyedOnLogState pins the guard: the refusal is
// remembered for the exact bytes that produced it and is dropped as soon as the
// log changes.
func TestSessionReplayRefusalCacheIsKeyedOnLogState(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	logPath := store.SessionEventLog(path)
	forgetSessionReplayRefusal(logPath)

	if _, ok := cachedSessionReplayRefusal(logPath); ok {
		t.Fatal("cache is not empty at the start of the test")
	}
	refusal := &SessionReplayLimitError{Path: path, Resource: "event_records", Value: 400_001, Limit: 400_000}
	rememberSessionReplayRefusal(logPath, refusal)
	cached, ok := cachedSessionReplayRefusal(logPath)
	if !ok {
		t.Fatal("a limit refusal must be remembered")
	}
	if !errors.Is(cached, ErrSessionReplayLimitExceeded) {
		t.Fatalf("cached error = %v", cached)
	}

	// A non-limit failure must not be cached: those deserve a retry.
	forgetSessionReplayRefusal(logPath)
	rememberSessionReplayRefusal(logPath, errors.New("torn tail"))
	if _, ok := cachedSessionReplayRefusal(logPath); ok {
		t.Fatal("an ordinary error must not be cached")
	}

	// Growing the log invalidates the entry without any explicit bookkeeping.
	rememberSessionReplayRefusal(logPath, refusal)
	if _, ok := cachedSessionReplayRefusal(logPath); !ok {
		t.Fatal("refusal disappeared after re-remembering")
	}
	appendSessionDAGEntriesForTest(t, logPath)
	if _, ok := cachedSessionReplayRefusal(logPath); ok {
		t.Fatal("a changed log must be retried, not served from the refusal cache")
	}
}

// TestLoadSessionTranscriptServesRefusalsWithoutReplaying proves the guard is on
// the load path, not just available as a helper.
func TestLoadSessionTranscriptServesRefusalsWithoutReplaying(t *testing.T) {
	path := dagTestSession(t)
	dagLinearLog(t, path)
	logPath := store.SessionEventLog(path)
	forgetSessionReplayRefusal(logPath)
	t.Cleanup(func() { forgetSessionReplayRefusal(logPath) })

	if _, err := loadSessionTranscript(context.Background(), path, defaultSessionReplayLimits, nil); err != nil {
		t.Fatalf("baseline load: %v", err)
	}
	refusal := &SessionReplayLimitError{Path: path, Resource: "event_records", Value: 400_001, Limit: 400_000}
	rememberSessionReplayRefusal(logPath, refusal)

	_, err := loadSessionTranscript(context.Background(), path, defaultSessionReplayLimits, nil)
	if !errors.Is(err, ErrSessionReplayLimitExceeded) {
		t.Fatalf("load did not serve the cached refusal: %v", err)
	}
}

func appendSessionDAGEntriesForTest(t *testing.T, logPath string) {
	t.Helper()
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n"); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
