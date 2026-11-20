package memory

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"reasonix/internal/fileutil"
)

type SaveOptions struct {
	ExpectedRevision        int
	RequireExpectedRevision bool
	RequireCreate           bool
	ClearExpiry             bool // drop an inherited expires_at instead of preserving it
}

type SaveResult struct {
	Path     string
	Memory   Memory
	Previous *Memory
}

type MigrationReport struct {
	Migrated int
}

var memoryStoreMutationMu sync.Mutex

// migrateHeaderBytes bounds the frontmatter probe MigrateV2 needs. The id and
// revision keys it looks for live in the first lines of a memory file, so
// reading whole files to learn them made every boot pay a full read of the
// entire store (tl1683 归因报告 §4.2: 881 files / 2.1MB = 295ms hot,
// 1240ms cold) before it could conclude "nothing to migrate".
//
// 1024 is measured, not guessed: across the live store's 886 fact files the
// frontmatter block (bytes up to the closing fence) measures min 231 / p50
// 360 / p90 481 / p99 657 / max 912 bytes — a 1KB head covers every one of
// them, so the probe decides without a fallback. A file whose frontmatter
// does not fit is reported as undecidable and falls back to the full read, so
// the bound can never change the verdict.
const migrateHeaderBytes = 1024

// migrateHeaderBufPool recycles the head-read buffers: the scan touches every
// fact file on every boot, and a fresh 4KB allocation per file is pure waste.
var migrateHeaderBufPool = sync.Pool{New: func() any {
	buf := make([]byte, migrateHeaderBytes)
	return &buf
}}

// migrationCandidate is one memory file that still needs the v2 migration.
type migrationCandidate struct {
	dir  string
	path string
}

// migrateScanMarker is the persisted per-file verdict of the last migration
// scan (tl1706): for every fact file, the (size, mtimeMs) hash at the time it
// was confirmed to already carry v2 metadata. A file whose stat still matches
// the recorded hash cannot have changed content (every write path goes through
// atomic replace, which always bumps mtime), so its "needs no migration"
// verdict carries over and the scan never opens it. This is the same
// (size, mtime) trust model the recall FTS reconcile already uses
// (index_fts.go: ensureRecallFTS "Unchanged files are never re-read").
//
// Why it exists: the classification phase opens every fact file to read its
// head. On this machine that costs ~130ms warm but 14–19s when the pool's
// pages are cold (940 small-file opens × ~20ms NTFS cold access — measured,
// see docs/reasonix/report/tl1706-清理后首启慢归因-20261120.md). Every boot
// paid that whenever a cleanup had evicted the cache or touched the store,
// which is exactly the "first boot after cleanup is slow" report (tl1706).
// With the marker, a changed store only re-judges the files whose stat
// actually changed; a cold cache with an unchanged store opens none.
type migrateScanMarker struct {
	// Version guards against misreading a future format change.
	Version int               `json:"version"`
	Files   map[string]string `json:"files"` // path -> "{size}|{mtimeMs}" hash at last "v2 confirmed"
}

const migrateScanMarkerVersion = 1

const migrateScanMarkerName = "migrate-v2-scan.json"

// migrateScanMarkerPath returns where the persisted scan verdict lives, "" when
// the store has no base directory. It sits beside the recall FTS index under
// .index/ — derived, disposable, never scanned as a fact file.
func migrateScanMarkerPath(s Store) string {
	base := s.Dir
	if base == "" {
		base = s.GlobalDir
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, ".index", migrateScanMarkerName)
}

// readMigrateScanMarker loads the persisted verdict. Any unreadable or
// foreign-version marker means "no verdict" — the scan then runs in full, the
// pre-tl1706 behavior, which is always safe.
func readMigrateScanMarker(s Store) *migrateScanMarker {
	path := migrateScanMarkerPath(s)
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var marker migrateScanMarker
	if err := json.Unmarshal(raw, &marker); err != nil || marker.Version != migrateScanMarkerVersion {
		return nil
	}
	return &marker
}

