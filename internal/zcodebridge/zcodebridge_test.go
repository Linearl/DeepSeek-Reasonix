package zcodebridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// openGood starts a bridge over the healthy mock. Construction failures are
// fatal: a broken harness is never a skip (workspace discipline).
func openGood(t *testing.T) *Bridge {
	t.Helper()
	b, err := Open(context.Background(), helperConfig(t, mockGood))
	if err != nil {
		t.Fatalf("Open(good mock) failed: %v", err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestOpenHandshakeSucceedsOnGoodMock(t *testing.T) {
	b := openGood(t)
	select {
	case <-b.Dead():
		t.Fatal("bridge died during handshake")
	default:
	}
}

func TestListDecodesTolerantly(t *testing.T) {
	b := openGood(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessions, err := b.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("want 3 sessions, got %d", len(sessions))
	}
	first := sessions[0]
	if first.SessionID != "sess_active_1" || first.Title != "主开发" || first.Status != "idle" || first.Mode != "build" {
		t.Fatalf("session[0] decoded wrong: %+v", first)
	}
	if first.WorkspacePath != `C:\ws` {
		t.Fatalf("workspace path decoded wrong: %q", first.WorkspacePath)
	}
	if first.CreatedAtMs != 1790000000000 || first.UpdatedAtMs != 1790000001000 {
		t.Fatalf("timestamps decoded wrong: %+v", first)
	}
	// The mock plants an unknown key (traceId/sessionKind); the tolerant
	// reader must have carried the raw row through untouched.
	if !strings.Contains(string(first.Raw), "trace-extra-unknown-field") {
		t.Fatal("raw session row lost unknown fields")
	}
}

func TestEventsIncrementalPullAndColdSession(t *testing.T) {
	b := openGood(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	evs, err := b.Events(ctx, "sess_active_1", 0, 2)
	if err != nil {
		t.Fatalf("Events(first page): %v", err)
	}
	if len(evs) != 2 || evs[0].Seq != 1 || evs[1].Seq != 2 {
		t.Fatalf("first page wrong: %+v", evs)
	}
	if evs[0].Type != "turn.started" || evs[1].Type != "message.upserted" {
		t.Fatalf("event types wrong: %q %q", evs[0].Type, evs[1].Type)
	}

	// Incremental: afterSeq=2 → next page seq 3..4, and the mock echoes the
	// params it received inside the payload for end-to-end verification.
	var echo struct {
		Payload struct {
			EchoAfterSeq int64 `json:"echoAfterSeq"`
			EchoLimit    *int  `json:"echoLimit"`
		} `json:"payload"`
	}
	evs2, err := b.Events(ctx, "sess_active_1", evs[1].Seq, 0)
	if err != nil {
		t.Fatalf("Events(second page): %v", err)
	}
	if len(evs2) != 2 || evs2[0].Seq != 3 {
		t.Fatalf("second page wrong: %+v", evs2)
	}
	if err := json.Unmarshal(evs2[1].Raw, &echo); err != nil {
		t.Fatalf("echo payload: %v", err)
	}
	if echo.Payload.EchoAfterSeq != 2 {
		t.Fatalf("afterSeq param did not reach the peer: %+v", echo.Payload)
	}
	if echo.Payload.EchoLimit != nil {
		t.Fatalf("limit=0 must be omitted from params, peer saw %v", *echo.Payload.EchoLimit)
	}

	// Cold session: domain state, typed error, not a protocol failure.
	if _, err := b.Events(ctx, "sess_cold", 0, 0); !errors.Is(err, ErrSessionNotActive) {
		t.Fatalf("cold session: want ErrSessionNotActive, got %v", err)
	}
}

func TestSendTextEnvelopeAndAcks(t *testing.T) {
	b := openGood(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ack, err := b.SendText(ctx, "sess_active_2", "请继续推进 M4a 验收", DeliveryQueue)
	if err != nil {
		t.Fatalf("SendText(queue): %v", err)
	}
	if !ack.Accepted() || ack.Status != "accepted" {
		t.Fatalf("ack wrong: %+v", ack)
	}
	if ack.CommandID == "" {
		t.Fatal("ack missing commandId")
	}
	var result struct {
		Type         string `json:"type"`
		Delivery     string `json:"delivery"`
		InputID      string `json:"inputId"`
		EchoText     string `json:"echoText"`
		EchoClientID string `json:"echoClientId"`
		EchoIssuedAt int64  `json:"echoIssuedAt"`
	}
	if err := json.Unmarshal(ack.Result, &result); err != nil {
		t.Fatalf("ack result: %v", err)
	}
	if result.Type != "inputAccepted" || result.Delivery != "queue" || result.InputID != "in_1" {
		t.Fatalf("ack result wrong: %+v", result)
	}
	if result.EchoText != "请继续推进 M4a 验收" || result.EchoClientID != defaultClientID || result.EchoIssuedAt <= 0 {
		t.Fatalf("envelope members did not reach the peer: %+v", result)
	}

	// startNow (phone semantics) rides the same frozen envelope.
	ack2, err := b.SendText(ctx, "sess_active_2", "电话语义注入", DeliveryStartNow)
	if err != nil {
		t.Fatalf("SendText(startNow): %v", err)
	}
	var result2 struct {
		Delivery string `json:"delivery"`
	}
	if err := json.Unmarshal(ack2.Result, &result2); err != nil || result2.Delivery != "startNow" {
		t.Fatalf("startNow delivery wrong: %+v err=%v", result2, err)
	}

	// Client-side validation before anything hits the wire.
	if _, err := b.SendText(ctx, "sess_active_2", "x", Delivery("bogus")); err == nil || !strings.Contains(err.Error(), "requestedDelivery") {
		t.Fatalf("invalid delivery not rejected: %v", err)
	}
	if _, err := b.SendText(ctx, "", "x", DeliveryQueue); err == nil {
		t.Fatal("empty sessionId not rejected")
	}
}

func TestSendTextRejectedAckIsTypedError(t *testing.T) {
	b := openGood(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ack, err := b.SendText(ctx, "sess_active_2", "mock-reject", DeliveryGuide)
	var ackErr *AckError
	if !errors.As(err, &ackErr) {
		t.Fatalf("want AckError, got %v", err)
	}
	if ack.Status != "rejected" || ack.ReasonCode != "fault.command.session_busy" {
		t.Fatalf("rejected ack wrong: %+v", ack)
	}
	if ack.Accepted() {
		t.Fatal("rejected ack must not report accepted")
	}
	if !strings.Contains(ackErr.Error(), "session has an active turn") {
		t.Fatalf("AckError message lost the peer reason: %v", ackErr)
	}
}

func TestSendTextPerSessionInFlightGuard(t *testing.T) {
	b := openGood(t)
	// Simulate one in flight without needing a slow peer: hold the guard via
	// the same primitive SendText uses.
	if err := b.acquireInFlight("sess_active_1"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := b.SendText(ctx, "sess_active_1", "x", DeliveryQueue); !errors.Is(err, ErrInFlight) {
		t.Fatalf("want ErrInFlight, got %v", err)
	}
	// Another session is untouched.
	b.releaseInFlight("sess_active_1")
	if _, err := b.SendText(ctx, "sess_active_1", "x", DeliveryQueue); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestOpenFailsClosedOnBadFlowResult(t *testing.T) {
	_, err := Open(context.Background(), helperConfig(t, mockBadFlow))
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("want ErrProtocolMismatch, got %v", err)
	}
}

func TestOpenFailsClosedOnBadListResult(t *testing.T) {
	_, err := Open(context.Background(), helperConfig(t, mockBadList))
	if !errors.Is(err, ErrProtocolMismatch) {
		t.Fatalf("want ErrProtocolMismatch, got %v", err)
	}
}

func TestOpenFailsClosedOnDeadPeer(t *testing.T) {
	// A peer that never answers: the handshake must fail inside its budget,
	// tear the child down, and surface as a closed/mismatch error — never
	// hang the caller.
	// (mockHang reads stdin forever, so stdout stays open; the timeout fires.)
	cfg := helperConfig(t, mockHang)
	cfg.HandshakeTimeout = 700 * time.Millisecond
	start := time.Now()
	_, err := Open(context.Background(), cfg)
	if err == nil {
		t.Fatal("Open must fail against a silent peer")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("handshake timeout took %v, gate leaked", elapsed)
	}
	if !errors.Is(err, ErrProtocolMismatch) && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrClosed) {
		t.Fatalf("unexpected error class: %v", err)
	}
}

func TestOpenRejectsBrokenConfig(t *testing.T) {
	if _, err := Open(context.Background(), Config{Args: []string{"app-server", " "}}); err == nil {
		t.Fatal("blank argv element must be rejected")
	}
}

func TestTolerantReaderSurvivesGarbageAndNotifications(t *testing.T) {
	b := openGoodTolerant(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := b.List(ctx); err != nil {
		t.Fatalf("List over noisy channel: %v", err)
	}
	if b.conn.badLines.Load() == 0 {
		t.Fatal("garbage lines were not counted")
	}
	if b.conn.notify.Load() == 0 {
		t.Fatal("unrecognized notifications were not counted")
	}
}

func openGoodTolerant(t *testing.T) *Bridge {
	t.Helper()
	b, err := Open(context.Background(), helperConfig(t, mockGarbage))
	if err != nil {
		t.Fatalf("Open(garbage mock) failed: %v", err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestChildDeathFailsPendingAndSignalsDead(t *testing.T) {
	b, err := Open(context.Background(), helperConfig(t, mockDie))
	if err != nil {
		t.Fatalf("Open(die mock): %v", err)
	}
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The mock exits the moment v4/command arrives; the pending call must
	// fail closed with ErrClosed and Dead must fire.
	_, err = b.SendText(ctx, "sess_active_1", "x", DeliveryQueue)
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed after child death, got %v", err)
	}
	select {
	case <-b.Dead():
	case <-time.After(5 * time.Second):
		t.Fatal("Dead() never fired after child exit")
	}
	// Subsequent calls fail fast with the same sentinel.
	if _, err := b.List(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("List after death: want ErrClosed, got %v", err)
	}
}

func TestCloseKillsChildPromptly(t *testing.T) {
	b := openGood(t)
	closed := make(chan struct{})
	go func() {
		b.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked longer than the kill grace")
	}
	select {
	case <-b.Dead():
	default:
		t.Fatal("Dead() must fire after Close")
	}
	b.Close() // idempotent, no panic
	if _, err := b.List(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("List after Close: want ErrClosed, got %v", err)
	}
}

func TestWireFrameHasNoJSONRPCKey(t *testing.T) {
	frame, err := marshalRequest("7", methodSessionList, nil)
	if err != nil {
		t.Fatalf("marshalRequest: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(frame, &raw); err != nil {
		t.Fatalf("frame not an object: %v", err)
	}
	if _, ok := raw["jsonrpc"]; ok {
		t.Fatalf("frame carries the forbidden jsonrpc key: %s", frame)
	}
	for _, key := range []string{"id", "method"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("frame missing %s: %s", key, frame)
		}
	}
	if _, ok := raw["params"]; ok {
		t.Fatalf("nil params must omit the key: %s", frame)
	}
	frameWithParams, err := marshalRequest("8", methodSessionEvents, map[string]any{"sessionId": "s"})
	if err != nil {
		t.Fatalf("marshalRequest(params): %v", err)
	}
	if !strings.Contains(string(frameWithParams), `"params":{`) {
		t.Fatalf("params lost: %s", frameWithParams)
	}
}

func TestCommandIDShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newCommandID()
		if len(id) != 36 || strings.Count(id, "-") != 4 {
			t.Fatalf("command id not uuid-shaped: %q", id)
		}
		if seen[id] {
			t.Fatalf("command id collision: %q", id)
		}
		seen[id] = true
		if id[14] != '7' {
			t.Fatalf("command id version nibble wrong: %q", id)
		}
	}
}

func TestDeliveryValidation(t *testing.T) {
	for _, d := range []Delivery{DeliveryStartNow, DeliveryQueue, DeliveryGuide} {
		if !d.valid() {
			t.Fatalf("frozen delivery %q marked invalid", d)
		}
	}
	if Delivery("enqueue").valid() {
		t.Fatal("legacy enqueue must not pass as requestedDelivery")
	}
}
