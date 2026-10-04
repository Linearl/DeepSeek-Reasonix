package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"reasonix/internal/config"
	"reasonix/internal/provider"
)

// Task 373-R1: content-addressed image storage for session event logs.
//
// Vision-heavy sessions re-serialize every still-visible image into each new
// message entry, so an append-only log accumulates hundreds of duplicate
// copies of the same bytes (measured: 200 attachments / 35 unique / 165
// duplicate copies = 124MiB pure redundancy in one 259MB file). With the
// experimental_image_dedup gate ON, the write path stores each image blob
// once in a <log>.imgpack/ sidecar directory (hash-keyed one-file-per-blob,
// atomic temp+rename, blob written before the referencing entry) and keeps
// only the FIRST occurrence inline — later occurrences carry a
// reasonix-img://<sha256hex> reference. Upstream readers see the first copy
// inline (fully readable) and gracefully skip the non-data-URL references
// (provider layers only parse data URLs); our reader de-references references
// from the pack on load. Gate OFF: the write path is byte-identical to before
// and no pack is created; the read-side de-reference is not gated, so files
// written while the gate was on keep loading correctly after it is switched
// off.

const (
	// imageRefScheme marks a de-duplicated image reference inside a message.
	imageRefScheme = "reasonix-img://"
	// imageDedupeMinBytes skips tiny images (icons) — the dedupe overhead is
	// not worth it below this size.
	imageDedupeMinBytes = 4096
)

// imagePackDir maps an event-log path to its blob directory.
func imagePackDir(logPath string) string { return logPath + ".imgpack" }

// imageBlobPath maps a hash to its one-file-per-blob path. Naming blobs by
// their own hash keeps writes idempotent: two racers write the same bytes to
// the same temp file and rename over the same final name.
func imageBlobPath(logPath, hashHex string) string {
	return fmt.Sprintf("%s/%s.imgblob", imagePackDir(logPath), hashHex)
}

// imageHash is the content hash used as the dedupe key and reference id.
func imageHash(image string) string {
	sum := sha256.Sum256([]byte(image))
	return hex.EncodeToString(sum[:])
}

func imageRef(hashHex string) string { return imageRefScheme + hashHex }

func isImageRef(s string) bool {
	return strings.HasPrefix(s, imageRefScheme)
}

func imageRefHash(s string) string { return strings.TrimPrefix(s, imageRefScheme) }

// Image dedup modes (task 373-R1.1 three-position switch):
//   - "off": byte-identical write path, no pack (default).
//   - "first": first occurrence inline, later occurrences referenced
//     (original R1 behaviour).
//   - "all": every image above the min-size floor becomes a reference —
//     zero image bytes in the events log. Tradeoff (same signed
//     fork-only extension as R1): ALL images become invisible if the file
//     is opened by upstream readers. Sub-floor tiny images (<4KB icons)
//     stay inline even in "all" to keep the pack directory bounded.
const (
	imageDedupOff   = "off"
	imageDedupFirst = "first"
	imageDedupAll   = "all"
)

// imageDedupMode reads the task-373-R1.1 three-position switch (the
// experimental_image_dedup config key is the settings surface;
// REASONIX_IMAGE_DEDUP=first|all|off overrides for one process). Default
// off: the write path is byte-identical to before and no pack is created.
// The read-side de-reference is NOT gated, so files written in any mode
// keep loading correctly after the switch changes.
func imageDedupMode() string {
	switch os.Getenv("REASONIX_IMAGE_DEDUP") {
	case imageDedupFirst, imageDedupAll:
		return os.Getenv("REASONIX_IMAGE_DEDUP")
	}
	mode := imageDedupOff
	if cfg, err := config.Load(); err == nil && cfg != nil {
		switch cfg.Agent.ExperimentalImageDedup {
		case imageDedupFirst, imageDedupAll:
			mode = cfg.Agent.ExperimentalImageDedup
		}
	}
	return mode
}

// imageBlobExists reports whether the pack already holds this hash.
func imageBlobExists(logPath, hashHex string) bool {
	_, err := os.Stat(imageBlobPath(logPath, hashHex))
	return err == nil
}

