package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/evidence"
	"reasonix/internal/provider"
	"reasonix/internal/shellsafe"
	"reasonix/internal/tool"
)

func (s *Session) addWriteIntent(callID string, raw json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range slices.Backward(s.Messages) {
		for j, c := range s.Messages[i].ToolCalls {
			if c.ID != callID {
				continue
			}
			calls := append([]provider.ToolCall(nil), s.Messages[i].ToolCalls...)
			calls[j].WriteIntents = append(append([]json.RawMessage(nil), c.WriteIntents...), append(json.RawMessage(nil), raw...))
			s.Messages[i].ToolCalls = calls
			s.version++
			s.rewriteVersion++
			return true
		}
	}
	return false
}

func (a *Agent) withWriteRecovery(ctx context.Context, call provider.ToolCall) context.Context {
	return tool.WithWriteIntentHook(ctx, func(intent tool.FileWriteIntent) error {
		raw, err := json.Marshal(intent)
		if err != nil {
			return err
		}
		if !a.sess.conversation.addWriteIntent(call.ID, raw) {
			return fmt.Errorf("write intent has no durable tool call: %s", call.ID)
		}
		return event.EmitChecked(a.svc.sink, event.Event{Kind: event.Notice, WriteIntent: true})
	})
}

func (a *Agent) verifyInterruptedWrites(ctx context.Context, r *provider.InterruptedTurnRecovery) *provider.InterruptedTurnRecovery {
	a.turn.writeRecovery = make(map[string]provider.ToolCall)
	a.turn.unknownRecovery = make(map[string]provider.ToolCall)
	if r == nil || len(r.UnknownTools) == 0 {
		return r
	}
	msgs := a.sess.conversation.Snapshot()
	for _, summary := range append([]provider.InterruptedToolSummary(nil), r.UnknownTools...) {
		var call *provider.ToolCall
		for _, m := range msgs {
			for _, c := range m.ToolCalls {
				if c.ID == summary.ID {
					copy := c
					call = &copy
				}
			}
		}
		if call != nil {
			keyCall := *call
			if _, name, ambiguous := a.svc.tools.ResolveCall(call.Name); len(ambiguous) == 0 && name != "" {
				keyCall.Name = name
			}
			a.turn.writeRecovery[writeRecoveryKey(keyCall)] = *call
			a.turn.unknownRecovery[writeRecoveryKey(keyCall)] = *call
		}
		if call == nil || len(call.WriteIntents) == 0 {
			continue
		}
		t, _, ambiguous := a.svc.tools.ResolveCall(call.Name)
		checks, satisfied := checkRecordedWrite(ctx, t, *call)
		r.WriteChecks = append(r.WriteChecks, checks...)
		if len(ambiguous) > 0 || !satisfied {
			continue
		}

		// This verifies the postcondition, not the original execution result.
		r.SatisfiedWrites = append(r.SatisfiedWrites, summary)
		r.UnknownTools = slices.DeleteFunc(r.UnknownTools, func(c provider.InterruptedToolSummary) bool { return c.ID == summary.ID })
	}
	return r
}

// recoveryReadOnlyBash reports whether a bash command is safe to re-run after
// an interrupted turn. It reuses the two existing static, fail-closed
// classifiers (unparseable shapes, real redirections, and expansions all count
// as writes):
//  1. the shellsafe permission-reader family (ls/grep/cat/git status/go vet —
//     statically proven zero writes), and
//  2. the evidence verification-command family (go test/pytest/npm test —
//     they may refresh host build caches but leave workspace content alone,
//     so a re-run has no durable side effect), minus the code-executing
//     writers the verification list still accepts (cargo check/clippy,
//     git diff --ext-diff), which stay blocked.
//
// This is a recovery-layer judgment, deliberately narrower than "auto-approve
// a new command": it is only consulted when this call is byte-identical to the
// interrupted one (writeRecoveryKey already matched).
func recoveryReadOnlyBash(args json.RawMessage) bool {
	command := bashCommandFromArgs(args)
	if command == "" {
		return false
	}
	effect := shellsafe.ClassifyBash(command)
	if effect.IsPermissionReader() {
		return true
	}
	if !evidence.IsVerificationCommand(command) {
		return false
	}
	// The verification list also accepts families the permission layer bills
	// as code-executing writers (cargo check/clippy run build.rs and write
	// target/ artifacts; git diff --ext-diff runs an external helper). Those
	// keep their write-class blocking: re-running them can durably change
	// workspace state, so the fail-closed classification wins.
	return !effect.ExecutesCode && (effect.Certainty != shellsafe.EffectKnown || effect.Writes == 0)
}

