package boot

import (
	"sync"

	"reasonix/internal/agent"
)

// Task 284: cross-session subscriptions — the process-wide service behind
// subscribe_session. One instance (and therefore ONE push loop) per process:
// agent tool registration runs per tab build, so a per-build construction
// would spawn a parallel loop per rebuild and each loop would push the same
// event — the first-publish-wins pattern matches PublishCollabDrainInboxGate
// above for exactly the same reason.
var (
	subscribeSvcOnce   sync.Once
	subscribeSvcShared *agent.SubscribeService
)

// SharedSubscribeService returns the process-wide subscription service. The
// FIRST boot's SessionCollabConfig wins for the process lifetime (identity
// and mailbox settings of a collab service do not vary per tab).
func SharedSubscribeService(cfg agent.SessionCollabConfig) *agent.SubscribeService {
	subscribeSvcOnce.Do(func() {
		subscribeSvcShared = agent.NewSubscribeService(cfg)
	})
	return subscribeSvcShared
}
