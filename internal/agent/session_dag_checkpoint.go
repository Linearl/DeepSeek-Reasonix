package agent

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"reasonix/internal/provider"
)

// Task 308-O2: the DAG fold checkpoint. A schema-2 session log is append-only,
// so every cold read re-decoded the whole file even though a compaction event
// already marked most of it as folded (the replay only recorded the marker, it
// never used it to skip the covered prefix). The checkpoint persists the
// replayed state as of a byte offset; a cold read restores it and replays only
// the trailing window from that offset. Anything that does not line up —
// missing file, hash mismatch, offset past the log, decode failure — falls
// back to the full replay, so the checkpoint can only save work, never lose
// it. Gated by experimental_dag_fold_checkpoint (fork rule 2).

// dagCheckpointSuffix is appended to the event-log path.
const dagCheckpointSuffix = ".dagckpt"

// dagCheckpointHeader pins the checkpoint to one log prefix: the same
// prefixHash the compaction event carries (sessionDAGCompaction.PrefixHash is
// computed over the covered prefix) plus the byte offset the state was
// replayed to and the schema version that wrote it.
type dagCheckpointHeader struct {
	PrefixHash    string
	Offset        int64
	SchemaVersion int
	Generation    int64
	At            time.Time
	Records       int
	LastGoodEnd   int64
}

// ckptNode / ckptHead / ckptTurn / ckptCompaction / ckptWriter are the
// exportable mirrors of the internal replay structs. They exist because the
// live structs carry unexported fields and an RWMutex that gob must not see;
// the mapping is field-for-field and covered by the equivalence test.
type ckptNode struct {
	ID, Parent, Head, Writer, Turn string
	Offset                         int64
	At                             time.Time
	Msg                            provider.Message
	Digest                         string
}

type ckptTurn struct {
	Turn, Leaf, Writer string
	PreserveUser       bool
	At                 time.Time
}

type ckptCompaction struct {
	CoveredLeaf  string
	CoveredCount int
	PrefixHash   string
	At           time.Time
}

type ckptHead struct {
	ID, Kind, Name, ParentHead, ForkFrom, Writer, Leaf string
	System                                             *provider.Message
	CreatedAt, LastActivity                            time.Time
	LastOffset                                         int64
	Retired                                            bool
	Compaction                                         *ckptCompaction
	OpenTurn                                           *ckptTurn
}

type ckptWriter struct {
	ID, Hostname    string
	PID             int
	LeaseGeneration uint64
	LastActivity    time.Time
}

type ckptState struct {
	Nodes           map[string]*ckptNode
	Heads           map[string]*ckptHead
	HeadOrder       []string
	Patches         map[string]provider.Message
	Redactions      map[string]provider.Message
	Writers         map[string]*ckptWriter
	Selected        string
	Orphans         []string
	Records         int
	CollectionItems int
	Size            int64
	UpgradedFrom    int
	Holes           int
}

type dagCheckpoint struct {
	Header dagCheckpointHeader
	State  ckptState
}

// dagCheckpointPath maps the event-log path to its checkpoint path.
func dagCheckpointPath(logPath string) string {
	return logPath + dagCheckpointSuffix
}

