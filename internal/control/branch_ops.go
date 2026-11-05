package control

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// Fork branches the conversation at the start of turn into a NEW session file,
// preserving the current one as the branch point, and switches to the branch. Code
// is untouched (it's a conversation operation). Like a conversation rewind it needs
// the live boundary, so it is unavailable for resumed-session turns and refused
// while a turn runs. Returns the new session path.
func (c *Controller) Fork(turn int) (string, error) {
	return c.ForkNamed(turn, "")
}

func (c *Controller) ForkNamed(turn int, name string) (string, error) {
	return c.forkNamed(turn, name, true)
}

// ForkSession copies the conversation at the start of turn into a new session
// file without switching this controller to it. Desktop uses this to open the
// branch in a new tab while the source tab keeps its current transcript.
func (c *Controller) ForkSession(turn int, name string) (string, error) {
	return c.forkNamed(turn, name, false)
}

func (c *Controller) forkNamed(turn int, name string, switchToFork bool) (string, error) {
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return "", c.rewindFail(fmt.Errorf("cannot fork while a turn is running"))
		}
		return "", c.rewindFail(err)
	}
	defer c.endRotation()
	return c.forkNamedReady(turn, name, switchToFork, agent.HeadKindFork)
}

// forkNamedReady forks at turn's boundary: a schema-2 session that switches
// gets a new head in its own log (head id returned); every other case still
// creates a new session file (path returned).
func (c *Controller) forkNamedReady(turn int, name string, switchToFork bool, kind string) (string, error) {
	if c.executor == nil {
		return "", c.rewindFail(fmt.Errorf("checkpoints unavailable"))
	}
	if c.sessionDir == "" {
		return "", c.rewindFail(fmt.Errorf("fork needs session persistence, which is disabled"))
	}
	boundary, hasBound := c.checkpoints.boundary(turn)
	if !hasBound {
		return "", c.rewindFail(fmt.Errorf("fork unavailable for turn %d (resumed session)", turn))
	}
	// tl1280: in-log head forks left with the write-path slow lane; a
	// conversation fork always creates a new session file now. switchToFork is
	// kept in the signature so the rewind call sites stay unchanged.
	_ = switchToFork

	// Persist the current conversation first so the branch point survives, then
	// seed a fresh session with the messages up to the fork and switch to it.
	if err := c.SnapshotDurable(); err != nil {
		slog.Warn("controller: pre-fork snapshot", "err", err)
	}
	parentPath := c.SessionPath()
	parentID := agent.BranchID(parentPath)
	src := c.executor.Session().Snapshot()
	if boundary > len(src) {
		boundary = len(src)
	}
	forked := append([]provider.Message(nil), src[:boundary]...)
	sess := c.seedBranchedSession(forked, parentPath)

	newPath := agent.NewSessionPath(c.sessionDir, c.activeLabel())
	if err := sess.SaveIfAbsent(newPath); err != nil {
		return "", c.rewindFail(err)
	}
	if _, err := sess.CopyValidContextProjection(parentPath, newPath); err != nil {
		slog.Warn("controller: fork did not inherit context projection", "err", err)
	}
	forkPreview, forkTurns := agent.SessionPreviewFromMessages(forked)
	if err := agent.SaveBranchMeta(newPath, agent.BranchMeta{
		Name:             strings.TrimSpace(name),
		ParentID:         parentID,
		ForkTurn:         turn,
		ForkMessageIndex: boundary,
		Preview:          forkPreview,
		Turns:            forkTurns,
		SchemaVersion:    agent.BranchMetaCountsVersion,
	}); err != nil {
		return "", c.rewindFail(err)
	}
	if switchToFork {
		commitTransition, err := c.prepareSessionTransition(newPath, "fork", sess)
		if err != nil {
			return "", c.rewindFail(fmt.Errorf("bind fork session: %w", err))
		}
		// See snapshotMu: the swap must not interleave with an in-flight save.
		c.snapshotMu.Lock()
		commitTransition.publish()
		// Load the child sidecar when the covered prefix survived the fork. The
		// loader rebinds its lineage key without touching the parent's sidecar.
		c.bindExecutorProjection(newPath, true)
		c.ResetPlannerSession()
		c.rebindCheckpoints(newPath)
		// A historical fork rewinds before later failures, so it starts with no
		// active recovery event even though it inherits the session preference.
		c.loadRecoveryState(newPath)
		if c.guardianSess != nil {
			c.guardianSess.Reset()
		}
		// Switching into the fork is a new logical session for temporary files.
		c.rotateSessionTemp()
		c.snapshotMu.Unlock()
	}
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("forked conversation at turn %d into a new session", turn)})
	return newPath, nil
}

