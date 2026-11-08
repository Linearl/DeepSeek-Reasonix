package jobs

import (
	"io"
	"os"
)

// tl1479 — the UI's non-consuming output read.
//
// OutputForSession advances the job's read cursor; that cursor belongs to the
// agent's bash_output incremental reads. A UI poller (the capsule's output
// detail tab) must never consume from it, or the agent would silently miss
// output it has not read yet. SnapshotOutputForSession therefore reads the
// same buffers cursor-free and bounded.

// snapshotOutputTailBytes bounds one non-consuming UI read. Same magnitude as
// the streaming buffer (defaultTailBytes), so a multi-megabyte artifact is
// never re-read and re-shipped to the frontend on every poll tick.
const snapshotOutputTailBytes = 64 * 1024

// SnapshotOutputForSession returns the job's recent output plus its status
// WITHOUT consuming anything (tl1479). Repeated calls return the same view and
// leave no trace on the job's incremental reader: a later bash_output /
// OutputForSession still sees every byte it would have seen without the
// snapshot. ok is false when the id is unknown.
func (m *Manager) SnapshotOutputForSession(parentSession, id string) (text string, status Status, ok bool) {
	j := m.get(parentSession, id)
	if j == nil {
		return "", "", false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.artifactPath != "" {
		text = j.readArtifactTailLocked(snapshotOutputTailBytes)
	} else {
		text = string(j.tail)
	}
	// Surface a terminal result for task jobs the same way OutputForSession
	// does — but without flipping resultRead, so the agent's first bash_output
	// still receives the answer. Snapshot reads stay side-effect free.
	if text == "" && j.result != "" {
		text = j.result
	}
	return text, j.status, true
}

// readArtifactTailLocked reads at most limit trailing bytes of the artifact.
// Unlike readArtifactSinceOffsetLocked it never touches readOffset and never
// records an artifact error: the snapshot is a UI courtesy, and a transient
// read failure must not leak into the agent's bash_output path.
func (j *Job) readArtifactTailLocked(limit int64) string {
	f, err := os.Open(j.artifactPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	offset := size - limit
	if offset < 0 {
		offset = 0
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return ""
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return string(b)
}
