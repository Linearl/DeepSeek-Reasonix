package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"reasonix/internal/provider"
)

// samplingRequest is a once-prepared, frozen provider request for one model
// round. All stream retries replay this exact payload — no synthetic recovery
// messages, no schema reorder, no previous_response_id drift from failed attempts.
type samplingRequest struct {
	req provider.Request
	// prov is the request's destination, captured at freeze (task 148): a
	// session-scoped model override must not flip mid-round, mirroring how
	// EffortOverride rides inside the frozen payload. nil falls back to the
	// agent's effective destination at stream time.
	prov provider.Provider
}

// destination resolves the round's destination provider: the one captured at
// freeze when set, else the agent's current effective destination.
func (s samplingRequest) destination(a *Agent) provider.Provider {
	if s.prov != nil {
		return s.prov
	}
	return a.providerForRequest()
}

func isEmptyStreamResult(text, reasoning string, calls []provider.ToolCall, responsesItems []json.RawMessage, serverSearch []provider.ServerSearchCall) bool {
	return strings.TrimSpace(text) == "" &&
		strings.TrimSpace(reasoning) == "" &&
		len(calls) == 0 &&
		len(responsesItems) == 0 &&
		len(serverSearch) == 0
}

// modelInputMessages derives the stable provider-visible view from durable
// storage. Tool Content is the first-visible bounded result; RawContent stays
// local and is available only through the explicit session result reader.
func modelInputMessages(msgs []provider.Message) []provider.Message {
	return provider.ModelMessages(msgs)
}

// normalizeModelRequestMessages is shared by ordinary sampling and compaction
// replay so their cacheable prefix has the same role projection and metadata
// cleanup. Interceptors deliberately remain outside this helper.
func (a *Agent) normalizeModelRequestMessages(msgs []provider.Message) []provider.Message {
	requestMessages := a.providerProjectionMessages(modelInputMessages(provider.RepairRejectedArguments(msgs)))
	// ModelMessages intentionally has a zero-copy fast path for clean input.
	// Detach before removing local metadata from the request-only representation.
	requestMessages = append([]provider.Message(nil), requestMessages...)
	for i := range requestMessages {
		requestMessages[i].CreatedAt = 0
		if requestMessages[i].Role == provider.RoleUser {
			requestMessages[i].Content = reTrailingExecutionPolicy.ReplaceAllString(requestMessages[i].Content, "")
		}
	}
	return requestMessages
}

func (a *Agent) streamProviderRequest(ctx context.Context, dest provider.Provider, req provider.Request) (<-chan provider.Chunk, error) {
	if err := provider.ValidateModelTranscript(req.Messages); err != nil {
		return nil, err
	}
	if dest == nil {
		dest = a.providerForRequest()
	}
	ch, err := dest.Stream(ctx, req)
	if err != nil {
		if limit := provider.AsOutputLimitError(err); !provider.ManagedRecovery(ctx) && limit != nil && req.MaxTokens > limit.MaxOutputTokens {
			a.learnOutputBudget(limit.MaxOutputTokens)
			retryReq := req
			retryReq.MaxTokens = limit.MaxOutputTokens
			return dest.Stream(ctx, retryReq)
		}
		return nil, err
	}
	// HTTP-level output-limit errors are returned before a stream channel is
	// created by SendWithRetry. Preserve the original channel directly so
	// cancellation and live chunk timing remain unchanged.
	return ch, nil
}

