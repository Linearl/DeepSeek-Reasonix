package sessioninbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 任务999 (用户 2026-10-19 拍板): single-item 4→16 MiB, total 64→128 MiB.
// These tests pin the decision and prove the gates actually pass/reject at the
// new ceilings — the numbers are product decisions, not tuning knobs, so a
// silent re-tune must fail here.
//
// Sizing note: the gate checks the ENCODED blob, and completeEnqueueEnvelope
// mirrors empty Display/Raw from Submit — plain text is stored three times.
// Real multi-image messages keep the bulk once (FrozenImages data URLs) with
// short text, so the fixture builds items in that shape and measures with
// encodeEnvelope instead of guessing text lengths.

func TestTask999DefaultLimits(t *testing.T) {
	if DefaultMaxItemBytes != 16<<20 {
		t.Fatalf("DefaultMaxItemBytes = %d, want %d (任务999: 4→16 MiB)", DefaultMaxItemBytes, 16<<20)
	}
	if DefaultMaxTotalBytes != 128<<20 {
		t.Fatalf("DefaultMaxTotalBytes = %d, want %d (任务999: 64→128 MiB)", DefaultMaxTotalBytes, 128<<20)
	}
	if SoftNoticeItemBytes != 4<<20 {
		t.Fatalf("SoftNoticeItemBytes = %d, want %d (任务999: advisory threshold stays 4 MiB)", SoftNoticeItemBytes, 4<<20)
	}
}

