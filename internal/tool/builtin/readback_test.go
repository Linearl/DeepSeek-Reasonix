package builtin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

func readBackEnabledEdit() editFile {
	return editFile{readBack: true}
}

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readBackSchemaField(t *testing.T, schema json.RawMessage) bool {
	t.Helper()
	var parsed struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(schema, &parsed); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	_, ok := parsed.Properties["readBack"]
	return ok
}

func TestReadBackSchemaOnlyWithFamilyOn(t *testing.T) {
	if readBackSchemaField(t, editFile{}.Schema()) {
		t.Fatal("readBack must stay absent from the edit_file schema while the 工具优化 family is off (铁律 2)")
	}
	if !readBackSchemaField(t, readBackEnabledEdit().Schema()) {
		t.Fatal("readBack must appear in the edit_file schema with the family on")
	}
	if readBackSchemaField(t, multiEdit{}.Schema()) {
		t.Fatal("readBack must stay absent from the multi_edit schema while the family is off")
	}
	if !readBackSchemaField(t, multiEdit{readBack: true}.Schema()) {
		t.Fatal("readBack must appear in the multi_edit schema with the family on")
	}
}

func TestReadBackOffIgnoresTheParameter(t *testing.T) {
	// Both runs share one fixture path: the outputs must be byte-identical,
	// path included.
	path := filepath.Join(t.TempDir(), "fixture.txt")
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := editFile{}.Execute(context.Background(), json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"beta","new_string":"BETA"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// With the family off, a passed readBack is ignored: byte-identical output
	// and no observation filed.
	ctx, sink := tool.WithReadBackCollector(context.Background())
	got, err := editFile{}.Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"beta","new_string":"BETA","readBack":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != base {
		t.Fatalf("family off changed the result:\nbase=%q\ngot =%q", base, got)
	}
	if sink.Path != "" || len(sink.LineHashes) > 0 {
		t.Fatal("family off must not file a read-back observation")
	}
}

func TestReadBackEditReturnsWindowAndObservation(t *testing.T) {
	lines := make([]string, 0, 60)
	for i := 1; i <= 60; i++ {
		lines = append(lines, "line-"+strings.Repeat("x", 2)+strconv.Itoa(i))
	}
	body := strings.Join(lines, "\n") + "\n"
	path := writeFixture(t, body)

	ctx, sink := tool.WithReadBackCollector(context.Background())
	out, err := readBackEnabledEdit().Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"`+lines[29]+`","new_string":"edited-30","readBack":true}`))
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(body, lines[29], "edited-30", 1)

	if !strings.HasPrefix(out, "edited "+path) {
		t.Fatalf("summary header missing: %q", readBackTestFirstLine(out))
	}
	header := "read_back " + path + " lines 10-50 (edited lines 30-30, context ±20 lines)"
	if !strings.Contains(out, header) {
		t.Fatalf("read_back header missing or wrong:\nwant %q\ngot:\n%s", header, out)
	}
	if !strings.Contains(out, "  30→edited-30\n") {
		t.Fatalf("edited line missing from the numbered window:\n%s", out)
	}
	if !strings.Contains(out, "  10→"+lines[9]+"\n") {
		t.Fatalf("context-before line missing:\n%s", out)
	}
	// The window's last line carries no trailing newline (the block is trimmed).
	if !strings.Contains(out, "  50→"+lines[49]) {
		t.Fatalf("context-after line missing:\n%s", out)
	}

	if sink.Path != path || sink.StartLine != 10 || len(sink.LineHashes) != 41 {
		t.Fatalf("observation = path %q start %d hashes %d", sink.Path, sink.StartLine, len(sink.LineHashes))
	}
	updatedLines := splitReadBackLines(updated)
	for i, hash := range sink.LineHashes {
		sum := sha256.Sum256([]byte(updatedLines[9+i]))
		if hash != hex.EncodeToString(sum[:]) {
			t.Fatalf("hash %d does not match the post-write line content", i)
		}
	}
	if sink.Snapshot == "" {
		t.Fatal("observation must carry the post-write snapshot")
	}
}

func TestReadBackObservationSnapshotMatchesFollowUpDeclaration(t *testing.T) {
	body := "alpha\nbeta\ngamma\n"
	path := writeFixture(t, body)
	e := editFile{readBack: true}

	ctx, sink := tool.WithReadBackCollector(context.Background())
	if _, err := e.Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"beta","new_string":"BETA","readBack":true}`)); err != nil {
		t.Fatal(err)
	}
	// A follow-up edit re-reads the file and derives its evidence snapshot the
	// same way; it must land on exactly the version the window was filed with.
	src, err := readEditSource(context.Background(), nil, path)
	if err != nil {
		t.Fatal(err)
	}
	if src.readSnapshot(path) != sink.Snapshot {
		t.Fatalf("follow-up snapshot %q does not match the filed window snapshot %q", src.readSnapshot(path), sink.Snapshot)
	}
}