// recordMigrateScanMarker persists the verdict (best effort). A failure only
// costs the next boot a full re-scan — the pre-tl1706 behavior — never
// correctness, so it is logged and otherwise swallowed.
func recordMigrateScanMarker(s Store, files map[string]string) {
	path := migrateScanMarkerPath(s)
	if path == "" || len(files) == 0 {
		return
	}
	raw, err := json.Marshal(migrateScanMarker{Version: migrateScanMarkerVersion, Files: files})
	if err != nil {
		slog.Warn("memory: migrate scan marker marshal failed", "err", err)
		return
	}
	if err := writeMemoryAtomic(path, raw, 0o644); err != nil {
		slog.Warn("memory: migrate scan marker write failed (next boot re-scans)", "err", err)
	}
}

// classifyMigrations decides which files still need the v2 migration, carrying
// over verdicts from the persisted marker for files whose stat is unchanged
// and reading only the head of the rest. The returned confirmed map lists
// every file verified to be v2 in this pass (carried over + freshly judged).
func classifyMigrations(files []factFileState, marker *migrateScanMarker) (candidates []migrationCandidate, confirmed map[string]string, err error) {
	confirmed = make(map[string]string, len(files))
	for _, f := range files {
		if marker != nil {
			if verdict, ok := marker.Files[f.path]; ok && verdict == f.hash {
				confirmed[f.path] = f.hash
				continue
			}
		}
		needs, complete, err := needsMigration(f.path)
		if err != nil {
			return nil, nil, err
		}
		if !complete {
			raw, err := os.ReadFile(f.path)
			if err != nil {
				return nil, nil, err
			}
			needs = memoryNeedsMigration(string(raw))
		}
		if needs {
			candidates = append(candidates, migrationCandidate{dir: filepath.Dir(f.path), path: f.path})
		} else {
			confirmed[f.path] = f.hash
		}
	}
	return candidates, confirmed, nil
}

// MigrateV2 brings legacy memory files up to the v2 metadata shape.
//
// The work is split in two phases on purpose. Classification (phase 1) is
// IO-bound and runs WITHOUT the process-wide mutation mutex; only the actual
// rewrites (phase 2) take the lock. Before this split the whole scan ran
// under that mutex, and the mutex is the one every memory write holds while
// it rewrites the whole MEMORY.md index — so a boot that overlapped a write
// waited for the write instead of doing its own work (tl1683 归因报告 §4.2:
// the segment's own workload is 295ms hot / 1240ms cold while its P95 was
// 21–29s, i.e. 22–93x the work it was doing). The lock's semantics are
// unchanged — phase 2 still mutates under it.
//
// tl1706 adds a persisted per-file verdict (migrateScanMarker) on top: files
// whose (size, mtime) stat is unchanged since the last scan carried a v2
// verdict skip the head read entirely, so the cold-cache first boot after a
// cleanup no longer pays a ~15–19s full re-open of the pool.
func (s Store) MigrateV2() (MigrationReport, error) {
	// diag-boot2red 回归修复：tl1706 把扫描从 migrationCandidates 换成 FTS
	// reconcile 复用的 scanFactFiles 后，「store 路径存在但不是目录」这类用户
	// 可见配置错误被静默吞掉（scanFactFiles 对 ReadDir 失败一律 continue——
	// 这对索引重建是合理契约，对 migration 不是）。这里恢复 tl1706 之前的
	// 目录 preflight，让 boot 照旧发 "Memory metadata migration" 警告。
	if err := s.checkStoreDirs(); err != nil {
		return MigrationReport{}, err
	}
	files := scanFactFiles(s)
	marker := readMigrateScanMarker(s)
	candidates, confirmed, err := classifyMigrations(files, marker)
	if err != nil {
		return MigrationReport{}, err
	}
	if len(candidates) == 0 {
		// Lock-free by tl1683 contract: nothing to rewrite, no lock needed.
		// The verdict write is atomic and idempotent — racing boots write the
		// same content (or, when a concurrent write changed the store mid-pass,
		// the loser's stale hashes simply fail to match on the next boot and
		// those files are re-judged: self-healing, never wrong).
		recordMigrateScanMarker(s, confirmed)
		return MigrationReport{}, nil
	}
	memoryStoreMutationMu.Lock()
	defer memoryStoreMutationMu.Unlock()
	var report MigrationReport
	for _, candidate := range candidates {
		migrated, err := s.migrateFile(candidate.dir, candidate.path)
		if err != nil {
			return report, err
		}
		if migrated {
			report.Migrated++
		}
	}
	// The rewrites above changed sizes and mtimes, so the carried-over hashes
	// are stale. Re-stat once (directory enumeration only) and persist the
	// post-migration verdicts. On the error path above no marker is recorded:
	// the next boot re-scans in full, the pre-tl1706 behavior.
	postFiles := scanFactFiles(s)
	post := make(map[string]string, len(postFiles))
	for _, f := range postFiles {
		post[f.path] = f.hash
	}
	recordMigrateScanMarker(s, post)
	return report, nil
}

