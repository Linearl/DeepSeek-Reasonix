package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/store"
)

// probeLogForSave classifies the event log for a save, refusing a schema this
// build cannot own and healing a torn schema-1 tail before anything appends
// behind it. tl1280: the dagSaveRoute branch that routed a schema-2 log onto
// the slow lane left with the write path itself; a sealed schema-2 log still
// refuses here so the flat writer never appends into a log this build no
// longer owns.
func probeLogForSave(path string) (sessionEventLogProbe, error) {
	probe, err := probeSessionEventLog(path)
	if err != nil {
		return probe, err
	}
	if probe.futureSchema {
		return probe, fmt.Errorf("session event log for %s uses schema %d; this build supports up to %d", path, probe.schemaVersion, sessionDAGSchemaVersion)
	}
	if probe.native && probe.size > 0 {
		if err := repairSessionEventLogTail(path); err != nil {
			return probe, fmt.Errorf("repair session event log: %w", err)
		}
	}
	return probe, nil
}

// SessionLogSchemaEnv is the env var that used to switch the session-log
// writer between the flat schema-1 form and the schema-2 DAG form ("v1"/"v2").
// tl1280: the DAG writer left with the write-path slow lane, so the variable
// no longer routes anything — the constant stays because tests and operator
// docs still name it, and an unknown or "v2" value is simply ignored (every
// session writes flat).
const SessionLogSchemaEnv = "REASONIX_SESSION_LOG"

// sameSessionLogPath compares two session log paths by canonical identity so
// case/separator differences in the raw forms cannot split a single physical
// file into two reuse guards (task 196fix).
func sameSessionLogPath(a, b string) bool {
	if a == "" || b == "" {
		// Empty never claims a physical file: canonical("") resolves to the
		// working directory, which would make two empties "match".
		return false
	}
	return canonicalSessionSavePath(a) == canonicalSessionSavePath(b)
}

// SessionDAGCensus is the tl-1220 counter ② reading over a set of session
// directories: how many stored sessions carry an event-log sidecar, and how
// many of those sidecars classify as DAG (probe.dag hit) — the tl1173 §4.3
// retirement-condition source. tl1280: the census itself survives the slow
// lane's deletion because the doctor and the `dag-sessions` CLI still report
// the reading; only the save-path nil-cache observation counter went with the
// writer.
type SessionDAGCensus struct {
	ScannedDirs      int   `json:"scanned_dirs"`       // directories actually scanned
	DirErrors        int   `json:"dir_errors"`         // directories that could not be read
	EventLogSessions int   `json:"event_log_sessions"` // sessions with an event-log sidecar (plain or archived)
	DAGSessions      int   `json:"dag_sessions"`       // probe.dag hits — the retirement counter
	NativeSessions   int   `json:"native_sessions"`    // schema-1 event logs
	OtherSessions    int   `json:"other_sessions"`     // present but not classifiable as ours (foreign/future/unreadable)
	ArchivedOnly     int   `json:"archived_only"`      // of EventLogSessions: zst archive with no plaintext sibling
	DAGArchivedOnly  int   `json:"dag_archived_only"`  // of DAGSessions: zst archive with no plaintext sibling
	TotalLogBytes    int64 `json:"total_log_bytes"`    // plaintext bytes (archive: header plain_bytes) summed over sidecars
}

// CensusStoredDAGSessions scans each directory (flat, one session per
// transcript stem, matching how the stores lay sessions out) and classifies
// every event-log sidecar head. Read-only by construction: the classifier
// opens files and decodes a bounded head window, and archived logs are
// decoded in memory — it never calls the materializing paths, so an archived
// session stays archived after the census.
func CensusStoredDAGSessions(dirs []string) SessionDAGCensus {
	var census SessionDAGCensus
	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				// An absent optional root (e.g. the experimental sessions-v4
				// store on a machine that never enabled it) is not an error —
				// it holds zero sessions by definition.
				continue
			}
			census.DirErrors++
			continue
		}
		census.ScannedDirs++
		seen := map[string]bool{} // per-dir stem dedup: plaintext wins over a leftover archive
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			var stem string
			var archived bool
			switch {
			case store.IsSessionEventLogName(name):
				stem = strings.TrimSuffix(name, store.SessionEventLogSuffix)
			case strings.HasSuffix(name, store.SessionEventLogZstSuffix):
				stem = strings.TrimSuffix(name, store.SessionEventLogZstSuffix)
				archived = true
			default:
				continue // transcripts, leases, meta, .damaged/.rotating/.materializing siblings
			}
			if seen[stem] {
				continue
			}
			seen[stem] = true
			// An archive next to its plaintext is a mid-compression leftover:
			// the plaintext is the source of truth (session_events_zst.go 稳态
			// 约定), so the pair counts once, as plaintext.
			plainStat, plainErr := os.Stat(filepath.Join(dir, stem+store.SessionEventLogSuffix))
			pairArchived := archived && (plainErr != nil || plainStat.IsDir())
			plainBytes := int64(0)
			if !pairArchived && plainErr == nil && !plainStat.IsDir() {
				plainBytes = plainStat.Size()
			}
			cls, size := classifySessionEventLogHead(filepath.Join(dir, stem+".jsonl"))
			census.EventLogSessions++
			if pairArchived {
				census.ArchivedOnly++
			}
			if size > 0 {
				plainBytes = size
			}
			census.TotalLogBytes += plainBytes
			switch cls {
			case eventLogClassDAG:
				census.DAGSessions++
				if pairArchived {
					census.DAGArchivedOnly++
				}
			case eventLogClassNative:
				census.NativeSessions++
			default:
				census.OtherSessions++
			}
		}
	}
	return census
}

