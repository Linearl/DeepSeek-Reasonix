package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

// tl1379 功能层：重置会话费用——只清费用记账（ledger+标量+分来源费用），
// token 统计保持不动；幂等；sidecar 同步落盘。
// tl1557：破坏性动作可追溯——非零重置 slog 留痕（前值+币种+会话路径），
// 已零会话的幂等重写不写日志。

func newCostTrackingTab(t *testing.T) (*App, *WorkspaceTab, string) {
	t.Helper()
	sessionPath := filepath.Join(t.TempDir(), "session.jsonl")
	tab := &WorkspaceTab{ID: "cost-reset", SessionPath: sessionPath}
	app := &App{tabs: map[string]*WorkspaceTab{tab.ID: tab}, tabOrder: []string{tab.ID}, activeTabID: tab.ID}
	return app, tab, sessionPath
}

// tl1557：非零费用重置必须 slog 留痕（前值可追溯），幂等重写不产生日志行。
func TestClearSessionCostLogsPrevValue(t *testing.T) {
	app, tab, sessionPath := newCostTrackingTab(t)
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 100_000, TotalTokens: 100_000},
		Pricing:     &provider.Pricing{Input: 1, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
	})
	if tab.telemetrySnapshot().Usage.SessionCost <= 0 {
		t.Fatalf("fixture setup wrong: cost=%f", tab.telemetrySnapshot().Usage.SessionCost)
	}

	var buf bytes.Buffer
	prevHandler := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(prevHandler)

	if err := app.ClearSessionCostForTab(tab.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	line := buf.String()
	if !strings.Contains(line, "desktop: session cost reset") {
		t.Fatalf("non-zero reset not logged: %q", line)
	}
	if !strings.Contains(line, "prev_cost=") || !strings.Contains(line, "prev_currency=CNY") {
		t.Fatalf("log line missing prev value fields: %q", line)
	}
	if !strings.Contains(line, sessionPath) {
		t.Fatalf("log line missing session path: %q", line)
	}

	// 幂等重置：已零会话再清一次不产生新日志行。
	buf.Reset()
	if err := app.ClearSessionCostForTab(tab.ID); err != nil {
		t.Fatalf("idempotent reset: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("already-zero reset must stay silent, got: %q", buf.String())
	}
}

func TestClearSessionCostClearsCostKeepsTokens(t *testing.T) {
	app, tab, sessionPath := newCostTrackingTab(t)
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 1_000_000, CompletionTokens: 500_000, TotalTokens: 1_500_000, ReasoningTokens: 100_000},
		Pricing:     &provider.Pricing{Input: 1, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
		SessionHit:  200_000,
		SessionMiss: 800_000,
	})
	before := tab.telemetrySnapshot().Usage
	if before.SessionCost <= 0 || before.TotalTokens != 1_500_000 || before.RequestCount != 1 {
		t.Fatalf("fixture setup wrong: cost=%f tokens=%d requests=%d", before.SessionCost, before.TotalTokens, before.RequestCount)
	}

	if err := app.ClearSessionCostForTab(tab.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got := tab.telemetrySnapshot().Usage
	if got.SessionCost != 0 || got.SessionCostUsd != 0 || got.SessionCurrency != "" {
		t.Fatalf("cost scalars not cleared: %+v", got)
	}
	if got.SessionCostQuote == nil || !got.SessionCostQuote.Complete || got.SessionCostQuote.Selected == nil || got.SessionCostQuote.Selected.Amount != "0" {
		t.Fatalf("post-reset quote = %+v, want complete matched zero", got.SessionCostQuote)
	}
	if got.CostLedger == nil || len(got.CostLedger.Entries) != 0 {
		t.Fatalf("cost ledger not cleared: %+v", got.CostLedger)
	}
	// token 统计保持
	if got.TotalTokens != 1_500_000 || got.PromptTokens != 1_000_000 || got.CompletionTokens != 500_000 || got.ReasoningTokens != 100_000 {
		t.Fatalf("token stats mutated: %+v", got)
	}
	if got.CacheHitTokens != 200_000 || got.CacheMissTokens != 800_000 || got.RequestCount != 1 {
		t.Fatalf("cache/request stats mutated: %+v", got)
	}
	if src := got.Sources[event.UsageSourceExecutor]; src.TotalTokens != 1_500_000 || src.SessionCost != 0 {
		t.Fatalf("per-source split wrong: tokens=%d cost=%f", src.TotalTokens, src.SessionCost)
	}

	// sidecar 同步落盘：读取持久化文件核对费用已清零。
	b, err := readFileUTF8(sessionPath + ".telemetry.json")
	if err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	var persisted tabTelemetrySnapshot
	if err := json.Unmarshal(b, &persisted); err != nil {
		t.Fatalf("sidecar parse: %v", err)
	}
	if persisted.Usage.SessionCost != 0 || (persisted.Usage.CostLedger != nil && len(persisted.Usage.CostLedger.Entries) != 0) {
		t.Fatalf("persisted sidecar still holds cost: cost=%f entries=%d", persisted.Usage.SessionCost, len(persisted.Usage.CostLedger.Entries))
	}
	if persisted.Usage.TotalTokens != 1_500_000 {
		t.Fatalf("persisted tokens mutated: %d", persisted.Usage.TotalTokens)
	}
}

func TestClearSessionCostIdempotent(t *testing.T) {
	app, tab, _ := newCostTrackingTab(t)
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 100_000, TotalTokens: 100_000},
		Pricing:     &provider.Pricing{Input: 1, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
	})
	if err := app.ClearSessionCostForTab(tab.ID); err != nil {
		t.Fatalf("first reset: %v", err)
	}
	first := tab.telemetrySnapshot().Usage
	if err := app.ClearSessionCostForTab(tab.ID); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	second := tab.telemetrySnapshot().Usage
	if first.SessionCost != second.SessionCost || first.TotalTokens != second.TotalTokens {
		t.Fatalf("second reset drifted: first=%+v second=%+v", first.SessionCost, second.SessionCost)
	}
	if second.SessionCost != 0 || second.TotalTokens != 100_000 {
		t.Fatalf("post-double-reset state wrong: cost=%f tokens=%d", second.SessionCost, second.TotalTokens)
	}
}

// 后续用量事件照常入账：重置不是「冻结」，新费用从零重新累计。
func TestClearSessionCostThenNewUsageAccrues(t *testing.T) {
	app, tab, _ := newCostTrackingTab(t)
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 1_000_000, TotalTokens: 1_000_000},
		Pricing:     &provider.Pricing{Input: 1, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
	})
	if err := app.ClearSessionCostForTab(tab.ID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	tab.recordUsage(event.Event{
		Usage:       &provider.Usage{PromptTokens: 500_000, TotalTokens: 500_000},
		Pricing:     &provider.Pricing{Input: 2, Currency: "CNY"},
		UsageSource: event.UsageSourceExecutor,
	})
	got := tab.telemetrySnapshot().Usage
	if got.SessionCost < 0.99 || got.SessionCost > 1.01 {
		t.Fatalf("post-reset accrual = %f, want ~1", got.SessionCost)
	}
	if got.TotalTokens != 1_500_000 {
		t.Fatalf("tokens must keep accruing across reset: %d", got.TotalTokens)
	}
}
