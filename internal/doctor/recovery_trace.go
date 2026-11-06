package doctor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/store"
)

// ForkCensusReport is the tl1443 fork census over every session dir on this
// machine: physical forks per local day, per write-path trigger and per
// disk-side cause, plus all traced recovery decisions. Aggregate-only; records
// written before tl1443 count as "untraced".
type ForkCensusReport struct {
	SessionDirs    int            `json:"session_dirs"`
	Records        int            `json:"records"`
	Forks          int            `json:"forks"`
	ForksByDay     map[string]int `json:"forks_by_day,omitempty"`
	ForksByTrigger map[string]int `json:"forks_by_trigger,omitempty"`
	ForksByCause   map[string]int `json:"forks_by_cause,omitempty"`
	Decisions      map[string]int `json:"decisions,omitempty"`
	UnreadEntries  int            `json:"unread_entries"`
	ScanAborts     int            `json:"scan_aborts"`
}

// recoveryForkRecord is the census view of one conflicts.jsonl line. At stays
// raw so a malformed stamp only drops the day bucket, never the record.
type recoveryForkRecord struct {
	At               json.RawMessage `json:"at"`
	Outcome          string          `json:"outcome"`
	ExistingRecovery bool            `json:"existing_recovery"`
	Decision         string          `json:"decision"`
	Reason           string          `json:"reason"`
	Trigger          string          `json:"trigger"`
	DiskWriter       string          `json:"disk_writer"`
	PeerTrigger      string          `json:"peer_trigger"`
}

const recoveryUntraced = "untraced"

// recoveryTraceDays bounds the per-day text line; the JSON report keeps all.
const recoveryTraceDays = 14

// collectRecoveryForks walks every dir, including trashed sessions and the
// self-heal backups whose copies hold the only surviving lines of promoted
// mains. Copies repeat lines byte for byte, so each line counts once.
func collectRecoveryForks(dirs []string) ForkCensusReport {
	report := ForkCensusReport{}
	seenDirs := map[string]bool{}
	seenLines := map[string]bool{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		key := strings.ToLower(filepath.Clean(dir))
		if dir == "" || seenDirs[key] {
			continue
		}
		seenDirs[key] = true
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		report.SessionDirs++
		_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				report.UnreadEntries++
				return nil
			}
			if entry.IsDir() {
				if path != dir && !forkCensusDescends(dir, path) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(entry.Name(), store.SessionConflictLogSuffix) {
				report.scanRecoveryForkFile(path, seenLines)
			}
			return nil
		})
	}
	return report
}

// forkCensusDescends limits the walk to where conflict logs live: trashed
// session items and self-heal backups, at any depth below those two roots — a
// trashed item can itself carry .pre-repair-* copies, and a backup can nest
// session subdirs, so a fixed two-level shape (the old rule) silently dropped
// unique lines that survive only in the deeper copy. Checkpoint, inbox and job
// dirs hold thousands of files and never a conflict log, so everything outside
// these two subtrees stays closed.
func forkCensusDescends(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return parts[0] == ".trash" || strings.Contains(parts[0], ".pre-repair-")
}

func (r *ForkCensusReport) scanRecoveryForkFile(path string, seenLines map[string]bool) {
	file, err := os.Open(path)
	if err != nil {
		r.UnreadEntries++
		return
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || seenLines[line] {
			continue
		}
		seenLines[line] = true
		var rec recoveryForkRecord
		if json.Unmarshal([]byte(line), &rec) != nil || rec.Outcome == "" {
			continue
		}
		r.Records++
		r.note(rec)
	}
	if scanner.Err() != nil {
		r.ScanAborts++
	}
}

func (r *ForkCensusReport) note(rec recoveryForkRecord) {
	if rec.Decision != "" {
		bumpRecoveryCount(&r.Decisions, rec.Decision+"/"+rec.Reason)
	}
	if !recoveryCreatesPhysicalVersion(rec) {
		return
	}
	r.Forks++
	var at time.Time
	if json.Unmarshal(rec.At, &at) == nil && !at.IsZero() {
		bumpRecoveryCount(&r.ForksByDay, at.Format("2006-01-02"))
	}
	trigger, cause := recoveryUntraced, recoveryUntraced
	if rec.Decision != "" {
		trigger, cause = rec.Trigger, recoveryForkCause(rec)
	}
	bumpRecoveryCount(&r.ForksByTrigger, trigger)
	bumpRecoveryCount(&r.ForksByCause, cause)
}

func recoveryCreatesPhysicalVersion(rec recoveryForkRecord) bool {
	switch rec.Outcome {
	case "forked_recovery_branch", "forked_file_lock_recovery", "moved_to_stable_recovery":
		return !rec.ExistingRecovery
	}
	return false
}

// recoveryForkCause names who wrote the disk side a fork lost to: the ledger
// writer's origin, refined by the in-process peer's write path. Forks without
// a disk side (lock timeout, external removal) fall back to their reason.
func recoveryForkCause(rec recoveryForkRecord) string {
	switch {
	case rec.DiskWriter == "":
		return rec.Reason
	case rec.DiskWriter == "self_process" && rec.PeerTrigger != "":
		return rec.DiskWriter + "/" + rec.PeerTrigger
	default:
		return rec.DiskWriter
	}
}

func bumpRecoveryCount(m *map[string]int, key string) {
	if strings.TrimSpace(key) == "" {
		key = "unknown"
	}
	if *m == nil {
		*m = map[string]int{}
	}
	(*m)[key]++
}

func renderRecoveryForks(b *strings.Builder, r ForkCensusReport) {
	fmt.Fprintf(b, "\nrecovery forks (all workspaces)\n")
	fmt.Fprintf(b, "  scanned      %d session dirs, %d distinct records, %d forks\n", r.SessionDirs, r.Records, r.Forks)
	if r.UnreadEntries > 0 || r.ScanAborts > 0 {
		fmt.Fprintf(b, "  warning      %d unread entries, %d aborted scans\n", r.UnreadEntries, r.ScanAborts)
	}
	if len(r.ForksByDay) > 0 {
		days := make([]string, 0, len(r.ForksByDay))
		for day := range r.ForksByDay {
			days = append(days, day)
		}
		sort.Strings(days)
		if len(days) > recoveryTraceDays {
			days = days[len(days)-recoveryTraceDays:]
		}
		fmt.Fprintf(b, "  by day       %s\n", joinRecoveryCounts(r.ForksByDay, days))
	}
	for _, line := range []struct {
		label  string
		counts map[string]int
	}{
		{"by trigger", r.ForksByTrigger},
		{"by cause  ", r.ForksByCause},
		{"decisions ", r.Decisions},
	} {
		if len(line.counts) > 0 {
			fmt.Fprintf(b, "  %s   %s\n", line.label, joinRecoveryCounts(line.counts, recoveryKeysByCount(line.counts)))
		}
	}
}

// recoveryKeysByCount orders keys by descending count, then name, so the
// dominant source leads the line.
func recoveryKeysByCount(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

func joinRecoveryCounts(counts map[string]int, keys []string) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+":"+strconv.Itoa(counts[key]))
	}
	return strings.Join(parts, " ")
}
