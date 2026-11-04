package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"reasonix/internal/agent"
)

// tl1367 line 1+2: the shared holder-identity module behind every desktop
// lease-busy surface — classification, machine block, detail text, probe.

func holderIdentityAliveForTest(t *testing.T, alive func(int) bool, image func(int) (string, string)) {
	t.Helper()
	origAlive := desktopLeaseAlive
	desktopLeaseAlive = alive
	origImage := agent.ProcessImageName
	agent.ProcessImageName = image
	t.Cleanup(func() {
		desktopLeaseAlive = origAlive
		agent.ProcessImageName = origImage
	})
}

func TestSessionLeaseHolderClassBranches(t *testing.T) {
	holderIdentityAliveForTest(t,
		func(pid int) bool { return pid == 2 || pid == 3 || pid == 4 },
		func(pid int) (string, string) {
			switch pid {
			case 2:
				return "reasonix-desktop.exe", `C:\Apps\reasonix-desktop.exe`
			case 3:
				return "chrome.exe", `C:\Apps\chrome.exe`
			default:
				return "other.exe", ""
			}
		})

	cases := map[string]struct {
		pid  int
		want string
	}{
		"self":   {os.Getpid(), "self"},
		"live":   {2, "live"},
		"reused": {3, "reused"},
		// pid 4 is alive per the seam but its image is unrelated — the reused
		// verdict outranks the generic alive family.
		"reused-other": {4, "reused"},
		"zombie":       {999, "zombie"},
	}
	for name, tc := range cases {
		info := &agent.SessionLeaseInfo{PID: tc.pid}
		if got := sessionLeaseHolderClass(info, desktopLeaseAlive); got != tc.want {
			t.Fatalf("%s: class = %q, want %q", name, got, tc.want)
		}
	}
	if got := sessionLeaseHolderClass(nil, desktopLeaseAlive); got != "unknown" {
		t.Fatalf("nil info class = %q, want unknown", got)
	}
	if got := sessionLeaseHolderClass(&agent.SessionLeaseInfo{PID: 0}, desktopLeaseAlive); got != "unknown" {
		t.Fatalf("pid-0 class = %q, want unknown", got)
	}
}

// holderBlockShape parses the [reasonix-holder k="v" ...] prefix out of a
// longer detail string (values are sanitized to never contain " or ]).
var holderBlockShape = regexp.MustCompile(`\[reasonix-holder ([^\]]+)\]`)

func parseHolderBlock(t *testing.T, text string) map[string]string {
	t.Helper()
	m := map[string]string{}
	match := holderBlockShape.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("text %q does not carry a holder block", text)
	}
	pair := regexp.MustCompile(`(\w+)="([^"]*)"`)
	for _, kv := range pair.FindAllStringSubmatch(match[1], -1) {
		m[kv[1]] = kv[2]
	}
	return m
}

func TestSessionLeaseHolderBlockCarriesFullIdentity(t *testing.T) {
	holderIdentityAliveForTest(t, func(int) bool { return true }, func(int) (string, string) {
		return "reasonix-desktop.exe", `C:\Program Files\Reasonix\reasonix-desktop.exe`
	})
	info := &agent.SessionLeaseInfo{
		PID:       4321,
		Hostname:  "DESKTOP-XYZ",
		AcquiredAt: time.Date(2026, 11, 3, 4, 34, 56, 0, time.UTC),
		WriterID:  "writer-abc",
	}
	block := sessionLeaseHolderBlock("live", info)
	fields := parseHolderBlock(t, block)
	for k, want := range map[string]string{
		"class":     "live",
		"pid":       "4321",
		"name":      "reasonix-desktop.exe",
		"path":      `C:\Program Files\Reasonix\reasonix-desktop.exe`,
		"host":      "DESKTOP-XYZ",
		"acquired":  "2026-11-03T04:34:56Z",
		"writer":    "writer-abc",
	} {
		if fields[k] != want {
			t.Fatalf("block field %s = %q, want %q (block: %s)", k, fields[k], want, block)
		}
	}
	if sessionLeaseHolderBlock("live", nil) != "" {
		t.Fatal("nil info must produce no block so the text degrades to the plain sentinel")
	}
}

func TestSessionLeaseBusyDetailPrefixesHolderBlock(t *testing.T) {
	holderIdentityAliveForTest(t, func(int) bool { return true }, func(int) (string, string) {
		return "reasonix-desktop.exe", `C:\Program Files\Reasonix\reasonix-desktop.exe`
	})
	leaseErr := &agent.SessionLeaseError{Path: "C:\\hidden\\session.jsonl", Info: &agent.SessionLeaseInfo{PID: 4321, Hostname: "H"}}
	detail := sessionLeaseBusyDetail(&sessionLeaseHeldBusyError{cause: leaseErr})
	if !strings.HasPrefix(detail, "[reasonix-holder ") {
		t.Fatalf("detail must start with the machine holder block, got %q", detail)
	}
	if !strings.Contains(detail, "close that window") {
		t.Fatalf("detail must keep the actionable sentence, got %q", detail)
	}
	if strings.Contains(detail, "C:\\hidden\\session.jsonl") {
		t.Fatalf("the session path must stay sanitized, got %q", detail)
	}
	fields := parseHolderBlock(t, detail)
	if fields["pid"] != "4321" || fields["class"] != "live" {
		t.Fatalf("holder block fields wrong: %#v", fields)
	}
}

