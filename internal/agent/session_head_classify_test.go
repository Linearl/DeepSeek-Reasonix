package agent

import (
	"os"
	"testing"
)

// TestHeadDivergenceClassification pins task 203's attribution chain: the
// competing writer's identity record decides external vs local, and a missing
// record falls back to unknown (treated like external downstream so a real
// outside writer is never silenced).
func TestHeadDivergenceClassification(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	st := &sessionDAGState{writers: map[string]*sessionDAGWriter{
		"self-writer":  {id: "self-writer", pid: os.Getpid(), hostname: host},
		"other-writer": {id: "other-writer", pid: os.Getpid() + 1000, hostname: host},
		"other-host":   {id: "other-host", pid: os.Getpid(), hostname: host + "-other"},
		"no-pid":       {id: "no-pid", hostname: host},
	}}
	cases := []struct {
		name   string
		writer string
		want   string
	}{
		{"in-process writer is local", "self-writer", HeadDivergenceLocal},
		{"other pid is external", "other-writer", HeadDivergenceExternal},
		{"same pid other host is external", "other-host", HeadDivergenceExternal},
		{"missing identity record is unknown", "no-pid", HeadDivergenceUnknown},
		{"writer never registered is unknown", "ghost-writer", HeadDivergenceUnknown},
		{"empty writer is unknown", "", HeadDivergenceUnknown},
	}
	for _, tc := range cases {
		if got := classifyHeadDivergence(st, tc.writer); got != tc.want {
			t.Errorf("%s: classify(%q) = %q, want %q", tc.name, tc.writer, got, tc.want)
		}
	}
	if got := classifyHeadDivergence(nil, "self-writer"); got != HeadDivergenceUnknown {
		t.Errorf("nil state classify = %q, want %q", got, HeadDivergenceUnknown)
	}
}

// TestHeadDivergenceUnknownReasons pins task 646's missing-identity causes:
// each unknown class must name which record was missing so the
// concurrent-writer log can be attributed from the log alone.
func TestHeadDivergenceUnknownReasons(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	st := &sessionDAGState{writers: map[string]*sessionDAGWriter{
		"no-pid": {id: "no-pid", hostname: host},
		"local":  {id: "local", pid: os.Getpid(), hostname: host},
	}}
	cases := []struct {
		name       string
		state      *sessionDAGState
		writer     string
		wantClass  string
		wantReason string
	}{
		{"nil state", nil, "w", HeadDivergenceUnknown, "no_replay_state"},
		{"empty writer id", st, "", HeadDivergenceUnknown, "no_writer_id"},
		{"writer never registered", st, "ghost", HeadDivergenceUnknown, "writer_unregistered"},
		{"identity record without pid", st, "no-pid", HeadDivergenceUnknown, "pid_missing"},
		{"registered local writer", st, "local", HeadDivergenceLocal, ""},
	}
	for _, tc := range cases {
		class, reason := classifyHeadDivergenceReason(tc.state, tc.writer)
		if class != tc.wantClass || reason != tc.wantReason {
			t.Errorf("%s: classifyReason(%q) = (%q, %q), want (%q, %q)", tc.name, tc.writer, class, reason, tc.wantClass, tc.wantReason)
		}
	}
}