// prepareSamplingRequest freezes one model-round request (preflight + interceptors).
// Output budgets are resolved only here and never change the compact_ratio
// trigger. Physical overflow may attempt at most one recovery summary.
//
// tl1669 US2: a FAILED compaction must not kill the turn. The competitor
// semantics (dsh "warn then continuing the turn", zcode "a compaction failure
// should not fail the turn") are: the reason is recorded, and the round goes
// on with the context the session already has. Only a cancellation (the user
// stopped the turn) and a request that cannot be admitted at all (the view is
// genuinely unsendable) still end the round.
func (a *Agent) prepareSamplingRequest(ctx context.Context) (samplingRequest, error) {
	// The compaction transaction and the request assembly are separated on
	// purpose: only a COMPACTION failure may take the fallback leg below, and
	// the extension interceptors must run exactly once per round (they can
	// have side effects — "Extension interceptors run only on the real
	// sampling request so side-effecting plugins are not double-invoked").
	prepared, perr := a.contextManager().Prepare(ctx, ContextPreparePolicy{Trigger: CompactionTriggerPressure})
	if perr != nil {
		if ctx.Err() != nil {
			// Cancellation is the user's own action — not a compaction outcome.
			return samplingRequest{}, perr
		}
		if fallback, ok := a.compactionFallbackRequest(ctx, perr); ok {
			return fallback, nil
		}
		return samplingRequest{}, perr
	}
	frozen, err := a.buildSamplingRequestFrom(ctx, CompactionTriggerPressure, prepared.Messages)
	if err != nil {
		return samplingRequest{}, err
	}
	if err := a.applyAdmissionToRequest(&frozen.req); err != nil {
		// One-shot physical overflow recovery. Do not loop.
		startProjectionVersion := a.currentProjectionVersion()
		if _, perr := a.contextManager().Prepare(ctx, ContextPreparePolicy{
			Trigger: CompactionTriggerOverflow,
			Force:   true,
		}); perr != nil {
			return samplingRequest{}, err
		}
		if a.currentProjectionVersion() <= startProjectionVersion {
			return samplingRequest{}, err
		}
		rebuilt, rerr := a.buildSamplingRequest(ctx, CompactionTriggerPressure)
		if rerr != nil {
			return samplingRequest{}, rerr
		}
		if aerr := a.applyAdmissionToRequest(&rebuilt.req); aerr != nil {
			return samplingRequest{}, aerr
		}
		shape := a.requestCalibrationShape(rebuilt.req)
		a.sess.output.activeReqShape.Store(&shape)
		return samplingRequest{req: freezeProviderRequest(rebuilt.req), prov: rebuilt.prov}, nil
	}
	shape := a.requestCalibrationShape(frozen.req)
	a.sess.output.activeReqShape.Store(&shape)
	return samplingRequest{req: freezeProviderRequest(frozen.req), prov: frozen.prov}, nil
}

// compactionFallbackRequest rebuilds the round's request on the CURRENT
// visible context after the compaction round failed (tl1669 US2). The context
// is used as it stands — no second compaction attempt, no silent retry storm.
// Reports false when the current view cannot be assembled or cannot be
// admitted: a request that cannot be sent is not sent, and the original
// failure stays the honest outcome.
func (a *Agent) compactionFallbackRequest(ctx context.Context, cause error) (samplingRequest, bool) {
	// The fallback exists for a COMPACTION failure, not for a context that
	// physically cannot be sent. When the view sits at or above the hard
	// ceiling the truncation+sliding-window rescue ladder has already failed
	// (that is the only way Prepare reports an error there) — the honest
	// outcome is the typed ErrCompactionRequired, not a round on a view the
	// provider cannot accept.
	if cur := a.contextManager().currentPrepared(); cur.InputTokens >= a.hardInputCeiling() {
		return samplingRequest{}, false
	}
	frozen, err := a.buildSamplingRequestFrom(ctx, CompactionTriggerPressure, a.modelVisibleMessages())
	if err != nil {
		return samplingRequest{}, false
	}
	if err := a.applyAdmissionToRequest(&frozen.req); err != nil {
		return samplingRequest{}, false
	}
	a.recordCompactionTurnFallback(cause)
	shape := a.requestCalibrationShape(frozen.req)
	a.sess.output.activeReqShape.Store(&shape)
	return samplingRequest{req: freezeProviderRequest(frozen.req), prov: frozen.prov}, true
}

