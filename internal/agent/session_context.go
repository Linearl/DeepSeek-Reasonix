package agent

import (
	"context"
	"slices"

	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/sessioncontext"
)

// TurnContextBundle carries role-specific runtime snapshots without changing
// Runner.Run. BootstrapOnly lets synthetic continuations repair an upgraded
// legacy session that has no snapshot, while never consuming later updates.
type TurnContextBundle struct {
	Executor      sessioncontext.Snapshot
	Planner       sessioncontext.Snapshot
	BootstrapOnly bool
}

type turnContextBundleKey struct{}
type turnContextRoleKey struct{}

type turnContextRole uint8

const (
	turnContextExecutor turnContextRole = iota
	turnContextPlanner
)

type turnContextDiagnostics struct {
	snapshot sessioncontext.Snapshot
	stats    sessioncontext.Diagnostics
	target   string
	reasons  []string
}

// WithTurnContextBundle attaches provider-visible runtime context to one host
// turn. Empty bundles preserve the old context identity and request fast path.
func WithTurnContextBundle(ctx context.Context, bundle TurnContextBundle) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if bundle.Executor.Content == "" && bundle.Planner.Content == "" {
		return ctx
	}
	return context.WithValue(ctx, turnContextBundleKey{}, bundle)
}

// WithoutTurnContextBundle prevents a child agent from inheriting the parent's
// provider-visible runtime snapshot through context.Context. Child sessions
// receive only the explicit task/workspace context assembled by their caller;
// fork/continue transcript inheritance remains an explicit session operation.
func WithoutTurnContextBundle(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, turnContextBundleKey{}, struct{}{})
}

func (a *Agent) prepareProviderTurn(ctx context.Context, input string) string {
	return a.withTurnPreferences(input)
}

func withPlannerTurnContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, turnContextRoleKey{}, turnContextPlanner)
}

func turnContextFromContext(ctx context.Context) (sessioncontext.Snapshot, bool, turnContextRole) {
	if ctx == nil {
		return sessioncontext.Snapshot{}, false, turnContextExecutor
	}
	bundle, ok := ctx.Value(turnContextBundleKey{}).(TurnContextBundle)
	if !ok {
		return sessioncontext.Snapshot{}, false, turnContextExecutor
	}
	role, _ := ctx.Value(turnContextRoleKey{}).(turnContextRole)
	snapshot := bundle.Executor
	if role == turnContextPlanner {
		snapshot = bundle.Planner
	}
	if snapshot.Content == "" {
		return sessioncontext.Snapshot{}, false, role
	}
	parsed, valid := sessioncontext.Parse(snapshot.Content)
	if !valid || parsed.Digest != snapshot.Digest {
		return sessioncontext.Snapshot{}, false, role
	}
	return parsed, bundle.BootstrapOnly, role
}

// AppendTurnContext appends the role-appropriate snapshot exactly once per
// digest. It derives state from model-visible history so resume, rewind, fork,
// and compaction need no persisted sidecar field.
func (a *Agent) AppendTurnContext(ctx context.Context) bool {
	return a.appendTurnContextAndMessages(ctx)
}

// AppendTurnContextAndUser appends a role-appropriate snapshot and the real
// user message in one Session.AddBatch. This keeps mid-turn autosave from
// persisting a context-only admission boundary.
func (a *Agent) AppendTurnContextAndUser(ctx context.Context, user provider.Message) bool {
	return a.appendTurnContextAndMessages(ctx, user)
}

func (a *Agent) appendTurnContextAndMessages(ctx context.Context, messages ...provider.Message) bool {
	if a == nil {
		return false
	}
	sess := a.sess.session()
	if sess == nil {
		return false
	}
	contextMessage, appendContext := a.prepareTurnContext(ctx)
	if !appendContext && len(messages) == 0 {
		return false
	}
	batch := make([]provider.Message, 0, len(messages)+1)
	if appendContext {
		batch = append(batch, contextMessage)
	}
	batch = append(batch, messages...)
	sess.AddBatch(batch...)
	return appendContext
}

