package main

// tl1367 line 1, interaction-timing half: the occupancy question must surface
// when the user STARTS a delete/close (the trash confirm), not after the
// archive fails and a force dialog appears. ProbeSessionOccupancy is the
// read-only preflight the frontend calls before running a destructive session
// operation: it answers busy plus WHO holds the session (holder class, pid,
// process name/image path, hostname, acquire time, writer id).
//
// Advisory only: the authoritative refusal still happens inside the archive
// itself (removal guard under the mutation gates), so a stale probe can never
// let a destructive op through.

import (
	"strings"
	"time"

	"reasonix/internal/agent"
)

// SessionOccupancyInfo is the preflight answer for one session. PID/Name/Path
// identify the holder PROCESS (the image path is the program on disk, not a
// session path); Detail carries the same English sentence the refusal would
// render, so an older frontend can fall back to it unchanged.
type SessionOccupancyInfo struct {
	Busy       bool   `json:"busy"`
	Class      string `json:"class,omitempty"` // self | zombie | reused | live | unknown
	PID        int    `json:"pid,omitempty"`
	Name       string `json:"name,omitempty"`
	Path       string `json:"path,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	AcquiredAt string `json:"acquiredAt,omitempty"` // RFC3339, UTC
	WriterID   string `json:"writerId,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

// ProbeSessionOccupancy reports whether sessionPath's lease would refuse a
// destructive operation right now, and the holder identity when it would.
// Unknown/empty paths answer not-busy: there is nothing to warn about.
func (a *App) ProbeSessionOccupancy(sessionPath string) SessionOccupancyInfo {
	key := sessionRuntimeKey(strings.TrimSpace(sessionPath))
	if key == "" {
		return SessionOccupancyInfo{}
	}
	info, _ := agent.LoadSessionLeaseInfo(key)
	selfBusy := agent.SessionLeaseHeldByCurrentRuntime(key)
	_, externallyLocked, inspectErr := agent.InspectSessionLease(key)
	externalBusy := inspectErr == nil && externallyLocked
	if !selfBusy && !externalBusy {
		// Not busy now. A foreign info record on a free lock self-cleans via
		// the probe inside SessionLeaseHeldByOtherRuntime; nothing to report.
		return SessionOccupancyInfo{}
	}
	out := SessionOccupancyInfo{Busy: true}
	out.Class = sessionLeaseHolderClass(info, desktopLeaseAlive)
	if info == nil || info.PID <= 0 {
		out.Detail = errSessionBusyElsewhere.Error()
		return out
	}
	out.PID = info.PID
	if name, path := agent.ProcessImageName(info.PID); strings.TrimSpace(name) != "" || strings.TrimSpace(path) != "" {
		out.Name, out.Path = strings.TrimSpace(name), strings.TrimSpace(path)
	}
	out.Hostname = strings.TrimSpace(info.Hostname)
	if !info.AcquiredAt.IsZero() {
		out.AcquiredAt = info.AcquiredAt.UTC().Format(time.RFC3339)
	}
	out.WriterID = strings.TrimSpace(info.WriterID)
	out.Detail = sessionLeaseBusyDetail(&agent.SessionLeaseError{Path: key, Info: info})
	return out
}
