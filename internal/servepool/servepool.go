// Package servepool manages a pool of per-project `reasonix serve`
// sub-processes behind a single-entry HTTP gateway for remote clients
// (#8983). Serves are spawned lazily (first request for a stopped project),
// bind 127.0.0.1 with random ports, and are reclaimed after an idle window.
package servepool

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/installlayout"
	"reasonix/internal/proc"
	"reasonix/internal/safego"
)

// Config controls the pool.
type Config struct {
	// ReasonixBin is the binary used to spawn serve sub-processes. Defaults
	// to os.Executable() (the desktop process is reasonix itself).
	ReasonixBin string
	// PortFileDir is where per-project serve.port / serve.token files are
	// written; defaults to the project's .reasonix directory.
	PortFileDir string
	// IdleTimeout is how long a project serve may sit without traffic before
	// being reclaimed. Default 15 minutes.
	IdleTimeout time.Duration
	// SpawnTimeout bounds waiting for a spawned serve to become ready.
	// Default 8 seconds.
	SpawnTimeout time.Duration
	// ProjectRoots is the initial project list (desktop-projects.json roots
	// or CLI config); RefreshProjects can update it later.
	ProjectRoots []string
	// ProjectColors maps a project root (cleaned) to its color token, used to
	// decorate the /manifest entries so remote clients can render the color.
	ProjectColors map[string]string
	// ProjectGroups maps a project root (cleaned) to its group name, used by
	// remote clients to group projects (e.g. GrandCouncil project folders).
	ProjectGroups map[string]string
	// Virtual lists manifest-only projects with no backing serve process
	// (e.g. the desktop "global" session scope). Open is a no-op and the
	// gateway serves their /sessions inline from a registered source.
	Virtual []ProjectState
}

// SessionEntry is one session-list row the gateway serves for a virtual
// project. Shape-compatible with internal/serve's sessionListEntry JSON so
// remote clients render virtual and real projects identically.
type SessionEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Title      string `json:"title,omitempty"`
	Turns      int    `json:"turns,omitempty"`
	Current    bool   `json:"current,omitempty"`
	Running    bool   `json:"running,omitempty"`
	TakenOver  bool   `json:"takenOver,omitempty"`
	MtimeMilli int64  `json:"mtimeMilli"`
	// HeldBy reports lease ownership for clients, same semantics as the
	// real serve list: "me", "other", or "" (free).
	//
	// Direction (task 244 B7, the "never route into a corpse" rule): "other"
	// is read as a live holder even though it may be a dead process's leftover
	// lease -- the reader stays conservative and refuses rather than stealing
	// from something that might still be alive. Users resolve it by hand, or
	// on the desktop side via experimental_orphan_lease_reclaim (task 244 B5),
	// which reclaims only after the recorded PID is proven dead. "" means no
	// readable lease metadata: free to attempt, with the OS file lock as the
	// final arbiter.
	HeldBy string `json:"heldBy,omitempty"`
}

// ProjectState mirrors the manifest entry a remote client sees.
type ProjectState struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Root     string `json:"root"`
	State    string `json:"state"` // stopped | starting | running | degraded | failed
	Color    string `json:"color,omitempty"`
	Group    string `json:"group,omitempty"`
	Sessions int    `json:"sessions,omitempty"`
	Err      string `json:"err,omitempty"`
}

// Manager owns the pool. All methods are safe for concurrent use.
type Manager struct {
	cfg      Config
	mu       sync.Mutex
	bin      string
	projects map[string]*project // keyed by project id (workspace slug)
	virtuals map[string]ProjectState
	stop     chan struct{}
	done     chan struct{}
}

type project struct {
	state string
	root  string
	id    string
	color string
	group string
	cmd   *exec.Cmd
	// job is the KILL_ON_JOB_CLOSE Job Object handle from proc.StartTracked
	// (task 272 L2): it reaps this serve whenever the desktop dies by ANY
	// path — taskkill, crash, watchdog exit — so an orphan can never keep a
	// session lease held (incident ②④).
	job           uintptr
	port          int
	token         string
	lastUse       time.Time
	failures      int
	degradedUntil time.Time
	err           string
}

