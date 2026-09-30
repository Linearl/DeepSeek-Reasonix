package serve

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"reasonix/internal/safego"
	"reasonix/internal/zcodebridge"
)

// zcodebridge wiring (M4a). The bridge gives Reasonix the reasonix→zcode
// real-time injection face (phone semantics) on top of a spawned
// `zcode app-server --stdio` child. It rides the same fail-closed posture as
// bus-mcp/bus-worker: opt-in, and any startup failure only means "serve runs
// without a bridge" — never a downed session server.
//
// M4a intentionally has NO [config] table yet: a parallel branch owns
// internal/config/render.go right now, so the slice is wired through
// environment variables instead. A later slice moves these to
// [serve.zcode_bridge] once render.go is conflict-free.
//
//	REASONIX_ZCODE_BRIDGE=1                 — enable the bridge
//	REASONIX_ZCODE_BRIDGE_COMMAND=<argv0>   — override the CLI (default "zcode";
//	                                          e.g. "node" driving an installed
//	                                          ZCode desktop zcode.cjs)
//	REASONIX_ZCODE_BRIDGE_ARGS=<a\nb>       — override the argv tail, one
//	                                          argument per line (never split on
//	                                          spaces: Windows paths)
//	REASONIX_ZCODE_BRIDGE_WORKSPACE=<dir>   — child working directory
const (
	zcodeBridgeEnvEnabled   = "REASONIX_ZCODE_BRIDGE"
	zcodeBridgeEnvCommand   = "REASONIX_ZCODE_BRIDGE_COMMAND"
	zcodeBridgeEnvArgs      = "REASONIX_ZCODE_BRIDGE_ARGS"
	zcodeBridgeEnvWorkspace = "REASONIX_ZCODE_BRIDGE_WORKSPACE"
)

// Reconnect backoff (crash → relaunch → events continue from the caller's
// afterSeq; the CLI journal is durable, so nothing is lost).
const (
	zcodeBridgeRetryInitial = 2 * time.Second
	zcodeBridgeRetryBase    = 5 * time.Second
	zcodeBridgeRetryCap     = 60 * time.Second
	zcodeBridgeRetryFactor  = 2
)

// zcodeBridgeEnvConfigForTest lets tests force the env surface without
// t.Setenv races; production reads the process environment.
var zcodeBridgeEnvConfigForTest func() (enabled bool, cfg zcodebridge.Config)

// startZcodeBridge spawns the bridge when the environment opts in. Called
// next to startBusWorker in both serve lifetimes. The live bridge is readable
// via ZcodeBridge; the mail→inject consumer wiring is a later slice.
func (s *Server) startZcodeBridge() {
	enabled, cfg := zcodeBridgeEnvConfig()
	if !enabled {
		return
	}
	safego.Go("serve.zcodebridge", func() { s.runZcodeBridge(cfg) })
}

// zcodeBridgeEnvConfig reads the env surface; the test seam overrides it
// wholesale.
func zcodeBridgeEnvConfig() (bool, zcodebridge.Config) {
	if zcodeBridgeEnvConfigForTest != nil {
		return zcodeBridgeEnvConfigForTest()
	}
	if os.Getenv(zcodeBridgeEnvEnabled) != "1" {
		return false, zcodebridge.Config{}
	}
	cfg := zcodebridge.Config{
		Command:   strings.TrimSpace(os.Getenv(zcodeBridgeEnvCommand)),
		Workspace: strings.TrimSpace(os.Getenv(zcodeBridgeEnvWorkspace)),
	}
	if raw := os.Getenv(zcodeBridgeEnvArgs); strings.TrimSpace(raw) != "" {
		cfg.Args = strings.Split(raw, "\n")
	}
	return true, cfg
}

// runZcodeBridge keeps one bridge alive for the serve lifetime (background
// context — process exit is the stop signal, the Job Object fells the child
// tree, matching the busworker house pattern). A protocol-face mismatch is
// NOT retried: the peer does not speak the frozen M4a face, and per spec §三
// the client refuses instead of guessing.
func (s *Server) runZcodeBridge(cfg zcodebridge.Config) {
	// Resolve the child's effective workspace once (the mail injector prefers
	// sessions whose workspace matches it). Empty config inherits the serve
	// process cwd, which is what the child will actually run in.
	ws := strings.TrimSpace(cfg.Workspace)
	if ws == "" {
		if cwd, err := os.Getwd(); err == nil {
			ws = cwd
		}
	}
	s.zcodeBridgeWorkspace.Store(&ws)

	backoff := zcodeBridgeRetryInitial
	first := true
	for {
		if !first {
			time.Sleep(backoff)
			backoff *= zcodeBridgeRetryFactor
			if backoff > zcodeBridgeRetryCap {
				backoff = zcodeBridgeRetryCap
			}
		}
		first = false

		bridge, err := zcodebridge.Open(context.Background(), cfg)
		if err != nil {
			if errors.Is(err, zcodebridge.ErrProtocolMismatch) {
				slog.Warn("serve: zcode bridge refused: frozen protocol face mismatch; not retrying", "err", err)
				return
			}
			slog.Warn("serve: zcode bridge open failed; will retry", "err", err, "backoff", backoff)
			continue
		}
		backoff = zcodeBridgeRetryBase
		s.zcodeBridge.Store(bridge)
		slog.Info("serve: zcode bridge connected",
			"command", cfg.Command, "args", strings.Join(cfg.Args, " "))
		<-bridge.Dead()
		s.zcodeBridge.CompareAndSwap(bridge, nil)
		slog.Warn("serve: zcode bridge child exited; will rebuild",
			"stderr_tail", zcodeBridgeTailLine(bridge.StderrTail()))
	}
}

// ZcodeBridge returns the live bridge, or nil while none is connected.
func (s *Server) ZcodeBridge() *zcodebridge.Bridge {
	return s.zcodeBridge.Load()
}

// zcodeBridgeTailLine condenses child stderr for one log line.
func zcodeBridgeTailLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}
