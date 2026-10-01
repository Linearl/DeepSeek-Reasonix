package zcodebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Session is the subset of zcodeSessionInfoSchema the bridge consumes
// (zcode-protocol-legacy-types.ts:135, strict). Fields the bus does not use
// stay in Raw, so upstream schema additions cannot break enumeration
// (tolerant reader); the fields we do read are type-checked — wrong types are
// a frozen-face mismatch, not a zero-value guess.
type Session struct {
	SessionID       string
	Title           string
	Status          string
	Mode            string
	WorkspacePath   string
	ParentSessionID string
	CreatedAtMs     int64
	UpdatedAtMs     int64
	Raw             json.RawMessage
}

// List enumerates the sessions this app-server instance knows. Wire:
// session/list {} → {sessions: [...]}. An empty result is a valid answer
// (fresh workspace), not an error.
func (b *Bridge) List(ctx context.Context) ([]Session, error) {
	if err := b.alive(); err != nil {
		return nil, err
	}
	raw, err := b.conn.call(ctx, methodSessionList, nil)
	if err != nil {
		return nil, err
	}
	return decodeSessionList(raw)
}

func decodeSessionList(raw json.RawMessage) ([]Session, error) {
	var parsed struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := decodeJSONNoTrailing(raw, &parsed); err != nil {
		return nil, fmt.Errorf("session/list result shape: %w", err)
	}
	out := make([]Session, 0, len(parsed.Sessions))
	for i, item := range parsed.Sessions {
		var info struct {
			SessionID       *string          `json:"sessionId"`
			Title           *string          `json:"title"`
			Status          *string          `json:"status"`
			Mode            *string          `json:"mode"`
			Workspace       *json.RawMessage `json:"workspace"`
			ParentSessionID *string          `json:"parentSessionId"`
			CreatedAt       *int64           `json:"createdAt"`
			UpdatedAt       *int64           `json:"updatedAt"`
		}
		if err := decodeJSONNoTrailing(item, &info); err != nil {
			return nil, fmt.Errorf("session/list item %d: %w", i, err)
		}
		s := Session{Raw: item}
		if info.SessionID == nil || *info.SessionID == "" {
			return nil, fmt.Errorf("session/list item %d: missing sessionId", i)
		}
		s.SessionID = *info.SessionID
		if info.Title != nil {
			s.Title = *info.Title
		}
		if info.Status != nil {
			s.Status = *info.Status
		}
		if info.Mode != nil {
			s.Mode = *info.Mode
		}
		if info.ParentSessionID != nil {
			s.ParentSessionID = *info.ParentSessionID
		}
		if info.CreatedAt != nil {
			s.CreatedAtMs = *info.CreatedAt
		}
		if info.UpdatedAt != nil {
			s.UpdatedAtMs = *info.UpdatedAt
		}
		if info.Workspace != nil {
			var ws struct {
				WorkspacePath *string `json:"workspacePath"`
			}
			if err := decodeJSONNoTrailing(*info.Workspace, &ws); err != nil {
				return nil, fmt.Errorf("session/list item %d workspace: %w", i, err)
			}
			if ws.WorkspacePath != nil {
				s.WorkspacePath = *ws.WorkspacePath
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// Event is one session event from the ordered journal
// (zcodeEventEnvelopeSchema + discriminated type, zcode-protocol/index.ts:1037).
// Payload stays raw: the M4a consumer (bus bookkeeping) needs order and type
// visibility, not every payload shape.
type Event struct {
	Seq       int64
	Type      string
	SessionID string
	TurnID    string
	Timestamp int64
	Raw       json.RawMessage
}

// Events pulls the next incremental page of a session's event journal.
// afterSeq is the last seq the caller consumed; the reply holds events with
// seq > afterSeq in journal order. A cold session (not materialized in this
// app-server process) fails with ErrSessionNotActive — callers skip it and
// retry after the session becomes active; this is a domain state, not a
// protocol failure (verified live: idle sessions answer -32004).
func (b *Bridge) Events(ctx context.Context, sessionID string, afterSeq int64, limit int) ([]Event, error) {
	if err := b.alive(); err != nil {
		return nil, err
	}
	if sessionID == "" {
		return nil, errors.New("zcodebridge: empty sessionId")
	}
	params := map[string]any{"sessionId": sessionID}
	if afterSeq > 0 {
		params["afterSeq"] = afterSeq
	}
	if limit > 0 {
		params["limit"] = limit
	}
	raw, err := b.conn.call(ctx, methodSessionEvents, params)
	if err != nil {
		var re *rpcError
		if errors.As(err, &re) && re.Code == errCodeSessionUnavailable {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotActive, sessionID)
		}
		return nil, err
	}
	return decodeEvents(raw)
}

func decodeEvents(raw json.RawMessage) ([]Event, error) {
	var parsed struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := decodeJSONNoTrailing(raw, &parsed); err != nil {
		return nil, fmt.Errorf("session/events result shape: %w", err)
	}
	out := make([]Event, 0, len(parsed.Events))
	for i, item := range parsed.Events {
		var env struct {
			Seq       *int64  `json:"seq"`
			Type      *string `json:"type"`
			SessionID *string `json:"sessionId"`
			TurnID    *string `json:"turnId"`
			Timestamp *int64  `json:"timestamp"`
		}
		if err := decodeJSONNoTrailing(item, &env); err != nil {
			return nil, fmt.Errorf("session/events item %d: %w", i, err)
		}
		if env.Seq == nil {
			return nil, fmt.Errorf("session/events item %d: missing seq", i)
		}
		ev := Event{Seq: *env.Seq, Raw: item}
		if env.Type != nil {
			ev.Type = *env.Type
		}
		if env.SessionID != nil {
			ev.SessionID = *env.SessionID
		}
		if env.TurnID != nil {
			ev.TurnID = *env.TurnID
		}
		if env.Timestamp != nil {
			ev.Timestamp = *env.Timestamp
		}
		out = append(out, ev)
	}
	return out, nil
}

// decodeJSONNoTrailing decodes JSON while rejecting trailing garbage after
// the top-level value. Unknown object keys are tolerated on purpose — the
// spec's tolerant-reader rule keeps the bridge alive across additive zcode
// evolution; the fields this package consumes are pointer-checked so a type
// change on a consumed field still fails loudly instead of decoding to a
// zero-value guess.
func decodeJSONNoTrailing(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("unexpected trailing JSON at offset " + strconv.Itoa(int(dec.InputOffset())))
	}
	return nil
}
