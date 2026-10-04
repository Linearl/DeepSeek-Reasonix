package agent

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// imageDedupFixture builds a session log through the real entry path with the
// task-373-R1 transform (dedupeMessageImages) applied per message, exactly as
// the gated addAppends path does. mode: imageDedupFirst | imageDedupAll.
func imageDedupFixture(t *testing.T, dir, mode string) (sessionPath, logPath, big, small string) {
	t.Helper()
	sessionPath = dir + "/s1"
	logPath = store.SessionEventLog(sessionPath)
	base := time.UnixMilli(1790800000000)

	big = "data:image/png;base64," + strings.Repeat("B", 9000) + "BIGPAYLOAD"
	small = "data:image/png;base64,tiny"

	sys := dagMsg(provider.RoleSystem, "sys", "S0")
	u1 := dagMsg(provider.RoleUser, "with big image", "U1")
	a1 := dagMsg(provider.RoleAssistant, "ack", "A1")
	u2 := dagMsg(provider.RoleUser, "same big image again", "U2")
	u1.Images = []string{big, small}
	u2.Images = []string{big}

	parent := ""
	for i, m := range []provider.Message{sys, u1, a1, u2} {
		// Same transform the gated addAppends path applies before encoding.
		dedupeMessageImages(&m, logPath, mode)
		e := dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second))
		dagAppend(t, sessionPath, e)
		parent = m.ID
	}
	return sessionPath, logPath, big, small
}

// TestImageDedupFirstInlineRestReferenced is the task-373-R1 core proof for
// the "first" position: the first occurrence of a unique image stays inline
// in the log and every later occurrence is a reasonix-img:// reference whose
// blob lives in the .imgpack sidecar (written before the referencing entry).
// Loading through the normal replay path restores the original bytes — the
// read side is not gated.
func TestImageDedupFirstInlineRestReferenced(t *testing.T) {
	dir := t.TempDir()
	sessionPath, logPath, big, small := imageDedupFixture(t, dir, imageDedupFirst)

	// The pack holds exactly one blob (the big image; the small one is below
	// the min-size floor and stays inline everywhere).
	if !imageBlobExists(logPath, imageHash(big)) {
		t.Fatal("big image blob missing from pack")
	}
	if imageBlobExists(logPath, imageHash(small)) {
		t.Fatal("small image below the floor should not have been stored")
	}

	// Raw log check: exactly one inline occurrence of the big image, and at
	// least one reference form present.
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if inline := strings.Count(string(raw), big); inline != 1 {
		t.Fatalf("inline occurrences of the big image = %d, want 1 (first-copy-inline)", inline)
	}
	if refs := strings.Count(string(raw), imageRef(imageHash(big))); refs == 0 {
		t.Fatal("no reasonix-img references written for the duplicate copy")
	}

	// Load through the normal replay path: references resolve back to the
	// original bytes (read side is not gated).
	st := dagReplay(t, sessionPath)
	msgs, _ := st.materialize(st.selectedHead())
	seen := 0
	for _, m := range msgs {
		for _, img := range m.Images {
			if img == big {
				seen++
			}
		}
	}
	// The transcript is semantically unchanged by dedup: U1 carried the big
	// image and U2 re-carried it, so the materialized view still shows both
	// occurrences — dedup compressed the STORAGE, not the content.
	if seen != 2 {
		t.Fatalf("materialized transcript carries the big image %d times, want 2", seen)
	}
}

// TestImageDedupGateOffByteIdentical: with the gate off (the transform simply
// not applied), no pack and no references are produced — the write path is
// byte-identical to pre-373-R1.
func TestImageDedupGateOffByteIdentical(t *testing.T) {
	dir := t.TempDir()
	sessionPath := dir + "/s1"
	logPath := store.SessionEventLog(sessionPath)
	base := time.UnixMilli(1790800000000)

	big := "data:image/png;base64,MEGABLOB"
	m := dagMsg(provider.RoleUser, "x", "U1")
	m.Images = []string{big}

	// Gate off: do NOT call dedupeMessageImages — exactly the gated-out
	// behaviour.
	e := dagMessageEntry(t, SessionMainHead, "", "t1", m, base)
	dagAppend(t, sessionPath, e)

	if imageBlobExists(logPath, imageHash(big)) {
		t.Fatal("gate-off write created a pack blob")
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), imageRefScheme) {
		t.Fatal("gate-off write produced an image reference")
	}
	if !strings.Contains(string(raw), big) {
		t.Fatal("gate-off write lost the inline image")
	}
}

