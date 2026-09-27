package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	"reasonix/internal/config"
)

// Task 338: read-only pages for the monitoring panel — a WS time series from
// the existing perf samples and a pprof breakdown for the heap button. Both
// are pure on-demand reads: they start no sampler and write nothing, so the
// task-182 "closed = zero cost" contract is untouched.

// PerfPoint is one plotted sample. Only the fields the chart shows cross the
// bridge; the jsonl keeps the full perfSample (task 182).
type PerfPoint struct {
	TS           string  `json:"ts"`
	WorkingSetMB float64 `json:"workingSetMb"`
	HeapInuseMB  float64 `json:"heapInuseMb"`
	Goroutines   int     `json:"goroutines"`
	Handles      uint32  `json:"handles"`
	CPUPercent   float64 `json:"cpuPercent"`
}

// PerfTimeSeriesView is the window the panel asked for. Available says whether
// any sample landed inside the window; Enabled mirrors the sampler switch so
// the panel can point at it instead of showing a dead chart (task 338 empty
// state).
type PerfTimeSeriesView struct {
	Enabled         bool        `json:"enabled"`
	Available       bool        `json:"available"`
	IntervalSeconds int         `json:"intervalSeconds"`
	Points          []PerfPoint `json:"points"`
}

// readPerfSamples returns the perf-sample-*.jsonl points inside [now-window,
// now], oldest first. A missing directory means "never sampled" (no error, no
// directory created); an unparsable line is skipped so a partially written
// tail cannot hide the series.
func readPerfSamples(dir string, window time.Duration, now time.Time) ([]perfSample, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cutoff := now.Add(-window)
	var out []perfSample
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, perfMonitorFilePrefix) || !strings.HasSuffix(name, perfMonitorFileSuffix) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		for _, line := range bytes.Split(data, []byte("\n")) {
			trimmed := bytes.TrimSpace(line)
			if len(trimmed) == 0 {
				continue
			}
			var sample perfSample
			if err := json.Unmarshal(trimmed, &sample); err != nil {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, sample.TS)
			if err != nil || ts.Before(cutoff) || ts.After(now.Add(time.Minute)) {
				continue
			}
			out = append(out, sample)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TS < out[j].TS })
	return out, nil
}

// PerfTimeSeries serves the monitoring panel's WS chart (task 338). windowMinutes
// is clamped to (0, 48h]; 0 or a bogus value falls back to the last hour.
func (a *App) PerfTimeSeries(windowMinutes int) PerfTimeSeriesView {
	if windowMinutes <= 0 || windowMinutes > 48*60 {
		windowMinutes = 60
	}
	view := PerfTimeSeriesView{Points: []PerfPoint{}, IntervalSeconds: 5}
	if cfg, err := config.Load(); err == nil && cfg != nil {
		view.Enabled = cfg.Desktop.ExperimentalPerfMonitor || cfg.Agent.ExperimentalPerfMonitor
		if cfg.Agent.PerfMonitorIntervalSeconds > 0 {
			view.IntervalSeconds = cfg.Agent.PerfMonitorIntervalSeconds
		}
	}
	samples, err := readPerfSamples(perfMonitorDir(), time.Duration(windowMinutes)*time.Minute, time.Now())
	if err != nil {
		return view
	}
	for _, sample := range samples {
		view.Points = append(view.Points, PerfPoint{
			TS:           sample.TS,
			WorkingSetMB: sample.WorkingSetMB,
			HeapInuseMB:  sample.HeapInuseMB,
			Goroutines:   sample.Goroutines,
			Handles:      sample.Handles,
			CPUPercent:   sample.CPUPercent,
		})
	}
	view.Available = len(view.Points) > 0
	return view
}

// HeapCategory is one pie slice: bytes are raw, percent is of the profile's
// inuse_space total (1 decimal; the five slices sum to 100 within rounding).
type HeapCategory struct {
	Key     string  `json:"key"`
	Name    string  `json:"name"`
	Bytes   int64   `json:"bytes"`
	Percent float64 `json:"percent"`
}

// HeapBreakdownView is the post-sample pie data plus the pprof file path so
// the panel can keep the export-for-troubleshooting entry (task 338).
type HeapBreakdownView struct {
	Path       string         `json:"path"`
	SampledAt  string         `json:"sampledAt"`
	TotalBytes int64          `json:"totalBytes"`
	Categories []HeapCategory `json:"categories"`
}