func TestSessionLeaseBusyDetailReusedPidNamesOwnerProgram(t *testing.T) {
	holderIdentityAliveForTest(t, func(int) bool { return true }, func(int) (string, string) {
		return "indexer-pro.exe", `C:\tools\indexer-pro.exe`
	})
	detail := sessionLeaseBusyDetail(&sessionLeaseHeldBusyError{cause: &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: 5555}}})
	if !strings.Contains(detail, "stale lease record") || !strings.Contains(detail, "indexer-pro.exe") {
		t.Fatalf("reused-pid detail = %q, must name the stale record and the owning program", detail)
	}
	classification := sessionLeaseBusyClassification(&sessionLeaseHeldBusyError{cause: &agent.SessionLeaseError{Path: "p", Info: &agent.SessionLeaseInfo{PID: 5555}}})
	if !errors.Is(classification, errSessionBusyZombie) {
		t.Fatalf("a reused pid is a stale record and must classify into the zombie sentinel family, got %v", classification)
	}
}

// ProbeSessionOccupancy: a real in-process lease makes the probe answer busy
// with the self class; after release it answers not busy.
func TestProbeSessionOccupancySelfHeldLease(t *testing.T) {
	app := &App{}
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "probe-target.jsonl")
	if err := os.WriteFile(sessionPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if app.ProbeSessionOccupancy(sessionPath).Busy {
		t.Fatal("a fresh session must probe not busy")
	}
	lease, err := agent.TryAcquireSessionLease(sessionPath)
	if err != nil {
		t.Fatalf("lease acquire failed: %v", err)
	}
	defer lease.Release()
	probe := app.ProbeSessionOccupancy(sessionPath)
	if !probe.Busy || probe.Class != "self" || probe.PID == 0 {
		t.Fatalf("self-held probe = %+v, want busy self with pid", probe)
	}
	// tl1367-fix defect B: the self guidance names THIS window and the real
	// next step (close the session tab / stop the run) — never "another
	// runtime/window".
	if probe.Detail == "" || !strings.Contains(probe.Detail, "running in this Reasonix window") {
		t.Fatalf("probe detail = %q, want the self-holder sentence", probe.Detail)
	}
	if strings.Contains(probe.Detail, "another runtime") || strings.Contains(probe.Detail, "another Reasonix window") {
		t.Fatalf("probe detail = %q, must not misdirect to another runtime/window", probe.Detail)
	}
	lease.Release()
	if app.ProbeSessionOccupancy(sessionPath).Busy {
		t.Fatal("after release the probe must answer not busy")
	}
}

// tl1367-fix defect A regression: the single-archive chain used to render only
// the classification sentinel in Error(), so the frontend could never parse a
// holder block and every lease-busy toast degraded to the generic "another
// Reasonix window" copy — even when the holder was this very window. The
// message must carry the machine-readable holder block (same text the batch
// failure list renders), while both compat faces stay reachable.
func TestSessionLeaseHeldBusyErrorMessageCarriesHolderBlock(t *testing.T) {
	restoreAlive := desktopLeaseAlive
	desktopLeaseAlive = func(int) bool { return true }
	defer func() { desktopLeaseAlive = restoreAlive }()

	selfErr := &agent.SessionLeaseError{
		Path: "session.jsonl",
		Info: &agent.SessionLeaseInfo{PID: os.Getpid(), WriterID: "writer-x", Hostname: "host-x"},
	}
	msg := (&sessionLeaseHeldBusyError{cause: selfErr}).Error()
	if !strings.Contains(msg, `[reasonix-holder class="self"`) {
		t.Fatalf("message missing self holder block: %q", msg)
	}
	if strings.Contains(msg, "session.jsonl") {
		t.Fatalf("message leaks the session path: %q", msg)
	}

	// Compat faces: the sentinel classification and the original lease error
	// both stay reachable through Unwrap/errors.
	wrapped := &sessionLeaseHeldBusyError{cause: selfErr}
	if !errors.Is(wrapped, errSessionBusyElsewhere) {
		t.Fatal("errors.Is(errSessionBusyElsewhere) failed on the wrapped refusal")
	}
	var leaseErr *agent.SessionLeaseError
	if !errors.As(wrapped, &leaseErr) || leaseErr.Info == nil || leaseErr.Info.PID != os.Getpid() {
		t.Fatal("errors.As(*agent.SessionLeaseError) failed on the wrapped refusal")
	}

	// No usable identity (tl1383): the message renders the leftover copy —
	// never the "another window" fallback, whose guidance (close the other
	// window) is unexecutable when nobody can name the holder.
	bare := (&sessionLeaseHeldBusyError{cause: &agent.SessionLeaseError{Path: "session.jsonl"}}).Error()
	if bare != errSessionBusyUnidentifiedHolder.Error() {
		t.Fatalf("identity-less message = %q, want the leftover copy %q", bare, errSessionBusyUnidentifiedHolder.Error())
	}
}