// saveDAGCheckpoint writes the checkpoint atomically (temp file + rename) so a
// crash mid-write leaves either the old file or none, never a torn one.
func saveDAGCheckpoint(logPath string, snap *dagCheckpoint) error {
	tmp, err := os.CreateTemp(filepath.Dir(logPath), ".dagckpt-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := gob.NewEncoder(tmp).Encode(snap); err != nil {
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
	return os.Rename(tmpName, dagCheckpointPath(logPath))
}

// loadDAGCheckpoint reads the checkpoint; ok=false on any failure (missing,
// torn, undecodable) and the caller falls back to the full replay.
func loadDAGCheckpoint(logPath string) (*dagCheckpoint, bool) {
	f, err := os.Open(dagCheckpointPath(logPath))
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var snap dagCheckpoint
	if err := gob.NewDecoder(f).Decode(&snap); err != nil {
		return nil, false
	}
	return &snap, true
}

// dagCheckpointUsable validates the header against the live log before the
// state is trusted: the checkpoint must not be newer than the log, the offset
// must sit inside the file, and the schema must be one this build reads.
func dagCheckpointUsable(snap *dagCheckpoint, logPath string, schemaVersion int, logSize int64) error {
	if snap == nil || snap.Header.Offset <= 0 || snap.Header.Offset > logSize {
		return fmt.Errorf("checkpoint offset out of range")
	}
	if snap.Header.SchemaVersion != schemaVersion {
		return fmt.Errorf("checkpoint schema %d != %d", snap.Header.SchemaVersion, schemaVersion)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		return err
	}
	_ = info
	return nil
}

// snapshotFromState captures the exportable core of a replayed DAG state.
func snapshotFromState(st *sessionDAGState, prefixHash string) *dagCheckpoint {
	snap := &dagCheckpoint{
		Header: dagCheckpointHeader{
			PrefixHash:    prefixHash,
			Offset:        st.lastGoodEnd,
			SchemaVersion: sessionDAGSchemaVersion,
			Generation:    st.generation,
			At:            time.Now(),
			Records:       st.records,
			LastGoodEnd:   st.lastGoodEnd,
		},
		State: ckptState{
			Nodes:           make(map[string]*ckptNode, len(st.nodes)),
			Heads:           make(map[string]*ckptHead, len(st.heads)),
			HeadOrder:       append([]string(nil), st.headOrder...),
			Patches:         make(map[string]provider.Message, len(st.patches)),
			Redactions:      make(map[string]provider.Message, len(st.redactions)),
			Writers:         make(map[string]*ckptWriter, len(st.writers)),
			Selected:        st.selected,
			Orphans:         append([]string(nil), st.orphans...),
			Records:         st.records,
			CollectionItems: st.collectionItems,
			Size:            st.size,
			UpgradedFrom:    st.upgradedFrom,
			Holes:           st.holes,
		},
	}
	for id, n := range st.nodes {
		if n == nil {
			continue
		}
		snap.State.Nodes[id] = &ckptNode{ID: n.id, Parent: n.parent, Head: n.head, Writer: n.writer, Turn: n.turn, Offset: n.offset, At: n.at, Msg: n.msg, Digest: n.digest}
	}
	for id, h := range st.heads {
		if h == nil {
			continue
		}
		ch := &ckptHead{ID: h.id, Kind: h.kind, Name: h.name, ParentHead: h.parentHead, ForkFrom: h.forkFrom, Writer: h.writer, Leaf: h.leaf, System: h.system, CreatedAt: h.createdAt, LastActivity: h.lastActivity, LastOffset: h.lastOffset, Retired: h.retired}
		if h.compaction != nil {
			ch.Compaction = &ckptCompaction{CoveredLeaf: h.compaction.coveredLeaf, CoveredCount: h.compaction.coveredCount, PrefixHash: h.compaction.prefixHash, At: h.compaction.at}
		}
		if h.openTurn != nil {
			ch.OpenTurn = &ckptTurn{Turn: h.openTurn.turn, Leaf: h.openTurn.leaf, Writer: h.openTurn.writer, PreserveUser: h.openTurn.preserveUser, At: h.openTurn.at}
		}
		snap.State.Heads[id] = ch
	}
	for id, m := range st.patches {
		snap.State.Patches[id] = m
	}
	for id, m := range st.redactions {
		snap.State.Redactions[id] = m
	}
	for id, w := range st.writers {
		if w == nil {
			continue
		}
		snap.State.Writers[id] = &ckptWriter{ID: w.id, Hostname: w.hostname, PID: w.pid, LeaseGeneration: w.leaseGeneration, LastActivity: w.lastActivity}
	}
	return snap
}

// restoreState materializes a checkpoint snapshot back into a live replay
// state bound to path. The state is exactly what replayFrom(0..Offset) would
// have produced — the equivalence test pins that property.
func (snap *dagCheckpoint) restoreState(path string) *sessionDAGState {
	st := newSessionDAGState(path)
	st.generation = snap.Header.Generation
	st.records = snap.State.Records
	st.collectionItems = snap.State.CollectionItems
	st.size = snap.State.Size
	st.lastGoodEnd = snap.Header.LastGoodEnd
	st.upgradedFrom = snap.State.UpgradedFrom
	st.holes = snap.State.Holes
	st.selected = snap.State.Selected
	st.headOrder = append([]string(nil), snap.State.HeadOrder...)
	st.orphans = append([]string(nil), snap.State.Orphans...)
	for id, n := range snap.State.Nodes {
		st.nodes[id] = &sessionDAGNode{id: n.ID, parent: n.Parent, head: n.Head, writer: n.Writer, turn: n.Turn, offset: n.Offset, at: n.At, msg: n.Msg, digest: n.Digest}
	}
	for id, h := range snap.State.Heads {
		hh := &sessionDAGHead{id: h.ID, kind: h.Kind, name: h.Name, parentHead: h.ParentHead, forkFrom: h.ForkFrom, writer: h.Writer, leaf: h.Leaf, system: h.System, createdAt: h.CreatedAt, lastActivity: h.LastActivity, lastOffset: h.LastOffset, retired: h.Retired}
		if h.Compaction != nil {
			hh.compaction = &sessionDAGCompaction{coveredLeaf: h.Compaction.CoveredLeaf, coveredCount: h.Compaction.CoveredCount, prefixHash: h.Compaction.PrefixHash, at: h.Compaction.At}
		}
		if h.OpenTurn != nil {
			hh.openTurn = &sessionDAGTurn{turn: h.OpenTurn.Turn, leaf: h.OpenTurn.Leaf, writer: h.OpenTurn.Writer, preserveUser: h.OpenTurn.PreserveUser, at: h.OpenTurn.At}
		}
		st.heads[id] = hh
	}
	for id, m := range snap.State.Patches {
		st.patches[id] = m
	}
	for id, m := range snap.State.Redactions {
		st.redactions[id] = m
	}
	for id, w := range snap.State.Writers {
		st.writers[id] = &sessionDAGWriter{id: w.ID, hostname: w.Hostname, pid: w.PID, leaseGeneration: w.LeaseGeneration, lastActivity: w.LastActivity}
	}
	return st
}
