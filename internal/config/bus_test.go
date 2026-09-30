package config

import (
	"strings"
	"testing"
)

// The bus role table is one fail-closed unit: one bad role, one empty token
// or one shared token must poison the WHOLE table, because a half-broken
// table must never become a half-open mailbox (the endpoint refuses to mount
// on it and the agent side refuses to resolve from it).
func TestValidateBusRolesFailClosed(t *testing.T) {
	valid := map[string]string{"dev": "tok-a", "heartbeat": "tok-b"}
	if err := ValidateBusRoles(valid); err != nil {
		t.Fatalf("valid table must pass: %v", err)
	}
	if err := ValidateBusRoles(nil); err != nil {
		t.Fatalf("empty table is not an error here (the enabled gate decides): %v", err)
	}
	cases := []struct {
		name  string
		table map[string]string
	}{
		{"invalid name", map[string]string{"Bad_Role": "t"}},
		{"path-ish name", map[string]string{"../evil": "t"}},
		{"leading dash", map[string]string{"-x": "t"}},
		{"too long", map[string]string{strings.Repeat("a", 33): "t"}},
		{"empty token", map[string]string{"dev": "   "}},
		{"duplicate token", map[string]string{"dev": "t", "hb": "t"}},
	}
	for _, tc := range cases {
		if err := ValidateBusRoles(tc.table); err == nil {
			t.Fatalf("%s: want error, got nil", tc.name)
		}
	}
}

// Deterministic reporting: the alphabetically first violation names itself,
// regardless of map iteration order.
func TestValidateBusRolesDeterministicError(t *testing.T) {
	table := map[string]string{"zz-bad": "t", "aa-bad": "t"}
	err := ValidateBusRoles(table)
	if err == nil || !strings.Contains(err.Error(), `"aa-bad"`) {
		t.Fatalf("want the sorted-first role reported, got %v", err)
	}
}

// The agent-side contact table (bus #1): enrolled roles become zcode-<role>
// contacts only while the bus is enabled AND the table validates; the worker
// pool contact joins only while the pool is enabled and only when it carries
// the synthetic-contact shape.
func TestBusContactsFrom(t *testing.T) {
	mk := func(enabled bool, roles map[string]string, workerEnabled bool, contact string) *Config {
		cfg := Default()
		cfg.Serve.BusMCP.Enabled = enabled
		cfg.Serve.BusMCP.Roles = roles
		cfg.Serve.BusWorker.Enabled = workerEnabled
		cfg.Serve.BusWorker.Contact = contact
		return cfg
	}

	if got := busContactsFrom(mk(true, map[string]string{"dev": "t", "heartbeat": "h"}, false, "")); len(got) != 2 ||
		got[0] != "zcode-dev" || got[1] != "zcode-heartbeat" {
		t.Fatalf("roles must map to sorted zcode contacts, got %v", got)
	}

	if got := busContactsFrom(mk(false, map[string]string{"dev": "t"}, true, "")); len(got) != 1 || got[0] != "zcode-worker" {
		t.Fatalf("bus off + pool on: only the pool contact is addressable, got %v", got)
	}

	// Fail closed on the whole role table: one broken role removes every role
	// contact, not just itself.
	if got := busContactsFrom(mk(true, map[string]string{"dev": "t", "Bad_Role": "t"}, false, "")); got != nil {
		t.Fatalf("invalid table must yield no contacts, got %v", got)
	}
	if got := busContactsFrom(mk(true, map[string]string{"dev": "t", "hb": "t"}, false, "")); got != nil {
		t.Fatalf("shared token must yield no contacts, got %v", got)
	}

	// The pool contact is free-form config, so the shape gate decides:
	// default fills in, zcode-shaped custom passes, anything else stays
	// unaddressable.
	if got := busContactsFrom(mk(false, nil, true, "")); len(got) != 1 || got[0] != "zcode-worker" {
		t.Fatalf("empty contact must default to zcode-worker, got %v", got)
	}
	if got := busContactsFrom(mk(false, nil, true, "zcode-build-pool")); len(got) != 1 || got[0] != "zcode-build-pool" {
		t.Fatalf("zcode-shaped custom contact must pass, got %v", got)
	}
	if got := busContactsFrom(mk(false, nil, true, "../evil")); got != nil {
		t.Fatalf("traversal-shaped contact must be dropped, got %v", got)
	}
	if got := busContactsFrom(mk(false, nil, true, "agent")); got != nil {
		t.Fatalf("non-zcode contact must be dropped, got %v", got)
	}

	if got := busContactsFrom(mk(false, nil, false, "")); got != nil {
		t.Fatalf("everything off must yield no contacts, got %v", got)
	}
	if got := busContactsFrom(nil); got != nil {
		t.Fatalf("nil config must yield no contacts, got %v", got)
	}
}

// The bus mailbox resolution mirrors busmcp.New: an explicit mail_dir wins,
// anything else defers to the shared session-collab directory.
func TestBusMailDirFrom(t *testing.T) {
	if got := busMailDirFrom(nil); got != "" {
		t.Fatalf("nil config must defer, got %q", got)
	}
	cfg := Default()
	if got := busMailDirFrom(cfg); got != "" {
		t.Fatalf("unset mail_dir must defer to the shared dir, got %q", got)
	}
	cfg.Serve.BusMCP.MailDir = "  D:/bus-mail  "
	if got := busMailDirFrom(cfg); got != "D:/bus-mail" {
		t.Fatalf("explicit mail_dir must win trimmed, got %q", got)
	}
}
