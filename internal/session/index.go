package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"reasonix/internal/fileutil"
)

// Read-amplification counters (task 187). They only observe: the paths below
// already existed, and these make the difference between "rebuilt the index
// again" and "extended it by the appended tail" visible to tests and callers.
var (
	sparseIndexRebuilds   atomic.Uint64
	sparseIndexExtensions atomic.Uint64
	sparseIndexMemoryHits atomic.Uint64
)

const (
	sparseIndexCodec    = "reasonix.session.offset-index/v1"
	sparseIndexInterval = 256
	sparseIdentityBytes = 4096
)

type sparseIndex struct {
	Codec        string             `json:"codec"`
	LogSize      int64              `json:"logSize"`
	LogModTimeNS int64              `json:"logModTimeNs"`
	LogIdentity  string             `json:"logIdentity"`
	LastSequence uint64             `json:"lastSequence"`
	CommitCount  uint64             `json:"commitCount"`
	Entries      []sparseIndexEntry `json:"entries"`
	// HeadSignature covers only the leading bytes of the log. The full identity
	// also folds in size, mtime and the tail, so on an append-only log it changes
	// with every write and can never serve as a cache key; the head does not
	// change, which is what makes an incremental extension possible.
	HeadSignature string `json:"headSignature,omitempty"`
	partial       bool
}

type sparseIndexEntry struct {
	FirstSequence uint64 `json:"firstSequence"`
	Offset        int64  `json:"offset"`
}

func sparseIndexPath(cacheDir string) string {
	return filepath.Join(cacheDir, "events.offset-index.json")
}

// sparseIndexCacheLimit bounds the in-process index cache. A snapshot reads the
// log page by page, so the same index is asked for repeatedly within one
// operation; the cache turns those repeats into a map lookup instead of a file
// open, a stat, two 4 KiB reads and a JSON decode.
const sparseIndexCacheLimit = 64

type sparseIndexCacheEntry struct {
	index     sparseIndex
	logSize   int64
	modTimeNS int64
	identity  string
}

var (
	sparseIndexCacheMu    sync.Mutex
	sparseIndexCacheOrder []string
	sparseIndexCache      = map[string]sparseIndexCacheEntry{}
)

func sparseIndexCacheKey(dir, cacheDir string) string { return dir + "|" + cacheDir }

func loadCachedSparseIndex(dir, cacheDir string, info os.FileInfo, identity string) (sparseIndex, bool) {
	sparseIndexCacheMu.Lock()
	defer sparseIndexCacheMu.Unlock()
	entry, ok := sparseIndexCache[sparseIndexCacheKey(dir, cacheDir)]
	if !ok || entry.logSize != info.Size() || entry.modTimeNS != info.ModTime().UnixNano() || entry.identity != identity {
		return sparseIndex{}, false
	}
	return entry.index, true
}

func storeCachedSparseIndex(dir, cacheDir string, index sparseIndex, info os.FileInfo, identity string) {
	sparseIndexCacheMu.Lock()
	defer sparseIndexCacheMu.Unlock()
	key := sparseIndexCacheKey(dir, cacheDir)
	if _, seen := sparseIndexCache[key]; !seen {
		sparseIndexCacheOrder = append(sparseIndexCacheOrder, key)
		for len(sparseIndexCacheOrder) > sparseIndexCacheLimit {
			evicted := sparseIndexCacheOrder[0]
			sparseIndexCacheOrder = sparseIndexCacheOrder[1:]
			delete(sparseIndexCache, evicted)
		}
	}
	sparseIndexCache[key] = sparseIndexCacheEntry{
		index: index, logSize: info.Size(), modTimeNS: info.ModTime().UnixNano(), identity: identity,
	}
}

// extendSparseIndex continues the scan from the end of the cached index instead
// of restarting at offset zero. The previous scan stopped at a commit boundary at
// the then end of the log, so the cached size is a legal restart offset; the
// rebuilt index validates itself before it is trusted, and anything unexpected
// falls back to a full rebuild.
func extendSparseIndex(ctx context.Context, file *os.File, info os.FileInfo, identity, head, codec, dir string, cached sparseIndex) (sparseIndex, bool) {
	extended := cached
	commitIndex := int(cached.CommitCount)
	visit := func(offset int64, commit Commit) bool {
		if ctx.Err() != nil {
			return false
		}
		if commitIndex%sparseIndexInterval == 0 {
			extended.Entries = append(extended.Entries, sparseIndexEntry{FirstSequence: commit.FirstSequence, Offset: offset})
		}
		commitIndex++
		extended.CommitCount++
		extended.LastSequence = commit.LastSequence()
		return true
	}
	var err error
	if codec == Codec {
		err = scanV4CommitFileRefs(ctx, file, cached.LogSize, cached.LastSequence+1, contentStoreForSessionDir(dir), nil, visit)
	} else {
		err = scanCommitFileCodec(file, cached.LogSize, cached.LastSequence+1, codec, nil, visit)
	}
	if err != nil || ctx.Err() != nil {
		return sparseIndex{}, false
	}
	extended.LogSize = info.Size()
	extended.LogModTimeNS = info.ModTime().UnixNano()
	extended.LogIdentity = identity
	extended.HeadSignature = head
	if !extended.validFor(info, identity) {
		return sparseIndex{}, false
	}
	return extended, true
}