// checkStoreDirs restores the store-shape preflight migrationCandidates used
// to run before tl1706: a store path that exists but is not a directory is a
// user-visible misconfiguration and must surface as an error (boot turns it
// into a warning), never be silently skipped. A missing path stays a no-op.
// Deliberately separate from scanFactFiles — the FTS reconcile's silent
// continue contract is correct for index rebuilds and stays untouched.
func (s Store) checkStoreDirs() error {
	for _, dir := range s.dirs() {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("memory store path %q is not a directory", dir)
		}
	}
	return nil
}

// migrationCandidates lists the memory files that still need the v2
// migration, reading only the head of each file. A file whose frontmatter
// does not fit in the head falls back to the full read, so the verdict is
// always the one the pre-tl1683 full-read code reached.
func (s Store) migrationCandidates() ([]migrationCandidate, error) {
	var out []migrationCandidate
	for _, dir := range s.dirs() {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		info, err := os.Stat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("memory store path %q is not a directory", dir)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Name() == indexFile || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			needs, complete, err := needsMigration(path)
			if err != nil {
				return nil, err
			}
			if !complete {
				raw, err := os.ReadFile(path)
				if err != nil {
					return nil, err
				}
				needs = memoryNeedsMigration(string(raw))
			}
			if needs {
				out = append(out, migrationCandidate{dir: dir, path: path})
			}
		}
	}
	return out, nil
}

// migrateFile rewrites one legacy memory file in place. The caller holds the
// process-wide mutation mutex. The frontmatter is re-checked here because
// phase 1 ran without the lock: a concurrent write may have migrated the
// file in between.
func (s Store) migrateFile(dir, path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if !memoryNeedsMigration(string(raw)) {
		return false, nil
	}
	memory, ok := loadMemory(path)
	if !ok {
		return false, nil
	}
	memory.Name = slug(memory.Name)
	if memory.Scope == "" {
		memory.Scope = s.scopeForDir(dir)
	}
	// The rewrite below is in place: persist the exact original bytes
	// as <name>.md.bak first so a bad migration stays recoverable
	// (upstream 11846/PR11857 semantics). Backup failure refuses the
	// migration; an existing backup is kept as the first copy so a
	// retried migration never overwrites the pre-migration snapshot.
	// The .bak suffix keeps it out of every reader — fact discovery,
	// index, recall, and revisions all filter on ".md".
	bakPath := path + ".bak"
	bakInfo, bakErr := os.Stat(bakPath)
	switch {
	case os.IsNotExist(bakErr):
		if err := writeMemoryAtomic(bakPath, raw, 0o644); err != nil {
			return false, fmt.Errorf("memory migration backup %q failed: %w", bakPath, err)
		}
	case bakErr != nil:
		return false, fmt.Errorf("memory migration backup %q: %w", bakPath, bakErr)
	case !bakInfo.Mode().IsRegular():
		return false, fmt.Errorf("memory migration backup %q exists and is not a regular file", bakPath)
	}
	if err := writeMemoryAtomic(path, []byte(render(memory, memory.Name)), 0o644); err != nil {
		return false, err
	}
	if err := reindexIn(dir, memory.Name, memory); err != nil {
		return false, err
	}
	return true, nil
}

// memoryNeedsMigration reports whether a memory file's bytes still need the
// v2 migration: an already-migrated file carries a non-empty id and a
// positive revision in its frontmatter.
func memoryNeedsMigration(raw string) bool {
	frontmatter, _ := splitFrontmatter(raw)
	return strings.TrimSpace(frontmatter["id"]) == "" || parsePositiveInt(frontmatter["revision"]) <= 0
}

