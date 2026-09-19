package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/control"
)

// Host performance monitor (task 184).
//
// Why: the 14.9 GB working-set report arrived as a single snapshot, so every
// candidate explanation had to be argued structurally rather than measured.
// This sampler turns the next occurrence into a time series — one JSON line per
// interval carrying the process counters, cumulative IO growth, the sizes of the
// session store's key files, and the session layer's resident operation
// bookkeeping (the remaining suspect for that growth).
//
// Off by default, and "off" is literal: with the switch off no ticker, goroutine
// or file handle exists.
const (
	perfMonitorDirName          = "perf"
	perfMonitorFilePrefix       = "perf-sample-"
	perfMonitorFileSuffix       = ".jsonl" // safe here: the session scanner only adopts *.jsonl inside session dirs
	perfMonitorDefaultSeconds   = 5
	perfMonitorMinSeconds       = 1
	perfMonitorMaxSeconds       = 300
	perfMonitorDefaultRetention = 168 // 7 days
	perfMonitorMaxKeyFiles      = 24
	perfMonitorMaxFileBytes     = 20 << 20
	// Heap profiles are the point of the memory investigation: one dump every
	// minute while the monitor runs, keeping only the newest few, so the profile
	// from the inflation moment is on disk without anyone having to act in time.
	perfMonitorHeapInterval  = 60 * time.Second
	perfMonitorHeapKept      = 3
	perfMonitorPruneInterval = time.Hour
)

// perfSample is one line of the time series. Field names are stable: the point
// of the file is to be read later by a person or a one-liner, not by this code.
type perfSample struct {
	TS              string  `json:"ts"`
	UptimeSeconds   float64 `json:"uptimeSeconds"`
	IntervalSeconds float64 `json:"intervalSeconds"`

	WorkingSetMB float64 `json:"workingSetMb,omitempty"`
	PrivateMB    float64 `json:"privateMb,omitempty"`
	HeapInuseMB  float64 `json:"heapInuseMb"`
	HeapSysMB    float64 `json:"heapSysMb"`
	Goroutines   int     `json:"goroutines"`
	Handles      uint32  `json:"handles,omitempty"`
	CPUPercent   float64 `json:"cpuPercent,omitempty"`
	// OSCounters is false when the platform reported nothing (see
	// perf_monitor_other.go): a zero must never be mistaken for a measurement.
	OSCounters bool `json:"osCounters"`

	IOWriteMB      float64 `json:"ioWriteMb,omitempty"`
	IOReadMB       float64 `json:"ioReadMb,omitempty"`
	IOWriteDeltaMB float64 `json:"ioWriteDeltaMb,omitempty"`
	IOReadDeltaMB  float64 `json:"ioReadDeltaMb,omitempty"`

	// FilesKB is path -> KiB for the configured key-file table.
	FilesKB map[string]float64 `json:"filesKb,omitempty"`

	// Heartbeat marks a sample whose key numbers were unchanged: it keeps the
	// series alive without paying for the file table again (back-off heartbeat).
	Heartbeat bool `json:"heartbeat,omitempty"`

	// Byte totals of the two session roots, with the append-only event logs
	// broken out: those are the files a long session keeps rewriting, so their
	// growth is what a "disk is growing" report is actually about.
	StoreMB    float64 `json:"storeMb"`
	ProjectsMB float64 `json:"projectsMb"`
	EventsMB   float64 `json:"eventsMb"`

	// Open work surface: how many tabs exist and how many of them carry a v4
	// store, so a memory curve can be read against session count.
	TabsOpen         int `json:"tabsOpen"`
	ResidentSessions int `json:"residentSessions"`

	// V4 residency: the in-memory idempotency bookkeeping of the open v4 stores.
	V4SessionsOpen   int   `json:"v4SessionsOpen"`
	V4Operations     int   `json:"v4Operations"`
	V4OperationBytes int64 `json:"v4OperationBytes"`
}