func (c *Controller) CheckpointHasBoundary(turn int) bool {
	boundary, ok := c.checkpoints.boundary(turn)
	if !ok {
		return false
	}
	// After compaction or a head switch the key may point past the current
	// message log; treat those turns as "no boundary" so the UI can disable
	// the button. Len is lock-guarded for the frontend goroutines calling this.
	return boundary <= c.executor.Session().Len()
}

// Branch copies the current conversation into a child branch and switches to it.
// Unlike Fork, it branches at the current tip and does not require a checkpoint.
func (c *Controller) Branch(name string) (string, error) {
	return c.branchTip(name, false)
}

// BranchToFile branches at the current tip into a separate session file even
// when the live log is schema-2 and Branch would take an in-log head (task
// 540): remote surfaces (GrandCouncil) list and resume sessions by file path,
// so the serve child-session spawn needs a distinct file the client can
// switch back from with /resume. Branch's behavior is unchanged — this only
// forces the file-fork path that Branch already uses for schema-1 logs.
func (c *Controller) BranchToFile(name string) (string, error) {
	return c.branchTip(name, true)
}

func (c *Controller) branchTip(name string, forceFile bool) (string, error) {
	if c.executor == nil {
		return "", c.rewindFail(fmt.Errorf("branch unavailable"))
	}
	if c.sessionDir == "" {
		return "", c.rewindFail(fmt.Errorf("branch needs session persistence, which is disabled"))
	}
	// Hold the rotation gate across the Snapshot and the switch below so a turn
	// cannot start mid-branch and then have its session replaced.
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return "", c.rewindFail(fmt.Errorf("cannot branch while a turn is running"))
		}
		return "", c.rewindFail(err)
	}
	defer c.endRotation()
	if !c.executor.Session().HasContent() {
		return "", c.rewindFail(fmt.Errorf("nothing to branch yet"))
	}
	if err := c.SnapshotDurable(); err != nil {
		return "", c.rewindFail(err)
	}
	// tl1280: in-log head forks left with the write-path slow lane; every
	// branch creates a new session file.
	_ = forceFile
	parentPath := c.SessionPath()
	parentID := agent.BranchID(parentPath)
	src := c.executor.Session().Snapshot()
	branched := append([]provider.Message(nil), src...)
	sess := c.seedBranchedSession(branched, parentPath)

	newPath := agent.NewSessionPath(c.sessionDir, c.activeLabel())
	if err := sess.SaveIfAbsent(newPath); err != nil {
		return "", c.rewindFail(err)
	}
	if _, err := sess.CopyValidContextProjection(parentPath, newPath); err != nil {
		slog.Warn("controller: branch did not inherit context projection", "err", err)
	}
	branchPreview, branchTurns := agent.SessionPreviewFromMessages(branched)
	if err := agent.SaveBranchMeta(newPath, agent.BranchMeta{
		Name:             strings.TrimSpace(name),
		ParentID:         parentID,
		ForkTurn:         -1,
		ForkMessageIndex: len(branched),
		Preview:          branchPreview,
		Turns:            branchTurns,
		SchemaVersion:    agent.BranchMetaCountsVersion,
	}); err != nil {
		return "", c.rewindFail(err)
	}
	commitTransition, err := c.prepareSessionTransition(newPath, "branch", sess)
	if err != nil {
		return "", c.rewindFail(fmt.Errorf("bind branch session: %w", err))
	}
	// See snapshotMu: the swap must not interleave with an in-flight save.
	c.snapshotMu.Lock()
	commitTransition.publish()
	c.bindExecutorProjection(newPath, true)
	c.ResetPlannerSession()
	c.rebindCheckpoints(newPath)
	if c.guardianSess != nil {
		c.guardianSess.Reset()
	}
	c.carryRecoveryState(newPath)
	c.rotateSessionTemp()
	c.snapshotMu.Unlock()
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("created branch %s", agent.BranchID(newPath))})
	return newPath, nil
}

