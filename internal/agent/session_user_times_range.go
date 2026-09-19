package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// LoadSessionUserTimesRange maps message ids to their persisted created-at
// milliseconds by reading the event log forward from byte offset `from`.
//
// It is the forward companion of LoadSessionUserTimesTail, for callers that
// already hold a result covering everything before `from` and only need what an
// append added. The two scans index the same records but resolve conflicts in
// opposite directions: the reverse scan keeps the first (newest) record it
// meets, while this one lets later records overwrite earlier ones — both end up
// reporting the newest value for an id.
//
// `from` need not land on a line boundary; a partial leading line cannot be
// decoded and is skipped, which can at worst lose one record's time.
func LoadSessionUserTimesRange(path string, from int64) (map[string]int64, error) {
	times := map[string]int64{}
	if strings.TrimSpace(path) == "" {
		return times, nil
	}
	f, err := os.Open(store.SessionEventLog(path))
	if err != nil {
		return times, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return times, err
	}
	if from < 0 {
		from = 0
	}
	if from >= info.Size() {
		return times, nil
	}
	if _, err := f.Seek(from, io.SeekStart); err != nil {
		return times, err
	}
	reader := bufio.NewReaderSize(f, 1<<20)
	// A resume offset is usually a file size observed between writes, so it can
	// land mid-record. Only drop the leading line when the byte before the offset
	// says we are not already at a line start — otherwise the append's first
	// record would be lost, which is exactly the case this scan exists for.
	skipLeading := false
	if from > 0 {
		var previous [1]byte
		if _, err := f.ReadAt(previous[:], from-1); err == nil && previous[0] != '\n' {
			skipLeading = true
		}
	}
	for {
		line, readErr := readSessionUserTimesLine(reader)
		if len(line) > 0 {
			if skipLeading {
				// The first line can start mid-record when `from` came from a size
				// observed between writes; it cannot be decoded.
				skipLeading = false
			} else {
				recordSessionUserTimesLineLatest(line, times)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return times, readErr
		}
	}
	return times, nil
}

// readSessionUserTimesLine returns one record without its line ending, growing
// past the reader's buffer so a record carrying inline images does not truncate
// the scan. A non-EOF error means the tail was torn mid-write: the caller keeps
// what it decoded.
func readSessionUserTimesLine(reader *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(chunk) > 0 {
			buf = append(buf, chunk...)
		}
		if err == nil {
			return bytes.TrimRight(buf, "\r\n"), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			if len(buf) > sessionUserTimesTailLineLimit {
				// Abandon an over-long record instead of growing without bound;
				// the rest of the log still yields its own records.
				buf = buf[:0]
			}
			continue
		}
		return bytes.TrimRight(buf, "\r\n"), err
	}
}

// recordSessionUserTimesLineLatest is the forward-scan counterpart of
// recordSessionUserTimesTailLine: the newest record for an id wins, so a later
// patch (or a re-saved message) overwrites what was seen earlier.
func recordSessionUserTimesLineLatest(line []byte, times map[string]int64) {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return
	}
	var entry sessionUserTimesTailEntry
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&entry); err != nil {
		return
	}
	switch entry.Type {
	case sessionDAGTypeMessage:
		if entry.ID == "" || len(entry.Msgs) != 1 {
			return
		}
		if entry.Msgs[0].Role != string(provider.RoleUser) {
			return
		}
		overwriteTailTime(times, entry.ID, entry.Msgs[0].CreatedAt)
	case sessionDAGTypePatch:
		if entry.Target == "" || len(entry.Msgs) != 1 {
			return
		}
		overwriteTailTime(times, entry.Target, entry.Msgs[0].CreatedAt)
	}
}

func overwriteTailTime(times map[string]int64, id string, at int64) {
	if id == "" {
		return
	}
	if at <= 0 {
		// A patch that only rewrites content says nothing about the time; keep
		// whatever the message entry recorded.
		return
	}
	times[id] = at
}
