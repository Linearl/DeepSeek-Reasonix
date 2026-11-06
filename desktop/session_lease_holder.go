package main

// tl1367 line 1: lease-holder identity for desktop refusals. Every lease-busy
// surface (removal-guard refusal, trash-target probe, occupancy preflight,
// structured error text, desktop log) reads the holder through this module so
// "who is holding this session" has ONE definition: a holder class, the pid,
// the holder process name and image path, hostname, acquire time, and the
// holding runtime's writer id. tl1258 was blocked on exactly this capture; the
// 2026-11-03 archive-refusal incident could not distinguish a self-held orphan
// from a reused pid without it.

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"reasonix/internal/agent"
)

// desktopLeaseAlive is the holder-liveness seam for the desktop lease
// surfaces (mirrors leaseReconcileProcessAlive): production uses
// desktopProcessAlive; tests inject deterministic verdicts.
var desktopLeaseAlive = desktopProcessAlive

// sessionLeaseHolderClass classifies the recorded holder of a lease refusal.
// tl1429 枚举化:分类全集收敛为常量,新增分类必须落在这里,拒绝日志/文案/
// 前端解析共用同一枚举——
//
//	self      — this process holds the lease (another runtime of this window)
//	self_lock — the lock is held by a handle THIS PROCESS booked in the
//	            in-process lock registry while the identity record is missing
//	            or pid-less (the lost-binding self-lock form; tl1429). The
//	            refusal identity comes from the registry, not the record.
//	zombie    — the recorded pid is dead: a crashed-process leftover
//	reused    — the recorded pid is ALIVE but its executable is not this
//	            application: the pid number was reused by an unrelated
//	            program, so the record is stale even though "alive" (the
//	            tl1367 line 3 misjudgment form — pre-fix it read as a live
//	            foreign window)
//	live      — a live process running a Reasonix-like executable: real
//	            cross-process mutual exclusion
//	unknown   — no usable identity anywhere (no record AND no registry
//	            entry); the sentinel text stays generic
const (
	holderClassSelf     = "self"
	holderClassSelfLock = "self_lock"
	holderClassZombie   = "zombie"
	holderClassReused   = "reused"
	holderClassLive     = "live"
	holderClassUnknown  = "unknown"
)

// desktopProcessAlive (not agent.ProcessAlive) is passed by callers that want
// the desktop test seams; both implement the same husk-aware verdict.
func sessionLeaseHolderClass(info *agent.SessionLeaseInfo, alive func(int) bool) string {
	if info == nil || info.PID <= 0 {
		return holderClassUnknown
	}
	if info.PID == os.Getpid() {
		return holderClassSelf
	}
	if !alive(info.PID) {
		return holderClassZombie
	}
	if !agent.ProcessLooksLikeSelf(info.PID) {
		return holderClassReused
	}
	return holderClassLive
}

// sessionLeaseHolderBlock renders the machine-readable holder block that
// prefixes lease-busy detail text (tl1367 line 2 channel). Shape:
//
//	[reasonix-holder class="live" pid="27260" name="Reasonix.exe"
//	   path="C:\...\Reasonix.exe" host="DESKTOP" acquired="2026-11-03T04:34:56Z"
//	   writer="writer-abc"]
//
// Every value is quoted so the frontend parser is one regex over k="v" pairs;
// empty parts are omitted. Returns "" when no usable identity exists — the
// text then degrades to the plain sentinel family. The image path identifies
// the holder program and is explicitly requested by tl1367 line 1; it is not
// a session path and reveals nothing about the session.
func sessionLeaseHolderBlock(class string, info *agent.SessionLeaseInfo) string {
	if info == nil || info.PID <= 0 {
		return ""
	}
	// blockValue quotes verbatim — %q would escape every backslash in a
	// Windows path into \\ and make the human-readable block (and the
	// frontend's k="v" regex) unusable. Characters that would break the block
	// grammar itself (double quote, closing bracket, newline) are stripped.
	blockValue := strings.NewReplacer(`"`, "", "]", "", "\n", " ", "\r", "").Replace
	var b strings.Builder
	b.WriteString(`[reasonix-holder class="` + blockValue(class) + `"`)
	fmt.Fprintf(&b, ` pid="%d"`, info.PID)
	name, imagePath := agent.ProcessImageName(info.PID)
	if strings.TrimSpace(name) != "" {
		b.WriteString(` name="` + blockValue(strings.TrimSpace(name)) + `"`)
	}
	if strings.TrimSpace(imagePath) != "" {
		b.WriteString(` path="` + blockValue(strings.TrimSpace(imagePath)) + `"`)
	}
	if host := strings.TrimSpace(info.Hostname); host != "" {
		b.WriteString(` host="` + blockValue(host) + `"`)
	}
	if !info.AcquiredAt.IsZero() {
		b.WriteString(` acquired="` + info.AcquiredAt.UTC().Format(time.RFC3339) + `"`)
	}
	if writer := strings.TrimSpace(info.WriterID); writer != "" {
		b.WriteString(` writer="` + blockValue(writer) + `"`)
	}
	b.WriteString("]")
	return b.String()
}

