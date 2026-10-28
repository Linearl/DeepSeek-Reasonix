package agent

// tl-1220（tl1173 V4 观测计数落地）——纯观测，零行为影响：
//
//   - 计数①：save 链图获取（dagStateForSave）整本重放时守卫原因 reason=nil_cache
//     且日志字节数 >32MiB 的组合计数。口径来自 tl1173 评估 rev2.1 §五（830 §3.5
//     建议 3）：量化「冷启动首存/首触」——进程图缓存为空时对大日志的整本重放——
//     的真实占比。32MiB 阈值复用 sessionTranscriptTailThresholdBytes（首绘尾部
//     窗口阈值，session_load.go），与评估报告引用的是同一个常量，不另立口径。
//     上报通道 = 现有 telemetry（slog 结构化行，落 desktop.log）+ 导出 getter
//     （进程内可查，同 SessionGraphCacheStats 惯例）。
//   - 计数②：存量 DAG 会话数——全库 events 侧车 probe.dag 命中的会话数
//     （tl1173 §4.3 读兼容退场条件计数源：计数 <10 或时间 ≥3 个月触发评估）。
//     查询口 = `reasonix dag-sessions`（internal/cli/dagsessions.go，只读）。
//
// 零行为影响的自证边界：
//   - 计数①的调用点只挂在整本重放分支内部（extended 快路径连函数都不进）；
//     计数器无任何控制流读者（仅 getter 读取），日志行只出现在刚付完整本重放
//     代价的路径上（一行 slog 相对重放本身是噪声）。
//   - 计数②的普查是只读扫描：不走 limitsForSessionLog（那会触发 zst 物化落盘），
//     归档态日志在内存里解头部窗口分类，绝不写盘（测试钉住）。

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"reasonix/internal/store"
)

// sessionDAGNilCacheLargeReplays 计数①：reason=nil_cache 且 log_bytes>32MiB
// 的 save 链整本重放累计次数。进程内累计，无持久化——与图缓存命中计数同口径
// （运行期观测，装机读数看日志行里的 count_total）。
var sessionDAGNilCacheLargeReplays atomic.Uint64

// SessionDAGNilCacheLargeReplays returns the cumulative count of save-chain
// full DAG replays whose guard reason was nil_cache while the replayed log
// exceeded the 32MiB tail threshold (tl-1220 counter ①). Observation only:
// nothing reads it for control flow.
func SessionDAGNilCacheLargeReplays() uint64 {
	return sessionDAGNilCacheLargeReplays.Load()
}

// noteSessionDAGNilCacheLargeReplay is the single observation point for
// counter ①. Called from dagStateForSave only on the full-replay branch, with
// the guard reason that forced the replay and the replayed byte count
// (st.size, which a full replay sets to the log's file size). Extended fast
// paths never reach here, so the hot save loop pays one string compare at
// most — and only after it already paid a full replay.
func noteSessionDAGNilCacheLargeReplay(reason string, logBytes int64) {
	if reason != "nil_cache" || logBytes <= sessionTranscriptTailThresholdBytes {
		return
	}
	total := sessionDAGNilCacheLargeReplays.Add(1)
	slog.Info("session: dag nil_cache large log full replay (tl-1220 observation)",
		"reason", reason,
		"log_bytes", logBytes,
		"threshold_bytes", int64(sessionTranscriptTailThresholdBytes),
		"count_total", total)
}

// SessionDAGCensus is the tl-1220 counter ② reading over a set of session
// directories: how many stored sessions carry an event-log sidecar, and how
// many of those sidecars classify as DAG (probe.dag hit) — the tl1173 §4.3
// retirement-condition source.
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
