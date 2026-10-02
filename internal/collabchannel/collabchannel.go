// Package collabchannel is task 349: chat channels as durable entities.
//
// 群聊本质 (2026-09-28 user definition, the authoritative requirement):
//  1. one channel many conversations can talk in;
//  2. every conversation can send to it;
//  3. every member receives every message.
//
// Shape (0928 拍板4 + 方案 §3b): the channel is a VIRTUAL address plus
// tool-layer expansion to single sends — there is no second delivery channel.
// Publish writes the message to the channel's SQLite entity (the pub/sub
// log), then fans out per member through the SAME sessioncollab MailStore the
// task-309 mailbox uses (铁律 8), pacing members 2-5s apart with delivery=
// followup so an idle member is never steer-woken, under a per-channel hourly
// message cap (429 storm governance). Per-recipient delivered/read live in
// their own fanout rows — one member's state never overwrites another's.
package collabchannel

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"reasonix/internal/sessioncollab"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (house pattern: projectiondb/topicstate)
)

const schemaVersion = 2

// ErrHourlyCap is the per-channel 429 guard: publish refused until the
// rolling hour window frees up.
var ErrHourlyCap = errors.New("collabchannel: hourly message cap reached")

// ErrNotFound reports an unknown channel reference (name or id).
var ErrNotFound = errors.New("collabchannel: channel not found")

// ErrNotSender rejects a cancel by anyone but the original sender (取消消息
// 是发送方权利，不是管理员权利 — one session cannot retract another's line).
var ErrNotSender = errors.New("collabchannel: only the sender can cancel a message")

// DefaultHourlyCap is the storm ceiling for a channel that sets no explicit
// limit (429: 单群每小时消息上限).
const DefaultHourlyCap = 30

// Store is the channel entity over SQLite beside the mail directory.
type Store struct {
	db   *sql.DB
	mail *sessioncollab.MailStore

	// now is the ms clock (injectable for tests).
	now func() int64
	// pace returns the delay before member i of a fan-out (i >= 1); nil keeps
	// the production 2-5s jitter (429 错峰).
	pace func(i int) time.Duration
	// sleep honors pace (injectable so tests run instantly).
	sleep func(time.Duration)

	// drainMu serializes fan-out drains across goroutines (per-connection
	// SQLite writers must not interleave a claim/ack pair).
	drainMu sync.Mutex
}

// Open builds a store over mailDir (channels.db lives beside the mail files —
// the channel layer shares the mailbox root by design) and pairs it with the
// task-309 MailStore it fans out through.
func Open(mailDir string) (*Store, error) {
	if strings.TrimSpace(mailDir) == "" {
		return nil, errors.New("collabchannel: mail directory is required")
	}
	if err := os.MkdirAll(mailDir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", diskFileDSN(filepath.Join(mailDir, "channels.db")))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	for _, pragma := range []string{
		`PRAGMA journal_mode=WAL`,
		`PRAGMA synchronous=NORMAL`,
		`PRAGMA busy_timeout=2000`,
		`PRAGMA foreign_keys=ON`,
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("collabchannel: %s: %w", pragma, err)
		}
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{
		db:    db,
		mail:  sessioncollab.NewMailStore(mailDir),
		now:   func() int64 { return time.Now().UnixMilli() },
		sleep: time.Sleep,
	}, nil
}

// SetClock overrides the ms clock (tests).
func (s *Store) SetClock(now func() int64) { s.now = now }

// SetPace overrides the inter-member delay (tests run with 0; production
// defaults to the 2-5s jitter below).
func (s *Store) SetPace(pace func(i int) time.Duration) { s.pace = pace }

