package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// tl-1220 counter ② 的查询口验收：`reasonix dag-sessions` 的文本与 --json 两种
// 输出都要把存量 DAG 会话数（probe.dag 命中）读对，且退出码走 verdict 契约。
// 夹具直接写侧车头部样本（普查只读首记录，与 agent 层真实保存链路的集成由
// save_dag_observation_test.go 覆盖）。

const (
	dagSessionsDAGHead    = "{\"schema_version\":2,\"type\":\"log\"}\n"
	dagSessionsNativeHead = "{\"schema_version\":1,\"type\":\"replace\"}\n"
)

// dagSessionsFixtureDir builds one store: a plain DAG session, an
// archived-only DAG session, a schema-1 session and a foreign-head sidecar.
func dagSessionsFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeSidecar := func(stem, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, stem+store.SessionEventLogSuffix), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSidecar("dag1", dagSessionsDAGHead+"{}\n")
	writeSidecar("flat1", dagSessionsNativeHead)
	writeSidecar("junk", "not a schema we own\n")
	archiveSidecar(t, dir, "dag2", dagSessionsDAGHead+"{}\n")
	return dir
}

// archiveSidecar writes stem's events sidecar as a canonical zst archive
// (self-describing header line + payload) with no plaintext sibling.
func archiveSidecar(t *testing.T, dir, stem, payload string) {
	t.Helper()
	zstPath := filepath.Join(dir, stem+store.SessionEventLogZstSuffix)
	f, err := os.Create(zstPath)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(f)
	if err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(struct {
		SchemaVersion int       `json:"schema_version"`
		Kind          string    `json:"kind"`
		PlainBytes    int64     `json:"plain_bytes"`
		CompressedAt  time.Time `json:"compressed_at"`
	}{
		SchemaVersion: 1,
		Kind:          "reasonix-session-events-zst",
		PlainBytes:    int64(len(payload)),
		CompressedAt:  time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(append(header, '\n')); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// dagCensusLine extracts the retirement-counter line from the text output.
func dagCensusLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "dag (probe.dag hit)") {
			return line
		}
	}
	return ""
}

func TestRenderDagSessionsText(t *testing.T) {
	dir := dagSessionsFixtureDir(t)
	var out bytes.Buffer
	if code := renderDagSessions(&out, []string{dir}, false); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	text := out.String()
	line := dagCensusLine(text)
	if line == "" {
		t.Fatalf("text output missing the retirement counter line:\n%s", text)
	}
	// dag1 (plain) + dag2 (archived-only) = 2; the count is the first numeric
	// field after the label ("dag (probe.dag hit) <count> <- ...").
	fields := strings.Fields(line)
	if len(fields) < 4 || fields[3] != "2" {
		t.Fatalf("dag count wrong in line %q:\n%s", line, text)
	}
	if !strings.Contains(text, "archived-only (zst)") || !strings.Contains(text, "dag archived-only") {
		t.Fatalf("archived breakdown missing:\n%s", text)
	}
	if !strings.Contains(text, "native (schema-1)") {
		t.Fatalf("native line missing:\n%s", text)
	}
}

func TestRenderDagSessionsJSON(t *testing.T) {
	dir := dagSessionsFixtureDir(t)
	var out bytes.Buffer
	if code := renderDagSessions(&out, []string{dir}, true); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var envelope struct {
		SchemaVersion int                    `json:"schema_version"`
		Tool          string                 `json:"tool"`
		Verdict       string                 `json:"verdict"`
		Payload       agent.SessionDAGCensus `json:"payload"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, out.String())
	}
	if envelope.Tool != "dag-sessions" || envelope.Verdict != VerdictOK {
		t.Fatalf("envelope = %+v", envelope)
	}
	if envelope.Payload.DAGSessions != 2 || envelope.Payload.EventLogSessions != 4 ||
		envelope.Payload.NativeSessions != 1 || envelope.Payload.OtherSessions != 1 ||
		envelope.Payload.ArchivedOnly != 1 || envelope.Payload.DAGArchivedOnly != 1 {
		t.Fatalf("payload census = %+v", envelope.Payload)
	}
}

func TestRenderDagSessionsEmpty(t *testing.T) {
	var out bytes.Buffer
	if code := renderDagSessions(&out, []string{t.TempDir()}, true); code != 0 {
		t.Fatalf("exit = %d, want 0 (empty is a reportable outcome)", code)
	}
	var envelope struct {
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Verdict != VerdictEmpty {
		t.Fatalf("verdict = %q, want %q", envelope.Verdict, VerdictEmpty)
	}
}
