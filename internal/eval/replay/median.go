// Package replay computes paired-run medians for use_capability eval harnesses.
package replay

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

// Pair is one proxy-vs-baseline observation.
type Pair struct {
	Name              string  `json:"name"`
	ProxyListCount    int     `json:"proxy_list_count"`
	BaselineListCount int     `json:"baseline_list_count"`
	ProxyLatencyMs    float64 `json:"proxy_latency_ms"`
	BaselineLatencyMs float64 `json:"baseline_latency_ms"`
}

// Report is the median of five (or more) paired runs.
type Report struct {
	Pairs              int     `json:"pairs"`
	MedianListDelta    float64 `json:"median_list_delta"`
	MedianLatencyDelta float64 `json:"median_latency_delta"`
}

// LoadPairs reads a JSON array of paired runs.
func LoadPairs(path string) ([]Pair, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pairs []Pair
	if err := json.Unmarshal(raw, &pairs); err != nil {
		return nil, fmt.Errorf("decode paired runs: %w", err)
	}
	return pairs, nil
}

// MedianReport returns median(proxy-baseline) for list count and latency.
func MedianReport(pairs []Pair) Report {
	list := make([]float64, 0, len(pairs))
	lat := make([]float64, 0, len(pairs))
	for _, p := range pairs {
		list = append(list, float64(p.ProxyListCount-p.BaselineListCount))
		lat = append(lat, p.ProxyLatencyMs-p.BaselineLatencyMs)
	}
	return Report{
		Pairs:              len(pairs),
		MedianListDelta:    median(list),
		MedianLatencyDelta: median(lat),
	}
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// ReleaseRun is one content-free measurement from the fixed paired replay set.
type ReleaseRun struct {
	DurationMs             float64 `json:"duration_ms"`
	TotalTokens            int     `json:"total_tokens"`
	CacheHitRate           float64 `json:"cache_hit_rate"`
	MainModelRounds        int     `json:"main_model_rounds"`
	ToolArgumentFailures   int     `json:"tool_argument_failures"`
	RemoteInvalidCalls     int     `json:"remote_invalid_calls"`
	Clarifications         int     `json:"clarifications"`
	FoundSourceConflict    bool    `json:"found_source_conflict"`
	RespectedUserDecision  bool    `json:"respected_user_decision"`
	ClosedImplementation   bool    `json:"closed_implementation_choices"`
	CorrectAnchorsAndTests bool    `json:"correct_anchors_and_tests"`
}

// ReleasePair compares one candidate run with its same-model/task baseline.
type ReleasePair struct {
	Name      string     `json:"name"`
	Baseline  ReleaseRun `json:"baseline"`
	Candidate ReleaseRun `json:"candidate"`
}

// ReleaseDataset is intentionally distinct from the unit-test median fixture.
// Only evidence_kind=live_paired can satisfy the release gate.
type ReleaseDataset struct {
	EvidenceKind string        `json:"evidence_kind"`
	Model        string        `json:"model"`
	TaskSet      string        `json:"task_set"`
	Pairs        []ReleasePair `json:"pairs"`
}

// ReleaseGateReport is safe to publish: it contains aggregate measurements and
// failure labels, never prompts, paths, credentials, or tool arguments.
type ReleaseGateReport struct {
	Pass                    bool     `json:"pass"`
	Pairs                   int      `json:"pairs"`
	MedianDurationReduction float64  `json:"median_duration_reduction"`
	P90CandidateDurationMs  float64  `json:"p90_candidate_duration_ms"`
	MedianTokenReduction    float64  `json:"median_token_reduction"`
	P90CandidateTokens      float64  `json:"p90_candidate_tokens"`
	MedianCacheDelta        float64  `json:"median_cache_delta"`
	MedianCandidateRounds   float64  `json:"median_candidate_rounds"`
	Failures                []string `json:"failures,omitempty"`
}

// LoadReleaseDataset reads a release-gate dataset. It never accepts the legacy
// array fixture, which prevents synthetic median examples from being mistaken
// for performance evidence.
func LoadReleaseDataset(path string) (ReleaseDataset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ReleaseDataset{}, err
	}
	var dataset ReleaseDataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		return ReleaseDataset{}, fmt.Errorf("decode release dataset: %w", err)
	}
	return dataset, nil
}