// NewManager builds a pool manager with the given config.
func NewManager(cfg Config) (*Manager, error) {
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 15 * time.Minute
	}
	if cfg.SpawnTimeout == 0 {
		cfg.SpawnTimeout = 8 * time.Second
	}
	bin := cfg.ReasonixBin
	if bin == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("servepool: resolve own binary: %w", err)
		}
		bin = self
		// Launcher installs (upstream desktop layout) ship the real CLI beside
		// the GUI exe as reasonix-cli.exe; the launcher itself ignores the
		// "serve" subcommand, so spawning it would silently time out. Prefer
		// the sibling CLI when present so per-project serves understand the
		// CLI contract (--port-file etc).
		bin = resolveServeBinary(self)
	}
	m := &Manager{
		cfg:      cfg,
		bin:      bin,
		projects: map[string]*project{},
		virtuals: map[string]ProjectState{},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	for _, root := range cfg.ProjectRoots {
		m.addProjectLocked(root)
	}
	for _, v := range cfg.Virtual {
		if v.ID = strings.TrimSpace(v.ID); v.ID != "" {
			v.State = "running"
			m.virtuals[v.ID] = v
		}
	}
	// The manager loop runs for the process lifetime; a panic in it would
	// kill the desktop (its goroutine is outside the App goSafe reach).
	safego.Go("servepool.manager.loop", m.loop)
	return m, nil
}

// resolveServeBinary picks the binary serve sub-processes are spawned with.
//
//  1. Sibling reasonix-cli.exe: launcher installs ship the real CLI beside the
//     GUI exe, and only the CLI understands the serve contract (--port-file).
//  2. Active CLI from current.json: after a version switch the running desktop
//     may live in versions/<ver>.replaced-<nonce>/ — a backup of the previous
//     layout that does not contain a CLI (task 248) — while versions/<new>/
//     does. ResolveInstallRoot walks up from the running exe to the install
//     root, so the active version's CLI is found from any layout directory.
//  3. Fall back to the running executable itself; serve attempts on it fail
//     fast into the spawn error path instead of misbehaving quietly.
func resolveServeBinary(self string) string {
	cli := filepath.Join(filepath.Dir(self), installlayout.CLIBinaryName())
	if st, statErr := os.Stat(cli); statErr == nil && !st.IsDir() {
		return cli
	}
	if installRoot, rootErr := installlayout.ResolveInstallRoot(self); rootErr == nil && installRoot != "" {
		if cliPath, cliErr := installlayout.ActiveCLIPath(installRoot); cliErr == nil {
			log.Printf("[servepool] sibling %s missing next to %q; using active CLI %q", installlayout.CLIBinaryName(), self, cliPath)
			return cliPath
		}
	}
	return self
}

// WorkspaceSlug mirrors config.WorkspaceSlug: the flat directory name used
// as the stable project id (avoids importing internal/config into the pool).
func WorkspaceSlug(root string) string {
	root = filepath.Clean(root)
	base := filepath.Base(root)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "workspace"
	}
	return base
}

func (m *Manager) addProjectLocked(root string) {
	root = filepath.Clean(root)
	if root == "" {
		return
	}
	id := WorkspaceSlug(root)
	if _, ok := m.projects[id]; ok {
		return
	}
	color := ""
	if m.cfg.ProjectColors != nil {
		color = m.cfg.ProjectColors[root]
	}
	group := ""
	if m.cfg.ProjectGroups != nil {
		group = m.cfg.ProjectGroups[root]
	}
	m.projects[id] = &project{state: "stopped", root: root, id: id, color: color, group: group}
}

// RefreshProjects replaces the project list, preserving running instances
// and marking newly removed projects for stop on next reclaim.
func (m *Manager) RefreshProjects(roots []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	for _, r := range roots {
		r = filepath.Clean(r)
		if r == "" {
			continue
		}
		seen[WorkspaceSlug(r)] = true
		m.addProjectLocked(r)
	}
	for id, p := range m.projects {
		if !seen[id] && p.state == "stopped" {
			delete(m.projects, id)
		}
	}
}

// Projects returns the manifest snapshot.
func (m *Manager) Projects() []ProjectState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ProjectState, 0, len(m.projects)+len(m.virtuals))
	for _, p := range m.projects {
		ps := ProjectState{ID: p.id, Root: p.root, State: p.state, Err: p.err, Color: p.color, Group: p.group}
		if p.port > 0 {
			ps.Name = filepath.Base(p.root)
		} else {
			ps.Name = filepath.Base(p.root)
		}
		out = append(out, ps)
	}
	for _, v := range m.virtuals {
		out = append(out, v)
	}
	return out
}

// IsVirtual reports whether the id is a manifest-only project with no serve
// process; the gateway serves such projects' /sessions inline.
func (m *Manager) IsVirtual(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.virtuals[id]
	return ok
}

// Open ensures the project's serve is running and returns its id. It blocks
// until the serve is ready or the spawn times out.
func (m *Manager) Open(id string) error {
	m.mu.Lock()
	if _, virtual := m.virtuals[id]; virtual {
		m.mu.Unlock()
		return nil
	}
	p, ok := m.projects[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("servepool: unknown project %q", id)
	}
	switch p.state {
	case "running", "starting":
		p.lastUse = time.Now()
		m.mu.Unlock()
		return nil
	case "degraded":
		if time.Now().Before(p.degradedUntil) {
			m.mu.Unlock()
			return fmt.Errorf("servepool: project %q is degraded (rapid crashes); retry later", id)
		}
		p.state = "stopped"
		p.failures = 0
	}
	p.state = "starting"
	p.err = ""
	m.mu.Unlock()
	return m.spawn(p)
}