// putImageBlob stores the blob once (idempotent via same-name rename) before
// the referencing entry is appended.
func putImageBlob(logPath, hashHex string, data []byte) error {
	dir := imagePackDir(logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	final := imageBlobPath(logPath, hashHex)
	if _, err := os.Stat(final); err == nil {
		return nil
	}
	tmp, err := os.CreateTemp(dir, ".imgblob-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, final)
}

// getImageBlob loads the blob bytes for a hash, trying every pack directory
// a reader of this log may resolve from (imagePackCandidates order). The
// first candidate's error is returned so the message keeps naming the
// primary sidecar, unchanged from the pre-P19 format.
func getImageBlob(logPath, hashHex string) ([]byte, error) {
	var firstErr error
	for _, dir := range imagePackCandidates(logPath) {
		data, err := os.ReadFile(dir + "/" + hashHex + ".imgblob")
		if err == nil {
			return data, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, fmt.Errorf("image blob %s: %w", shortImageHash(hashHex), firstErr)
}

// imagePackCandidates lists the pack directories a reader of logPath may
// resolve blobs from, in priority order (task P19). The save path stores
// blobs next to the session transcript (addAppends receives <id>.jsonl, so
// the pack lands at <id>.jsonl.imgpack), while every read side derives the
// pack from the log it replays (<id>.events.jsonl → <id>.events.jsonl.imgpack).
// That split left every reference in a schema-2 event log permanently
// unresolvable — and each replay pass re-warned per occurrence. Readers now
// try their own sidecar first (fixtures and hand-built packs keep working)
// and fall back to the session transcript's sidecar, which is where the
// write path actually puts blobs — including packs written before P19.
func imagePackCandidates(logPath string) []string {
	if base, ok := strings.CutSuffix(logPath, ".events.jsonl"); ok {
		return []string{imagePackDir(logPath), imagePackDir(base + ".jsonl")}
	}
	return []string{imagePackDir(logPath)}
}

// shortImageHash clamps the hash prefix used in log fields and error strings;
// a malformed reference shorter than 12 chars must not panic the loader.
func shortImageHash(hashHex string) string {
	if len(hashHex) < 12 {
		return hashHex
	}
	return hashHex[:12]
}

// dedupeMessageImages rewrites m.Images in place per the signed design: the
// first occurrence of each unique image stays inline (upstream-readable);
// every later occurrence becomes a reasonix-img:// reference with the blob
// stored in the pack (written before the referencing entry lands). Returns
// the number of references installed. An empty Images field is a no-op.
func dedupeMessageImages(m *provider.Message, logPath, mode string) int {
	if len(m.Images) == 0 || mode == imageDedupOff {
		return 0
	}
	refs := 0
	for i, img := range m.Images {
		if len(img) < imageDedupeMinBytes || isImageRef(img) {
			continue
		}
		hashHex := imageHash(img)
		if imageBlobExists(logPath, hashHex) {
			m.Images[i] = imageRef(hashHex)
			refs++
			continue
		}
		// First sighting. "first" keeps it inline for upstream readability
		// and stores the blob so later copies (and our own reader) can
		// resolve; "all" references it too — zero image bytes in the log
		// (signed fork-only tradeoff, task 373-R1.1).
		if err := putImageBlob(logPath, hashHex, []byte(img)); err != nil {
			slog.Warn("image blob store failed, keeping inline", "err", err)
			continue
		}
		if mode == imageDedupAll {
			m.Images[i] = imageRef(hashHex)
			refs++
		}
	}
	return refs
}

// resolveMessageImages de-references reasonix-img:// entries in m.Images from
// the pack, restoring the original inline bytes. Read-side: NOT gated by the
// write switch, so files written under the gate keep loading after it is
// switched off. Missing blobs degrade to an unresolved reference with a
// warning — never a load failure.
func resolveMessageImages(m *provider.Message, logPath string) {
	if len(m.Images) == 0 {
		return
	}
	for i, img := range m.Images {
		if !isImageRef(img) {
			continue
		}
		hash := imageRefHash(img)
		if missingImageRef(logPath, hash) {
			continue
		}
		data, err := getImageBlob(logPath, hash)
		if err != nil {
			rememberMissingImageRef(logPath, hash)
			slog.Warn("image reference unresolved", "ref", shortImageHash(hash), "err", err)
			continue
		}
		m.Images[i] = string(data)
	}
}

// imageRefMissingCap bounds the process-wide negative cache. One replay of a
// vision-heavy event log meets the same missing ref hundreds of times and a
// long-lived runtime replays on every save/head-op (measured 2026-10-04: one
// session produced 230k warnings in ~90 minutes, ≈66MB/h of desktop.log).
// Blobs are immutable and written before their referencing entry, so a hash
// that resolved as missing once stays missing for the process's lifetime —
// repeats skip both the stat I/O and the log line.
const imageRefMissingCap = 4096

var (
	imageRefMissingMu   sync.Mutex
	imageRefMissingSeen = make(map[string]struct{})
)

// missingImageRef reports whether this (log, ref) pair already resolved as
// missing in this process; callers skip the read entirely.
func missingImageRef(logPath, hash string) bool {
	imageRefMissingMu.Lock()
	defer imageRefMissingMu.Unlock()
	_, seen := imageRefMissingSeen[logPath+"\x00"+hash]
	return seen
}

// rememberMissingImageRef records a first-sighting miss. When the cache is
// full it is reset wholesale: the amortized cost is one extra warning per
// cap distinct refs, and steady-state logs (blob count ≪ cap) stay silent
// after the first pass.
func rememberMissingImageRef(logPath, hash string) {
	imageRefMissingMu.Lock()
	defer imageRefMissingMu.Unlock()
	if len(imageRefMissingSeen) >= imageRefMissingCap {
		imageRefMissingSeen = make(map[string]struct{}, imageRefMissingCap)
	}
	imageRefMissingSeen[logPath+"\x00"+hash] = struct{}{}
}