// needsMigration decides from the file head alone whether path still needs
// the v2 migration. complete=false means the head could not decide (the
// frontmatter block does not fit in it), and the caller must fall back to a
// full read.
func needsMigration(path string) (needs bool, complete bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return false, false, err
	}
	defer f.Close()
	bufp := migrateHeaderBufPool.Get().(*[]byte)
	defer migrateHeaderBufPool.Put(bufp)
	// One read, not io.ReadFull: a regular file read returns everything that
	// is left, so a short read means "this file is smaller than the head" —
	// which is exactly the "we have the whole file" case. io.ReadFull would
	// spend a second syscall per file to reach the same conclusion.
	n, err := f.Read(*bufp)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, false, err
	}
	head := string((*bufp)[:n])
	// A short read means the whole file is in the head, so the head decides.
	if n < migrateHeaderBytes || frontmatterHeadComplete(head) {
		return memoryNeedsMigration(head), true, nil
	}
	return false, false, nil
}

// frontmatterHeadComplete reports whether the frontmatter block of head is
// fully contained in head. A file with no frontmatter at all is complete by
// definition — the full read would reach the same verdict. A head that ends
// inside an open fence is not decidable: the block may be longer than the
// head, or the file may genuinely lack a closing fence.
func frontmatterHeadComplete(head string) bool {
	normalized := strings.ReplaceAll(head, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return true
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return true
		}
	}
	return false
}

// inheritOnUpdate keeps the update-omittable fields of an existing revision:
// an update that leaves scope, activation, volatility, expiry, verification,
// or keywords/triggers empty preserves them, it does not clear them. ClearExpiry is
// the explicit exception — dropping a boundary must be a stated intent.
func inheritOnUpdate(m Memory, existing Memory, clearExpiry bool) Memory {
	if strings.TrimSpace(string(m.Scope)) == "" {
		m.Scope = existing.Scope
	}
	if NormalizeActivation(string(m.Activation)) == "" {
		m.Activation = existing.Activation
	}
	if NormalizeVolatility(string(m.Volatility)) == "" {
		m.Volatility = existing.Volatility
	}
	if NormalizeSubjectKey(m.SubjectKey) == "" {
		m.SubjectKey = existing.SubjectKey
	}
	if clearExpiry {
		m.ExpiresAt = time.Time{}
	} else if m.ExpiresAt.IsZero() {
		m.ExpiresAt = existing.ExpiresAt
	}
	if m.LastVerifiedAt.IsZero() {
		m.LastVerifiedAt = existing.LastVerifiedAt
	}
	if strings.TrimSpace(m.Keywords) == "" {
		m.Keywords = existing.Keywords
	}
	if strings.TrimSpace(m.Triggers) == "" {
		m.Triggers = existing.Triggers
	}
	return m
}

// validateSave runs the cross-fact invariants once identity, scope, and
// inheritance are resolved: the pinned budget (or its plan-E count-quota
// successor) and subject uniqueness.
func (s Store) validateSave(m Memory) error {
	if pinnedIndexMode() {
		// Plan E (tl1193): bodies are off the standing prefix, so body runes
		// no longer cost the session anything and the 1500-rune budget retires
		// in this mode; growth is bounded by entry count instead. Default off
		// keeps the 1192 ratchet repair byte-for-byte.
		if err := s.validatePinnedIndexQuota(m); err != nil {
			return err
		}
		return s.validateSubjectKey(m)
	}
	if err := s.validatePinnedBudget(m); err != nil {
		return err
	}
	return s.validateSubjectKey(m)
}

// validatePinnedBudget rejects a save that would push the total pinned-body
// runes over PinnedGuidanceBudgetChars while also growing that total. A store
// that already sits over budget stays writable: replacing or shrinking an
// existing pinned body never worsens the overshoot, so curation can proceed —
// the write-only check used to leave the store blind to legacy guidance
// accumulating past the cap, after which every pinned write failed and the
// only escape was silently downgrading preferences to relevant. Legacy
// virtually-pinned guidance counts — it occupies the same prefix.
func (s Store) validatePinnedBudget(m Memory) error {
	if ResolveActivation(m) != ActivationPinned {
		return nil
	}
	newTotal := BodyChars(m)
	currentTotal := 0
	guidance := s.pinnedGuidance()
	for _, pinned := range guidance {
		chars := BodyChars(pinned)
		currentTotal += chars
		if pinned.ID == m.ID || (m.ID == "" && pinned.Name == m.Name) {
			continue // this save replaces that body; its old size only counts in currentTotal
		}
		newTotal += chars
	}
	if newTotal <= PinnedGuidanceBudgetChars || newTotal <= currentTotal {
		return nil
	}
	return pinnedBudgetError(newTotal, guidance, m)
}

