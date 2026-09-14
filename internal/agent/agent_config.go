package agent

import "reasonix/internal/tool"

// agentConfig is everything New fixes for an Agent's lifetime; nothing writes
// it afterwards, which agent_config_test.go enforces. Embedded rather than
// nested so access stays flat, as perTurnState already does. Separating it lets
// the struct-state ratchet count reachable states instead of size: an immutable
// field combines with nothing. Anything that changes after New goes on Agent.
type agentConfig struct {
	// traceAsState enables the Trace-as-State compaction work (task 60): the
	// summary sees the assistant's reasoning, a stall is routed to re-reading on
	// short contexts, and self-directed folds carry guards. Off by default -- every
	// path it touches must behave exactly as before when it is off.
	traceAsState bool
	// restartUpdater publishes a staged build and relaunches the app (task 81).
	// Bound by the host; nil in a host that cannot swap its own install, which
	// is what makes the tool report itself unavailable rather than half-fail.
	restartUpdater tool.RestartUpdater
	maxSteps           int
	maxStepsKey        string
	reasoningByteLimit int
	maxOutputTokens    int
	temperature        float64
	usageSource        string
	modelRef           string
	highSpeedModels    []string
	// workspaceID is a prompt-cache lineage component, so it must not move
	// while an agent lives — a change would silently rekey the cache.
	workspaceID string
	// classifierTaskText is the host-trusted task text for delivery intent
	// classification, set by sub-agent spawners whose Run input carries host
	// framing. Empty means classify the raw input verbatim.
	classifierTaskText string
	// writeWorkspaceRoot scopes write reservations when writeScheduler is set.
	writeWorkspaceRoot string
	// subagentDepth caps delegation; at maxSubagentDepth the recursive
	// agent/skill tools are excluded.
	subagentDepth    int
	maxSubagentDepth int
	// autopilot marks an unattended run; see Options.Autopilot (task 56).
	autopilot bool
	// skipToolRecoveryFence lets writes proceed while an unresolved external
	// effect is pending. Unattended hosts (yolo/auto/autopilot) set this so the
	// run is not stranded on a recovery panel nobody can answer (task 107).
	skipToolRecoveryFence bool
	// contextWindow and compactRatio decide when at most one provider-visible
	// checkpoint is installed; recentKeep and archiveDir shape what it keeps.
	contextWindow          int
	compactRatio           float64
	recentKeep             int
	archiveDir             string
	legacyAnchorSafetyGate bool
	// readCoordinatorShadow fixes the internal read-coordinator rollout switch
	// for the whole run; see Options.ReadPipeline.
	readCoordinatorShadow bool
	// legacyImplicitFullReads restores the pre-intent read default for rollback.
	legacyImplicitFullReads bool
}

// ReadPipelineOptions carries the internal read-pipeline rollback switches. The
// new behavior is the default; each switch exists so an operator can fall back
// for diagnosis, is host-local, and is fixed for the whole run.
type ReadPipelineOptions struct {
	// LegacyCoordinator restores the legacy incomplete-read execution owner.
	LegacyCoordinator bool
	// LegacyEvidenceGates turns the writer-declared evidence check off.
	LegacyEvidenceGates bool
	// LegacyImplicitFullReads restores the old rule that a read with no window
	// promised the whole file. It exists for diagnosis and rollback only.
	LegacyImplicitFullReads bool
}