// Branches lists saved conversation branches in this controller's session dir.
func (c *Controller) Branches() ([]agent.BranchInfo, error) {
	if c.sessionDir == "" {
		return nil, fmt.Errorf("session persistence is disabled")
	}
	if err := c.SnapshotDurable(); err != nil {
		return nil, err
	}
	branches, err := agent.ListBranches(c.sessionDir)
	if err != nil {
		return nil, err
	}
	return c.withHeadBranches(branches), nil
}

func (c *Controller) SwitchBranch(ref string) (agent.BranchInfo, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return agent.BranchInfo{}, c.rewindFail(fmt.Errorf("usage: /switch <branch id|name>"))
	}
	// Hold the rotation gate across the branch listing/load and the switch so a
	// turn cannot start between the check and the SetSession below.
	if err := c.beginRotation(); err != nil {
		if errors.Is(err, errTurnRunningRotation) {
			return agent.BranchInfo{}, c.rewindFail(fmt.Errorf("cannot switch branches while a turn is running"))
		}
		return agent.BranchInfo{}, c.rewindFail(err)
	}
	defer c.endRotation()
	branches, err := c.Branches()
	if err != nil {
		return agent.BranchInfo{}, c.rewindFail(err)
	}
	match, err := resolveBranch(branches, ref)
	if err != nil {
		return agent.BranchInfo{}, c.rewindFail(err)
	}
	if !agent.IsVisibleSession(match.Path) {
		return agent.BranchInfo{}, c.rewindFail(fmt.Errorf("branch %q not found", ref))
	}
	// tl1280: in-log head switching left with the write-path slow lane, so a
	// branch with a HeadID is a leftover schema-2 row the catalog no longer
	// produces; fall through to the file-branch switch below.
	loaded, err := agent.LoadSession(match.Path)
	if err != nil {
		return agent.BranchInfo{}, c.rewindFail(err)
	}
	commitTransition, err := c.prepareSessionTransition(match.Path, "switch", loaded)
	if err != nil {
		return agent.BranchInfo{}, c.rewindFail(fmt.Errorf("bind switched session: %w", err))
	}
	// See snapshotMu: the swap must not interleave with an in-flight save.
	c.snapshotMu.Lock()
	commitTransition.publish()
	c.bindExecutorProjection(match.Path, true)
	c.ResetPlannerSession()
	c.rebindCheckpoints(match.Path)
	c.restoreTerminalGoalTodos(match.Path)
	c.loadGuardianSession()
	c.loadRecoveryState(match.Path)
	c.rotateSessionTemp()
	c.snapshotMu.Unlock()
	c.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelInfo,
		Text: fmt.Sprintf("switched to branch %s", branchDisplayName(match))})
	return match, nil
}

// ResolveBranchRef resolves a /switch-style branch reference (id, unique
// prefix, name, or path) against a branch listing, using the same matching
// rules as SwitchBranch. Frontends use it to learn the target session path
// before switching — e.g. to move their session lease first.
func ResolveBranchRef(branches []agent.BranchInfo, ref string) (agent.BranchInfo, error) {
	return resolveBranch(branches, strings.TrimSpace(ref))
}