// pinnedBudgetError renders the rejection as numbers plus a ranked ledger: the
// writer sees the exact overshoot and a copyable /memory unpin line for the
// largest entry it did not itself write, instead of a dead end that pushes
// curation toward silently downgrading facts.
func pinnedBudgetError(newTotal int, guidance []Memory, m Memory) error {
	deficit := newTotal - PinnedGuidanceBudgetChars
	var b strings.Builder
	fmt.Fprintf(&b, "pinning this fact would put pinned guidance at %d chars, over the %d budget (deficit %d): rules that must always hold belong in REASONIX.md/AGENTS.md instructions\n",
		newTotal, PinnedGuidanceBudgetChars, deficit)
	fmt.Fprintf(&b, "pinned budget total=%d limit=%d deficit=%d; current pinned entries (name | chars | scope | updated_at | legacy):\n",
		newTotal, PinnedGuidanceBudgetChars, deficit)
	suggest := ""
	suggestChars := 0
	for _, pinned := range guidance {
		legacy := "no"
		if NormalizeActivation(string(pinned.Activation)) == "" {
			legacy = "yes"
		}
		fmt.Fprintf(&b, "  %s | %d | %s | %s | %s\n",
			pinned.Name, BodyChars(pinned), NormalizeFactScope(string(pinned.Scope)),
			formatMemoryDay(pinned.UpdatedAt), legacy)
		isSelf := pinned.ID == m.ID || (m.ID == "" && pinned.Name == m.Name)
		if !isSelf && BodyChars(pinned) > suggestChars {
			suggest, suggestChars = pinned.Name, BodyChars(pinned)
		}
	}
	if suggest != "" {
		fmt.Fprintf(&b, "frees the most budget: /memory unpin %s", suggest)
	}
	return fmt.Errorf("%s", b.String())
}

func formatMemoryDay(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.UTC().Format("2006-01-02")
}

// PinnedGuidanceUsage reports how much of the pinned budget the store currently
// occupies: resolved-pinned fact count and total body runes. /memory renders it
// so an overshoot is visible before it blocks a write, not only after.
func (s Store) PinnedGuidanceUsage() (facts, chars int) {
	for _, m := range s.pinnedGuidance() {
		facts++
		chars += BodyChars(m)
	}
	return facts, chars
}

// MaterializeLegacyPinned records an explicit activation: pinned on every fact
// that only rides session-context through ResolveActivation's legacy default.
// Injection is unchanged — the same bodies pin before and after — but the
// virtually-pinned set is emptied, so the budget check and the /memory ledger
// act on explicit curation instead of an invisible default. Each rewrite goes
// through the normal save path: the pre-materialization state lands in
// .revisions and /memory revisions|restore can roll it back. Idempotent; runs
// at boot next to MigrateV2. Returns the number of facts materialized.
func (s Store) MaterializeLegacyPinned() (int, error) {
	materialized := 0
	for _, fact := range s.ListAll() {
		if NormalizeActivation(string(fact.Activation)) != "" || ResolveActivation(fact) != ActivationPinned {
			continue
		}
		fact.Activation = ActivationPinned
		if _, err := s.SaveWithOptions(fact, SaveOptions{}); err != nil {
			return materialized, err
		}
		materialized++
	}
	return materialized, nil
}