// recordCompactionTurnFallback reports a failed compaction that did NOT end the
// turn (tl1669 US2: 失败原因落日志). The transcript side of the story is the
// failed card (emitCompactionFailed → attempt N + reason, tl1648); this line
// is the turn-log half, so the log always names WHY the round continued on
// the uncompacted context.
//
// tl1675 boundary (tl1675 is a separate piece, later in this wave): tl1675
// owns the pass-1 zero-log defect and the chunked-fallback criteria. This
// function is the MINIMUM log the US2 story needs in this piece — the
// compaction failure reason reaches the turn log here, at the place where the
// decision "the turn continues" is taken.
func (a *Agent) recordCompactionTurnFallback(cause error) {
	reason := compactionFailureReason(cause)
	attempt := a.sess.compaction.failureStreak.Load()
	slog.Warn("agent: context compaction failed — the turn continues with the current context",
		"session", a.sessionLogRef(), "attempt", attempt, "reason", reason, "err", cause)
}

func (a *Agent) buildSamplingRequest(ctx context.Context, trigger string) (samplingRequest, error) {
	prepared, err := a.contextManager().Prepare(ctx, ContextPreparePolicy{Trigger: trigger})
	if err != nil {
		return samplingRequest{}, err
	}
	return a.buildSamplingRequestFrom(ctx, trigger, prepared.Messages)
}

// buildSamplingRequestFrom assembles the provider request on an explicit
// message view. It never runs a compaction round of its own — the caller owns
// the view (prepareSamplingRequest's fallback leg passes the current visible
// context, the normal leg passes the prepared/compacted one).
func (a *Agent) buildSamplingRequestFrom(ctx context.Context, trigger string, messages []provider.Message) (samplingRequest, error) {
	// CreatedAt is durable UI metadata, not model input. Strip it from the
	// transport copy so wall-clock differences never invalidate the provider's
	// prompt-cache prefix (and custom providers cannot accidentally send it).
	requestMessages := a.normalizeModelRequestMessages(messages)
	// context.prepare: extensions may rewrite the message copy feeding THIS
	// request. The session log is never touched — the replacement is
	// ephemeral, so the next request starts from the unmodified history.
	requestMessages, err := a.interceptContextPrepare(ctx, requestMessages)
	if err != nil {
		return samplingRequest{}, err
	}
	// Task 148: the destination is fixed once per round — request shaping
	// (native tool search) and the wire both talk to the same provider even if
	// a model switch lands mid-round.
	dest := a.providerForRequest()
	req := provider.Request{
		Messages:       requestMessages,
		Tools:          a.providerToolSchemas(),
		MaxTokens:      a.destinationMaxOutputTokens(),
		Temperature:    provider.OptionalTemperature(a.temperature),
		ResponseFormat: responseFormatFromRequest(ctx),
		EffortOverride: a.effortOverrideForRequest(),
	}
	if provider.NativeToolSearchEnabled(dest) {
		req.ToolSearch = &provider.ToolSearch{Enabled: true}
	}
	// provider.request: the fully assembled request gets one last ruling
	// (revalidated by the payload registry) before it goes on the wire.
	req, err = a.interceptProviderRequest(ctx, req)
	if err != nil {
		return samplingRequest{}, err
	}
	if err := provider.ValidateModelTranscript(req.Messages); err != nil {
		return samplingRequest{}, err
	}
	return samplingRequest{req: req, prov: dest}, nil
}