func resolveBranch(branches []agent.BranchInfo, ref string) (agent.BranchInfo, error) {
	refLower := strings.ToLower(ref)
	var matches []agent.BranchInfo
	for _, b := range branches {
		nameLower := strings.ToLower(strings.TrimSpace(b.Name))
		switch {
		case b.ID == ref || strings.EqualFold(b.ID, ref):
			return b, nil
		case b.HeadID != "" && b.HeadID == ref:
			return b, nil
		case b.Name != "" && nameLower == refLower:
			matches = append(matches, b)
		case strings.HasPrefix(strings.ToLower(b.ID), refLower):
			matches = append(matches, b)
		case strings.HasPrefix(strings.ToLower(shortBranchID(b.ID)), refLower):
			matches = append(matches, b)
		case b.Path == ref:
			return b, nil
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return agent.BranchInfo{}, fmt.Errorf("branch %q is ambiguous", ref)
	}
	return agent.BranchInfo{}, fmt.Errorf("branch %q not found", ref)
}

func branchDisplayName(b agent.BranchInfo) string {
	if strings.TrimSpace(b.Name) != "" {
		return fmt.Sprintf("%s (%s)", b.Name, b.ID)
	}
	return b.ID
}

// identityRepairPrompt returns the identity contract to seed onto a headless
// transcript: the controller's own base prompt when one is wired (fresh
// assembly — the same prompt a /new session would carry), falling back to the
// canonical product default. tl1407: a transcript without this header is not
// safe to persist (reload loses the agent-identity contract) and used to be
// refused forever by the snapshot guard.
func (c *Controller) identityRepairPrompt() string {
	if prompt := c.freshSessionBasePrompt(); prompt != "" {
		return prompt
	}
	return config.DefaultSystemPrompt
}

// seedBranchedSession builds the fresh session a fork/branch switches to. A
// carried prefix that already starts with the system message is taken over
// verbatim (unchanged healthy path); a headless prefix gets the controller's
// identity contract seeded in front — previously the child inherited the
// missing header and every descendant stayed headless forever (the
// 20260927-020815 collab lineage). parentPath is only for the diagnostic line.
func (c *Controller) seedBranchedSession(prefix []provider.Message, parentPath string) *agent.Session {
	if len(prefix) > 0 && prefix[0].Role == provider.RoleSystem {
		sess := agent.NewSession("")
		sess.Messages = prefix
		return sess
	}
	slog.Warn("controller: fork/branch seeded a system header onto a headless parent transcript",
		"label", c.Label(), "parent", parentPath)
	sess := agent.NewSession(c.identityRepairPrompt())
	if len(prefix) > 0 {
		sess.Messages = append(sess.Messages, prefix...)
	}
	return sess
}

// afterHeadSwitch re-derives the per-transcript runtime state after the
// session moved to another head of the same log. tl1280: head switches left
// with the slow lane, so no caller reaches it any more; the body stays as the
// rebind recipe a future producer would reuse. Callers hold snapshotMu.
func (c *Controller) afterHeadSwitch(path string) {
	c.bindExecutorProjection(path, true)
	c.ResetPlannerSession()
	if c.guardianSess != nil {
		c.guardianSess.Reset()
	}
	c.rotateSessionTemp()
	c.emitHeadEvents()
	// Same path, different transcript: serve and remote clients rebind on this
	// barrier exactly as they do for a resume; local desktop tabs learn the
	// head in the desktop PR.
	c.sink.Emit(event.Event{Kind: event.SessionChanged, SessionPath: path, SessionReset: true})
}

// tl1280: the in-log head surfaces (withHeadBranches / headBranchSession /
// SessionHead) left with the write-path slow lane. Branches are session files
// again; the three names stay so port.go, branches.go and the desktop tab
// wiring compile against the same interface while always answering the
// file-only shape.

// sessionHeadPolicy groups the frontend's choice between in-log heads and
// separate session files for branch operations. tl1280: only the file shape
// remains, so the flag is accepted and ignored.
type sessionHeadPolicy struct {
	fileBranchesOnly bool
}

// headBranchSession always returns nil now: no branch operation may create a
// head inside a log any more.
func (c *Controller) headBranchSession() *agent.Session {
	return nil
}

// withHeadBranches is the identity mapping: there are no in-log heads left to
// fold into the file branch list.
func (c *Controller) withHeadBranches(branches []agent.BranchInfo) []agent.BranchInfo {
	return branches
}

// SessionHead always reports the schema-1 shape (ok=false): the live session
// no longer sits on a log head.
func (c *Controller) SessionHead() (agent.HeadRef, bool) {
	return agent.HeadRef{}, false
}
