package main

import (
	"math"
	"strings"
	"testing"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/stats"
)

// TestResolveStatsRange covers the six branches of resolveStatsRange: the four
// preset day ranges, custom valid/invalid dates, and the unknown-range default.
// resolveStatsRange anchors to its own internal time.Now(), which cannot be
// injected, so the test takes the clock once before and once after the call
// (tl1694): a run at midnight may straddle the day boundary, and the "to"
// day must then equal one of the two bracketing days. Relative expectations
// are pinned via the span (to-from+1 days) instead of absolute calendar dates,
// which stays byte-for-byte as strict as before on any non-midnight run.
func TestResolveStatsRange(t *testing.T) {
	now := time.Now()
	today := func(offsetDays int) time.Time {
		y, m, d := now.AddDate(0, 0, offsetDays).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	}

	tests := []struct {
		name    string
		req     UsageStatsRequest
		span    int       // success cases: expected inclusive day count (to-from+1)
		fixed   [2]string // custom-range echo: exact from/to dates, clock-independent
		wantErr string    // substring of the error, "" for success
	}{
		{
			name: "preset 7 days",
			req:  UsageStatsRequest{Range: "7"},
			span: 7,
		},
		{
			name: "preset 14 days",
			req:  UsageStatsRequest{Range: "14"},
			span: 14,
		},
		{
			name: "preset 30 days",
			req:  UsageStatsRequest{Range: "30"},
			span: 30,
		},
		{
			name: "preset 90 days",
			req:  UsageStatsRequest{Range: "90"},
			span: 90,
		},
		{
			name:  "custom valid dates",
			req:   UsageStatsRequest{Range: "custom", From: "2026-07-01", To: "2026-07-31"},
			span:  31,
			fixed: [2]string{"2026-07-01", "2026-07-31"},
		},
		{
			name:    "custom missing from",
			req:     UsageStatsRequest{Range: "custom", To: "2026-07-31"},
			wantErr: "needs valid from/to dates",
		},
		{
			name:    "custom malformed to",
			req:     UsageStatsRequest{Range: "custom", From: "2026-07-01", To: "not-a-date"},
			wantErr: "needs valid from/to dates",
		},
		{
			name:    "custom reversed dates",
			req:     UsageStatsRequest{Range: "custom", From: "2026-07-31", To: "2026-07-01"},
			wantErr: "must not be after",
		},
		{
			name: "custom future to date",
			req: UsageStatsRequest{
				Range: "custom",
				From:  today(0).Format(dateLayout),
				// tl1694: To 推远到 today(+2)——即便构造后测试运行跨过午夜，
				// 生产钟当天最多偏移一天，To 仍必为未来日，恒报错。
				To: today(2).Format(dateLayout),
			},
			wantErr: "must not be in the future",
		},
		{
			name: "custom maximum span",
			req: UsageStatsRequest{
				Range: "custom",
				From:  today(-(maxStatsCustomRangeDays - 1)).Format(dateLayout),
				To:    today(0).Format(dateLayout),
			},
			span:  maxStatsCustomRangeDays,
			fixed: [2]string{today(-(maxStatsCustomRangeDays - 1)).Format(dateLayout), today(0).Format(dateLayout)},
		},
		{
			name: "custom over maximum span",
			req: UsageStatsRequest{
				Range: "custom",
				From:  today(-maxStatsCustomRangeDays).Format(dateLayout),
				To:    today(0).Format(dateLayout),
			},
			wantErr: "cannot exceed",
		},
		{
			name: "empty range defaults to 7 days",
			req:  UsageStatsRequest{},
			span: 7,
		},
		{
			name: "unknown range defaults to 7 days",
			req:  UsageStatsRequest{Range: "365"},
			span: 7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()
			from, to, err := resolveStatsRange(tt.req)
			after := time.Now()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Compare local calendar-day identity: resolveStatsRange returns
			// from at 00:00:00 and to at 23:59:59.
			dayOf := func(tm time.Time) string { return tm.Format(dateLayout) }
			if tt.fixed[0] != "" {
				// Custom ranges echo their input dates — independent of the
				// production clock, no midnight straddle possible.
				if dayOf(from) != tt.fixed[0] {
					t.Fatalf("from: want %s, got %s", tt.fixed[0], dayOf(from))
				}
				if dayOf(to) != tt.fixed[1] {
					t.Fatalf("to: want %s, got %s", tt.fixed[1], dayOf(to))
				}
			} else {
				// tl1694: the production clock lies within [before, after]; a
				// midnight-straddling run accepts either bracketing day for
				// "to", then anchors "from" off the actual "to" so the span
				// invariant stays exact.
				if dayOf(to) != dayOf(before) && dayOf(to) != dayOf(after) {
					t.Fatalf("to: want %s or %s, got %s", dayOf(before), dayOf(after), dayOf(to))
				}
				if wantFrom := dayOf(to.AddDate(0, 0, -(tt.span - 1))); dayOf(from) != wantFrom {
					t.Fatalf("from: want %s, got %s", wantFrom, dayOf(from))
				}
			}
			// Span invariant (DST-safe day diff): inclusive day count.
			fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, from.Location())
			toDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, to.Location())
			if got := int(math.Round(toDay.Sub(fromDay).Hours()/24)) + 1; got != tt.span {
				t.Fatalf("span: want %d days, got %d", tt.span, got)
			}
		})
	}
}

// TestResolveStatsRangeToIsEndOfDay pins the "to" boundary to 23:59:59 so a
// future change cannot silently shrink the range to a single moment.
func TestResolveStatsRangeToIsEndOfDay(t *testing.T) {
	_, to, err := resolveStatsRange(UsageStatsRequest{Range: "7"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if to.Hour() != 23 || to.Minute() != 59 || to.Second() != 59 {
		t.Fatalf("to must be end-of-day 23:59:59, got %v", to)
	}
}

// sleepOffMidnight (tl1694)：距本地午夜不足 guard 时先睡过午夜再留余量。
// 用途：Emit 与 Query 都各自内部取 time.Now()（不可注入），两次取钟不能夹着
// 日界跳变——否则记录落桶日与查询窗错开一天。只在午夜前 3 秒内运行才会等待。
func sleepOffMidnight(t *testing.T) {
	t.Helper()
	const guard = 3 * time.Second
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	if d := next.Sub(now); d < guard {
		t.Logf("tl1694 midnight guard: sleeping %v past local midnight", d+guard)
		time.Sleep(d + guard)
	}
}

func TestUsageStatsFlushesPendingRecorderWrites(t *testing.T) {
	sleepOffMidnight(t)
	t.Setenv("REASONIX_STATE_HOME", t.TempDir())
	recorder := stats.NewRecorder(event.Discard, config.StatsDir(), "desktop")
	recorder.Emit(event.Event{
		Kind: event.Usage, ModelRef: "deepseek/model",
		Usage: &provider.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	})

	result, err := (&App{}).UsageStats(UsageStatsRequest{Range: "7", Source: "desktop"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tokens != 5 || result.Requests != 1 {
		t.Fatalf("usage stats = tokens %d requests %d, want 5/1", result.Tokens, result.Requests)
	}
}