type perfMonitor struct {
	app       *App
	dir       string
	interval  time.Duration
	retention time.Duration
	paths     []string

	started time.Time
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once

	mu         sync.Mutex
	lastIO     procCounters
	lastSample time.Time
	lastCPU    float64
	// lastKey is the previous sample's key numbers: identical keys mean nothing
	// moved, which is what turns a sample into a heartbeat instead of a full line.
	lastKey string
}

// perfMonitorSettings resolves the config into sampler settings, clamping the
// same way the setters do so a hand-edited config cannot create a busy loop.
func perfMonitorSettings(cfg *config.Config) (time.Duration, time.Duration, []string) {
	seconds := perfMonitorDefaultSeconds
	retention := perfMonitorDefaultRetention
	var paths []string
	if cfg != nil {
		if cfg.Agent.PerfMonitorIntervalSeconds > 0 {
			seconds = cfg.Agent.PerfMonitorIntervalSeconds
		}
		if cfg.Agent.PerfMonitorRetentionHours > 0 {
			retention = cfg.Agent.PerfMonitorRetentionHours
		}
		paths = append(paths, cfg.Agent.PerfMonitorPaths...)
	}
	if seconds < perfMonitorMinSeconds {
		seconds = perfMonitorMinSeconds
	}
	if seconds > perfMonitorMaxSeconds {
		seconds = perfMonitorMaxSeconds
	}
	return time.Duration(seconds) * time.Second, time.Duration(retention) * time.Hour, paths
}

// perfMonitorDir is where samples live: under the desktop logs, never inside a
// session directory (the session scanner adopts *.jsonl, so the suffix keeps
// these files out of it, and the directory keeps them out of the way).
func perfMonitorDir() string {
	return filepath.Join(config.MemoryUserDir(), desktopLogDirName, perfMonitorDirName)
}

func newPerfMonitor(app *App, dir string, interval, retention time.Duration, paths []string) *perfMonitor {
	return &perfMonitor{
		app:       app,
		dir:       dir,
		interval:  interval,
		retention: retention,
		paths:     paths,
		started:   time.Now(),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
}

// Start begins sampling. It samples once immediately so a short-lived run still
// produces a line, then on every interval.
func (m *perfMonitor) Start() {
	if m == nil {
		return
	}
	go m.loop()
}

func (m *perfMonitor) Stop() {
	if m == nil {
		return
	}
	m.once.Do(func() {
		close(m.stop)
		<-m.done
	})
}

func (m *perfMonitor) loop() {
	defer close(m.done)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	prune := time.NewTicker(perfMonitorPruneInterval)
	defer prune.Stop()
	heap := time.NewTicker(perfMonitorHeapInterval)
	defer heap.Stop()
	m.sampleAndWrite(time.Now())
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.sampleAndWrite(now)
		case <-prune.C:
			m.prune(time.Now())
		case now := <-heap.C:
			m.writeHeapProfile(now)
		}
	}
}

func (m *perfMonitor) sampleAndWrite(now time.Time) {
	sample := m.takeSample(now)
	m.warnThresholds(sample)
	if err := m.appendSample(now, sample); err != nil {
		// The monitor must never be able to break the app it observes.
		slog.Warn("desktop: perf monitor write sample failed", "err", err)
	}
}

// warnThresholds turns a sample that left the observed band into a log line, so
// the same curve is visible in desktop.log without opening the series.
func (m *perfMonitor) warnThresholds(sample perfSample) {
	warn := func(metric string, value, limit float64) {
		slog.Warn("desktop: perf monitor threshold", "metric", metric, "value", value, "limit", limit)
	}
	if sample.OSCounters && sample.WorkingSetMB > perfMonitorWarnWorkingSetMB {
		warn("workingSetMb", sample.WorkingSetMB, perfMonitorWarnWorkingSetMB)
	}
	if sample.EventsMB > perfMonitorWarnEventsMB {
		warn("eventsMb", sample.EventsMB, perfMonitorWarnEventsMB)
	}
	if sample.StoreMB > perfMonitorWarnStoreMB {
		warn("storeMb", sample.StoreMB, perfMonitorWarnStoreMB)
	}
	if float64(sample.V4OperationBytes)/(1<<20) > perfMonitorWarnV4OpsMB {
		warn("v4OperationMb", float64(sample.V4OperationBytes)/(1<<20), perfMonitorWarnV4OpsMB)
	}
}

