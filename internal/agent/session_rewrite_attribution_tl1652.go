package agent

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"reasonix/internal/store"
)

// tl1652：改写归因完成即落盘。
//
// 病灶（tl1651 追查现场）：Session.Rewrite 把 provider-visible 改写原因
// （compact / snip / rewind_truncate / rewind_restore / guardian_merge /
// tool_guard_before_sampling / system_prompt_refresh / projection note）排进
// pendingContentReasons（纯内存队列），唯一消费者是下一次 provider 请求前的
// DrainContentRewriteReasons（run_loop.go）。改写后、下次请求前关机 = 归因
// 永久丢失——20261116 13:35:55-14:44 窗口的指纹过期归因就是这样丢的。崩溃面
// 同理：队列只在进程内存里。
//
// 修复原则（用户令 20261116：完成就要落盘）：产生归因的那一刻同步追加一行
// JSON 到会话兄弟侧车 <stem>.rewritelog.jsonl（与 .repairlog.jsonl 同惯例，
// 追加式审计，不进会话列表，删会话一并清）。drain 归因机制原样保留——本
// 文件只补持久化，不碰 CompareShape 的归因语义（non-goals）。
//
// 落盘时机分两档：
//   - baseline 已建立（session 有 persisted.path）：产生即写（flush 在
//     s.mu 之外，锁内只做一次 slice append，不进 save/锁时序）；
//   - baseline 未建立（首个 save 之前，路径未知）：记录滞留内存待写缓冲，
//     保存成功建立 baseline 后由 saveObservedAfterUpgrade 补写（关机前有
//     save 就不丢；从未保存过的会话本来就没有可归因的盘上形态）。
// 写失败：slog.Warn 后放回缓冲带重试（上限 rewriteAttributionMaxPending，
// 超限丢最旧——永久性失败不无限堆积）。缓冲滞留同样受该上限保护。

// rewriteAttributionMaxPending bounds the not-yet-written attribution buffer:
// transient write failures retry on the next trigger; a permanent failure
// (disk full, permissions) must not grow the buffer without bound.
const rewriteAttributionMaxPending = 64

// RewriteAttributionRecord is one line of <stem>.rewritelog.jsonl. Fields are
// fixed-shape (no omitempty) so readers can rely on every key being present.
type RewriteAttributionRecord struct {
	// At is the UTC RFC3339 timestamp of the moment the rewrite landed.
	At string `json:"at"`
	// Writer is SessionWriterID() — which process wrote this rewrite.
	Writer string `json:"writer"`
	// Source names the queue entry point: "rewrite" (Session.Rewrite),
	// "replace" (Session.Replace, provider-visible but reasonless),
	// "note" (NoteContentRewrite), "system_prompt"
	// (SetLeadingSystemPromptWithReason), "tool_guard"
	// (updateBatchGuardResults).
	Source string `json:"source"`
	// Reason is the rewrite reason exactly as queued for drain attribution
	// ("compact_auto", "snip", "rewind_truncate", …). Empty only for
	// source="replace".
	Reason string `json:"reason"`
	// RewriteVersion is the Session.rewriteVersion AFTER this rewrite's bump.
	RewriteVersion int `json:"rewrite_version"`
	// BeforeMsgs / AfterMsgs are transcript lengths around the rewrite;
	// -1 when the entry point has no before/after shape (note / system
	// prompt / tool guard).
	BeforeMsgs int `json:"before_msgs"`
	AfterMsgs  int `json:"after_msgs"`
}

// rewriteAttributionSources are the valid Source values; the manifest test
// pins the queue call sites against this set.
const (
	attributionSourceRewrite     = "rewrite"
	attributionSourceReplace     = "replace"
	attributionSourceNote        = "note"
	attributionSourceSystemPmt   = "system_prompt"
	attributionSourceToolGuard   = "tool_guard"
	attributionSourceUnspecified = "unspecified"
)

// queueRewriteAttributionLocked appends one record to the durable-write
// buffer under s.mu. Buffer overflow drops the OLDEST record (the sidecar
// write, not this buffer, is the durable trail) with one WARN per drop.
func (s *Session) queueRewriteAttributionLocked(rec RewriteAttributionRecord) {
	if len(s.pendingAttribution) >= rewriteAttributionMaxPending {
		dropped := s.pendingAttribution[0]
		s.pendingAttribution = append(s.pendingAttribution[:0], s.pendingAttribution[1:]...)
		slog.Warn("session: rewrite attribution buffer overflow; oldest record dropped before sidecar write",
			"dropped_reason", dropped.Reason, "dropped_at", dropped.At, "cap", rewriteAttributionMaxPending)
	}
	s.pendingAttribution = append(s.pendingAttribution, rec)
}

// flushRewriteAttribution drains the not-yet-written buffer to the sidecar.
// Called outside s.mu (callers must not hold it): file I/O never belongs
// inside the session lock. A write failure returns the records to the front
// of the buffer (capped) so the next trigger retries them.
func (s *Session) flushRewriteAttribution() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if len(s.pendingAttribution) == 0 {
		s.mu.Unlock()
		return
	}
	path := s.persisted.path
	if path == "" {
		// No persistence baseline yet: the sidecar target does not exist.
		// Keep buffering; the first successful save re-arms the flush.
		s.mu.Unlock()
		return
	}
	recs := s.pendingAttribution
	s.pendingAttribution = nil
	s.mu.Unlock()

	logPath := store.SessionRewriteAttributionLog(path)
	if err := appendRewriteAttributionRecords(logPath, recs); err != nil {
		slog.Warn("session: rewrite attribution sidecar write failed; records returned to buffer for retry",
			"path", logPath, "records", len(recs), "err", err)
		s.mu.Lock()
		room := rewriteAttributionMaxPending - len(s.pendingAttribution)
		if room > 0 && room < len(recs) {
			recs = recs[len(recs)-room:]
			slog.Warn("session: rewrite attribution retry buffer truncated",
				"kept", len(recs), "cap", rewriteAttributionMaxPending)
		}
		s.pendingAttribution = append(recs, s.pendingAttribution...)
		s.mu.Unlock()
	}
}

// appendRewriteAttributionRecords appends records as JSON lines. A failed
// write aborts the whole batch (the caller re-queues it) so the sidecar never
// holds a partial ordering ambiguity.
func appendRewriteAttributionRecords(logPath string, recs []RewriteAttributionRecord) error {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, rec := range recs {
		blob, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(blob, '\n')); err != nil {
			return err
		}
	}
	return nil
}

// ListSessionRewriteAttribution reads a session's rewrite attribution journal
// oldest first. A missing log is an empty list, not an error — a session with
// no rewrite history never grew one.
func ListSessionRewriteAttribution(sessionPath string) ([]RewriteAttributionRecord, error) {
	logPath := store.SessionRewriteAttributionLog(sessionPath)
	if logPath == "" {
		return nil, nil
	}
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []RewriteAttributionRecord
	dec := json.NewDecoder(f)
	for {
		var rec RewriteAttributionRecord
		if err := dec.Decode(&rec); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// newRewriteAttributionRecord stamps the common fields of one record.
func newRewriteAttributionRecord(source, reason string, rewriteVersion int) RewriteAttributionRecord {
	return RewriteAttributionRecord{
		At:             time.Now().UTC().Format(time.RFC3339Nano),
		Writer:         SessionWriterID(),
		Source:         source,
		Reason:         reason,
		RewriteVersion: rewriteVersion,
		BeforeMsgs:     -1,
		AfterMsgs:      -1,
	}
}