// sessionLeaseHolderLogAttrs renders the structured log attributes for a
// refusal (tl1367 line 1: the desktop log must carry the holder identity so a
// retry leaves a traceable record even after the toast is gone). tl1429: when
// the identity record is missing or pid-less (the constant pid=0 form) but
// the in-process lock registry booked the hold, the refusal identity comes
// from the registry — holder_class flips to self_lock and the holder kind and
// hold time are dumped. A pid=0 refusal with a registry hit is never
// zero-information again.
func sessionLeaseHolderLogAttrs(info *agent.SessionLeaseInfo) []any {
	return sessionLeaseHolderLogAttrsWithEvidence(info, nil)
}

func sessionLeaseHolderLogAttrsWithEvidence(info *agent.SessionLeaseInfo, evidence []agent.SessionLockHolderEvidence) []any {
	class := sessionLeaseHolderClass(info, desktopLeaseAlive)
	identityMissing := info == nil || info.PID <= 0
	if identityMissing && len(evidence) > 0 {
		class = holderClassSelfLock
	}
	attrs := []any{"holder_class", class}
	if identityMissing {
		if len(evidence) == 0 {
			return append(attrs, "holder_pid", 0)
		}
		// tl1429 registry identity replaces the bare pid=0: which runtime/op
		// holds the lock and since when. tl1429c: an in-process holder no
		// longer logs holder_pid=0 at all — a pid field that reads "identity
		// unknown" for a holder the registry can name is the misreading the
		// field log kept tripping on. holder_scope=in_process says the holder
		// lives inside this process; the kind/since pair says which flow.
		// Logged values still name no session path — the evidence type is
		// redacted by construction.
		attrs = append(attrs, "holder_scope", "in_process", "holder_lock_kind", evidence[0].Kind)
		if !evidence[0].AcquiredAt.IsZero() {
			attrs = append(attrs, "holder_lock_since", evidence[0].AcquiredAt.UTC().Format(time.RFC3339))
		}
		return attrs
	}
	attrs = append(attrs, "holder_pid", info.PID)
	if name, path := agent.ProcessImageName(info.PID); strings.TrimSpace(name) != "" || strings.TrimSpace(path) != "" {
		attrs = append(attrs, "holder_process", agent.ReasonixProcessLabel(info.PID))
	}
	if host := strings.TrimSpace(info.Hostname); host != "" {
		attrs = append(attrs, "holder_host", host)
	}
	if !info.AcquiredAt.IsZero() {
		attrs = append(attrs, "holder_acquired_at", info.AcquiredAt.UTC().Format(time.RFC3339))
	}
	if writer := strings.TrimSpace(info.WriterID); writer != "" {
		attrs = append(attrs, "holder_writer", writer)
	}
	return attrs
}

// logSessionLeaseHolderRefusal emits one structured WARN for a lease refusal
// with the full holder identity. Logged values name the holder PROCESS, never
// the session path (same sanitization line as the pre-existing refusal logs).
// op is the acquiring flow's tag (tl1429b-2): pairing it with the acquire-side
// success line turns an 08:26-style loop into an attributable timeline.
func logSessionLeaseHolderRefusal(msg string, err error, op string) {
	var leaseErr *agent.SessionLeaseError
	info := (*agent.SessionLeaseInfo)(nil)
	var evidence []agent.SessionLockHolderEvidence
	if errorsAsSessionLeaseError(err, &leaseErr) && leaseErr != nil {
		info = leaseErr.Info
		evidence = leaseErr.HeldInProcess
	}
	attrs := append([]any{"op", op}, sessionLeaseHolderLogAttrsWithEvidence(info, evidence)...)
	slog.Warn(msg, attrs...)
}

// sessionLeaseHeldBusyErrorFromProbe converts a boolean
// SessionLeaseHeldByOtherRuntime verdict into the same identity-carrying busy
// error the removal-guard refusal produces, so probe-path refusals (duplicate
// trash target, restore guards) carry the holder identity instead of the bare
// sentinel. Returns nil when the probe says not busy.
func sessionLeaseHeldBusyErrorFromProbe(sessionPath string) error {
	if !agent.SessionLeaseHeldByOtherRuntime(sessionPath) {
		return nil
	}
	info, _ := agent.LoadSessionLeaseInfo(sessionPath)
	return &sessionLeaseHeldBusyError{cause: &agent.SessionLeaseError{Path: sessionPath, Info: info}}
}

// errorsAsSessionLeaseError is a tiny indirection over errors.As so the refusal
// log helper reads symmetric with the detail renderer below.
func errorsAsSessionLeaseError(err error, target **agent.SessionLeaseError) bool {
	return errors.As(err, target)
}