// heapCategoryKeys is the task-338 bucket order (187-battle categories).
var heapCategoryKeys = []struct {
	key  string
	name string
	leaf string // substring that assigns a leaf frame to this bucket
}{
	{"transcript", "Transcript", "ranscript"},
	{"snapshot", "Snapshot", "napshot"},
	{"dag", "DAG", ""},
	{"frontendCache", "Frontend cache", ""},
	{"other", "Other", ""},
}

// SampleHeapBreakdown samples the live heap (same write as the old heap
// button — the pprof file remains on disk for export) and returns the
// category split for the pie chart (task 338).
func (a *App) SampleHeapBreakdown() (HeapBreakdownView, error) {
	view := HeapBreakdownView{Categories: []HeapCategory{}}
	helper := &perfMonitor{dir: perfMonitorDir()}
	path := helper.writeHeapProfile(time.Now())
	if path == "" {
		return view, fmt.Errorf("heap profile could not be written under %s", perfMonitorDir())
	}
	view.Path = path
	if info, err := os.Stat(path); err == nil {
		view.SampledAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return view, fmt.Errorf("read heap profile: %w", err)
	}
	parsed, err := profile.ParseData(data)
	if err != nil {
		return view, fmt.Errorf("parse heap profile: %w", err)
	}
	categories, total := aggregateHeapProfile(parsed)
	view.Categories = categories
	view.TotalBytes = total
	return view, nil
}

// aggregateHeapProfile folds every positive inuse_space sample into the
// task-338 buckets, matching leaf→root on the sample's frames so a hot
// transcript allocation lands in Transcript even when its callers span other
// subsystems. All five buckets are always returned (an empty pie shows an
// honest zero instead of a missing legend).
func aggregateHeapProfile(p *profile.Profile) ([]HeapCategory, int64) {
	byKey := map[string]int64{}
	for _, entry := range heapCategoryKeys {
		byKey[entry.key] = 0
	}
	if p == nil {
		return heapCategoriesFrom(byKey, 0), 0
	}
	valueIdx := -1
	for i, sampleType := range p.SampleType {
		if sampleType != nil && sampleType.Type == "inuse_space" {
			valueIdx = i
			break
		}
	}
	if valueIdx < 0 {
		// heap profiles end in inuse_space; taking the last value keeps the
		// chart working even if a future writer renames the middle types.
		valueIdx = len(p.SampleType) - 1
	}
	var total int64
	for _, sample := range p.Sample {
		if sample == nil || valueIdx < 0 || valueIdx >= len(sample.Value) {
			continue
		}
		value := sample.Value[valueIdx]
		if value <= 0 {
			continue
		}
		total += value
		byKey[heapClassify(sample.Location)] += value
	}
	return heapCategoriesFrom(byKey, total), total
}

// heapClassify assigns one sample by its first matching frame, leaf first.
func heapClassify(locations []*profile.Location) string {
	for _, location := range locations {
		if location == nil {
			continue
		}
		for _, line := range location.Line {
			if line.Function == nil {
				continue
			}
			name := line.Function.Name
			for _, entry := range heapCategoryKeys {
				if entry.key == "other" {
					continue
				}
				if entry.leaf != "" && strings.Contains(name, entry.leaf) {
					return entry.key
				}
				if entry.key == "dag" && (strings.Contains(name, "DAG") || strings.Contains(name, "dag")) {
					return entry.key
				}
				if entry.key == "frontendCache" && (strings.Contains(name, "atalog") || strings.Contains(name, "ydrat") || strings.Contains(name, "Cache")) {
					return entry.key
				}
			}
		}
	}
	return "other"
}

func heapCategoriesFrom(byKey map[string]int64, total int64) []HeapCategory {
	categories := make([]HeapCategory, 0, len(heapCategoryKeys))
	for _, entry := range heapCategoryKeys {
		bytes := byKey[entry.key]
		percent := 0.0
		if total > 0 {
			percent = math.Round(float64(bytes)*1000/float64(total)) / 10
		}
		categories = append(categories, HeapCategory{Key: entry.key, Name: entry.name, Bytes: bytes, Percent: percent})
	}
	return categories
}

// HeapBreakdownPath returns the newest exported pprof (for the panel's export
// entry without re-sampling); "" when none exists yet.
func HeapBreakdownPath() string {
	return heapBreakdownPathIn(perfMonitorDir())
}

// heapBreakdownPathIn is the injectable core — tests must not share the
// process-cached perf dir.
func heapBreakdownPathIn(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	newest := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "heap-") || !strings.HasSuffix(entry.Name(), ".pprof") {
			continue
		}
		if entry.Name() > newest {
			newest = entry.Name()
		}
	}
	if newest == "" {
		return ""
	}
	return filepath.Join(dir, newest)
}