func (m *perfMonitor) takeSample(now time.Time) perfSample {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	counters := readProcCounters()

	sample := perfSample{
		TS:              now.UTC().Format(time.RFC3339Nano),
		UptimeSeconds:   now.Sub(m.started).Seconds(),
		IntervalSeconds: m.interval.Seconds(),
		HeapInuseMB:     float64(mem.HeapInuse) / (1 << 20),
		HeapSysMB:       float64(mem.Sys) / (1 << 20),
		Goroutines:      runtime.NumGoroutine(),
		OSCounters:      counters.Available,
	}
	if counters.Available {
		sample.WorkingSetMB = float64(counters.WorkingSetBytes) / (1 << 20)
		sample.PrivateMB = float64(counters.PrivateBytes) / (1 << 20)
		sample.Handles = counters.Handles
		sample.IOWriteMB = float64(counters.WriteBytes) / (1 << 20)
		sample.IOReadMB = float64(counters.ReadBytes) / (1 << 20)
	}

	m.mu.Lock()
	previous, previousAt, previousCPU := m.lastIO, m.lastSample, m.lastCPU
	m.lastIO, m.lastSample, m.lastCPU = counters, now, counters.CPUSeconds
	m.mu.Unlock()

	if counters.Available && !previousAt.IsZero() {
		elapsed := now.Sub(previousAt).Seconds()
		sample.IOWriteDeltaMB = float64(counters.WriteBytes-previous.WriteBytes) / (1 << 20)
		sample.IOReadDeltaMB = float64(counters.ReadBytes-previous.ReadBytes) / (1 << 20)
		if elapsed > 0 {
			sample.CPUPercent = (counters.CPUSeconds - previousCPU) / elapsed * 100
			if sample.CPUPercent < 0 {
				sample.CPUPercent = 0
			}
		}
	}

	storeMB, projectsMB, eventsMB := m.collectTotals()
	sample.StoreMB, sample.ProjectsMB, sample.EventsMB = storeMB, projectsMB, eventsMB
	tabs, resident, operations, operationBytes := m.v4Residency()
	sample.TabsOpen, sample.ResidentSessions = tabs, resident
	sample.V4SessionsOpen, sample.V4Operations, sample.V4OperationBytes = resident, operations, operationBytes

	// Back-off heartbeat: when nothing moved (byte totals, session counts, RSS to
	// the megabyte) the file table would repeat itself, so the line keeps the
	// series alive without the walk.
	key := fmt.Sprintf("%.0f|%.0f|%.0f|%d|%d|%d|%.0f",
		storeMB, projectsMB, eventsMB, tabs, resident, operationBytes, sample.WorkingSetMB)
	m.mu.Lock()
	unchanged := m.lastKey == key && m.lastKey != ""
	m.lastKey = key
	m.mu.Unlock()
	if unchanged {
		return perfSample{
			TS:               sample.TS,
			UptimeSeconds:    sample.UptimeSeconds,
			IntervalSeconds:  sample.IntervalSeconds,
			Heartbeat:        true,
			WorkingSetMB:     sample.WorkingSetMB,
			PrivateMB:        sample.PrivateMB,
			HeapInuseMB:      sample.HeapInuseMB,
			HeapSysMB:        sample.HeapSysMB,
			Goroutines:       sample.Goroutines,
			Handles:          sample.Handles,
			CPUPercent:       sample.CPUPercent,
			OSCounters:       sample.OSCounters,
			StoreMB:          storeMB,
			ProjectsMB:       projectsMB,
			EventsMB:         eventsMB,
			TabsOpen:         tabs,
			ResidentSessions: resident,
			V4SessionsOpen:   resident,
			V4Operations:     operations,
			V4OperationBytes: operationBytes,
		}
	}
	sample.FilesKB = m.collectFiles()
	return sample
}