func (s Store) SaveWithOptions(m Memory, opts SaveOptions) (SaveResult, error) {
	memoryStoreMutationMu.Lock()
	defer memoryStoreMutationMu.Unlock()

	inputID := strings.TrimSpace(m.ID)
	inputRef := parseMemoryReference(m.Name)
	if inputID == "" && inputRef.qualified && strings.TrimSpace(string(m.Scope)) != "" &&
		NormalizeFactScope(string(m.Scope)) != inputRef.scope {
		return SaveResult{}, fmt.Errorf("memory reference scope %q conflicts with explicit scope %q", inputRef.scope, m.Scope)
	}
	var existing Memory
	var existingPath string
	var exists bool
	if inputID != "" {
		existing, existingPath, exists = s.findActive(inputID)
		if !exists {
			return SaveResult{}, fmt.Errorf("memory id %q not found", m.ID)
		}
	} else if inputRef.raw != "" {
		existing, existingPath, exists = s.findActive(m.Name)
	}
	if opts.RequireExpectedRevision {
		actual := 0
		if exists {
			actual = existing.Revision
		}
		if actual != opts.ExpectedRevision {
			return SaveResult{}, fmt.Errorf("memory revision conflict: expected %d, found %d", opts.ExpectedRevision, actual)
		}
	}
	if opts.RequireCreate && exists {
		return SaveResult{}, fmt.Errorf("memory %q already exists; automatic writes are create-only", existing.Name)
	}

	if inputRef.raw == "" {
		if !exists {
			return SaveResult{}, fmt.Errorf("memory needs a name")
		}
		m.Name = existing.Name
	} else if exists && inputID == "" {
		// Name-based references identify an existing fact; renames require its
		// stable ID. This also prevents display references such as foo.md or
		// project/foo.md from becoming new slugs during an update.
		m.Name = existing.Name
	} else {
		m.Name = inputRef.name
	}
	m.Name = slug(m.Name)
	if m.Name == "" {
		return SaveResult{}, fmt.Errorf("memory name needs at least one letter or digit")
	}
	now := time.Now().UTC()
	if exists {
		m.ID, m.Revision, m.CreatedAt = existing.ID, existing.Revision+1, existing.CreatedAt
		m = inheritOnUpdate(m, existing, opts.ClearExpiry)
	} else {
		m.ID = newMemoryID(m.Name, now)
		m.Revision = 1
		m.CreatedAt = now
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	m.UpdatedAt = now
	m.Type = NormalizeType(string(m.Type))
	if strings.TrimSpace(string(m.Scope)) == "" {
		if inputRef.qualified {
			m.Scope = inputRef.scope
		} else {
			m.Scope = FactScopeProject
		}
	} else {
		m.Scope = NormalizeFactScope(string(m.Scope))
	}
	if err := s.validateSave(m); err != nil {
		return SaveResult{}, err
	}

	dir := s.DirFor(m.Scope)
	if dir == "" {
		return SaveResult{}, fmt.Errorf("memory store unavailable (no user config dir)")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SaveResult{}, err
	}
	if collision, _, ok := s.findActiveInDir(dir, m.Name); ok && (!exists || collision.ID != existing.ID) {
		return SaveResult{}, fmt.Errorf("memory name %q is already used by id %q", m.Name, collision.ID)
	}
	path, err := safeJoin(dir, m.Name+".md")
	if err != nil {
		return SaveResult{}, err
	}
	if exists {
		if err := snapshotMemoryRevision(existingPath, existing); err != nil {
			return SaveResult{}, err
		}
	}
	if err := writeMemoryAtomic(path, []byte(render(m, m.Name)), 0o644); err != nil {
		return SaveResult{}, err
	}
	if exists && cleanMemoryPath(existingPath) != cleanMemoryPath(path) {
		if err := removeMemoryFromDir(existingPath, existing.Name); err != nil {
			return SaveResult{}, err
		}
	}
	if err := reindexIn(dir, m.Name, m); err != nil {
		return SaveResult{Path: path, Memory: m}, err
	}
	// Legacy unqualified name updates keep the previous single-active-copy
	// behavior. Stable IDs and scope-qualified references select one identity
	// exactly, so they must not remove a same-named fact in the other scope.
	if inputID == "" && !inputRef.qualified {
		for _, otherDir := range s.dirs() {
			if sameDir(otherDir, dir) {
				continue
			}
			if duplicate, _, ok := s.findActiveInDir(otherDir, m.Name); ok && duplicate.ID != m.ID {
				if _, err := archiveMemoryInDir(otherDir, duplicate.Name); err != nil {
					return SaveResult{}, err
				}
			}
		}
	}

	result := SaveResult{Path: path, Memory: m}
	if exists {
		previous := existing
		result.Previous = &previous
	}
	return result, nil
}

func (s Store) Read(ref string) (Memory, bool) {
	memory, _, ok := s.findActive(ref)
	return memory, ok
}

func (s Store) findActive(ref string) (Memory, string, bool) {
	parsed := parseMemoryReference(ref)
	if parsed.raw == "" {
		return Memory{}, "", false
	}
	if parsed.qualified {
		return s.findActiveInDir(s.DirFor(parsed.scope), parsed.raw)
	}
	for _, v := range slices.Backward(s.dirs()) {
		dir := v
		if memory, path, ok := s.findActiveInDir(dir, parsed.raw); ok {
			return memory, path, true
		}
	}
	return Memory{}, "", false
}

func (s Store) findActiveInDir(dir, ref string) (Memory, string, bool) {
	if strings.TrimSpace(dir) == "" {
		return Memory{}, "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Memory{}, "", false
	}
	parsed := parseMemoryReference(ref)
	wantName := parsed.name
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == indexFile || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		memory, ok := loadMemory(path)
		if !ok {
			continue
		}
		if memory.Scope == "" {
			memory.Scope = s.scopeForDir(dir)
		}
		if memory.ID == parsed.raw || slug(memory.Name) == wantName {
			memory.Name = slug(memory.Name)
			return memory, path, true
		}
	}
	return Memory{}, "", false
}

