package turncontrol

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func newTestServer(t *testing.T, verify VerifyCaller, handler Handler) *Server {
	t.Helper()
	s, err := NewServer("t-1", "tok-test", verify, handler)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func post(t *testing.T, s *Server, token, writer, op, turnID string, payload []byte) error {
	t.Helper()
	return Post(context.Background(), s.Addr(), token, writer, op, turnID, payload)
}

// Task 290 S2 acceptance: the three cross-process operations each reach the
// injected handler with the right identity, and success is a plain ok.
func TestTurnControlThreeOperationsDeliver(t *testing.T) {
	type call struct{ op, turnID, payload string }
	var calls []call
	s := newTestServer(t, nil, func(op, turnID string, payload []byte) error {
		calls = append(calls, call{op, turnID, string(payload)})
		return nil
	})
	for _, tc := range []struct{ op, body string }{
		{OpSteer, `{"text":"try the other file"}`},
		{OpAnswer, `{"id":"q1","option":"yes"}`},
		{OpCancel, ``},
	} {
		if err := post(t, s, "tok-test", "w1", tc.op, "t-1", []byte(tc.body)); err != nil {
			t.Fatalf("%s delivery failed: %v", tc.op, err)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("handler calls = %d, want 3: %+v", len(calls), calls)
	}
	for i, want := range []struct{ op, body string }{{OpSteer, `{"text":"try the other file"}`}, {OpAnswer, `{"id":"q1","option":"yes"}`}, {OpCancel, ``}} {
		if calls[i].op != want.op || calls[i].turnID != "t-1" || calls[i].payload != want.body {
			t.Fatalf("call %d = %+v, want op=%s turn=t-1 payload=%q", i, calls[i], want.op, want.body)
		}
	}
}

// Security boundary 3: no token or wrong token never reaches the handler.
func TestTurnControlRejectsBadToken(t *testing.T) {
	reached := false
	s := newTestServer(t, nil, func(op, turnID string, payload []byte) error {
		reached = true
		return nil
	})
	for _, token := range []string{"", "wrong"} {
		err := post(t, s, token, "w1", OpCancel, "t-1", nil)
		var de *DeliveryError
		if !errors.As(err, &de) || de.Code != CodeBadToken {
			t.Fatalf("token %q: err = %v, want DeliveryError code %s", token, err, CodeBadToken)
		}
	}
	if reached {
		t.Fatal("handler must never run for a bad token")
	}
}

// Idempotency: a request aimed at a turn that is no longer the registered one
// fails loudly (409) instead of acting on the wrong turn.
func TestTurnControlRejectsStaleTurn(t *testing.T) {
	reached := false
	s := newTestServer(t, nil, func(op, turnID string, payload []byte) error {
		reached = true
		return nil
	})
	err := post(t, s, "tok-test", "w1", OpCancel, "t-999", nil)
	var de *DeliveryError
	if !errors.As(err, &de) || de.Code != CodeStaleTurn {
		t.Fatalf("err = %v, want DeliveryError code %s", err, CodeStaleTurn)
	}
	if reached {
		t.Fatal("handler must never run for a stale turn id")
	}
}

// Security boundary 1: the injected caller verifier can refuse a writer.
func TestTurnControlRejectsUnauthorizedWriter(t *testing.T) {
	reached := false
	s := newTestServer(t,
		func(writerID string) bool { return writerID == "session-owner" },
		func(op, turnID string, payload []byte) error {
			reached = true
			return nil
		})
	if err := post(t, s, "tok-test", "intruder", OpCancel, "t-1", nil); err == nil {
		t.Fatal("unauthorized writer must be refused")
	} else {
		var de *DeliveryError
		if !errors.As(err, &de) || de.Code != CodeNotOwner {
			t.Fatalf("err = %v, want DeliveryError code %s", err, CodeNotOwner)
		}
	}
	if reached {
		t.Fatal("handler must never run for a refused writer")
	}
	if err := post(t, s, "tok-test", "session-owner", OpCancel, "t-1", nil); err != nil {
		t.Fatalf("authorized writer must pass: %v", err)
	}
}

// Handler failures travel back as explicit codes — never swallowed.
func TestTurnControlHandlerErrorsPropagate(t *testing.T) {
	cases := []struct {
		handlerErr error
		wantCode   string
	}{
		{ErrOwnerElsewhere, CodeOwnerElsewhere},
		{ErrNoPendingPrompt, CodeNoPendingPrompt},
		{ErrNotOwner, CodeNotOwner},
		{errors.New("disk locked"), "handler_error"},
	}
	for _, tc := range cases {
		s := newTestServer(t, nil, func(op, turnID string, payload []byte) error { return tc.handlerErr })
		err := post(t, s, "tok-test", "w1", OpSteer, "t-1", []byte(`{}`))
		var de *DeliveryError
		if !errors.As(err, &de) || de.Code != tc.wantCode {
			t.Fatalf("handler err %v: got %v, want code %s", tc.handlerErr, err, tc.wantCode)
		}
	}
}

// Security boundary 2: the listener is loopback by construction — there is
// no configuration that could expose it on a routable address.
func TestTurnControlLoopbackOnly(t *testing.T) {
	s := newTestServer(t, nil, func(op, turnID string, payload []byte) error { return nil })
	if !strings.HasPrefix(s.Addr(), "127.0.0.1:") {
		t.Fatalf("addr = %q, want 127.0.0.1:port", s.Addr())
	}
}

// Turn ends → endpoint closes → the token dies with it; callers get an
// explicit endpoint_unreachable instead of a hang or a silent no-op.
func TestTurnControlCloseInvalidatesEndpoint(t *testing.T) {
	s, err := NewServer("t-1", "tok-test", nil, func(op, turnID string, payload []byte) error { return nil })
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err = post(t, s, "tok-test", "w1", OpCancel, "t-1", nil)
	if err == nil || !strings.Contains(err.Error(), CodeEndpointUnreachable) {
		t.Fatalf("after Close: err = %v, want %s", err, CodeEndpointUnreachable)
	}
}

// Construction rejects half-built servers instead of starting a door with no
// lock or no one behind it.
func TestTurnControlNewServerValidates(t *testing.T) {
	if _, err := NewServer("", "tok", nil, func(op, turnID string, payload []byte) error { return nil }); err == nil {
		t.Fatal("empty turn id must be rejected")
	}
	if _, err := NewServer("t-1", "", nil, func(op, turnID string, payload []byte) error { return nil }); err == nil {
		t.Fatal("empty token must be rejected")
	}
	if _, err := NewServer("t-1", "tok", nil, nil); err == nil {
		t.Fatal("nil handler must be rejected")
	}
}