// Touch records activity for the project (idle-reclaim accounting).
func (m *Manager) Touch(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.projects[id]; ok {
		p.lastUse = time.Now()
	}
}

// Port returns the bound port of a running project serve (0 when not running).
func (m *Manager) Port(id string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.projects[id]; ok && p.state == "running" {
		return p.port
	}
	return 0
}

// Token returns the per-project auth token (empty when not running).
func (m *Manager) Token(id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.projects[id]; ok {
		return p.token
	}
	return ""
}

// Invalidate marks a project's cached serve as unresponsive so the next
// Open re-spawns instead of proxying to a dead port. The gateway calls this
// when a proxied write-back fails (dial refused after the serve died); the
// manager's state=running cache would otherwise stick forever.
func (m *Manager) Invalidate(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.projects[id]; ok && p.state == "running" {
		p.state = "degraded"
		p.port = 0
		p.failures++
	}
}

// Close stops every running serve and the manager loop.
func (m *Manager) Close() {
	close(m.stop)
	<-m.done
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.projects {
		m.stopLocked(p)
	}
}

func (m *Manager) stopLocked(p *project) {
	if p.cmd != nil && p.cmd.Process != nil {
		// Task 272 L2: KillTracked tears down the whole tree via the Job
		// handle (plus a KillTree fallback), never just the direct child.
		proc.KillTracked(p.cmd, p.job)
		_, _ = p.cmd.Process.Wait()
	}
	p.cmd = nil
	p.job = 0
	p.port = 0
	p.token = ""
	if p.state != "degraded" {
		p.state = "stopped"
	}
	p.err = ""
}

// spawn starts the project's serve and waits for readiness.
func (m *Manager) spawn(p *project) error {
	portFile := filepath.Join(m.portFileDir(p.root), "serve.port")
	tokenFile := filepath.Join(m.portFileDir(p.root), "serve.token")
	if err := os.MkdirAll(filepath.Dir(portFile), 0o755); err != nil {
		m.markFailed(p, fmt.Errorf("mkdir port dir: %w", err))
		return err
	}
	token := newToken()
	if err := os.WriteFile(tokenFile, []byte(token), 0o600); err != nil {
		m.markFailed(p, fmt.Errorf("write token file: %w", err))
		return err
	}
	cmd := proc.Command(m.bin,
		"serve",
		"--addr", "127.0.0.1:0",
		"--port-file", portFile,
		"--auth", "token",
		"--token", token,
	)
	cmd.Dir = p.root
	// Capture serve output into a bounded tail buffer so a spawn that never
	// becomes ready leaves the failure reason in the log (task 248: a silent
	// 8s timeout used to hide "serve: unknown command" from a wrong binary).
	out := &tailBuffer{max: 8 * 1024}
	cmd.Stdout = out
	cmd.Stderr = out
	// Task 272 L2 (iron rule 4): start inside a Job Object instead of a bare
	// proc.Command start — KILL_ON_JOB_CLOSE reaps the serve on ANY desktop
	// death (taskkill/crash/watchdog exit), so it can never linger holding a
	// session lease (incidents ②④). Required (fail-closed): a serve without
	// the job IS the orphan bug this fixes.
	job, err := proc.StartTrackedRequired(cmd)
	if err != nil {
		m.markFailed(p, fmt.Errorf("spawn serve: %w", err))
		log.Printf("[servepool] spawn serve failed project=%q bin=%q: %v", p.id, m.bin, err)
		return err
	}
	p.cmd = cmd
	p.job = job
	p.token = token
	// Record who owns this spawn (serve pid + desktop pid) so a LATER desktop
	// launch can reap an orphan whose owning desktop died (task 272 L3):
	// "owner pid dead + serve pid alive" is the orphan signature, no parent-PID
	// sniffing required. Audit M2: the third line records the serve image path
	// so the reap can refuse a recycled pid that no longer maps to this binary.
	spawnFile := filepath.Join(filepath.Dir(portFile), spawnFileRel)
	writeSpawnRecord(spawnFile,
		strconv.Itoa(cmd.Process.Pid)+"\n"+strconv.Itoa(os.Getpid())+"\n"+m.bin+"\n")
	// A stale port file from a previous spawn would be read on the first
	// poll and report success before the new serve even bound a socket —
	// proxying to a dead port (observed 502-in-48ms). Remove it first so
	// only the freshly spawned serve can recreate it.
	_ = os.Remove(portFile)
	deadline := time.Now().Add(m.cfg.SpawnTimeout)
	log.Printf("[servepool] spawning serve project=%q bin=%q portFile=%q", p.id, m.bin, portFile)
	ready := false
	defer func() {
		// tailBuffer is mutex-guarded, so this read is race-free even while
		// a straggler pipe copier is still writing (the timeout path uses
		// Process.Wait, which does not wait for exec's copiers). Success
		// skips the read deliberately: nothing went wrong, so dumping the
		// serve's progress chatter into the log would be noise.
		if !ready && out.Len() > 0 {
			log.Printf("[servepool] serve output project=%q bin=%q: %s", p.id, m.bin, out.String())
		}
	}()
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(portFile); err == nil {
			// The serve writes its actual bound listen address (host:port,
			// cli.writeServeAddrFile); parse the port portion. Accept a bare
			// port too so older serve builds keep working.
			var port int
			raw := strings.TrimSpace(string(data))
			if _, portStr, splitErr := net.SplitHostPort(raw); splitErr == nil {
				port, _ = strconv.Atoi(portStr)
			} else {
				port, _ = strconv.Atoi(raw)
			}
			// portFile written != socket bound: the serve can flush the addr
			// file just before listen(), so Open would return "ready" while
			// the first proxy dial still fails (→ 502). Confirm the port is
			// actually accepting connections before declaring readiness.
			if port > 0 && dialPort(port) {
				m.mu.Lock()
				p.port = port
				p.state = "running"
				p.lastUse = time.Now()
				p.failures = 0
				p.err = ""
				m.mu.Unlock()
				ready = true
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Task 272 L2: kill through the Job so a wedged spawn's descendants go too.
	proc.KillTracked(cmd, job)
	_, _ = cmd.Process.Wait()
	p.cmd = nil
	p.job = 0
	m.markFailed(p, fmt.Errorf("serve did not become ready within %s", m.cfg.SpawnTimeout))
	return errors.New("servepool: " + p.err)
}

// tailBuffer is a concurrency-safe io.Writer that keeps only the most recent
// max bytes. A long-running serve would otherwise grow the capture without
// bound; the interesting failure output ("unknown command", bind errors,
// panics) is what lands last before the process gives up.
//
// The mutex is what makes reading safe at all: exec's stdout/stderr pipe
// copiers are only waited on by cmd.Wait(), and the timeout path tears the
// process down with Process.Kill + Process.Wait(), which can leave a copier
// goroutine writing while the failure defer reads. Locking inside the buffer
// keeps that window race-free regardless of when the read happens.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		copy(t.buf, t.buf[over:])
		t.buf = t.buf[:t.max]
	}
	return len(p), nil
}

