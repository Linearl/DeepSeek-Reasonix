package main

import (
	"testing"
)

func TestGatewayPortDefaultAndOverride(t *testing.T) {
	t.Setenv("REASONIX_GATEWAY_PORT", "")
	if got := gatewayPort(); got != 18789 {
		t.Fatalf("default port = %d, want 18789", got)
	}
	t.Setenv("REASONIX_GATEWAY_PORT", "0")
	if got := gatewayPort(); got != 18789 {
		t.Fatalf("invalid override = %d, want 18789", got)
	}
	t.Setenv("REASONIX_GATEWAY_PORT", "99999")
	if got := gatewayPort(); got != 18789 {
		t.Fatalf("out-of-range override = %d, want 18789", got)
	}
	t.Setenv("REASONIX_GATEWAY_PORT", "23456")
	if got := gatewayPort(); got != 23456 {
		t.Fatalf("valid override = %d, want 23456", got)
	}
}

func TestProjectRootsFromRegistryEmpty(t *testing.T) {
	// The assertion is about an unregistered project list, so this test owns its
	// state: without an isolated desktop home it inherited the project roots an
	// earlier test in the package registered, which is why it only failed in a
	// full-package run.
	isolateDesktopUserDirs(t)
	roots := projectRootsFromRegistry()
	if roots == nil {
		t.Fatal("roots = nil, want empty slice")
	}
	if len(roots) != 0 {
		t.Fatalf("roots = %v, want empty", roots)
	}
}
