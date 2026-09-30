package zcodebridge

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// Delivery is sendText requestedDelivery (zcode-protocol-v4/command.ts:81-99).
type Delivery string

const (
	// DeliveryStartNow is the phone semantics: the CLI atomically preempts the
	// current turn — NOT routed through queue admission. This is the face the
	// whole bus exists for: real-time injection into a busy session.
	DeliveryStartNow Delivery = "startNow"
	// DeliveryQueue is the mail semantics: join the session's input queue.
	DeliveryQueue Delivery = "queue"
	// DeliveryGuide is a lightweight steer for a running turn.
	DeliveryGuide Delivery = "guide"
)

func (d Delivery) valid() bool {
	switch d {
	case DeliveryStartNow, DeliveryQueue, DeliveryGuide:
		return true
	}
	return false
}

// Ack is the frozen subset of commandAckSchema
// (zcode-protocol-v4/command.ts:430). Result stays raw: only the sendText
// result variants (inputAccepted/inputDisposition) can appear here and the
// bus currently bookskeep on status alone.
type Ack struct {
	CommandID          string          `json:"commandId"`
	Status             string          `json:"status"` // accepted|rejected|stale|duplicate|noop|failed
	ReasonCode         string          `json:"reasonCode,omitempty"`
	Message            string          `json:"message,omitempty"`
	RevisionAtDecision int64           `json:"revisionAtDecision"`
	Result             json.RawMessage `json:"result,omitempty"`
}

// Accepted reports whether the command was admitted.
func (a Ack) Accepted() bool { return a.Status == "accepted" || a.Status == "duplicate" }

// AckError is a definitive non-accepted CommandAck. It is a domain answer,
// not a transport or protocol failure — the frozen face worked, the CLI's
// CommandInbox refused this particular command.
type AckError struct {
	Ack Ack
}

func (e *AckError) Error() string {
	msg := e.Ack.Message
	if msg == "" {
		msg = e.Ack.ReasonCode
	}
	if msg == "" {
		msg = "(no reason given)"
	}
	return fmt.Sprintf("zcode command %s: %s: %s", clipLine(e.Ack.CommandID), e.Ack.Status, msg)
}

func clipLine(s string) string {
	if len(s) <= 32 {
		return s
	}
	return s[:32] + "…"
}

// commandIDCounter makes commandIds unique per process even under the same
// millisecond timestamp.
var commandIDCounter atomic.Uint64

// newCommandID mints a uuid-v7-shaped id (the CLI only validates a string,
// but the contract documents "uuid v7, client-generated, retry-stable" — we
// honor the shape: 48-bit unix-ms | version 7 | variant | random).
func newCommandID() string {
	var b [16]byte
	ms := uint64(time.Now().UnixMilli())
	putUint48(b[:6], ms)
	b[6] = 0x70 // version 7
	if _, err := rand.Read(b[8:]); err != nil {
		// Failure to read randomness must not produce a silent collision
		// vector; fall back to process-unique counter bits.
		n := commandIDCounter.Add(1)
		binary.BigEndian.PutUint64(b[8:], n)
		b[8] = b[8]&0x3f | 0x80
	} else {
		b[8] = b[8]&0x3f | 0x80 // variant 10
	}
	return formatUUID(b)
}

func putUint48(p []byte, v uint64) {
	p[0] = byte(v >> 40)
	p[1] = byte(v >> 32)
	p[2] = byte(v >> 24)
	p[3] = byte(v >> 16)
	p[4] = byte(v >> 8)
	p[5] = byte(v)
}

func formatUUID(b [16]byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexdigits[v>>4], hexdigits[v&0x0f])
	}
	return string(out)
}

// SendText injects text into a running session (spec 冻结面 #3). Exactly one
// sendText per session may be in flight; a second concurrent call fails with
// ErrInFlight — politeness limiting only, the CLI's CommandInbox owns real
// serialization and idempotence (stale CAS).
func (b *Bridge) SendText(ctx context.Context, sessionID, text string, d Delivery) (Ack, error) {
	if err := b.alive(); err != nil {
		return Ack{}, err
	}
	if sessionID == "" {
		return Ack{}, errors.New("zcodebridge: empty sessionId")
	}
	if !d.valid() {
		return Ack{}, fmt.Errorf("zcodebridge: invalid requestedDelivery %q (want startNow|queue|guide)", string(d))
	}
	if err := b.acquireInFlight(sessionID); err != nil {
		return Ack{}, err
	}
	defer b.releaseInFlight(sessionID)

	commandID := newCommandID()
	envelope := map[string]any{
		"commandId": commandID,
		"clientId":  b.cfg.ClientID,
		"sessionId": sessionID,
		"type":      "sendText",
		"payload": map[string]any{
			"text":              text,
			"requestedDelivery": string(d),
		},
		"issuedAt": time.Now().UnixMilli(),
	}
	raw, err := b.conn.call(ctx, methodCommand, envelope)
	if err != nil {
		return Ack{}, err
	}
	var ack Ack
	if err := decodeJSONNoTrailing(raw, &ack); err != nil {
		return Ack{}, fmt.Errorf("v4/command ack shape: %w", err)
	}
	if ack.CommandID == "" {
		return Ack{}, fmt.Errorf("v4/command ack missing commandId: %s", clipRaw(raw))
	}
	if ack.CommandID != commandID {
		return Ack{}, fmt.Errorf("v4/command ack commandId mismatch: sent %s got %s", commandID, ack.CommandID)
	}
	if ack.Status == "" {
		return Ack{}, fmt.Errorf("v4/command ack missing status: %s", clipRaw(raw))
	}
	if !ack.Accepted() {
		return ack, &AckError{Ack: ack}
	}
	return ack, nil
}
