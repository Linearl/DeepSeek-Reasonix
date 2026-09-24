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
