package plugin

import (
	"context"
	"fmt"
	"strings"
	"testing"

	mcpjsonrpc "github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// Task 256: the host MCP client must complete the classic initialize +
// notifications/initialized handshake with strict older servers. The SDK's
// newest negotiation starts with a SEP-2575 server/discover probe that
// computer-use 0.9.0-preview answers with "invalid request", so the host pins
// the protocol version for known-strict servers and falls back to the classic
// handshake when a build fails before completing.

func TestConnectOptionsPinsProtocolVersion(t *testing.T) {
	if got := connectOptions(""); got != nil {
		t.Fatalf("empty pin must keep the SDK default negotiation, got %+v", got)
	}
	if got := connectOptions("2025-06-18"); got == nil || got.ProtocolVersion != "2025-06-18" {
		t.Fatalf("pinned version lost: %+v", got)
	}
	if got := connectOptions("  2025-06-18  "); got == nil || got.ProtocolVersion != "2025-06-18" {
		t.Fatalf("pin must be trimmed: %+v", got)
	}
}

func TestLegacyConnectFallbackIsClassicSpec(t *testing.T) {
	if legacyConnectFallback != "2025-06-18" {
		t.Fatalf("fallback = %q, want the classic 2025-06-18 sequence", legacyConnectFallback)
	}
}

// TestConnectVersionPrefersEvidencedFallback covers the task-256 rework: a
// live protocol rejection (surfaces on tools/list because initialize answers
// from cache) must flip every future build onto the classic handshake, even
// over an explicit spec pin.
func TestConnectVersionPrefersEvidencedFallback(t *testing.T) {
	tr := &sdkSessionTransport{spec: Spec{Name: "x", ProtocolVersion: "2025-11-25"}}
	if got := tr.connectVersion(); got != "2025-11-25" {
		t.Fatalf("spec pin must be used before evidence, got %q", got)
	}
	tr.engageLegacyFallback()
	if got := tr.connectVersion(); got != legacyConnectFallback {
		t.Fatalf("evidenced fallback must win over the spec pin, got %q", got)
	}
	if !tr.legacyFallbackEngaged() {
		t.Fatal("engage must be observable")
	}
}

func TestIsProtocolRejectionMatchesStrictServerShapes(t *testing.T) {
	mcpjsonrpcErr := func(code int64, msg string) error {
		return &mcpjsonrpc.Error{Code: code, Message: msg}
	}
	yes := []error{
		mcpjsonrpcErr(mcpjsonrpc.CodeInvalidRequest, "invalid request"),
		mcpjsonrpcErr(-32600, "Invalid Request"),
		mcpjsonrpcErr(-32000, "unsupported protocol version 2025-11-25"),
		mcpjsonrpcErr(-32000, "protocol version not supported"),
		fmt.Errorf(`calling "tools/list": invalid request`),
	}
	for _, err := range yes {
		if !isProtocolRejection(err) {
			t.Fatalf("expected protocol rejection: %v", err)
		}
	}
	no := []error{
		nil,
		fmt.Errorf("transport error: connection reset by peer"),
		context.DeadlineExceeded,
		fmt.Errorf("MCP endpoint returned HTTP 404 without an established session"),
	}
	for _, err := range no {
		if isProtocolRejection(err) {
			t.Fatalf("expected NON-protocol rejection: %v", err)
		}
	}
}

func TestApplyKnownOverridesPinsComputerUse(t *testing.T) {
	base := Spec{Name: "computer-use", Type: "stdio", Command: "npx"}
	pinned := ApplyKnownOverrides(base, "")
	if pinned.ProtocolVersion != "2025-06-18" {
		t.Fatalf("computer-use must be pinned to the classic handshake, got %q", pinned.ProtocolVersion)
	}

	// An explicit protocol_version always wins over the known override.
	explicit := Spec{Name: "computer-use", Type: "stdio", Command: "npx", ProtocolVersion: "2025-11-25"}
	if got := ApplyKnownOverrides(explicit, ""); got.ProtocolVersion != "2025-11-25" {
		t.Fatalf("explicit pin must win, got %q", got.ProtocolVersion)
	}

	// Other servers keep the default negotiation.
	other := ApplyKnownOverrides(Spec{Name: "codegraph", Type: "stdio", Command: "codegraph"}, "")
	if other.ProtocolVersion != "" {
		t.Fatalf("unrelated server must keep the SDK default, got %q", other.ProtocolVersion)
	}
}

func TestIsComputerUseSpecMatchesNameAndCommand(t *testing.T) {
	cases := []struct {
		spec Spec
		want bool
	}{
		{spec: Spec{Name: "computer-use"}, want: true},
		{spec: Spec{Name: "Computer_Use"}, want: true},
		{spec: Spec{Name: "mcp", Command: "node C:\\tools\\reasonix-computer-use\\index.js"}, want: true},
		{spec: Spec{Name: "codegraph", Command: "codegraph"}, want: false},
		{spec: Spec{Name: "computerized-helper", Command: "helper"}, want: false},
	}
	for _, tc := range cases {
		if got := isComputerUseSpec(tc.spec); got != tc.want {
			t.Fatalf("isComputerUseSpec(%+v) = %v", tc.spec, got)
		}
	}
}

func TestPinnedVersionShortCircuitsDiscoverProbe(t *testing.T) {
	// The fallback pin must be strictly older than the SDK's SEP-2575 cutoff
	// (2026-07-28), otherwise Connect would still start with server/discover
	// and the strict server would reject the very first request again.
	if !(legacyConnectFallback < "2026-07-28") {
		t.Fatalf("fallback %q must sort below the SEP-2575 version", legacyConnectFallback)
	}
	if !strings.Contains(legacyConnectFallback, "2025") {
		t.Fatalf("fallback %q must be a 2025-series spec version", legacyConnectFallback)
	}
}