func (a *Agent) prepareTurnContext(ctx context.Context) (provider.Message, bool) {
	if a == nil {
		return provider.Message{}, false
	}
	sess := a.sess.session()
	if sess == nil {
		return provider.Message{}, false
	}
	snapshot, bootstrapOnly, role := turnContextFromContext(ctx)
	if snapshot.Content == "" {
		return provider.Message{}, false
	}
	visible := a.modelVisibleMessages()
	// Task 969: gate the append on the merged per-section view instead of the
	// newest envelope alone. Section deltas mean the newest snapshot may carry
	// only part of the runtime state; unchanged sections must not be re-sent or
	// the provider prefix cache pays for the standing catalog block again.
	merged, found := mergedTurnContextSections(visible)
	a.turn.sessionContext = turnContextDiagnostics{
		snapshot: snapshot,
		stats:    sessioncontext.SectionDiagnostics(snapshot),
		target:   role.String(),
	}
	if found {
		if bootstrapOnly {
			return provider.Message{}, false
		}
		delta := sessioncontext.BuildDelta(snapshot.Sections, merged)
		if delta.Content == "" {
			// Nothing changed since the merged earlier state: reuse the same
			// block and keep the provider prefix cache warm.
			return provider.Message{}, false
		}
		a.turn.sessionContext.snapshot = delta
		a.turn.sessionContext.reasons = changedTurnContextReasons(merged, snapshot.Sections)
		return HostGeneratedUserMessage(delta.Content), true
	}
	reason := "first_seen"
	if bootstrapOnly || hasPriorConversation(visible) {
		reason = "legacy_resume"
	}
	a.turn.sessionContext.reasons = []string{reason}
	return HostGeneratedUserMessage(snapshot.Content), true
}

// mergedTurnContextSections resolves the effective runtime sections from the
// newest snapshot backwards. Delta envelopes only supersede the sections they
// carry; a full snapshot terminates the walk because it supersedes every
// earlier snapshot, including sections it omits.
func mergedTurnContextSections(messages []provider.Message) (sessioncontext.Sections, bool) {
	var merged sessioncontext.Sections
	found := false
	for i := range slices.Backward(messages) {
		message := messages[i]
		if message.Role != provider.RoleUser || message.Origin != provider.MessageOriginHost {
			continue
		}
		snapshot, ok := sessioncontext.Parse(message.Content)
		if !ok {
			continue
		}
		found = true
		fillMissingSections(&merged, snapshot.Sections)
		if !snapshot.Delta {
			// A full snapshot resolves every remaining section (omission = empty)
			// and supersedes everything older.
			return merged, true
		}
	}
	return merged, found
}

// fillMissingSections copies each non-empty section value into dst where dst is
// still empty.
func fillMissingSections(dst *sessioncontext.Sections, src sessioncontext.Sections) {
	if src.Environment != "" && dst.Environment == "" {
		dst.Environment = src.Environment
	}
	if src.Workspace != "" && dst.Workspace == "" {
		dst.Workspace = src.Workspace
	}
	if src.SkillsCatalog != "" && dst.SkillsCatalog == "" {
		dst.SkillsCatalog = src.SkillsCatalog
	}
	if src.CapabilityCatalog != "" && dst.CapabilityCatalog == "" {
		dst.CapabilityCatalog = src.CapabilityCatalog
	}
	if src.BackgroundMemory != "" && dst.BackgroundMemory == "" {
		dst.BackgroundMemory = src.BackgroundMemory
	}
}

// sessionContextCarrierIndexes returns the message indexes that must survive a
// compaction cut: the newest snapshot carrying each section, plus the full
// snapshot that terminates the merge when one exists. Keeping only the newest
// envelope would drop sections that a delta relies on from older snapshots.
func sessionContextCarrierIndexes(messages []provider.Message) []int {
	type pending struct {
		index    int
		sections sessioncontext.Sections
		full     bool
	}
	var walk []pending
	var covered sessioncontext.Sections
	for i := range slices.Backward(messages) {
		message := messages[i]
		if message.Role != provider.RoleUser || message.Origin != provider.MessageOriginHost {
			continue
		}
		snapshot, ok := sessioncontext.Parse(message.Content)
		if !ok {
			continue
		}
		section := snapshot.Sections
		if !snapshot.Delta {
			// The full snapshot resolves the rest; only carry it.
			carriers := []int{i}
			for j := len(walk) - 1; j >= 0; j-- {
				carriers = append(carriers, walk[j].index)
			}
			slices.Reverse(carriers)
			return carriers
		}
		walk = append(walk, pending{index: i, sections: section})
		if section.Environment != "" {
			covered.Environment = section.Environment
		}
		if section.Workspace != "" {
			covered.Workspace = section.Workspace
		}
		if section.SkillsCatalog != "" {
			covered.SkillsCatalog = section.SkillsCatalog
		}
		if section.CapabilityCatalog != "" {
			covered.CapabilityCatalog = section.CapabilityCatalog
		}
		if section.BackgroundMemory != "" {
			covered.BackgroundMemory = section.BackgroundMemory
		}
		if covered.Environment != "" && covered.Workspace != "" &&
			covered.SkillsCatalog != "" && covered.CapabilityCatalog != "" &&
			covered.BackgroundMemory != "" {
			break
		}
	}
	if len(walk) == 0 {
		return nil
	}
	carriers := make([]int, 0, len(walk))
	for i := len(walk) - 1; i >= 0; i-- {
		carriers = append(carriers, walk[i].index)
	}
	return carriers
}