// EvaluateReleaseGate applies the Reasonix 1.33.0 paired-run publication gates.
func EvaluateReleaseGate(dataset ReleaseDataset) (ReleaseGateReport, error) {
	if dataset.EvidenceKind != "live_paired" {
		return ReleaseGateReport{}, fmt.Errorf("evidence_kind must be live_paired; synthetic fixtures cannot qualify a release")
	}
	if strings.TrimSpace(dataset.Model) == "" || strings.TrimSpace(dataset.TaskSet) == "" {
		return ReleaseGateReport{}, fmt.Errorf("model and task_set are required")
	}
	if len(dataset.Pairs) < 5 {
		return ReleaseGateReport{}, fmt.Errorf("at least 5 paired live runs are required, got %d", len(dataset.Pairs))
	}

	report := ReleaseGateReport{Pairs: len(dataset.Pairs)}
	var durationReductions, candidateDurations, tokenReductions, candidateTokens, cacheDeltas, candidateRounds []float64
	seen := map[string]bool{}
	for i, pair := range dataset.Pairs {
		name := strings.TrimSpace(pair.Name)
		if name == "" || seen[name] {
			return ReleaseGateReport{}, fmt.Errorf("pair %d has an empty or duplicate name", i+1)
		}
		seen[name] = true
		if err := validateReleaseRun(pair.Baseline, false); err != nil {
			return ReleaseGateReport{}, fmt.Errorf("pair %q baseline: %w", name, err)
		}
		if err := validateReleaseRun(pair.Candidate, true); err != nil {
			return ReleaseGateReport{}, fmt.Errorf("pair %q candidate: %w", name, err)
		}
		durationReductions = append(durationReductions, 1-pair.Candidate.DurationMs/pair.Baseline.DurationMs)
		candidateDurations = append(candidateDurations, pair.Candidate.DurationMs)
		tokenReductions = append(tokenReductions, 1-float64(pair.Candidate.TotalTokens)/float64(pair.Baseline.TotalTokens))
		candidateTokens = append(candidateTokens, float64(pair.Candidate.TotalTokens))
		cacheDeltas = append(cacheDeltas, pair.Candidate.CacheHitRate-pair.Baseline.CacheHitRate)
		candidateRounds = append(candidateRounds, float64(pair.Candidate.MainModelRounds))
		if pair.Candidate.RemoteInvalidCalls != 0 {
			report.Failures = append(report.Failures, name+": remote invalid calls must be 0")
		}
		if pair.Candidate.ToolArgumentFailures != 0 {
			report.Failures = append(report.Failures, name+": tool argument failures must be 0")
		}
		if pair.Candidate.Clarifications > 1 {
			report.Failures = append(report.Failures, name+": clarifications must be <= 1")
		}
	}
	report.MedianDurationReduction = median(durationReductions)
	report.P90CandidateDurationMs = percentile(candidateDurations, 0.9)
	report.MedianTokenReduction = median(tokenReductions)
	report.P90CandidateTokens = percentile(candidateTokens, 0.9)
	report.MedianCacheDelta = median(cacheDeltas)
	report.MedianCandidateRounds = median(candidateRounds)
	if report.MedianDurationReduction < 0.40 {
		report.Failures = append(report.Failures, "median duration reduction must be >= 40%")
	}
	if report.MedianTokenReduction < 0.35 {
		report.Failures = append(report.Failures, "median token reduction must be >= 35%")
	}
	if report.MedianCacheDelta < -0.02 {
		report.Failures = append(report.Failures, "median cache-hit rate must not drop by more than 2 percentage points")
	}
	if report.MedianCandidateRounds > 12 {
		report.Failures = append(report.Failures, "median main-model rounds must be <= 12")
	}
	report.Pass = len(report.Failures) == 0
	return report, nil
}

func validateReleaseRun(run ReleaseRun, requireQuality bool) error {
	if run.DurationMs <= 0 || run.TotalTokens <= 0 || run.MainModelRounds <= 0 {
		return fmt.Errorf("duration_ms, total_tokens, and main_model_rounds must be positive")
	}
	if run.CacheHitRate < 0 || run.CacheHitRate > 1 {
		return fmt.Errorf("cache_hit_rate must be between 0 and 1")
	}
	if run.ToolArgumentFailures < 0 || run.RemoteInvalidCalls < 0 || run.Clarifications < 0 {
		return fmt.Errorf("failure and clarification counters must be non-negative")
	}
	if requireQuality && (!run.FoundSourceConflict || !run.RespectedUserDecision || !run.ClosedImplementation || !run.CorrectAnchorsAndTests) {
		return fmt.Errorf("all four candidate quality checks must be true")
	}
	return nil
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	index := max(0, int(math.Ceil(float64(len(sorted))*quantile))-1)
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return sorted[index]
}