// TestImageDedupPackSurvivesRestart: blobs are stateless on disk (no
// in-memory registry), so a fresh process still recognizes stored blobs and
// the read side resolves references without the write gate.
func TestImageDedupPackSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	logPath := store.SessionEventLog(dir + "/s2")

	big := "data:image/png;base64,RESTARTBLOB"
	if err := putImageBlob(logPath, imageHash(big), []byte(big)); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	if !imageBlobExists(logPath, imageHash(big)) {
		t.Fatal("blob lost before restart simulation")
	}
	m := provider.Message{Images: []string{big}}
	resolveMessageImages(&m, logPath)
	if m.Images[0] != big {
		t.Fatal("resolveMessageImages could not restore the blob after restart")
	}
}

// TestImageDedupAllModeZeroInlineBytes is the task-373-R1.1 "all" position
// proof: even the FIRST occurrence becomes a reasonix-img:// reference, so
// the events log carries zero image bytes (sub-floor tiny images excepted —
// a 4KB icon per blob file would not pay for itself). The pack holds the
// blob, and the replay resolves references so the materialized transcript is
// still semantically complete (2 occurrences of the big image). Upstream
// readability tradeoff (signed fork-only): an upstream reader of this log
// sees no images at all — asserted here via the raw-bytes check.
func TestImageDedupAllModeZeroInlineBytes(t *testing.T) {
	dir := t.TempDir()
	sessionPath, logPath, big, small := imageDedupFixture(t, dir, imageDedupAll)

	if !imageBlobExists(logPath, imageHash(big)) {
		t.Fatal("big image blob missing from pack in all mode")
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	// Zero inline bytes of the big image in the log; both occurrences are
	// references. Upstream would render no image — the documented tradeoff.
	if inline := strings.Count(string(raw), big); inline != 0 {
		t.Fatalf("inline occurrences of the big image in all mode = %d, want 0", inline)
	}
	if refs := strings.Count(string(raw), imageRef(imageHash(big))); refs != 2 {
		t.Fatalf("reference occurrences in all mode = %d, want 2 (U1 first + U2 dup)", refs)
	}
	// The sub-floor small image stays inline even in all mode.
	if !strings.Contains(string(raw), small) {
		t.Fatal("sub-floor small image should stay inline in all mode")
	}

	st := dagReplay(t, sessionPath)
	msgs, _ := st.materialize(st.selectedHead())
	seen := 0
	for _, m := range msgs {
		for _, img := range m.Images {
			if img == big {
				seen++
			}
		}
	}
	if seen != 2 {
		t.Fatalf("materialized transcript in all mode carries the big image %d times, want 2", seen)
	}
}

// --- Task P19: desktop-log noise governance -------------------------------

// resetImageRefMissingCache clears the process-wide negative cache between
// tests so a previous test's misses cannot silence this test's warnings.
func resetImageRefMissingCache(t *testing.T) {
	t.Helper()
	imageRefMissingMu.Lock()
	defer imageRefMissingMu.Unlock()
	imageRefMissingSeen = make(map[string]struct{})
}

// TestImageRefResolvesFromSessionPackFallback is the task-P19 root-cause
// proof. The save path (addAppends) receives the session transcript path, so
// dedupe stores blobs in <id>.jsonl.imgpack; every reader replays the event
// log and looked only in <id>.events.jsonl.imgpack. With ONLY the session
// pack on disk — the state every pre-P19 schema-2 session is in — the
// event-log reader must still restore the original bytes via the fallback.
func TestImageRefResolvesFromSessionPackFallback(t *testing.T) {
	dir := t.TempDir()
	sessionPath := dir + "/s1.jsonl"
	logPath := store.SessionEventLog(sessionPath) // dir/s1.events.jsonl

	big := "data:image/png;base64," + strings.Repeat("P", 9000)
	m := dagMsg(provider.RoleUser, "x", "U1")
	m.Images = []string{big}
	// Write-side transform exactly as the gated addAppends applies it: the
	// pack is derived from the SESSION path, not the event-log path.
	dedupeMessageImages(&m, sessionPath, imageDedupFirst)
	if !imageBlobExists(sessionPath, imageHash(big)) {
		t.Fatal("blob missing from the session-transcript pack after write-side dedupe")
	}
	if imageBlobExists(logPath, imageHash(big)) {
		t.Fatal("test setup expected no events-log pack on disk")
	}

	// Reader side: resolve against the event-log path — what decodeOne and
	// replaySessionEventLog both pass — with the events pack absent.
	ref := provider.Message{Images: []string{imageRef(imageHash(big))}}
	resetImageRefMissingCache(t)
	resolveMessageImages(&ref, logPath)
	if ref.Images[0] != big {
		t.Fatalf("event-log reader failed to resolve via the session pack fallback: got %.40q", ref.Images[0])
	}
}

// TestImageRefMissingWarnsOncePerRef is the task-P19 governance proof: one
// replay of a vision-heavy log meets the same missing ref hundreds of times
// and a long-lived runtime replays on every save. The first sighting warns;
// every repeat of the same (log, ref) pair is silent (and skips the read).
// Distinct refs each still get their own first-sighting warning.
func TestImageRefMissingWarnsOncePerRef(t *testing.T) {
	dir := t.TempDir()
	logPath := store.SessionEventLog(dir + "/s1.jsonl")
	refA := imageRef(strings.Repeat("a", 64)) // no blob anywhere
	refB := imageRef(strings.Repeat("b", 64)) // no blob anywhere

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	resetImageRefMissingCache(t)

	for i := 0; i < 300; i++ {
		m := provider.Message{Images: []string{refA, refB}}
		resolveMessageImages(&m, logPath)
	}

	warns := strings.Count(buf.String(), "level=WARN")
	if warns != 2 {
		t.Fatalf("unresolved-ref warnings after 300 replays = %d, want 2 (one per distinct ref)", warns)
	}
	if n := strings.Count(buf.String(), "image reference unresolved"); n != 2 {
		t.Fatalf("warning message occurrences = %d, want 2", n)
	}
}

// TestImageRefMissingNegativeCacheSkipsReread pins the skip semantics the
// governance relies on: blobs are immutable and written before their
// referencing entry, so a ref that resolved as missing once stays a miss for
// the process lifetime — even if a blob file appears afterwards (that is a
// manual repair; a restart re-reads). This assertion is what makes the
// missing-read path a real negative cache, not just a log gate.
func TestImageRefMissingNegativeCacheSkipsReread(t *testing.T) {
	dir := t.TempDir()
	logPath := store.SessionEventLog(dir + "/s1.jsonl")
	hash := strings.Repeat("c", 64)
	resetImageRefMissingCache(t)

	first := provider.Message{Images: []string{imageRef(hash)}}
	resolveMessageImages(&first, logPath)
	if first.Images[0] == "" || !isImageRef(first.Images[0]) {
		t.Fatal("expected the first resolve to leave an unresolved reference")
	}

	// Manual repair: the blob appears on disk after the first miss.
	if err := putImageBlob(logPath, hash, []byte("repaired")); err != nil {
		t.Fatalf("seed repaired blob: %v", err)
	}
	second := provider.Message{Images: []string{imageRef(hash)}}
	resolveMessageImages(&second, logPath)
	if !isImageRef(second.Images[0]) {
		t.Fatal("negative cache did not skip the re-read (expected the ref to stay unresolved within the process)")
	}

	// A fresh "process" (cache cleared) sees the repair.
	resetImageRefMissingCache(t)
	third := provider.Message{Images: []string{imageRef(hash)}}
	resolveMessageImages(&third, logPath)
	if third.Images[0] != "repaired" {
		t.Fatalf("fresh cache should resolve the repaired blob, got %.40q", third.Images[0])
	}
}

// TestImageRefMalformedShortHashWarnsNotPanics pins the malformed-reference
// path: pre-P19 the warn call sliced hashHex[:12] directly, so a ref shorter
// than 12 chars panicked the loader mid-replay. It must degrade exactly like
// any other missing blob — one warning, then the negative cache silences the
// repeat — and leave the reference unresolved.
func TestImageRefMalformedShortHashWarnsNotPanics(t *testing.T) {
	dir := t.TempDir()
	logPath := store.SessionEventLog(dir + "/s1.jsonl")

	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	resetImageRefMissingCache(t)

	for i := 0; i < 2; i++ {
		m := provider.Message{Images: []string{imageRef("abc")}} // 3-char hash
		resolveMessageImages(&m, logPath)
		if !isImageRef(m.Images[0]) {
			t.Fatal("malformed ref must degrade to an unresolved reference, never panic")
		}
	}
	if n := strings.Count(buf.String(), "image reference unresolved"); n != 1 {
		t.Fatalf("malformed-ref warnings after 2 passes = %d, want 1 (negative cache applies)", n)
	}
	if !strings.Contains(buf.String(), "ref=abc") {
		t.Fatalf("warning should carry the full short hash, got: %s", buf.String())
	}
}
