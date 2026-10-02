package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"reasonix/internal/baseproc"
	"reasonix/internal/tool"
)

// gateStubTool is a minimal tool for catalog-gate tests.
type gateStubTool struct {
	name     string
	readOnly bool
}

func (t *gateStubTool) Name() string            { return t.name }
func (t *gateStubTool) Description() string     { return "stub " + t.name }
func (t *gateStubTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *gateStubTool) ReadOnly() bool          { return t.readOnly }
func (t *gateStubTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}

func gateRegistry(names ...string) *tool.Registry {
	reg := tool.NewRegistry()
	for _, n := range names {
		reg.Add(&gateStubTool{name: n})
	}
	return reg
}

// gatePipeRW adapts a pipe pair for baseproc.Options.Dial.
type gatePipeRW struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (p *gatePipeRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *gatePipeRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *gatePipeRW) Close() error                { return errors.Join(p.r.Close(), p.w.Close()) }

// newGateRemoteBase starts a surface-backed serve loop over in-memory pipes
// and returns the dialed/handshaked client from baseproc.Start — the real
// remote path, no unexported helpers used.
func newGateRemoteBase(t *testing.T, surface baseproc.ToolSurface) baseproc.BaseClient {
	t.Helper()
	serverIn, clientOut := io.Pipe()
	clientIn, serverOut := io.Pipe()
	s := baseproc.NewServer("gate-test")
	s.AttachToolSurface(surface)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Serve(ctx, serverIn, serverOut)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		_ = clientOut.Close()
		_ = clientIn.Close()
		_ = serverIn.Close()
		_ = serverOut.Close()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Log("gate serve loop did not exit in 5s")
		}
	})
	client := baseproc.Start(context.Background(), baseproc.Options{
		Enabled: true,
		Dial: func(context.Context) (io.ReadWriteCloser, func(), error) {
			rw := &gatePipeRW{r: clientIn, w: clientOut}
			return rw, func() { _ = rw.Close() }, nil
		},
		HandshakeTimeout: 5 * time.Second,
	})
	if client.Mode() != baseproc.ModeRemote {
		t.Fatalf("Start mode = %q, want remote (dial+hello must succeed)", client.Mode())
	}
	// S1c: the client is a lifecycle-managed view — it owns a supervision
	// goroutine and a backoff loop. Release it before the pipes go away
	// (cleanups run LIFO) or goleak sees the supervisor still parked.
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// TestBaseCatalogGateLocalPaths is the switch-off对照: no base client, or an
// inline one, must answer ToolContractEntries/AllToolContractEntries exactly
// like the pre-S1 registry path.
func TestBaseCatalogGateLocalPaths(t *testing.T) {
	build := func(base baseproc.BaseClient) *Controller {
		reg := gateRegistry("alpha", "beta")
		reg.SetProviderVisibleTools([]string{"alpha"})
		return New(Options{Registry: reg, BaseClient: base})
	}

	cases := []struct {
		name string
		base baseproc.BaseClient
	}{
		{name: "no base client (pre-S1 controller)"},
		{name: "inline base client (switch off / spawn fallback)",
			base: baseproc.InlineBaseClient{ServerVersion: "v-test",
				Surface: &baseproc.RegistrySurface{Reg: gateRegistry("alpha", "beta")}}},
	}
	for _, tc := range cases {
		c := build(tc.base)
		localProvider := c.mcp.registry().ContractEntries()
		localAll := c.mcp.registry().AllContractEntries()
		if got := c.ToolContractEntries(); !reflect.DeepEqual(got, localProvider) {
			t.Fatalf("%s: ToolContractEntries = %+v, want local %+v", tc.name, got, localProvider)
		}
		if got := c.AllToolContractEntries(); !reflect.DeepEqual(got, localAll) {
			t.Fatalf("%s: AllToolContractEntries = %+v, want local %+v", tc.name, got, localAll)
		}
	}
}

// TestBaseCatalogGateRoutesOverIPC: a remote base WITH the tools capability
// answers the controller's catalog queries over base.toolCatalog — the
// 开关开=经通道 side of the对照, with entries converted losslessly back to
// ContractEntry.
func TestBaseCatalogGateRoutesOverIPC(t *testing.T) {
	localReg := gateRegistry("local_only")
	remoteReg := gateRegistry("remote_only")
	remote := newGateRemoteBase(t, &baseproc.RegistrySurface{Reg: remoteReg})
	c := New(Options{Registry: localReg, BaseClient: remote})

	got := c.ToolContractEntries()
	if len(got) != 1 || got[0].Name != "remote_only" {
		t.Fatalf("ToolContractEntries = %+v, want the IPC catalog [remote_only]", got)
	}
	if got[0].Description != "stub remote_only" {
		t.Fatalf("description lost in ContractEntry conversion: %+v", got[0])
	}
	all := c.AllToolContractEntries()
	if len(all) != 1 || all[0].Name != "remote_only" {
		t.Fatalf("AllToolContractEntries = %+v, want the IPC catalog [remote_only]", all)
	}
}

// TestBaseCatalogGateFallsBackWithoutCapability: a remote base that does not
// advertise CapTools (today's real serve process — no registry hosted yet)
// makes the gate fall back to the local registry without spending a round
// trip (ErrNotWired is local), i.e. the switch-on-but-not-hosted state stays
// byte-identical to today.
func TestBaseCatalogGateFallsBackWithoutCapability(t *testing.T) {
	localReg := gateRegistry("local_only")
	remote := newGateRemoteBase(t, nil) // no surface attached → no CapTools
	c := New(Options{Registry: localReg, BaseClient: remote})

	if got := c.ToolContractEntries(); !reflect.DeepEqual(got, localReg.ContractEntries()) {
		t.Fatalf("ToolContractEntries = %+v, want local %+v", got, localReg.ContractEntries())
	}
	if got := c.AllToolContractEntries(); !reflect.DeepEqual(got, localReg.AllContractEntries()) {
		t.Fatalf("AllToolContractEntries = %+v, want local %+v", got, localReg.AllContractEntries())
	}
}
