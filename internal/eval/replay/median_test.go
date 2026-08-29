package replay

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestMedianReportFivePairedRuns(t *testing.T) {
	pairs, err := LoadPairs(filepath.Join("testdata", "paired_runs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 5 {
		t.Fatalf("pairs = %d, want 5", len(pairs))
	}
	got := MedianReport(pairs)
	if got.Pairs != 5 {
		t.Fatalf("report pairs = %d", got.Pairs)
	}
	if got.MedianListDelta >= 0 {
		t.Fatalf("median list delta = %v, want proxy fewer tools/list than baseline", got.MedianListDelta)
	}
	if got.MedianLatencyDelta >= 0 {
		t.Fatalf("median latency delta = %v, want proxy faster than baseline", got.MedianLatencyDelta)
	}
}

func TestReleaseGateRequiresLivePairedEvidence(t *testing.T) {
	if _, err := LoadReleaseDataset(filepath.Join("testdata", "paired_runs.json")); err == nil {
		t.Fatal("legacy synthetic array was accepted as release evidence")
	}
	dataset := passingReleaseDataset()
	dataset.EvidenceKind = "synthetic"
	if _, err := EvaluateReleaseGate(dataset); err == nil || !strings.Contains(err.Error(), "synthetic") {
		t.Fatalf("synthetic evidence error = %v", err)
	}
}

func TestReleaseGateAppliesPerformanceAndQualityThresholds(t *testing.T) {
	dataset := passingReleaseDataset()
	report, err := EvaluateReleaseGate(dataset)
	if err != nil || !report.Pass {
		t.Fatalf("passing gate = %+v err=%v", report, err)
	}
	dataset.Pairs[0].Candidate.RemoteInvalidCalls = 1
	report, err = EvaluateReleaseGate(dataset)
	if err != nil || report.Pass || len(report.Failures) == 0 {
		t.Fatalf("failing gate = %+v err=%v", report, err)
	}
}

func passingReleaseDataset() ReleaseDataset {
	pairs := make([]ReleasePair, 5)
	for i := range pairs {
		pairs[i] = ReleasePair{
			Name: "live-run-" + string(rune('1'+i)),
			Baseline: ReleaseRun{
				DurationMs: 20_000, TotalTokens: 1_000_000, CacheHitRate: 0.90, MainModelRounds: 19,
			},
			Candidate: ReleaseRun{
				DurationMs: 10_000, TotalTokens: 600_000, CacheHitRate: 0.89, MainModelRounds: 10,
				FoundSourceConflict: true, RespectedUserDecision: true, ClosedImplementation: true, CorrectAnchorsAndTests: true,
			},
		}
	}
	return ReleaseDataset{EvidenceKind: "live_paired", Model: "test-model", TaskSet: "test-fixture", Pairs: pairs}
}