// Thresholds come from the 2026-09-19 memory investigation: a 14.9 GB working set
// with a 256 MiB bolt cache and a 5.4x events/jsonl ratio. They are deliberately
// generous — the point is to catch a curve leaving the observed band, not to nag.
const (
	perfMonitorWarnWorkingSetMB = 4096
	perfMonitorWarnEventsMB     = 512
	perfMonitorWarnStoreMB      = 2048
	perfMonitorWarnV4OpsMB      = 256
)

// collectTotals walks the two session roots once per sample. The event logs are
// counted separately because they are the append-only files that grow with every
// turn.
func (m *perfMonitor) collectTotals() (storeMB, projectsMB, eventsMB float64) {
	storeMB = walkBytesMB(config.SessionStoreDir())
	projectsMB, eventsMB = walkProjectsBytesMB(config.MemoryUserDir())
	return storeMB, projectsMB, eventsMB
}

// walkBytesMB sums every regular file under root. Errors are ignored on purpose:
// a stat race must not silence a sample.
func walkBytesMB(root string) float64 {
	if root == "" {
		return 0
	}
	var total int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return float64(total) / (1 << 20)
}

// walkProjectsBytesMB sums the per-project session roots and, separately, the
// append-only event logs inside them.
func walkProjectsBytesMB(memoryRoot string) (totalMB float64, eventsMB float64) {
	if memoryRoot == "" {
		return 0, 0
	}
	root := filepath.Join(memoryRoot, "projects")
	var total, events int64
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry == nil || entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return nil
		}
		total += info.Size()
		if strings.HasSuffix(entry.Name(), ".events.jsonl") {
			events += info.Size()
		}
		return nil
	})
	return float64(total) / (1 << 20), float64(events) / (1 << 20)
}

// collectFiles expands the configured globs and keeps the biggest/most recently
// written files, so a long session directory cannot turn one sample into a full
// walk of the state root.
func (m *perfMonitor) collectFiles() map[string]float64 {
	patterns := m.paths
	if len(patterns) == 0 {
		patterns = defaultPerfMonitorPaths()
	}
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var entries []entry
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, path := range matches {
			info, statErr := os.Stat(path)
			if statErr != nil || info.IsDir() {
				continue
			}
			entries = append(entries, entry{path: path, size: info.Size(), mod: info.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].mod.After(entries[j].mod) })
	if len(entries) > perfMonitorMaxKeyFiles {
		entries = entries[:perfMonitorMaxKeyFiles]
	}
	out := make(map[string]float64, len(entries))
	for _, e := range entries {
		out[e.path] = float64(e.size) / 1024
	}
	return out
}

// defaultPerfMonitorPaths covers the four families this monitor exists for: the
// v4 recovery caches (one bolt per bridge session), the desktop log, and the
// transcripts a live session keeps rewriting.
func defaultPerfMonitorPaths() []string {
	memoryRoot := config.MemoryUserDir()
	return []string{
		filepath.Join(config.SessionStoreDir(), ".recovery-cache", "*", "recovery-v1.bolt"),
		filepath.Join(memoryRoot, desktopLogDirName, desktopLogSubDir, "*.log"),
		filepath.Join(memoryRoot, "projects", "*", "sessions", "*.jsonl"),
		filepath.Join(memoryRoot, "projects", "*", "sessions", "*.events.jsonl"),
		filepath.Join(memoryRoot, "projects", "*", "sessions", "*.turns.jsonl"),
	}
}