func latestTurnContextSnapshot(messages []provider.Message) (sessioncontext.Snapshot, bool) {
	for i := range slices.Backward(messages) {
		message := messages[i]
		if message.Role != provider.RoleUser || message.Origin != provider.MessageOriginHost {
			continue
		}
		if snapshot, ok := sessioncontext.Parse(message.Content); ok {
			return snapshot, true
		}
	}
	return sessioncontext.Snapshot{}, false
}

func isSessionContextMessage(message provider.Message) bool {
	if message.Role != provider.RoleUser || message.Origin != provider.MessageOriginHost {
		return false
	}
	return sessioncontext.IsContent(message.Content)
}

func (r turnContextRole) String() string {
	if r == turnContextPlanner {
		return "planner"
	}
	return "executor"
}

func hasPriorConversation(messages []provider.Message) bool {
	for _, message := range messages {
		if message.Role != provider.RoleSystem && !message.LocalOnly {
			return true
		}
	}
	return false
}

// changedTurnContextReasons attributes an append to the sections that differ
// from the merged earlier state (task 969 分节 digest).
func changedTurnContextReasons(previous, current sessioncontext.Sections) []string {
	var reasons []string
	if previous.Environment != current.Environment || previous.Workspace != current.Workspace {
		reasons = append(reasons, "runtime_changed")
	}
	if previous.BackgroundMemory != current.BackgroundMemory {
		reasons = append(reasons, "memory_changed")
	}
	if previous.SkillsCatalog != current.SkillsCatalog {
		reasons = append(reasons, "skills_changed")
	}
	if previous.CapabilityCatalog != current.CapabilityCatalog {
		reasons = append(reasons, "catalog_changed")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "snapshot_changed")
	}
	return reasons
}

func (a *Agent) attachSessionContextDiagnostics(diagnostics *CacheDiagnostics) {
	if a == nil || diagnostics == nil || a.turn.sessionContext.snapshot.Content == "" {
		return
	}
	diagnostics.SessionContext = eventSessionContextDiagnostics(a.turn.sessionContext)
}

func eventSessionContextDiagnostics(observed turnContextDiagnostics) *event.SessionContextDiagnostics {
	if observed.snapshot.Content == "" {
		return nil
	}
	section := func(stat sessioncontext.SectionStat) event.SessionContextSectionDiagnostics {
		return event.SessionContextSectionDiagnostics{Digest: stat.Digest, Chars: stat.Chars}
	}
	return &event.SessionContextDiagnostics{
		Version: observed.snapshot.Version, Digest: observed.snapshot.Digest,
		TargetRole: observed.target, Reasons: append([]string(nil), observed.reasons...),
		Environment: section(observed.stats.Environment), Workspace: section(observed.stats.Workspace),
		BackgroundMemory: section(observed.stats.BackgroundMemory), SkillsCatalog: section(observed.stats.SkillsCatalog),
		CapabilityCatalog: section(observed.stats.CapabilityCatalog),
	}
}

func captureTurnContextShape(system string, schemas []provider.ToolSchema, rewriteVersion int, messages []provider.Message) PrefixShape {
	shape := CaptureShape(system, schemas, rewriteVersion)
	// Task 969: attribute on the merged per-section state, not the newest
	// envelope — a section-delta digest alone would misreport the effective
	// runtime context whenever the delta only refreshed one section.
	if merged, ok := mergedTurnContextSections(messages); ok {
		digest := sessioncontext.Build(merged).Digest
		shape.SessionContextDigest = digest
		shape.PrefixHash = shortHash(map[string]string{
			"stable": shape.PrefixHash, "session_context": digest,
		})
	}
	return shape
}