type memoryReference struct {
	raw       string
	name      string
	scope     FactScope
	qualified bool
}

// parseMemoryReference understands provider-visible references without ever
// treating them as filesystem paths. Memory facts are flat files, so only one
// fixed scope component plus one filename is accepted as a qualified form.
func parseMemoryReference(ref string) memoryReference {
	raw := strings.TrimSpace(ref)
	parsed := memoryReference{raw: raw, name: slug(strings.TrimSuffix(raw, ".md"))}
	for _, candidate := range []FactScope{FactScopeProject, FactScopeGlobal} {
		prefix := string(candidate) + "/"
		if !strings.HasPrefix(raw, prefix) {
			continue
		}
		name := strings.TrimPrefix(raw, prefix)
		if name == "" || strings.ContainsAny(name, `/\\`) {
			return parsed
		}
		parsed.name = slug(strings.TrimSuffix(name, ".md"))
		parsed.scope = candidate
		parsed.qualified = true
		return parsed
	}
	return parsed
}

func (s Store) Revisions(ref string) []Memory {
	active, _, ok := s.findActive(ref)
	if !ok {
		return nil
	}
	seen := map[int]bool{}
	var revisions []Memory
	for _, dir := range s.dirs() {
		revisionDir := filepath.Join(dir, ".revisions", active.ID)
		entries, err := os.ReadDir(revisionDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			memory, ok := loadMemory(filepath.Join(revisionDir, entry.Name()))
			if !ok || memory.ID != active.ID || seen[memory.Revision] {
				continue
			}
			seen[memory.Revision] = true
			revisions = append(revisions, memory)
		}
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].Revision > revisions[j].Revision })
	return revisions
}

func (s Store) Restore(ref string, revision int) (SaveResult, error) {
	active, ok := s.Read(ref)
	if !ok {
		return SaveResult{}, fmt.Errorf("memory %q not found", ref)
	}
	if revision == active.Revision {
		return SaveResult{Path: s.Path(active.Name), Memory: active}, nil
	}
	var target Memory
	found := false
	for _, candidate := range s.Revisions(active.ID) {
		if candidate.Revision == revision {
			target = candidate
			found = true
			break
		}
	}
	if !found {
		return SaveResult{}, fmt.Errorf("memory %q revision %d not found", active.ID, revision)
	}
	target.ID = active.ID
	return s.SaveWithOptions(target, SaveOptions{ExpectedRevision: active.Revision, RequireExpectedRevision: true})
}