// v4Residency sums the resident operation bookkeeping of every open v4 store.
// It rides the existing controller accessors, so it adds no new lifetime rules.
func (m *perfMonitor) v4Residency() (tabs int, sessions int, operations int, bytes int64) {
	if m == nil || m.app == nil {
		return 0, 0, 0, 0
	}
	m.app.mu.Lock()
	tabList := make([]*WorkspaceTab, 0, len(m.app.tabs))
	for _, tab := range m.app.tabs {
		tabList = append(tabList, tab)
	}
	for _, tab := range m.app.detachedSessions {
		tabList = append(tabList, tab)
	}
	m.app.mu.Unlock()

	openTabs := len(tabList)
	seen := map[string]bool{}
	for _, tab := range tabList {
		if tab == nil {
			continue
		}
		ctrl, ok := m.app.controllerForTab(tab).(*control.Controller)
		if !ok || ctrl == nil {
			continue
		}
		bridge := ctrl.SessionV4()
		if bridge == nil {
			continue
		}
		service := bridge.Service()
		if service == nil {
			continue
		}
		for _, entry := range service.OperationResidency() {
			if entry.SessionID != "" {
				if seen[entry.SessionID] {
					continue
				}
				seen[entry.SessionID] = true
			}
			sessions++
			operations += entry.Operations
			bytes += entry.ApproxBytes
		}
	}
	return openTabs, sessions, operations, bytes
}

func (m *perfMonitor) dayPath(now time.Time) string {
	return filepath.Join(m.dir, perfMonitorFilePrefix+now.Format("20060102")+perfMonitorFileSuffix)
}

func (m *perfMonitor) appendSample(now time.Time, sample perfSample) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	payload, err := json.Marshal(sample)
	if err != nil {
		return err
	}
	path := m.dayPath(now)
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > perfMonitorMaxFileBytes {
		// One backup keeps the ceiling honest: samples near the limit stay
		// readable if the app then idles for hours.
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(payload, '\n'))
	return err
}

// writeHeapProfile dumps a Go heap profile next to the samples and keeps only the
// newest perfMonitorHeapKept of them: the goal is the profile taken while memory
// was inflated, not a history of profiles.
func (m *perfMonitor) writeHeapProfile(now time.Time) string {
	if m == nil {
		return ""
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		slog.Warn("desktop: perf monitor heap profile", "err", err)
		return ""
	}
	path := filepath.Join(m.dir, "heap-"+now.Format("20060102-150405")+".pprof")
	f, err := os.Create(path)
	if err != nil {
		slog.Warn("desktop: perf monitor heap profile", "err", err)
		return ""
	}
	if err := pprof.WriteHeapProfile(f); err != nil {
		_ = f.Close()
		slog.Warn("desktop: perf monitor heap profile", "err", err)
		return ""
	}
	if err := f.Close(); err != nil {
		slog.Warn("desktop: perf monitor heap profile", "err", err)
		return ""
	}
	m.pruneHeapProfiles()
	return path
}

func (m *perfMonitor) pruneHeapProfiles() {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "heap-") && strings.HasSuffix(name, ".pprof") {
			names = append(names, name)
		}
	}
	// The timestamp is in the name, so lexical order is chronological order.
	sort.Strings(names)
	for len(names) > perfMonitorHeapKept {
		_ = os.Remove(filepath.Join(m.dir, names[0]))
		names = names[1:]
	}
}

// SaveHeapProfile writes a heap profile on demand and returns its path. It works
// whether or not the sampler is running: capturing the heap of a process that has
// already inflated must never require the monitor to be switched on first.
func (a *App) SaveHeapProfile() (string, error) {
	helper := &perfMonitor{dir: perfMonitorDir()}
	path := helper.writeHeapProfile(time.Now())
	if path == "" {
		return "", fmt.Errorf("heap profile could not be written under %s", perfMonitorDir())
	}
	return path, nil
}

// prune drops sample files older than the retention window; the monitor must not
// become the growth it was built to find.
func (m *perfMonitor) prune(now time.Time) {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	cutoff := now.Add(-m.retention)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, perfMonitorFilePrefix) {
			continue
		}
		if info, statErr := entry.Info(); statErr == nil && info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(m.dir, name))
	}
}
