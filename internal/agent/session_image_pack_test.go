package agent

import (
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// imageDedupFixture builds a session log through the real entry path with the
// task-373-R1 transform (dedupeMessageImages) applied per message, exactly as
// the gated addAppends path does.
func imageDedupFixture(t *testing.T, dir string) (sessionPath, logPath, big, small string) {
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
		dedupeMessageImages(&m, logPath)
		e := dagMessageEntry(t, SessionMainHead, parent, "t1", m, base.Add(time.Duration(i)*time.Second))
		dagAppend(t, sessionPath, e)
		parent = m.ID
	}
	return sessionPath, logPath, big, small
}

// TestImageDedupFirstInlineRestReferenced is the task-373-R1 core proof: with
// the transform applied, the first occurrence of a unique image stays inline
// in the log and every later occurrence is a reasonix-img:// reference whose
// blob lives in the .imgpack sidecar (written before the referencing entry).
// Loading through the normal replay path restores the original bytes — the
// read side is not gated.
func TestImageDedupFirstInlineRestReferenced(t *testing.T) {
	dir := t.TempDir()
	sessionPath, logPath, big, small := imageDedupFixture(t, dir)

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