// recoveryReadOnlyCall reports whether the write-recovery gates should let the
// call through: either the tool itself is read-only, or it is a bash command
// whose static classification proves re-running has no durable side effect.
// Write-class calls keep the existing blocking behavior unchanged.
func recoveryReadOnlyCall(call provider.ToolCall, t tool.Tool) bool {
	if t.ReadOnly() {
		return true
	}
	if call.Name != "bash" {
		return false
	}
	return recoveryReadOnlyBash(json.RawMessage(call.Arguments))
}

func recoverPreviousUnknown(turn *turnRuntime, call provider.ToolCall, t tool.Tool) (toolOutcome, bool) {
	if recoveryReadOnlyCall(call, t) {
		return toolOutcome{}, false
	}
	if _, exists := turn.unknownRecovery[writeRecoveryKey(call)]; !exists {
		return toolOutcome{}, false
	}
	message := "The previous side-effecting tool call has an unknown outcome. Do not repeat it; inspect its effects with read-only tools first."
	return toolOutcome{output: message, errMsg: message, blocked: true}, true
}

// A terminal length limit can leave syntactically valid but incomplete args.
func (a *Agent) recordTruncatedToolResults(ctx context.Context, calls []provider.ToolCall) error {
	for _, call := range calls {
		outcome := toolOutcome{output: "error: tool was not executed because the model output reached its length limit; regenerate complete arguments", errMsg: "truncated tool arguments"}
		a.storeBatchToolResult(ctx, call, outcome)
		if err := a.emitBatchToolResult(call, outcome, 0, 0, false, time.Time{}); err != nil {
			return err
		}
	}
	return nil
}

func writeRecoveryKey(call provider.ToolCall) string {
	args := call.Arguments
	var decoded any
	if json.Unmarshal([]byte(args), &decoded) == nil {
		if b, err := json.Marshal(decoded); err == nil {
			args = string(b)
		}
	}
	return call.Name + "\x00" + args
}

func checkRecordedWrite(ctx context.Context, t tool.Tool, call provider.ToolCall) ([]provider.WriteRecoveryCheck, bool) {
	verifier, ok := t.(tool.WriteVerifier)
	if !ok || len(call.WriteIntents) == 0 {
		return nil, false
	}
	intents := map[string]tool.FileWriteIntent{}
	for _, raw := range call.WriteIntents {
		intent, ok := tool.DecodeWriteIntent(raw)
		if !ok {
			return nil, false
		}
		intents[intent.Path] = intent
	}
	paths := make([]string, 0, len(intents))
	for path := range intents {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	var checks []provider.WriteRecoveryCheck
	satisfied := true
	for _, path := range paths {
		state := verifier.VerifyWrite(ctx, intents[path])
		checks = append(checks, provider.WriteRecoveryCheck{CallID: call.ID, Path: path, State: string(state)})
		satisfied = satisfied && state == tool.WriteSatisfied
	}
	return checks, satisfied
}

func recoverPreviousWrite(ctx context.Context, turn *turnRuntime, call provider.ToolCall, t tool.Tool) (toolOutcome, bool) {
	if previous, exists := turn.writeRecovery[writeRecoveryKey(call)]; exists && !recoveryReadOnlyCall(call, t) {
		checks, satisfied := checkRecordedWrite(ctx, t, previous)
		if satisfied {
			return toolOutcome{output: "The previous file write postconditions are already satisfied; no write was repeated."}, true
		}
		message := "The previous identical write has an unknown outcome. Do not repeat it; inspect its effects with read-only tools first."
		if len(checks) > 0 {
			detail, _ := json.Marshal(checks)
			message += " Current postconditions: " + string(detail)
		}
		return toolOutcome{output: message, errMsg: message, blocked: true}, true
	}
	return toolOutcome{}, false
}