func defaultPace(int) time.Duration {
	// 2-5s jitter: spreads a fan-out so a burst never lands as a burst
	// (task 349 429 治理 — 展开层错峰).
	const span = int64(4) // 2,3,4,5 seconds
	n := time.Now().UnixNano()
	return time.Duration(2+(n%span+span)%span) * time.Second
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS channels (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			topic TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			hourly_limit INTEGER NOT NULL DEFAULT ` + fmt.Sprint(DefaultHourlyCap) + `
		)`,
		`CREATE TABLE IF NOT EXISTS members (
			channel_id TEXT NOT NULL,
			contact TEXT NOT NULL,
			joined_at INTEGER NOT NULL,
			PRIMARY KEY (channel_id, contact)
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			channel_id TEXT NOT NULL,
			at INTEGER NOT NULL,
			sender TEXT NOT NULL,
			body TEXT NOT NULL,
			cancelled_at INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_channel_at ON messages (channel_id, at)`,
		`CREATE TABLE IF NOT EXISTS fanout (
			message_id TEXT NOT NULL,
			member TEXT NOT NULL,
			fanout_id TEXT NOT NULL,
			state TEXT NOT NULL DEFAULT 'queued',
			error TEXT NOT NULL DEFAULT '',
			delivered_at INTEGER,
			read_at INTEGER,
			PRIMARY KEY (message_id, member)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("collabchannel: migrate: %w", err)
		}
	}
	// v1→v2: 取消墓碑列。旧库（schema v1）靠 ALTER 补列；新库 CREATE 已带，
	// 重复列报错按已存在忽略（SQLite: "duplicate column name"）。
	if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN cancelled_at INTEGER`); err != nil &&
		!strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		return fmt.Errorf("collabchannel: migrate cancelled_at: %w", err)
	}
	_, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, fmt.Sprint(schemaVersion))
	return err
}

// diskFileDSN mirrors the topicstate/projectiondb file URI (Windows drive →
// /C:/... path form) plus the two pragmas every store here wants.
func diskFileDSN(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	slash := filepath.ToSlash(abs)
	if runtime.GOOS == "windows" && len(slash) >= 2 && slash[1] == ':' {
		slash = "/" + slash
	}
	u := &url.URL{Scheme: "file", Path: slash}
	return u.String() + "?_pragma=busy_timeout%282000%29&_pragma=foreign_keys%281%29"
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func newID(prefix string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}

// Channel is one entity row plus its membership (查看频道 payload).
type Channel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Topic       string   `json:"topic,omitempty"`
	CreatedAt   int64    `json:"createdAt"`
	HourlyLimit int      `json:"hourlyLimit"`
	Members     []string `json:"members"`
	Messages    int      `json:"messages"`
}

// Message is one channel-side record (获取消息 payload).
type Message struct {
	ID      string `json:"id"`
	Channel string `json:"channel"`
	At      int64  `json:"at"`
	Sender  string `json:"sender"`
	Body    string `json:"body"`
	// CancelledAt is the cancel tombstone (取消消息): 0 = live. A cancelled
	// message keeps its history line but stops pending fan-out and is marked
	// in every later read/export.
	CancelledAt int64 `json:"cancelledAt,omitempty"`
}

// CancelResult reports what one cancel touched: the tombstone time, how many
// queued fan-out rows were stopped before delivery, and how many had already
// been delivered. Delivered copies stay (the task-309 inbox belongs to the
// recipient — there is no cross-owner inbox rewrite); the tombstone is what
// every later channel read / md export shows.
type CancelResult struct {
	MessageID        string `json:"messageId"`
	Channel          string `json:"channel"`
	CancelledAt      int64  `json:"cancelledAt"`
	CancelledQueued  int    `json:"cancelledQueued"`
	AlreadyDelivered int    `json:"alreadyDelivered"`
	AlreadyCancelled bool   `json:"alreadyCancelled"`
}

// FanoutRow is the per-recipient delivery state of one channel message
// (per-recipient delivered/read — one row per member, never merged).
type FanoutRow struct {
	MessageID   string `json:"messageId"`
	Member      string `json:"member"`
	FanoutID    string `json:"fanoutId"`
	State       string `json:"state"` // queued | delivered | failed
	Error       string `json:"error,omitempty"`
	DeliveredAt int64  `json:"deliveredAt,omitempty"`
	ReadAt      int64  `json:"readAt,omitempty"`
}

// FanoutStats summarizes one drain pass.
type FanoutStats struct {
	Queued    int `json:"queued"`
	Delivered int `json:"delivered"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`   // already in the member's inbox (crash retry)
	Cancelled int `json:"cancelled"` // row stopped by a cancel between claim and deliver
}

// resolve maps a channel name-or-id to its id.
func (s *Store) resolve(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("%w: empty reference", ErrNotFound)
	}
	var id string
	err := s.db.QueryRow(`SELECT id FROM channels WHERE id = ? OR name = ?`, ref, ref).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %q", ErrNotFound, ref)
	}
	return id, err
}

// CreateChannel registers a channel entity with its founding members
// (Go API: the three agent tools are list/read/send by 0928 ruling; creation
// belongs to a future panel/host surface).
func (s *Store) CreateChannel(name, topic string, hourlyLimit int, members ...string) (Channel, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Channel{}, errors.New("collabchannel: channel name is required")
	}
	if hourlyLimit <= 0 {
		hourlyLimit = DefaultHourlyCap
	}
	id := newID("chan_")
	now := s.now()
	tx, err := s.db.Begin()
	if err != nil {
		return Channel{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT INTO channels (id, name, topic, created_at, hourly_limit) VALUES (?, ?, ?, ?, ?)`,
		id, name, strings.TrimSpace(topic), now, hourlyLimit); err != nil {
		return Channel{}, fmt.Errorf("collabchannel: create %q: %w", name, err)
	}
	seen := map[string]bool{}
	for _, m := range members {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		if _, err := tx.Exec(`INSERT OR IGNORE INTO members (channel_id, contact, joined_at) VALUES (?, ?, ?)`, id, m, now); err != nil {
			return Channel{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Channel{}, err
	}
	return s.Channel(id)
}

// Join adds a member (idempotent).
func (s *Store) Join(ref, contact string) error {
	return s.memberOp(ref, contact, true)
}

// Leave removes a member (idempotent).
func (s *Store) Leave(ref, contact string) error {
	return s.memberOp(ref, contact, false)
}

func (s *Store) memberOp(ref, contact string, add bool) error {
	contact = strings.TrimSpace(contact)
	if contact == "" {
		return errors.New("collabchannel: contact is required")
	}
	id, err := s.resolve(ref)
	if err != nil {
		return err
	}
	if add {
		_, err = s.db.Exec(`INSERT OR IGNORE INTO members (channel_id, contact, joined_at) VALUES (?, ?, ?)`, id, contact, s.now())
	} else {
		_, err = s.db.Exec(`DELETE FROM members WHERE channel_id = ? AND contact = ?`, id, contact)
	}
	return err
}

// Channel loads one channel by ref.
func (s *Store) Channel(ref string) (Channel, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return Channel{}, err
	}
	var c Channel
	err = s.db.QueryRow(`SELECT id, name, topic, created_at, hourly_limit FROM channels WHERE id = ?`, id).
		Scan(&c.ID, &c.Name, &c.Topic, &c.CreatedAt, &c.HourlyLimit)
	if err != nil {
		return Channel{}, err
	}
	c.Members, err = s.membersOf(id)
	if err != nil {
		return Channel{}, err
	}
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE channel_id = ?`, id).Scan(&c.Messages)
	return c, nil
}

func (s *Store) membersOf(channelID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT contact FROM members WHERE channel_id = ? ORDER BY joined_at, contact`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListChannels answers 查看频道: every channel with membership and message
// counts, newest first.
func (s *Store) ListChannels() ([]Channel, error) {
	rows, err := s.db.Query(`SELECT id FROM channels ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Channel, 0, len(ids))
	for _, id := range ids {
		c, err := s.Channel(id)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// Publish is the pub/sub write half: the message lands in the channel's
// SQLite log AND gets one queued fan-out row per member (sender excluded —
// they see their own line through channel history, and a self-copy would
// echo back into their own mailbox as an inbound stranger). The hourly cap
// gates here (429). Fan-out delivery itself is DrainFanout — Publish never
// blocks on pacing.
func (s *Store) Publish(ref, sender, body string) (Message, error) {
	sender = strings.TrimSpace(sender)
	body = strings.TrimSpace(body)
	if sender == "" || body == "" {
		return Message{}, errors.New("collabchannel: sender and message are required")
	}
	id, err := s.resolve(ref)
	if err != nil {
		return Message{}, err
	}
	// Membership is read BEFORE the write transaction (a WAL reader beside an
	// open write tx is one more BUSY surface than this needs); a member who
	// joins mid-publish simply starts with the next message.
	members, err := s.membersOf(id)
	if err != nil {
		return Message{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var capPerHour int
	if err := tx.QueryRow(`SELECT hourly_limit FROM channels WHERE id = ?`, id).Scan(&capPerHour); err != nil {
		return Message{}, err
	}
	var recent int
	windowStart := s.now() - int64(time.Hour/time.Millisecond)
	if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE channel_id = ? AND at > ?`, id, windowStart).Scan(&recent); err != nil {
		return Message{}, err
	}
	if recent >= capPerHour {
		return Message{}, fmt.Errorf("%w: %s has %d messages in the last hour (limit %d)",
			ErrHourlyCap, ref, recent, capPerHour)
	}

	msg := Message{ID: newID("chm_"), Channel: id, At: s.now(), Sender: sender, Body: body}
	if _, err := tx.Exec(`INSERT INTO messages (id, channel_id, at, sender, body) VALUES (?, ?, ?, ?, ?)`,
		msg.ID, id, msg.At, sender, body); err != nil {
		return Message{}, err
	}
	for _, m := range members {
		if m == sender {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO fanout (message_id, member, fanout_id, state) VALUES (?, ?, ?, 'queued')`,
			msg.ID, m, newID("chf_")); err != nil {
			return Message{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// Cancel (取消消息) tombstones one channel message and stops its PENDING
// fan-out: queued rows flip to state='cancelled' so DrainFanout skips them,
// and an in-flight drain re-checks every row right before delivery, so a
// cancel wins even against a drain that is mid-pass. Copies already delivered
// into member mailboxes are NOT retracted — the task-309 inbox belongs to the
// recipient and no code path rewrites another session's inbox file; the
// tombstone is what every later channel history / md export shows. Only the
// original sender may cancel; cancelling twice is idempotent (the second call
// reports the first tombstone and stops nothing new).
func (s *Store) Cancel(ref, messageID, requester string) (CancelResult, error) {
	requester = strings.TrimSpace(requester)
	if requester == "" {
		return CancelResult{}, errors.New("collabchannel: requester identity is required")
	}
	id, err := s.resolve(ref)
	if err != nil {
		return CancelResult{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CancelResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var sender string
	var tombstone sql.NullInt64
	err = tx.QueryRow(`SELECT sender, cancelled_at FROM messages WHERE id = ? AND channel_id = ?`,
		messageID, id).Scan(&sender, &tombstone)
	if errors.Is(err, sql.ErrNoRows) {
		return CancelResult{}, fmt.Errorf("%w: message %q", ErrNotFound, messageID)
	}
	if err != nil {
		return CancelResult{}, err
	}
	if sender != requester {
		return CancelResult{}, fmt.Errorf("%w: %q was sent by %s, not %s", ErrNotSender, messageID, sender, requester)
	}

	res := CancelResult{MessageID: messageID, Channel: id}
	if tombstone.Valid && tombstone.Int64 > 0 {
		res.CancelledAt = tombstone.Int64
		res.AlreadyCancelled = true
	} else {
		res.CancelledAt = s.now()
		if _, err := tx.Exec(`UPDATE messages SET cancelled_at = ? WHERE id = ?`, res.CancelledAt, messageID); err != nil {
			return CancelResult{}, err
		}
	}
	qr, err := tx.Exec(`UPDATE fanout SET state = 'cancelled' WHERE message_id = ? AND state = 'queued'`, messageID)
	if err != nil {
		return CancelResult{}, err
	}
	if n, err := qr.RowsAffected(); err == nil {
		res.CancelledQueued = int(n)
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM fanout WHERE message_id = ? AND state = 'delivered'`,
		messageID).Scan(&res.AlreadyDelivered); err != nil {
		return CancelResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CancelResult{}, err
	}
	return res, nil
}

// Messages answers 获取消息: channel history since an optional ms bound,
// oldest first, hard-capped.
func (s *Store) Messages(ref string, sinceMs int64, limit int) ([]Message, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.Query(`SELECT id, channel_id, at, sender, body, COALESCE(cancelled_at, 0) FROM messages
		WHERE channel_id = ? AND (? = 0 OR at >= ?) ORDER BY at DESC LIMIT ?`, id, sinceMs, sinceMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.Channel, &m.At, &m.Sender, &m.Body, &m.CancelledAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	// Newest first from SQL; flip so callers read chronologically.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// DrainFanout is the expand-to-single-sends pass (拍板4 展开单发): every
// queued fan-out row is delivered through the task-309 MailStore with
// delivery=followup, pacing between members (default 2-5s jitter, injectable).
// Presence in the member's inbox is checked BEFORE delivering so a crash
// between deliver and mark never duplicates on retry. A drain is serialized;
// concurrent kicks queue behind drainMu.
func (s *Store) DrainFanout(ctx context.Context) (FanoutStats, error) {
	s.drainMu.Lock()
	defer s.drainMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := s.db.Query(`SELECT f.message_id, f.member, f.fanout_id, m.sender, m.at, m.body, c.name
		FROM fanout f JOIN messages m ON m.id = f.message_id
		JOIN channels c ON c.id = m.channel_id
		WHERE f.state = 'queued' ORDER BY m.at, f.member`)
	if err != nil {
		return FanoutStats{}, err
	}
	type job struct {
		messageID, member, fanoutID, sender, body string
		// channel is the group-source stamp (349 挂账 note①): the channel's
		// name at delivery time, so the recipient's inbox can show where the
		// mail came from even if the channel is renamed or gone later.
		channel string
		at      int64
	}
	var jobs []job
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.messageID, &j.member, &j.fanoutID, &j.sender, &j.at, &j.body, &j.channel); err != nil {
			rows.Close()
			return FanoutStats{}, err
		}
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return FanoutStats{}, err
	}
	rows.Close()

	stats := FanoutStats{Queued: len(jobs)}
	pace := s.pace
	if pace == nil {
		pace = defaultPace
	}
	for i, j := range jobs {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if i > 0 {
			s.sleep(pace(i)) // 错峰: never land a burst as a burst
		}
		// Cancel guard (取消消息): the rows were claimed before the pacing
		// sleeps, so re-check right before delivering — a cancel that lands
		// mid-drain still wins.
		var state string
		var tombstone sql.NullInt64
		if err := s.db.QueryRow(`SELECT f.state, m.cancelled_at
			FROM fanout f JOIN messages m ON m.id = f.message_id
			WHERE f.message_id = ? AND f.member = ?`, j.messageID, j.member).Scan(&state, &tombstone); err == nil &&
			(state != "queued" || (tombstone.Valid && tombstone.Int64 > 0)) {
			stats.Cancelled++
			continue
		}
		// Crash-retry dedupe: if the fan-out mail is already in this member's
		// inbox, just settle the row.
		if _, found := s.mail.ParentThread(j.member, j.fanoutID); found {
			_ = s.settle(j.messageID, j.member, "delivered", "", s.now())
			stats.Skipped++
			continue
		}
		_, err := s.mail.Deliver(sessioncollab.MailMessage{
			ID: j.fanoutID,
			// From is the original sender so the member can reply on-channel
			// through the same address they always reply to.
			From:     j.sender,
			To:       j.member,
			Body:     j.body,
			Delivery: string(sessioncollab.DeliveryFollowup), // 429: idle members are never steer-woken
			At:       j.at,
			// 349 挂账 note①: the group-source stamp — the task-320 inbox
			// surfaces it as the entry's channel identifier (a delivered
			// channel copy must not look identical to a direct send).
			Channel: j.channel,
		})
		if err != nil {
			_ = s.settle(j.messageID, j.member, "failed", err.Error(), 0)
			stats.Failed++
			continue
		}
		_ = s.settle(j.messageID, j.member, "delivered", "", s.now())
		stats.Delivered++
	}
	return stats, nil
}

func (s *Store) settle(messageID, member, state, errMsg string, at int64) error {
	_, err := s.db.Exec(`UPDATE fanout SET state = ?, error = ?, delivered_at = ?
		WHERE message_id = ? AND member = ?`, state, errMsg, at, messageID, member)
	return err
}

// FanoutStates returns the per-recipient rows of one channel (observability
// + tests: delivered/read are per member and never merged).
func (s *Store) FanoutStates(ref string) ([]FanoutRow, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT f.message_id, f.member, f.fanout_id, f.state, f.error, f.delivered_at, f.read_at
		FROM fanout f JOIN messages m ON m.id = f.message_id
		WHERE m.channel_id = ? ORDER BY m.at, f.member`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FanoutRow
	for rows.Next() {
		var r FanoutRow
		var deliveredAt, readAt sql.NullInt64
		if err := rows.Scan(&r.MessageID, &r.Member, &r.FanoutID, &r.State, &r.Error, &deliveredAt, &readAt); err != nil {
			return nil, err
		}
		r.DeliveredAt, r.ReadAt = deliveredAt.Int64, readAt.Int64
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkRead records the per-recipient read state (task 349: 已读具体到每个
// 支件方 — rows are keyed by (message, member), so one member's read can
// never overwrite another's). Returns how many rows moved.
func (s *Store) MarkRead(ref, member string, messageIDs []string) (int, error) {
	member = strings.TrimSpace(member)
	if member == "" || len(messageIDs) == 0 {
		return 0, nil
	}
	id, err := s.resolve(ref)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, mid := range messageIDs {
		res, err := s.db.Exec(`UPDATE fanout SET read_at = ?
			WHERE member = ? AND read_at IS NULL AND state = 'delivered'
			AND message_id = ? AND message_id IN (SELECT id FROM messages WHERE channel_id = ?)`,
			s.now(), member, mid, id)
		if err != nil {
			return total, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			total += int(n)
		}
	}
	return total, nil
}

// ExportMarkdown renders the channel history for humans (task 349: md 导出
// 供人类查看/事后汇总) — time, sender, body per line. Returns the path.
func (s *Store) ExportMarkdown(ref, path string) (string, error) {
	c, err := s.Channel(ref)
	if err != nil {
		return "", err
	}
	msgs, err := s.Messages(c.ID, 0, 500)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", c.Name)
	if c.Topic != "" {
		fmt.Fprintf(&b, "Topic: %s\n\n", c.Topic)
	}
	fmt.Fprintf(&b, "- members: %s\n", strings.Join(c.Members, ", "))
	fmt.Fprintf(&b, "- messages: %d (exported %s)\n\n", len(msgs),
		time.UnixMilli(s.now()).Format("2006-01-02 15:04:05"))
	for _, m := range msgs {
		if m.CancelledAt > 0 {
			// 取消墓碑：保留时间与发送方便于对账，正文不再渲染。
			fmt.Fprintf(&b, "- %s — %s（已取消）\n",
				time.UnixMilli(m.At).Format("2006-01-02 15:04:05"),
				m.Sender)
			continue
		}
		fmt.Fprintf(&b, "- %s — %s:\n  %s\n",
			time.UnixMilli(m.At).Format("2006-01-02 15:04:05"),
			m.Sender,
			strings.ReplaceAll(strings.TrimSpace(m.Body), "\n", "\n  "))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