// loadOrBuildSparseIndex treats the index as an expendable cache. A missing or
// corrupt cache is rebuilt by validating the complete durable log. Failure to
// write the rebuilt cache does not make an otherwise readable session fail.
func loadOrBuildSparseIndex(ctx context.Context, dir, cacheDir string) (sparseIndex, error) {
	if err := ctx.Err(); err != nil {
		return sparseIndex{}, err
	}
	manifest, err := readStoredManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return sparseIndex{}, err
	}
	logPath := logPathForManifest(dir, manifest)
	file, err := os.Open(logPath)
	if os.IsNotExist(err) {
		return sparseIndex{Codec: sparseIndexCodec, Entries: []sparseIndexEntry{}}, nil
	}
	if err != nil {
		return sparseIndex{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return sparseIndex{}, err
	}
	identity, err := sparseLogIdentity(file, info)
	if err != nil {
		return sparseIndex{}, err
	}
	head, err := headLogSignature(file, info)
	if err != nil {
		return sparseIndex{}, err
	}
	if cached, ok := loadCachedSparseIndex(dir, cacheDir, info, identity); ok {
		sparseIndexMemoryHits.Add(1)
		return cached, nil
	}
	if data, readErr := os.ReadFile(sparseIndexPath(cacheDir)); readErr == nil {
		var cached sparseIndex
		if json.Unmarshal(data, &cached) == nil {
			if cached.validFor(info, identity) {
				if cached.HeadSignature == "" {
					cached.HeadSignature = head
					writeSparseIndex(cacheDir, cached)
				}
				storeCachedSparseIndex(dir, cacheDir, cached, info, identity)
				return cached, nil
			}
			// Append-only log: the cached index still describes a prefix, so only
			// the appended tail needs scanning.
			if cached.extends(info, identity, head) {
				if extended, ok := extendSparseIndex(ctx, file, info, identity, head, manifest.Codec, dir, cached); ok {
					sparseIndexExtensions.Add(1)
					storeCachedSparseIndex(dir, cacheDir, extended, info, identity)
					writeSparseIndex(cacheDir, extended)
					return extended, nil
				}
			}
		}
	}

	rebuilt := sparseIndex{
		Codec:         sparseIndexCodec,
		LogSize:       info.Size(),
		LogModTimeNS:  info.ModTime().UnixNano(),
		LogIdentity:   identity,
		HeadSignature: head,
		Entries:       []sparseIndexEntry{},
	}
	commitIndex := 0
	visit := func(offset int64, commit Commit) bool {
		if ctx.Err() != nil {
			return false
		}
		if commitIndex%sparseIndexInterval == 0 {
			rebuilt.Entries = append(rebuilt.Entries, sparseIndexEntry{FirstSequence: commit.FirstSequence, Offset: offset})
		}
		commitIndex++
		rebuilt.CommitCount++
		rebuilt.LastSequence = commit.LastSequence()
		return true
	}
	if manifest.Codec == Codec {
		err = scanV4CommitFileRefs(ctx, file, 0, 1, contentStoreForSessionDir(dir), nil, visit)
	} else {
		err = scanCommitFileCodec(file, 0, 1, manifest.Codec, nil, visit)
	}
	if err != nil {
		return sparseIndex{}, err
	}
	if err := ctx.Err(); err != nil {
		return sparseIndex{}, err
	}
	sparseIndexRebuilds.Add(1)
	storeCachedSparseIndex(dir, cacheDir, rebuilt, info, identity)
	writeSparseIndex(cacheDir, rebuilt)
	return rebuilt, nil
}

// extends reports whether the index describes a prefix of the current log, which
// is the normal case for an append-only file: same head, and the log has only
// grown since the scan. The index can then be extended from its own end instead
// of being rebuilt from the start.
func (idx sparseIndex) extends(info os.FileInfo, identity, head string) bool {
	if idx.Codec != sparseIndexCodec || idx.HeadSignature == "" || idx.HeadSignature != head {
		return false
	}
	if idx.LogSize <= 0 || idx.LogSize > info.Size() {
		return false
	}
	// A truncated-then-regrown file shares a head but is not an extension.
	if idx.LogSize < info.Size() && idx.LogIdentity == identity {
		return false
	}
	return true
}