func openTask999Store(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(session, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// imageEnvelope mimics the real multi-image enqueue shape: short text, the
// byte bulk living once as a FrozenImages data URL.
func imageEnvelope(payloadBytes int) PromptEnvelope {
	return PromptEnvelope{
		SubmitText:   "see the attached screenshots",
		DisplayText:  "see the attached screenshots",
		RawText:      "see the attached screenshots",
		FrozenImages: []string{"data:image/png;base64," + strings.Repeat("i", payloadBytes)},
	}
}

func encodedSize(t *testing.T, env PromptEnvelope) int64 {
	t.Helper()
	_, _, size, err := encodeEnvelope(env)
	if err != nil {
		t.Fatal(err)
	}
	return size
}

// 验收①: an 8 MiB message (over the old 4 MiB gate) enqueues and reads back
// whole — no silent truncation on either the write or the read path.
func TestTask999Enqueue8MiBWholeRoundTrip(t *testing.T) {
	s := openTask999Store(t)
	env := imageEnvelope(8 << 20)
	if got := encodedSize(t, env); got <= SoftNoticeItemBytes || got > DefaultMaxItemBytes {
		t.Fatalf("fixture encoded size = %d, want in (%d, %d]", got, SoftNoticeItemBytes, DefaultMaxItemBytes)
	}
	rec, err := s.Enqueue(EnqueueRequest{Envelope: env})
	if err != nil {
		t.Fatalf("8MiB message rejected: %v", err)
	}
	if rec.ByteSize <= SoftNoticeItemBytes {
		t.Fatalf("receipt ByteSize = %d, want > %d", rec.ByteSize, SoftNoticeItemBytes)
	}
	meta, got, err := s.ReadItem(rec.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.FrozenImages) != 1 || len(got.FrozenImages[0]) != len(env.FrozenImages[0]) {
		t.Fatalf("image payload not read back whole: %d images, %d bytes (want 1, %d)",
			len(got.FrozenImages), len(got.FrozenImages[0]), len(env.FrozenImages[0]))
	}
	if got.SubmitText != env.SubmitText {
		t.Fatalf("text lost in round trip: %q", got.SubmitText)
	}
	if meta.ByteSize != rec.ByteSize {
		t.Fatalf("meta.ByteSize = %d, receipt said %d", meta.ByteSize, rec.ByteSize)
	}
}

// The 16 MiB single-item gate on the encoded blob: just under passes, just
// over is rejected with ErrItemTooLarge. The two fixtures need distinct text —
// the idempotency fingerprint ignores FrozenImages, so identical text would
// dedup the over-cap send onto the under-cap row before the size check.
func TestTask999SingleItemBoundary(t *testing.T) {
	s := openTask999Store(t)
	under := imageEnvelope(int(DefaultMaxItemBytes) - 4096)
	under.SubmitText, under.DisplayText, under.RawText = "under cap", "under cap", "under cap"
	if got := encodedSize(t, under); got > DefaultMaxItemBytes {
		t.Fatalf("under-cap fixture already encodes to %d > %d", got, DefaultMaxItemBytes)
	}
	if _, err := s.Enqueue(EnqueueRequest{Envelope: under}); err != nil {
		t.Fatalf("envelope under %d bytes rejected: %v", DefaultMaxItemBytes, err)
	}
	over := imageEnvelope(int(DefaultMaxItemBytes) + 4096)
	over.SubmitText, over.DisplayText, over.RawText = "over cap", "over cap", "over cap"
	if got := encodedSize(t, over); got <= DefaultMaxItemBytes {
		t.Fatalf("over-cap fixture encodes to only %d", got)
	}
	if _, err := s.Enqueue(EnqueueRequest{Envelope: over}); !errors.Is(err, ErrItemTooLarge) {
		t.Fatalf("envelope over the 16MiB gate: err = %v, want ErrItemTooLarge", err)
	}
}

// 验收①总量闸: items summing past 128 MiB trip ErrCapacityBytes while each
// item stays under the 16 MiB single-item gate. Items must be content-distinct
// or the default auto-idempotency fingerprint dedups them onto one row.
func TestTask999TotalBytesGate(t *testing.T) {
	s := openTask999Store(t)
	for i := 0; i < 8; i++ { // 8 × ~15 MiB ≈ 120 MiB < 128 MiB
		env := imageEnvelope(15 << 20)
		env.SubmitText, env.DisplayText, env.RawText = fmt.Sprintf("item %d", i), fmt.Sprintf("item %d", i), fmt.Sprintf("item %d", i)
		if got := encodedSize(t, env); got > DefaultMaxItemBytes {
			t.Fatalf("per-item fixture encodes to %d > item cap %d", got, DefaultMaxItemBytes)
		}
		if _, err := s.Enqueue(EnqueueRequest{Envelope: env}); err != nil {
			t.Fatalf("item %d rejected below the total gate: %v", i, err)
		}
	}
	ninth := imageEnvelope(15 << 20)
	ninth.SubmitText, ninth.DisplayText, ninth.RawText = "item 8", "item 8", "item 8"
	if _, err := s.Enqueue(EnqueueRequest{Envelope: ninth}); !errors.Is(err, ErrCapacityBytes) {
		t.Fatalf("9th item past 128MiB total: err = %v, want ErrCapacityBytes", err)
	}
}

// 验收③ (refs.go 同步): FreezeRefs reads file content with a LimitReader
// keyed to DefaultMaxItemBytes. A >4 MiB file must come back whole now that
// the item ceiling is 16 MiB; only files past the ceiling truncate.
func TestTask999FreezeRefsFollowsRaisedCap(t *testing.T) {
	ws := t.TempDir()
	big := strings.Repeat("e", 8<<20)
	if err := os.WriteFile(filepath.Join(ws, "big.txt"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	refs, err := FreezeRefs(context.Background(), ws, []string{"big.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	if refs[0].Truncated || len(refs[0].Content) != len(big) {
		t.Fatalf("8MiB ref truncated=%v len=%d — refs.go cap did not follow DefaultMaxItemBytes", refs[0].Truncated, len(refs[0].Content))
	}

	huge := strings.Repeat("f", DefaultMaxItemBytes+1024)
	if err := os.WriteFile(filepath.Join(ws, "huge.txt"), []byte(huge), 0o600); err != nil {
		t.Fatal(err)
	}
	refs, err = FreezeRefs(context.Background(), ws, []string{"huge.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !refs[0].Truncated || len(refs[0].Content) != DefaultMaxItemBytes {
		t.Fatalf("oversized ref: truncated=%v len=%d, want truncated at exactly %d", refs[0].Truncated, len(refs[0].Content), DefaultMaxItemBytes)
	}
}

// 源码级守护断言 (任务书硬条目): refs.go must stay SAME-SOURCE with the
// item limit — a divergent literal (the 4 MiB trap) is exactly the failure
// this task's checklist warns about, so grep the source itself.
func TestTask999RefsFreezeCapIsSameSource(t *testing.T) {
	src, err := os.ReadFile("refs.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "io.LimitReader(file, DefaultMaxItemBytes+1)") {
		t.Fatalf("refs.go no longer freezes with DefaultMaxItemBytes — update this guard only together with a deliberate cap change")
	}
	for _, literal := range []string{"4 << 20", "4<<20", "1 << 20", "1<<20"} {
		if strings.Contains(string(src), literal) {
			t.Fatalf("refs.go hardcodes a byte literal %q — the freeze cap must track DefaultMaxItemBytes, not drift", literal)
		}
	}
}
