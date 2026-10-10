package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/extension"
	"reasonix/internal/extension/dispatch"
	"reasonix/internal/extension/protocol"
)

func TestCompactionPrepareCannotExpandAutomaticSummaryPastWindow(t *testing.T) {
	const window = 60_000
	tests := []struct {
		name        string
		replacement func(*testing.T, json.RawMessage) protocol.InterceptResult
	}{
		{
			name: "messages",
			replacement: func(t *testing.T, _ json.RawMessage) protocol.InterceptResult {
				return replaceWith(t, dispatch.CompactionPreparePayload{
					Messages: []protocol.ProviderMessage{{
						Role: protocol.ProviderRoleUser, Content: strings.Repeat("x", window*4),
					}},
				})
			},
		},
		{
			name: "guidance",
			replacement: func(t *testing.T, raw json.RawMessage) protocol.InterceptResult {
				var payload dispatch.CompactionPreparePayload
				if err := json.Unmarshal(raw, &payload); err != nil {
					t.Fatalf("decode compaction.prepare payload: %v", err)
				}
				payload.Guidance = strings.Repeat("preserve expanded guidance ", window)
				return replaceWith(t, payload)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeDispatchClient{interceptFn: func(ev protocol.InterceptEvent, raw json.RawMessage) (protocol.InterceptResult, error) {
				if ev == protocol.EventCompactionPrepare {
					return tc.replacement(t, raw), nil
				}
				return protocol.InterceptResult{Decision: protocol.DecisionContinue}, nil
			}}
			prov := &opaqueWindowProvider{}
			a := agentOverForceWindow(t, prov, foldableSessionOverForce(120), window)
			a.svc.extensions = newExtDispatcher(client, true, nil, extension.PointCompactionPrepare)

			var rejected *ContextMaintenanceReceipt
			a.svc.sink = event.FuncSink(func(e event.Event) {
				if e.Kind == event.ContextMaintenanceEvent && e.Maintenance != nil && e.Maintenance.Status == "blocked" {
					rejected = &ContextMaintenanceReceipt{Status: e.Maintenance.Status, Reason: e.Maintenance.Reason}
				}
			})

			// The oversized replacement is never sent. 任务719: over the
			// ceiling the bounded tail view first degrades the provider-visible
			// request under the hard ceiling, so the lossy truncation rescue
			// loses its premise — the summary-budget rejection stays visible as
			// the blocked receipt while the turn still leaves with an
			// admissible view.
			if err := prepareContext(context.Background(), a, CompactionTriggerPressure); err != nil {
				t.Fatalf("pressure maintenance error = %v, want the rejection absorbed with an admissible view", err)
			}
			if len(prov.requests) != 0 {
				t.Fatalf("summary requests = %d, want none for an oversized extension replacement", len(prov.requests))
			}
			if rejected == nil || !strings.Contains(rejected.Reason, "prepared summary request") {
				t.Fatalf("blocked receipt = %+v, want the final summary-budget rejection", rejected)
			}
			if got := a.estimatedVisibleRequestTokens(a.modelVisibleMessages()); got >= a.hardInputCeiling() {
				t.Fatalf("visible view = %d tokens, want the bounded tail window under the ceiling %d", got, a.hardInputCeiling())
			}
			if receipt := a.sess.compactionState.LastReceipt; receipt == nil || receipt.Status != "blocked" {
				t.Fatalf("receipt = %+v, want the blocked summary-budget rejection kept", receipt)
			}
		})
	}
}