func (idx sparseIndex) validFor(info os.FileInfo, identity string) bool {
	if idx.Codec != sparseIndexCodec || idx.LogSize != info.Size() || idx.LogModTimeNS != info.ModTime().UnixNano() || idx.LogIdentity != identity {
		return false
	}
	var previousSequence uint64
	var previousOffset int64 = -1
	for i, entry := range idx.Entries {
		if entry.FirstSequence == 0 || entry.Offset < 0 || entry.Offset >= idx.LogSize || entry.FirstSequence <= previousSequence || entry.Offset <= previousOffset {
			return false
		}
		if i == 0 && (entry.FirstSequence != 1 || entry.Offset != 0) {
			return false
		}
		previousSequence, previousOffset = entry.FirstSequence, entry.Offset
	}
	return (idx.LastSequence == 0) == (len(idx.Entries) == 0) && (idx.CommitCount == 0) == (idx.LastSequence == 0)
}

func writeSparseIndex(cacheDir string, index sparseIndex) {
	data, err := json.Marshal(index)
	if err != nil || os.MkdirAll(cacheDir, 0o700) != nil {
		return
	}
	_ = fileutil.AtomicWriteFileStrict(sparseIndexPath(cacheDir), append(data, '\n'), 0o600)
}

func (s *Store) recordPersistedIndex(file *os.File, start int64, commits []Commit, lengths []int64) {
	if s == nil || file == nil || len(commits) == 0 || len(commits) != len(lengths) {
		return
	}
	info, err := file.Stat()
	if err != nil {
		return
	}
	offset := start
	for i, commit := range commits {
		if i == len(commits)-1 {
			s.tip = durableTip{LogOffset: offset + int64(lengths[i]), AnchorOffset: offset, AnchorFirst: commit.FirstSequence, AnchorCommitID: commit.ID, AnchorHash: commit.OperationHash}
		}
		offset += int64(lengths[i])
	}
	identity, err := sparseLogIdentity(file, info)
	if err != nil {
		return
	}
	head, err := headLogSignature(file, info)
	if err != nil {
		return
	}
	s.indexMu.Lock()
	index := s.index
	if index.partial {
		index.LogSize = info.Size()
		index.LogModTimeNS = info.ModTime().UnixNano()
		index.LogIdentity = identity
		index.HeadSignature = head
		index.LastSequence = commits[len(commits)-1].LastSequence()
		s.index = index
		s.indexMu.Unlock()
		return
	}
	if index.Codec != sparseIndexCodec || index.LogSize != start {
		s.indexMu.Unlock()
		_ = s.rebuildWriterIndex(file)
		return
	}
	offset = start
	for i, commit := range commits {
		if index.CommitCount%sparseIndexInterval == 0 {
			index.Entries = append(index.Entries, sparseIndexEntry{FirstSequence: commit.FirstSequence, Offset: offset})
		}
		index.CommitCount++
		index.LastSequence = commit.LastSequence()
		offset += int64(lengths[i])
	}
	index.LogSize = info.Size()
	index.LogModTimeNS = info.ModTime().UnixNano()
	index.LogIdentity = identity
	index.HeadSignature = head
	s.index = index
	s.indexMu.Unlock()
	writeSparseIndex(s.dir, index)
}

func (s *Store) rebuildWriterIndex(_ *os.File) error {
	index, err := loadOrBuildSparseIndex(context.Background(), s.dir, s.dir)
	if err != nil {
		return err
	}
	s.indexMu.Lock()
	s.index = index
	s.indexMu.Unlock()
	return nil
}

func (idx sparseIndex) checkpoint(offset uint64) sparseIndexEntry {
	if len(idx.Entries) == 0 {
		return sparseIndexEntry{FirstSequence: 1}
	}
	target := offset + 1
	position := max(sort.Search(len(idx.Entries), func(i int) bool { return idx.Entries[i].FirstSequence > target })-1, 0)
	return idx.Entries[position]
}

// headLogSignature hashes the leading bytes only, so it survives appends. It is
// the cache key the offset index can actually use.
func headLogSignature(file *os.File, info os.FileInfo) (string, error) {
	first := min(info.Size(), sparseIdentityBytes)
	if first <= 0 {
		return "", nil
	}
	buf := make([]byte, first)
	n, err := file.ReadAt(buf, 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	hash := sha256.Sum256(buf[:n])
	return hex.EncodeToString(hash[:]), nil
}

func sparseLogIdentity(file *os.File, info os.FileInfo) (string, error) {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%d:%d:", info.Size(), info.ModTime().UnixNano())
	readChunk := func(offset, length int64) error {
		if length <= 0 {
			return nil
		}
		buf := make([]byte, length)
		n, err := file.ReadAt(buf, offset)
		if err != nil && err != io.EOF {
			return err
		}
		_, _ = hash.Write(buf[:n])
		return nil
	}
	first := min(info.Size(), sparseIdentityBytes)
	if err := readChunk(0, first); err != nil {
		return "", err
	}
	if info.Size() > first {
		last := min(info.Size()-first, sparseIdentityBytes)
		if err := readChunk(info.Size()-last, last); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