func (t *tailBuffer) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.buf)
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimRight(string(t.buf), "\n")
}

func (m *Manager) markFailed(p *project, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p.err = err.Error()
	p.failures++
	if p.failures >= 3 {
		p.state = "degraded"
		p.degradedUntil = time.Now().Add(5 * time.Minute)
		// The 5-minute backoff is invisible otherwise: the UI shows "not running" and
		// nothing says the pool has decided to stop retrying. feature=servepool.
		slog.Warn("serve pool: project degraded after repeated failures",
			"feature", "servepool", "project", p.id, "failures", p.failures, "err", p.err)
	} else {
		p.state = "stopped"
	}
}

func (m *Manager) portFileDir(root string) string {
	if m.cfg.PortFileDir != "" {
		return m.cfg.PortFileDir
	}
	return filepath.Join(root, ".reasonix")
}

// loop ticks health checks and idle reclamation.
func (m *Manager) loop() {
	defer close(m.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.sweep()
		}
	}
}

func (m *Manager) sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, p := range m.projects {
		if p.state == "running" && now.Sub(p.lastUse) > m.cfg.IdleTimeout {
			// Idle reclamation is the fork's own lifecycle (upstream has no pool), and a
			// project that quietly stops is the first thing to rule out when the phone or a
			// remote tab reports a dead endpoint. feature= keeps pool lines greppable.
			slog.Info("serve pool: reclaiming idle project",
				"feature", "servepool", "project", p.id, "idle", now.Sub(p.lastUse).Round(time.Second).String())
			m.stopLocked(p)
		}
	}
}

// RandomToken returns a 32-byte hex gateway/per-project token.
func RandomToken() string { return newToken() }

// newToken returns a 32-byte hex gateway/per-project token.
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	return hex.EncodeToString(b)
}

// dialPort reports whether a local TCP port is accepting connections. This
// closes the spawn race where a freshly launched serve flushes its address
// file just before listen(): without it Open() reports "ready" but the first
// gateway proxy dial still fails (observed 502, "No connection").
func dialPort(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
