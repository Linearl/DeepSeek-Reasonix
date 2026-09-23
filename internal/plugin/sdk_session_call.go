package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
)

func (t *sdkSessionTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	managed, err := t.acquire(ctx)
	if err != nil {
		return nil, t.sanitizeError(err, nil)
	}
	result, err := t.invokeManaged(ctx, managed, method, params)
	if err == nil {
		t.clearRuntimeError(managed)
		return result, nil
	}

	if isExplicitMCPSessionMissing(err) || managed.session.ID() == "" && t.isStreamableHTTPNotFound(err) {
		if managed.session.ID() == "" {
			endpointErr := fmt.Errorf("MCP endpoint returned HTTP 404 without an established session: %w", err)
			t.noteRuntimeError(managed, SessionErrorProtocol, endpointErr)
			return nil, t.sanitizeError(endpointErr, managed)
		}
		t.noteRuntimeError(managed, SessionErrorSessionMissing, err)
		t.invalidate(managed)
		replacement, rebuildErr := t.acquire(ctx)
		if rebuildErr != nil {
			return nil, t.sanitizeError(fmt.Errorf("MCP session expired; rebuild failed: %w", rebuildErr), managed)
		}
		result, err = t.invokeManaged(ctx, replacement, method, params)
		if err == nil {
			t.clearRuntimeError(replacement)
			return result, nil
		}
		return nil, t.sanitizeError(err, replacement)
	}

	if isTerminalSDKError(err) || isAmbiguousTransportError(err) || errors.Is(err, context.DeadlineExceeded) {
		kind := SessionErrorStreamClosed
		if errors.Is(err, context.DeadlineExceeded) {
			kind = SessionErrorTimeout
		} else if !isTerminalSDKError(err) {
			kind = SessionErrorTransport
		}
		t.noteRuntimeError(managed, kind, err)
		t.invalidate(managed)
		if safeToReplayMCPMethod(method) {
			replacement, rebuildErr := t.acquire(ctx)
			if rebuildErr != nil {
				return nil, t.sanitizeError(fmt.Errorf("MCP connection closed; rebuild failed: %w", rebuildErr), managed)
			}
			result, err = t.invokeManaged(ctx, replacement, method, params)
			if err == nil {
				t.clearRuntimeError(replacement)
				return result, nil
			}
			return nil, t.sanitizeError(err, replacement)
		}
		t.startAutoReconnect()
		return nil, t.sanitizeError(fmt.Errorf("MCP tool connection closed after dispatch; execution result is unknown and the call was not retried: %w", err), managed)
	}

	kind := classifySessionError(err)
	t.noteRuntimeError(managed, kind, err)
	// Task 256: initialize answers from cache without touching the wire, so a
	// strict older server's rejection of the modern negotiation first
	// surfaces here — on tools/list or another real request. One time per
	// transport, rebuild the session speaking the classic handshake and retry
	// the same call; after that the evidence is conclusive and the error
	// propagates.
	if isProtocolRejection(err) && !t.legacyFallbackEngaged() && method != "initialize" {
		t.engageLegacyFallback()
		t.invalidate(managed)
		replacement, rebuildErr := t.acquire(ctx)
		if rebuildErr == nil {
			if retryResult, retryErr := t.invokeManaged(ctx, replacement, method, params); retryErr == nil {
				t.clearRuntimeError(replacement)
				slog.Warn("plugin: request succeeded after falling back to the legacy MCP handshake",
					"server", t.name, "method", method)
				return retryResult, nil
			}
		}
	}
	return nil, t.sanitizeError(err, managed)
}