// providerProjectionMessages applies provider-specific role compatibility to a
// request copy. Projection sidecars retain logical user-turn boundaries so
// explicit range compression can continue to resolve anchors across calls.
func (a *Agent) providerProjectionMessages(msgs []provider.Message) []provider.Message {
	if a != nil {
		// Task 148: project for the destination the request will actually reach
		// (session model override when armed), not always the construction
		// provider — same-family gate keeps the policy identical in accepted
		// cases; this keeps the shapes honest if a switch just landed.
		prov := a.providerForRequest()
		strongCutoff := a.sess.reasoningReplayStrongProjection
		if strongCutoff > 0 && a.strictAlternatingRoles {
			// The cutoff is measured after role coalescing on the repaired
			// request, so apply the same outbound shape before slicing it.
			msgs = coalesceProjectionUserRuns(msgs)
		}
		if strongCutoff > 0 {
			// A repaired thinking-400 conversation keeps the stripped
			// projection only for the history that caused the rejection.
			resolvedCutoff := resolveReasoningReplayPrefix(msgs, strongCutoff, a.sess.reasoningReplayStrongProjectionAnchor)
			if resolvedCutoff > 0 {
				if repaired, changed := provider.ProjectReasoningStrippedMessagesPrefix(prov, msgs, resolvedCutoff); changed {
					msgs = a.replayRecoveryFacts(msgs[:resolvedCutoff], repaired)
				}
			} else {
				// The canonical shape no longer contains the repair anchor
				// (for example after rewind). Do not silently disable all
				// provider projection; re-arm from the current history.
				a.sess.clearReasoningReplayStrongProjection()
				if repaired, changed := provider.ProjectReplaySafeMessages(prov, msgs); changed {
					msgs = repaired
				}
			}
		} else if repaired, changed := provider.ProjectReplaySafeMessages(prov, msgs); changed {
			msgs = repaired
		}
		if a.strictAlternatingRoles && a.sess.reasoningReplayStrongProjection <= 0 {
			return coalesceProjectionUserRuns(msgs)
		}
	}
	return msgs
}

// freezeProviderRequest deep-copies the provider-visible request surface so
// retries share identical messages, tools order, temperature, and format.
func freezeProviderRequest(req provider.Request) provider.Request {
	out := req
	if len(req.Messages) > 0 {
		out.Messages = append([]provider.Message(nil), req.Messages...)
		for i := range out.Messages {
			out.Messages[i].ThinkingBlocks = append([]provider.ThinkingBlock(nil), out.Messages[i].ThinkingBlocks...)
			if len(out.Messages[i].ToolCalls) > 0 {
				out.Messages[i].ToolCalls = append([]provider.ToolCall(nil), out.Messages[i].ToolCalls...)
			}
			if len(out.Messages[i].Images) > 0 {
				out.Messages[i].Images = append([]string(nil), out.Messages[i].Images...)
			}
			if len(out.Messages[i].ResponsesItems) > 0 {
				items := make([]json.RawMessage, len(out.Messages[i].ResponsesItems))
				for j, item := range out.Messages[i].ResponsesItems {
					items[j] = append(json.RawMessage(nil), item...)
				}
				out.Messages[i].ResponsesItems = items
			}
			if len(out.Messages[i].ServerSearch) > 0 {
				searches := make([]provider.ServerSearchCall, len(out.Messages[i].ServerSearch))
				for j, search := range out.Messages[i].ServerSearch {
					searches[j] = search
					if len(search.Results) > 0 {
						searches[j].Results = append([]provider.ServerSearchHit(nil), search.Results...)
					}
					if len(search.Raw) > 0 {
						searches[j].Raw = append(json.RawMessage(nil), search.Raw...)
					}
				}
				out.Messages[i].ServerSearch = searches
			}
		}
	}
	if len(req.Tools) > 0 {
		out.Tools = make([]provider.ToolSchema, len(req.Tools))
		for i, schema := range req.Tools {
			out.Tools[i] = schema
			if len(schema.Parameters) > 0 {
				out.Tools[i].Parameters = append(json.RawMessage(nil), schema.Parameters...)
			}
		}
	}
	if req.Temperature != nil {
		t := *req.Temperature
		out.Temperature = &t
	}
	if req.ResponseFormat != nil {
		rf := *req.ResponseFormat
		out.ResponseFormat = &rf
	}
	return out
}
