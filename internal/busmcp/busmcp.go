// Package busmcp exposes the shared sessioncollab mailbox and task cards as an
// MCP streamable-HTTP endpoint, so an external agent runtime (zcode) can read
// the shared inbox, send cross-runtime mail, and keep task cards without any
// Reasonix-side session. It is the "plug" half of the Reasonix task-bus design
// (docs/report/Reasonix任务总线-修改清单-20260930.md): Reasonix already owns the
// durable half via sessioncollab; this package only adapts it to MCP.
//
// Identity model: every configured role (e.g. "heartbeat", "dev") maps to a
// bearer token and a synthetic contact "zcode-<role>". Identity comes ONLY
// from the token — the X-Zcode-Role header is advisory, because headers can be
// forged by anything that can reach the listener. Every request is
// fail-closed: an unconfigured or empty role table denies everything, and the
// serve wiring never mounts these routes unless the feature is explicitly
// enabled in config.
package busmcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// validRole constrains role names so the derived contactID is always a safe
// filename component (MailStore uses "<contactID>.inbox.jsonl").
var validRole = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ErrDisabled is returned by New when the role table is empty or invalid —
// callers must treat it as "do not mount", never as a partial start.
var ErrDisabled = errors.New("busmcp: no usable roles configured")

// Config carries everything the bus needs. It mirrors [config.BusMCPConfig]
// but is declared locally so this package does not import config for a type
// serve already holds.
type Config struct {
	// Enabled gates mounting entirely; New still fails without usable roles.
	Enabled bool
	// Roles maps role name → bearer token. Tokens must be non-empty and
	// unique; contact for a role is "zcode-<role>".
	Roles map[string]string
	// MailDir overrides the shared collab mailbox directory. Empty uses
	// [config.SessionCollabMailDir].
	MailDir string
	// HopLimit overrides the mail chain ceiling; 0 keeps the package default.
	HopLimit int
	// EventTarget is the contact that HandleEvent delivers to. Empty defaults
	// to "zcode-heartbeat" so the heartbeat runtime sees hook pushes.
	EventTarget string
	// SpawnRoles lists the roles allowed to call collab_spawn. Empty denies
	// every role: the tool creates task cards and dispatches assignment
	// mail, so it stays closed until named explicitly.
	SpawnRoles []string
	// SpawnDailyQuota caps collab_spawn calls per role per local calendar
	// day; 0 keeps the default (20). The counter lives in this process, so
	// it is runaway protection, not billing.
	SpawnDailyQuota int
	// Injector is the optional mail→inject hook (bus dev item #3): after a
	// successful delivery to a zcode- contact it nudges a running zcode
	// session through the zcodebridge. Nil (tests, unwired serve) keeps the
	// realtime path out entirely; the durable inbox delivery is unaffected
	// either way. See inject.go for the contract.
	Injector MailInjector
}

// Server owns the bus surface: the MCP streamable handler, the /bus/events
// receiver, and the audit log. One Server is mounted per serve process.
type Server struct {
	mail  *sessioncollab.MailStore
	cards *sessioncollab.CardStore
	// roleByTk maps token → role (constant-time compared on lookup).
	roleByTk    map[string]string
	contact     map[string]string // role → contactID
	roleServers map[string]*mcp.Server
	mcpHandler  http.Handler
	eventTarget string
	auditMu     sync.Mutex
	auditPath   string
	// Spawn gate: which roles may hand out work, and how often. spawnMu
	// guards the counters; MailStore and CardStore serialize their own
	// writes, so this mutex only makes quota accounting exact.
	spawnAllowed map[string]bool
	spawnQuota   int
	spawnMu      sync.Mutex
	spawnUsed    map[string]int
	spawnDay     string
	// injector is the optional realtime nudge hook, set once at New and
	// read-only afterwards (see Config.Injector).
	injector MailInjector
}

// New validates cfg and builds the bus. A returned error means "do not
// mount"; serve logs it and continues without the bus routes.
func New(cfg Config) (*Server, error) {
	if !cfg.Enabled {
		return nil, ErrDisabled
	}
	if len(cfg.Roles) == 0 {
		return nil, fmt.Errorf("%w: roles table empty", ErrDisabled)
	}
	mailDir := cfg.MailDir
	if mailDir == "" {
		mailDir = config.SessionCollabMailDir()
	}
	if strings.TrimSpace(mailDir) == "" {
		return nil, errors.New("busmcp: no mailbox directory available (mail_dir unset and support dir unresolvable)")
	}
	s := &Server{
		mail:        sessioncollab.NewMailStoreWithHopLimit(mailDir, cfg.HopLimit),
		cards:       sessioncollab.NewCardStore(mailDir),
		roleByTk:    map[string]string{},
		contact:     map[string]string{},
		roleServers: map[string]*mcp.Server{},
		eventTarget: cfg.EventTarget,
		auditPath:   filepath.Join(mailDir, "bus-mcp-audit.jsonl"),
		injector:    cfg.Injector,
	}
	if s.eventTarget == "" {
		s.eventTarget = "zcode-heartbeat"
	}
	s.spawnAllowed = map[string]bool{}
	for _, role := range cfg.SpawnRoles {
		s.spawnAllowed[role] = true
	}
	s.spawnQuota = cfg.SpawnDailyQuota
	if s.spawnQuota <= 0 {
		s.spawnQuota = 20
	}
	s.spawnUsed = map[string]int{}
	s.spawnDay = time.Now().Format("2006-01-02")
	for role, token := range cfg.Roles {
		if !validRole.MatchString(role) {
			return nil, fmt.Errorf("busmcp: invalid role name %q (want [a-z0-9-]+)", role)
		}
		if strings.TrimSpace(token) == "" {
			return nil, fmt.Errorf("busmcp: role %q has an empty token", role)
		}
		if _, dup := s.roleByTk[token]; dup {
			return nil, fmt.Errorf("busmcp: duplicate token across roles (role %q)", role)
		}
		s.roleByTk[token] = role
		contact := "zcode-" + role
		s.contact[role] = contact
		s.roleServers[role] = s.newRoleServer(role, contact, s.spawnAllowed[role])
	}
	s.mcpHandler = mcp.NewStreamableHTTPHandler(s.serverForRequest, nil)
	return s, nil
}