// RestoreArchived recovers one archive entry as a new active revision. The
// archive path must be an entry currently owned by this Store. Recovery never
// overwrites an active identity or slug, and the archived state becomes an
// immutable revision snapshot before the new active file is created.
func (s Store) RestoreArchived(archivePath string) (SaveResult, error) {
	memoryStoreMutationMu.Lock()
	defer memoryStoreMutationMu.Unlock()

	archivePath = cleanMemoryPath(strings.TrimSpace(archivePath))
	archived, base, ok := s.findArchivedByPath(archivePath)
	if !ok {
		return SaveResult{}, fmt.Errorf("archived memory not found")
	}
	if active, _, exists := s.findActive(archived.ID); exists {
		return SaveResult{}, fmt.Errorf("memory id %q is already active as %q", archived.ID, active.Name)
	}
	if active, _, exists := s.findActive(archived.Name); exists {
		return SaveResult{}, fmt.Errorf("memory name %q is already active as id %q", archived.Name, active.ID)
	}

	if err := snapshotMemoryRevisionInDir(base, archivePath, archived); err != nil {
		return SaveResult{}, err
	}
	now := time.Now().UTC()
	restored := archived
	restored.Scope = s.scopeForDir(base)
	restored.Revision = s.maxKnownRevision(archived.ID) + 1
	if restored.Revision <= archived.Revision {
		restored.Revision = archived.Revision + 1
	}
	if restored.CreatedAt.IsZero() {
		restored.CreatedAt = now
	}
	restored.UpdatedAt = now
	path, err := safeJoin(base, restored.Name+".md")
	if err != nil {
		return SaveResult{}, err
	}
	if err := writeMemoryCreate(path, []byte(render(restored, restored.Name)), 0o644); err != nil {
		if os.IsExist(err) {
			return SaveResult{}, fmt.Errorf("memory name %q is already active", restored.Name)
		}
		return SaveResult{}, err
	}
	if err := reindexIn(base, restored.Name, restored); err != nil {
		return SaveResult{Path: path, Memory: restored}, err
	}
	if err := os.Remove(archivePath); err != nil && !os.IsNotExist(err) {
		return SaveResult{Path: path, Memory: restored}, err
	}
	return SaveResult{Path: path, Memory: restored}, nil
}

func (s Store) findArchivedByPath(want string) (Memory, string, bool) {
	for _, base := range s.dirs() {
		if strings.TrimSpace(base) == "" {
			continue
		}
		dir := filepath.Join(base, ".archive")
		info, err := os.Lstat(dir)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path, err := safeJoin(dir, entry.Name())
			if err != nil || cleanMemoryPath(path) != want {
				continue
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return Memory{}, "", false
			}
			archived, ok := loadMemory(path)
			if !ok {
				return Memory{}, "", false
			}
			if archived.Scope == "" {
				archived.Scope = s.scopeForDir(base)
			}
			archived.Name = slug(archived.Name)
			return archived, base, true
		}
	}
	return Memory{}, "", false
}

func (s Store) maxKnownRevision(id string) int {
	maxRevision := 0
	for _, base := range s.dirs() {
		if strings.TrimSpace(base) == "" {
			continue
		}
		for _, dir := range []string{filepath.Join(base, ".archive"), filepath.Join(base, ".revisions", id)} {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				path := filepath.Join(dir, entry.Name())
				info, err := os.Lstat(path)
				if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					continue
				}
				candidate, ok := loadMemory(path)
				if ok && candidate.ID == id && candidate.Revision > maxRevision {
					maxRevision = candidate.Revision
				}
			}
		}
	}
	return maxRevision
}

func snapshotMemoryRevision(path string, memory Memory) error {
	return snapshotMemoryRevisionInDir(filepath.Dir(path), path, memory)
}

func snapshotMemoryRevisionInDir(base, path string, memory Memory) error {
	if memory.ID == "" || memory.Revision < 1 {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dir := filepath.Join(base, ".revisions", memory.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%09d.md", memory.Revision)
	return writeMemoryAtomic(filepath.Join(dir, name), b, 0o644)
}

// writeMemoryAtomic publishes a fact file through the shared crash-safe
// writer (temp + fsync + replace), creating the parent directory on demand.
func writeMemoryAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, data, mode)
}

// writeMemoryCreate publishes a fact file only when path is still absent; a
// concurrent creator wins. The shared writer stages a complete temp file, so
// a crash can never leave a partial fact where active truth lives.
func writeMemoryCreate(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fileutil.AtomicCreateFile(path, data, mode)
}

func newMemoryID(name string, now time.Time) string {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err == nil {
		return "mem-" + hex.EncodeToString(raw)
	}
	sum := sha256.Sum256([]byte(name + "\x00" + strconv.FormatInt(now.UnixNano(), 10)))
	return "mem-" + hex.EncodeToString(sum[:16])
}

func legacyMemoryID(name string, scope FactScope) string {
	sum := sha256.Sum256([]byte("reasonix-memory-v2\x00" + string(NormalizeFactScope(string(scope))) + "\x00" + slug(name)))
	return "legacy-" + hex.EncodeToString(sum[:12])
}

func cleanMemoryPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}
