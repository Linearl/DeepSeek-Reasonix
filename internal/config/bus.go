package config

// The task-bus identity table: validation and live accessors shared by every
// face of the bus.
//
// Identity model (internal/busmcp): an enrolled role ("dev") maps to a
// synthetic mailbox contact "zcode-<role>". Three faces need the SAME table
// rules — the bus endpoint (busmcp.New refuses to mount on a broken table),
// the headless pool (busworker drains one contact), and, since bus #1, the
// agent-side talk_to_session which delivers to those synthetic contacts. The
// rules therefore live HERE (config owns the user-config data; busmcp imports
// config, so the dependency cannot point the other way), and busmcp.New calls
// into this file — one source, no drift between the endpoint and the agent.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// BusRolePattern constrains bus role names. A role becomes both a config key
// and the suffix of a mailbox filename ("zcode-<role>.inbox.jsonl"), so
// anything outside this class could turn a hand-edited config into a path
// traversal on the mailbox directory.
var BusRolePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// zcodeContactPattern is the full synthetic-contact shape: the "zcode-"
// prefix over a valid role. The pool contact (bus_worker.contact) is
// free-form config, so this gate — not trust in the config author — is what
// keeps a weird value unaddressable.
var zcodeContactPattern = regexp.MustCompile(`^zcode-[a-z0-9][a-z0-9-]{0,31}$`)

// busWorkerDefaultContact mirrors busworker's default pool contact. busworker
// owns the pool semantics; config only needs the value to apply the same
// shape gate to the effective (possibly empty) contact name.
const busWorkerDefaultContact = "zcode-worker"

// ValidateBusRoles applies the bus endpoint's fail-closed rules to a
// role→token table: every role name matches BusRolePattern, every token is
// non-empty, and no token is shared between two roles. Any violation fails
// the WHOLE table — the endpoint refuses to mount on it and the agent side
// refuses to resolve any contact from it, so a half-broken table can never
// become a half-open mailbox. Roles are checked in sorted order so the
// reported violation is deterministic.
func ValidateBusRoles(roles map[string]string) error {
	names := make([]string, 0, len(roles))
	for role := range roles {
		names = append(names, role)
	}
	sort.Strings(names)
	seen := map[string]string{}
	for _, role := range names {
		if !BusRolePattern.MatchString(role) {
			return fmt.Errorf("bus role %q must match [a-z0-9-]+ (max 32 chars, start alphanumeric)", role)
		}
		if strings.TrimSpace(roles[role]) == "" {
			return fmt.Errorf("bus role %q has an empty token", role)
		}
		if other, dup := seen[roles[role]]; dup {
			return fmt.Errorf("bus roles %q and %q share one token", other, role)
		}
		seen[roles[role]] = role
	}
	return nil
}

// BusContactsLive answers the addressable bus synthetic contacts
// ("zcode-<role>") for this machine, resolved from the user config AT CALL
// time: every enrolled [serve.bus_mcp] role while the bus is enabled and its
// table validates, plus the [serve.bus_worker] pool contact while the pool is
// enabled. Sorted. Empty when the bus is off, the table is invalid, or the
// config cannot be loaded — fail-closed, matching the endpoint, so a contact
// listed here is guaranteed to have a live bus behind it.
//
// talk_to_session consults this (bus #1) so a Reasonix session can address
// "zcode-dev" or the "zcode-worker" pool exactly like a zcode role can. Read
// per call: an enroll applies to the next send without a restart.
func BusContactsLive() []string {
	cfg, err := LoadUserConfigReadOnly()
	if err != nil || cfg == nil {
		return nil
	}
	return busContactsFrom(cfg)
}

// busContactsFrom is the pure core of BusContactsLive, split out so tests
// can exercise the fail-closed rules without a config file on disk.
func busContactsFrom(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	contacts := make([]string, 0, len(cfg.Serve.BusMCP.Roles)+1)
	if cfg.Serve.BusMCP.Enabled && ValidateBusRoles(cfg.Serve.BusMCP.Roles) == nil {
		for role := range cfg.Serve.BusMCP.Roles {
			contacts = append(contacts, "zcode-"+role)
		}
	}
	if cfg.Serve.BusWorker.Enabled {
		contact := strings.TrimSpace(cfg.Serve.BusWorker.Contact)
		if contact == "" {
			contact = busWorkerDefaultContact
		}
		if zcodeContactPattern.MatchString(contact) {
			contacts = append(contacts, contact)
		}
	}
	if len(contacts) == 0 {
		return nil
	}
	sort.Strings(contacts)
	return contacts
}

// BusMailDirLive resolves the mailbox directory bus contacts receive mail
// in: [serve.bus_mcp].mail_dir when set, the shared session-collab directory
// otherwise — the same resolution busmcp.New applies, so agent-side delivery
// (talk_to_session to a zcode contact) and the bus endpoint write one stream.
func BusMailDirLive() string {
	dir := ""
	if cfg, err := LoadUserConfigReadOnly(); err == nil && cfg != nil {
		dir = busMailDirFrom(cfg)
	}
	if dir == "" {
		dir = SessionCollabMailDir()
	}
	return dir
}

// busMailDirFrom is the pure core of BusMailDirLive, split out so tests can
// exercise the resolution without a config file on disk.
func busMailDirFrom(cfg *Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Serve.BusMCP.MailDir)
}