// sessionEventLogClass is the census-side classification of one event-log
// head. It mirrors probeSessionEventLog's schema switch without its
// materializing/budget side effects: the census must not repair, heal, or
// decompress-to-disk anything.
type sessionEventLogClass int

const (
	eventLogClassForeign sessionEventLogClass = iota // present, head not decodable as a native schema
	eventLogClassNative                              // schema-1 replace/append head
	eventLogClassDAG                                 // schema-2 DAG head (probe.dag hit)
	eventLogClassFuture                              // newer schema than this build
)

// classifySessionEventLogHead reads the head of the session's event log
// (plaintext when present, else the zst archive decoded in memory) and
// reports its class plus the log's byte size (archive: the header's
// plain_bytes). Missing logs and unreadable heads classify as foreign — the
// census counts sessions, and a sidecar it cannot own is not a DAG session.
func classifySessionEventLogHead(sessionPath string) (sessionEventLogClass, int64) {
	plain := store.SessionEventLog(sessionPath)
	if info, err := os.Stat(plain); err == nil && !info.IsDir() {
		cls, size := classifyPlainEventLogHead(plain)
		if cls != eventLogClassForeign || size > 0 {
			return cls, size
		}
		// Plaintext present but not ours at the head: fall through to the
		// archive check so a mid-compression leftover pair still classifies
		// by the archive bytes (plaintext wins only when it decodes).
	}
	return classifyArchivedEventLogHead(sessionPath)
}

// classifyPlainEventLogHead decodes the bounded head window of a plaintext
// event log — the same fast-path header probe the runtime probe uses.
func classifyPlainEventLogHead(plain string) (sessionEventLogClass, int64) {
	f, err := os.Open(plain)
	if err != nil {
		return eventLogClassForeign, 0
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() == 0 {
		return eventLogClassForeign, 0
	}
	schemaVersion, eventType, ok := probeSessionEventHeader(f)
	if !ok {
		return eventLogClassForeign, info.Size()
	}
	return classFromSchema(schemaVersion, eventType), info.Size()
}

// classifyArchivedEventLogHead decodes the archive's self-describing header
// line and the bounded head window of the payload in memory. Read-only: no
// materialization, no temp files — the archive stays exactly where it is.
//
// The header line and the schema probe share ONE bufio owned here. The shared
// readZstArchiveHeader helper cannot be used for this: its internal bufio is
// discarded on return and its read-ahead (up to 64KiB) may swallow the very
// bytes the probe needs next — measured 2026-10-28 on a production-shaped
// archive (65385 bytes lost), which misclassified a DAG archive as foreign.
// Keeping the buffer alive keeps every read-ahead byte readable by the probe.
func classifyArchivedEventLogHead(sessionPath string) (sessionEventLogClass, int64) {
	zst := sessionEventLogZstPathFor(sessionPath)
	if zst == "" {
		return eventLogClassForeign, 0
	}
	f, err := os.Open(zst)
	if err != nil {
		return eventLogClassForeign, 0
	}
	defer f.Close()
	dec, release, err := sessionEventZstStreamDecoder(f)
	if err != nil {
		return eventLogClassForeign, 0
	}
	defer release()
	buffered := bufio.NewReaderSize(dec.IOReadCloser(), sessionEventZstHeaderMax)
	line, err := buffered.ReadBytes('\n')
	if err != nil || len(line) > sessionEventZstHeaderMax {
		return eventLogClassForeign, 0
	}
	var header sessionEventZstHeader
	if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &header); err != nil || header.Kind != sessionEventZstKind {
		return eventLogClassForeign, 0
	}
	schemaVersion, eventType, ok := probeSessionEventHeader(buffered)
	if !ok {
		return eventLogClassForeign, header.PlainBytes
	}
	return classFromSchema(schemaVersion, eventType), header.PlainBytes
}

// classFromSchema mirrors probeSessionEventLog's schema switch (same
// constants, same precedence) so the census and the runtime probe cannot
// disagree about what "probe.dag hit" means.
func classFromSchema(schemaVersion int, eventType string) sessionEventLogClass {
	switch {
	case schemaVersion == sessionEventSchemaVersion &&
		(eventType == sessionEventTypeReplace || eventType == sessionEventTypeAppend):
		return eventLogClassNative
	case schemaVersion == sessionDAGSchemaVersion:
		return eventLogClassDAG
	case schemaVersion > sessionDAGSchemaVersion:
		return eventLogClassFuture
	default:
		return eventLogClassForeign
	}
}