func TestReadBackFailedEditFilesNothing(t *testing.T) {
	path := writeFixture(t, "alpha\nbeta\ngamma\n")
	ctx, sink := tool.WithReadBackCollector(context.Background())
	if _, err := readBackEnabledEdit().Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"missing","new_string":"x"}`)); err == nil {
		t.Fatal("expected the no-match edit to fail")
	}
	if sink.Path != "" {
		t.Fatal("a failed edit must never file read-back evidence")
	}
}

func TestReadBackCRLFHashesMatchReaderView(t *testing.T) {
	path := writeFixture(t, "alpha\r\nbeta\r\ngamma\r\n")
	ctx, sink := tool.WithReadBackCollector(context.Background())
	out, err := readBackEnabledEdit().Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"beta","new_string":"BETA","readBack":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "   2→BETA\n") {
		t.Fatalf("CRLF file must render without the carriage return: %s", out)
	}
	want := hex.EncodeToString(func() []byte { sum := sha256.Sum256([]byte("BETA")); return sum[:] }())
	if len(sink.LineHashes) < 2 || sink.LineHashes[1] != want {
		t.Fatalf("CRLF line hash must match the read_file view (carriage return stripped): %v", sink.LineHashes)
	}
}

func TestReadBackMultiEditWindowSpansFirstToLast(t *testing.T) {
	lines := make([]string, 0, 50)
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("%03d unique", i))
	}
	body := strings.Join(lines, "\n") + "\n"
	path := writeFixture(t, body)

	ctx, sink := tool.WithReadBackCollector(context.Background())
	out, err := multiEdit{readBack: true}.Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"edits":[{"old_string":"001 unique","new_string":"ONE"},{"old_string":"030 unique","new_string":"THIRTY"}],"readBack":true}`))
	if err != nil {
		t.Fatal(err)
	}
	// First edit at line 1, last at line 30: the window spans 1..50 (capped by
	// the file edges) and the observation vouches for the whole window.
	if !strings.Contains(out, "read_back "+path+" lines 1-50 (edited lines 1-30") {
		t.Fatalf("union window header wrong:\n%s", out)
	}
	if !strings.Contains(out, "   1→ONE\n") || !strings.Contains(out, "  30→THIRTY\n") {
		t.Fatalf("both edited lines must appear in the window:\n%s", out)
	}
	if sink.Path != path || sink.StartLine != 1 || len(sink.LineHashes) != 50 {
		t.Fatalf("observation = path %q start %d hashes %d", sink.Path, sink.StartLine, len(sink.LineHashes))
	}
}

func TestReadBackMultiEditSecondEditShiftsEarlierSpan(t *testing.T) {
	// The second edit sits BEFORE the first one in the file and is longer, so
	// the first edit's span shifts down; the union must cover both regions.
	body := "one\ntwo\nthree\nfour\nfive\nsix\nseven\n"
	path := writeFixture(t, body)
	ctx, sink := tool.WithReadBackCollector(context.Background())
	out, err := multiEdit{readBack: true}.Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"edits":[{"old_string":"five","new_string":"FIVE-EXTENDED"},{"old_string":"two","new_string":"TWO"}],"readBack":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "   2→TWO\n") || !strings.Contains(out, "   5→FIVE-EXTENDED\n") {
		t.Fatalf("shifted union window wrong:\n%s", out)
	}
	lines := splitReadBackLines("one\nTWO\nthree\nfour\nFIVE-EXTENDED\nsix\nseven")
	if len(sink.LineHashes) != len(lines) {
		t.Fatalf("window hashes = %d, want %d", len(sink.LineHashes), len(lines))
	}
	for i, hash := range sink.LineHashes {
		sum := sha256.Sum256([]byte(lines[i]))
		if hash != hex.EncodeToString(sum[:]) {
			t.Fatalf("hash %d does not match the post-write content", i)
		}
	}
}

func TestReadBackWindowCapTruncatesWithNotice(t *testing.T) {
	lines := make([]string, maxReadBackWindowLines+80)
	for i := range lines {
		lines[i] = fmt.Sprintf("%03d", i+1)
	}
	path := writeFixture(t, strings.Join(lines, "\n")+"\n")
	// The written replacement itself spans past the cap: the window keeps the
	// edited head and says so.
	wide := make([]string, maxReadBackWindowLines+50)
	for i := range wide {
		wide[i] = "fill-" + strconv.Itoa(i+1)
	}
	ctx, sink := tool.WithReadBackCollector(context.Background())
	out, err := readBackEnabledEdit().Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"`+lines[0]+`","new_string":`+quote(t, strings.Join(wide, "\n"))+`,"readBack":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, readBackSpanTruncated) {
		t.Fatalf("capped window must carry the truncation notice:\n%s", tail(out, 400))
	}
	if len(sink.LineHashes) != maxReadBackWindowLines {
		t.Fatalf("window hashes = %d, want the %d cap", len(sink.LineHashes), maxReadBackWindowLines)
	}
}

func TestReadBackDeleteCollapsesToBoundaryLine(t *testing.T) {
	path := writeFixture(t, "alpha\nbeta\ngamma\n")
	ctx, sink := tool.WithReadBackCollector(context.Background())
	out, err := readBackEnabledEdit().Execute(ctx, json.RawMessage(`{"path":`+quote(t, path)+`,"old_string":"beta\n","new_string":"","readBack":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "edited lines 2-2") {
		t.Fatalf("deletion must collapse onto its boundary line: %s", out)
	}
	for _, hash := range sink.LineHashes {
		if hash == hex.EncodeToString(func() []byte { sum := sha256.Sum256([]byte("beta")); return sum[:] }()) {
			t.Fatal("deleted line must not appear in the post-write window")
		}
	}
}

func TestBindReadBackOnlyLightsEditWriters(t *testing.T) {
	if !readBackSchemaField(t, BindReadBack(editFile{}, true).Schema()) {
		t.Fatal("BindReadBack must light edit_file")
	}
	if !readBackSchemaField(t, BindReadBack(multiEdit{}, true).Schema()) {
		t.Fatal("BindReadBack must light multi_edit")
	}
	if readBackSchemaField(t, BindReadBack(editFile{}, false).Schema()) {
		t.Fatal("BindReadBack with false must keep the schema unchanged")
	}
	if readBackSchemaField(t, BindReadBack(readFile{}, true).Schema()) {
		t.Fatal("BindReadBack must leave non-edit tools untouched")
	}
}

func quote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readBackTestFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