// serverForRequest is the per-request MCP server getter. The auth wrapper has
// already resolved the role into the request context; an absent role means a
// handler path bypassed the wrapper, which must never happen — returning nil
// makes the SDK answer with a protocol error rather than leaking a default.
func (s *Server) serverForRequest(r *http.Request) *mcp.Server {
	role, ok := roleFromContext(r.Context())
	if !ok {
		return nil
	}
	return s.roleServers[role]
}

// Handler returns the authenticated MCP endpoint. It is mounted at /mcp and
// must also be exempted from the serve-wide auth gate: the gate speaks
// cookie/query tokens for browsers, while MCP clients authenticate with the
// per-role bearer tokens checked here.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, ok := s.authorize(r)
		if !ok {
			s.audit("", "auth", "mcp "+r.Method+" denied", false)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		s.audit(role, "auth", "mcp "+r.Method+" ok", true)
		ctx := context.WithValue(r.Context(), roleKey{}, role)
		s.mcpHandler.ServeHTTP(w, r.WithContext(ctx))
	})
}

// HandleEvent receives hook pushes (POST /bus/events): the same role tokens
// as the MCP endpoint, a tiny JSON body, delivery into the shared mailbox so
// the event target sees hook pushes and agent-sent mail in one stream.
type EventPayload struct {
	Event     string `json:"event"`
	SessionID string `json:"session_id,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

func (s *Server) HandleEvent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	role, ok := s.authorize(r)
	if !ok {
		s.audit("", "auth", "event denied", false)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var payload EventPayload
	// Deliberately lenient: hook payloads evolve on the sender side, and an
	// event we半-understand is still worth delivering. Strictness belongs to
	// command endpoints, not event ingestion.
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid event payload", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(payload.Event) == "" {
		http.Error(w, "event is required", http.StatusBadRequest)
		return
	}
	body, err := marshalEventBody(role, payload)
	if err != nil {
		http.Error(w, "encode event failed", http.StatusInternalServerError)
		return
	}
	// Delivery stays empty so Deliver applies the mailbox default (steer with
	// followup degradation): a push is "deliver as soon as someone reads".
	msg, err := s.mail.Deliver(sessioncollab.MailMessage{
		From:   s.contact[role],
		To:     s.eventTarget,
		Body:   body,
		CardID: payload.SessionID,
	})
	if err != nil {
		s.audit(role, "event", payload.Event+": "+err.Error(), false)
		http.Error(w, "deliver event failed", http.StatusInternalServerError)
		return
	}
	s.audit(role, "event", payload.Event+" → "+s.eventTarget+" ("+msg.ID+")", true)
	// The mail is durable at this point; the nudge is best-effort on top of
	// it and must not influence the HTTP answer (see inject.go).
	s.notifyInjector(s.eventTarget, msg)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = writeJSON(w, map[string]string{"id": msg.ID, "to": s.eventTarget})
}

// authorize resolves the bearer token to a role. Tokens are compared in
// constant time regardless of match position so a timing probe cannot walk
// the table; the role list is small enough that this is cheap.
func (s *Server) authorize(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, prefix) {
		return "", false
	}
	given := h[len(prefix):]
	if given == "" {
		return "", false
	}
	givenBytes := []byte(given)
	for token, role := range s.roleByTk {
		if subtle.ConstantTimeCompare([]byte(token), givenBytes) == 1 {
			return role, true
		}
	}
	return "", false
}

// GenerateToken returns a fresh 256-bit hex token, the same strength class as
// the serve frontend's pre-shared token.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// roleKey is the context key carrying the authenticated role into the SDK's
// per-request server getter.
type roleKey struct{}

func roleFromContext(ctx context.Context) (string, bool) {
	role, ok := ctx.Value(roleKey{}).(string)
	return role, ok && role != ""
}

func marshalEventBody(role string, payload EventPayload) (string, error) {
	b, err := json.Marshal(map[string]string{
		"kind":       "bus-event",
		"event":      payload.Event,
		"role":       role,
		"session_id": payload.SessionID,
		"summary":    payload.Summary,
	})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func writeJSON(w http.ResponseWriter, v any) error {
	return json.NewEncoder(w).Encode(v)
}
