package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/store"
)

// SessionUserTimesTailBudgetBytes bounds how much of a session event log's tail
// the display-time overlay may read. The page it feeds is the newest history
// page (60 turns), whose user records sit in the last few megabytes of an
// append-only log — measured at 59 user messages inside the final 2 MiB and 131
// inside the final 4 MiB of a 146 MiB log — so a larger budget only re-reads
// what the page does not need.
//
// Task 123: before this reader existed, the overlay called
// LoadSessionUserMessages, which replays the whole DAG (probe: 4.0 s for a
// 36.9 MiB transcript / 146 MiB event log) to recover the same handful of
// timestamps.
const SessionUserTimesTailBudgetBytes int64 = 8 << 20

// sessionUserTimesTailChunkBytes is the read granularity of the reverse scan.
const sessionUserTimesTailChunkBytes int64 = 256 << 10

// sessionUserTimesTailLineLimit drops a record that cannot fit in one chunk (a
// message carrying inline images): its head would be missing from every buffer
// we decode, and holding it would stall the scan without ever yielding a line.
const sessionUserTimesTailLineLimit = 4 << 20

// LoadSessionUserTimesTail maps message ids to their persisted created-at
// milliseconds by reading only the tail of the session's event log.
//
// The full reader (LoadSessionUserMessages) replays the entire DAG, which costs
// seconds on a large session, while the display overlay only needs the times of
// the messages in the newest page. Matching by id rather than by ordinal keeps
// the result meaningful even when the scanned span contains rewinds or forks: a
// superseded id carries the value of the newest record that names it, and ids
// never recorded simply stay absent, leaving the caller's own fallback intact.
//
// ok reports whether the scan reached the start of the log within the budget.
// When it is false the returned map is a best-effort suffix, so a missing id
// means "not seen", never "this message has no time".
func LoadSessionUserTimesTail(path string, byteBudget int64) (map[string]int64, bool) {
	return loadSessionUserTimesTail(path, byteBudget, sessionUserTimesTailChunkBytes)
}

func loadSessionUserTimesTail(path string, byteBudget, chunk int64) (map[string]int64, bool) {
	times := map[string]int64{}
	if strings.TrimSpace(path) == "" {
		return times, false
	}
	f, err := os.Open(store.SessionEventLog(path))
	if err != nil {
		return times, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return times, false
	}
	if chunk <= 0 {
		chunk = sessionUserTimesTailChunkBytes
	}
	if byteBudget <= 0 {
		byteBudget = SessionUserTimesTailBudgetBytes
	}

	end := info.Size()
	var remainder []byte
	var read int64
	startedAtTop := false
	for end > 0 && read < byteBudget {
		size := chunk
		if size > end {
			size = end
		}
		if read+size > byteBudget {
			size = byteBudget - read
		}
		if size <= 0 {
			break
		}
		start := end - size
		buf := make([]byte, size)
		if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
			return times, false
		}
		read += size
		data := buf
		if len(remainder) > 0 {
			// remainder holds the newer bytes of a line whose head is in this
			// (earlier) chunk, so it is appended after the freshly read bytes.
			data = append(buf, remainder...)
		}
		lines := bytes.Split(data, []byte{'\n'})
		if start > 0 {
			remainder = lines[0]
			if len(remainder) > sessionUserTimesTailLineLimit {
				remainder = nil
			}
			lines = lines[1:]
		} else {
			remainder = nil
			startedAtTop = true
		}
		// Newest first: the first record naming an id wins, so a later patch
		// supersedes the message entry it replaces.
		for i := len(lines) - 1; i >= 0; i-- {
			recordSessionUserTimesTailLine(lines[i], times)
		}
		end = start
	}
	return times, startedAtTop
}

// sessionUserTimesTailEntry declares only the fields the overlay needs. Leaving
// content/raw_content undeclared keeps their (possibly megabyte) strings out of
// the decode result while the JSON scanner still skips them.
type sessionUserTimesTailEntry struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	Target string `json:"target"`
	Msgs   []struct {
		Role      string `json:"role"`
		CreatedAt int64  `json:"createdAt"`
	} `json:"msgs"`
}

func recordSessionUserTimesTailLine(line []byte, times map[string]int64) {
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
		// decodeOne accepts exactly one message per entry; anything else is
		// dropped from the transcript, so it must not contribute a time here.
		if entry.ID == "" || len(entry.Msgs) != 1 {
			return
		}
		if entry.Msgs[0].Role != string(provider.RoleUser) {
			return
		}
		recordTailTime(times, entry.ID, entry.Msgs[0].CreatedAt)
	case sessionDAGTypePatch:
		// A patch replaces the message body; when it carries a time it is the
		// authoritative one. Patches that only rewrite content leave the entry
		// below untouched.
		if entry.Target == "" || len(entry.Msgs) != 1 {
			return
		}
		recordTailTime(times, entry.Target, entry.Msgs[0].CreatedAt)
	}
}

func recordTailTime(times map[string]int64, id string, at int64) {
	if id == "" || at <= 0 {
		return
	}
	if _, seen := times[id]; seen {
		return
	}
	times[id] = at
}
