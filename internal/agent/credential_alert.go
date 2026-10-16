package agent

// 任务912 P1-d: an expired/invalid provider key is the one provider failure
// the user, not the agent, has to fix. Before this it surfaced only as a
// provider error inside a turn (and, for compaction, as a summary failure) —
// easy to miss, so a session silently burned 1.8M-token folds into a key that
// could never authenticate. The alert is global (one banner per credential,
// session-independent), throttled so a retrying loop cannot spam, and carries
// the provider's own actionable message in Detail — which never contains key
// material (provider.AuthError deliberately keeps Body out of Error()).

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/i18n"
	"reasonix/internal/provider"
)

// credentialAlertThrottle is how long one credential's banner stays up before
// the same rejection may announce itself again.
const credentialAlertThrottle = 10 * time.Minute

// credentialAlertWatch is the per-credential throttle. Belongs to the provider
// key rather than to any conversation, so sessionRuntime.reset leaves it alone.
type credentialAlertWatch struct {
	mu  sync.Mutex
	at  time.Time
	key string
}

// alertCredentialExpired raises the global banner for an HTTP 401. Anything
// that is not the key-rejection shape is a no-op, so call sites can pass every
// failure they see without classifying first.
func (a *Agent) alertCredentialExpired(err error) {
	if a == nil || a.svc.sink == nil || err == nil {
		return
	}
	if !summaryAuthExpired(err) {
		return
	}
	key := "401"
	var auth *provider.AuthError
	if errors.As(err, &auth) {
		key = provider.ProviderDisplayLabel(auth.Provider, auth.ProviderDisplayName, auth.Protocol) + ":401"
	}
	now := time.Now()
	a.sess.authAlert.mu.Lock()
	if !a.sess.authAlert.at.IsZero() &&
		a.sess.authAlert.key == key && now.Sub(a.sess.authAlert.at) < credentialAlertThrottle {
		a.sess.authAlert.mu.Unlock()
		return
	}
	a.sess.authAlert.at = now
	a.sess.authAlert.key = key
	a.sess.authAlert.mu.Unlock()

	slog.Error("agent: provider key rejected — user action required",
		"credential", key, "err", err)
	a.svc.sink.Emit(event.Event{
		Kind:   event.Notice,
		Level:  event.LevelWarn,
		Code:   event.NoticeCodeCredentialExpired,
		Text:   i18n.M.CredentialExpiredAlert,
		Detail: err.Error(),
	})
}
