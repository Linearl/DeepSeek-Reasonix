package collabchannel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/sessioncollab"
)

const hourMs = int64(time.Hour / time.Millisecond)

// newStore builds a test store: controllable clock (returned advance fn),
// zero pacing.
func newStore(t *testing.T) (*Store, string, func(int64)) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	clock := int64(1_700_000_000_000)
	s.SetClock(func() int64 { return clock })
	advance := func(ms int64) { clock += ms }
	s.SetPace(func(int) time.Duration { return 0 })
	return s, dir, advance
}

// 验收 1：channel 消息 SQLite 落库 + md 导出可读（时间/发送方/正文）。
func TestChannelSQLiteAndMarkdownExport(t *testing.T) {
	s, dir, advance := newStore(t)
	dev, err := s.CreateChannel("dev", "engineering channel", 0, "sc_a", "sc_b")
	if err != nil {
		t.Fatal(err)
	}
	if dev.HourlyLimit != DefaultHourlyCap {
		t.Fatalf("default hourly cap: %+v", dev)
	}
	first, err := s.Publish("dev", "sc_s", "deploy is green")
	if err != nil {
		t.Fatal(err)
	}
	advance(60_000)
	second, err := s.Publish("dev", "sc_s", "shipping the follow-up")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("message ids must be unique")
	}

	// ① SQLite 落库：直查 messages 表（包内可达 db），不走自己的读 API。
	var raw int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE channel_id = ?`, dev.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != 2 {
		t.Fatalf("messages table holds %d rows, want 2", raw)
	}

	// ② md 导出可读：时间 + 发送方 + 正文齐全。
	out := filepath.Join(dir, "export", "dev.md")
	if _, err := s.ExportMarkdown("dev", out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	md := string(b)
	if !strings.Contains(md, "# dev") || !strings.Contains(md, "engineering channel") {
		t.Fatalf("header missing:\n%s", md)
	}
	if !strings.Contains(md, "sc_s") || !strings.Contains(md, "deploy is green") || !strings.Contains(md, "shipping the follow-up") {
		t.Fatalf("sender/body missing:\n%s", md)
	}
	if !strings.Contains(md, "2023-11-15") { // 1_700_000_000_000 → UTC date
		t.Fatalf("timestamp missing:\n%s", md)
	}
}

// 验收 2：每个订阅成员收到——per-recipient delivered 独立记录 + read 独立
// 记录，不同收件方状态互不覆盖。
func TestFanoutPerRecipientDeliveredAndReadIndependent(t *testing.T) {
	s, dir, _ := newStore(t)
	if _, err := s.CreateChannel("dev", "", 0, "sc_a", "sc_b", "sc_c"); err != nil {
		t.Fatal(err)
	}
	msg, err := s.Publish("dev", "sc_s", "hello channel")
	if err != nil {
		t.Fatal(err)
	}
	stats, err := s.DrainFanout(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Delivered != 3 || stats.Failed != 0 || stats.Queued != 3 {
		t.Fatalf("fan-out stats: %+v", stats)
	}

	rows, err := s.FanoutStates("dev")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("want 3 per-recipient rows, got %d", len(rows))
	}
	fanoutIDs := map[string]bool{}
	for _, r := range rows {
		if r.State != "delivered" || r.DeliveredAt == 0 {
			t.Fatalf("row not marked delivered: %+v", r)
		}
		if r.ReadAt != 0 {
			t.Fatalf("fresh delivery must not be read: %+v", r)
		}
		fanoutIDs[r.FanoutID] = true
	}
	if len(fanoutIDs) != 3 {
		t.Fatalf("each member needs its own fan-out mail id: %+v", fanoutIDs)
	}

	// 每个成员的收件箱里确有这封信（同一 MailStore，验证 309 通道复用）。
	mail := sessioncollab.NewMailStore(dir)
	for _, member := range []string{"sc_a", "sc_b", "sc_c"} {
		inbox, err := mail.Inbox(member)
		if err != nil || len(inbox) != 1 {
			t.Fatalf("%s inbox: %v len=%d", member, err, len(inbox))
		}
		if inbox[0].Body != "hello channel" || inbox[0].From != "sc_s" {
			t.Fatalf("%s got %+v", member, inbox[0])
		}
		if inbox[0].Delivery != string(sessioncollab.DeliveryFollowup) {
			t.Fatalf("fan-out must be followup (429: idle members not woken): %+v", inbox[0])
		}
		if inbox[0].ID == "" || inbox[0].ID == msg.ID {
			t.Fatalf("fan-out ids are per-member: %+v", inbox[0])
		}
	}

	// 只有 sc_a 已读——b/c 保持未读，行互不覆盖。
	n, err := s.MarkRead("dev", "sc_a", []string{msg.ID})
	if err != nil || n != 1 {
		t.Fatalf("MarkRead: n=%d err=%v", n, err)
	}
	rows, _ = s.FanoutStates("dev")
	for _, r := range rows {
		switch r.Member {
		case "sc_a":
			if r.ReadAt == 0 {
				t.Fatalf("sc_a must be read: %+v", r)
			}
		default:
			if r.ReadAt != 0 {
				t.Fatalf("%s read state leaked from another member: %+v", r.Member, r)
			}
		}
	}
	// sc_a 重复标记不重复生效（NULL 守卫），sc_b 自己标自己。
	if n, _ := s.MarkRead("dev", "sc_a", []string{msg.ID}); n != 0 {
		t.Fatalf("re-mark must be a no-op, moved %d", n)
	}
	if n, _ := s.MarkRead("dev", "sc_b", []string{msg.ID}); n != 1 {
		t.Fatalf("sc_b marks its own row, got %d", n)
	}
	rows, _ = s.FanoutStates("dev")
	for _, r := range rows {
		want := r.Member == "sc_a" || r.Member == "sc_b"
		if (r.ReadAt != 0) != want {
			t.Fatalf("independent read rows broken: %+v", r)
		}
	}
}

// 验收 3：展开单发复用 309 通道——包内 grep 证据：交付面只有
// sessioncollab.MailStore，没有第二条投递通道。
func TestFanoutReusesMailboxNoSecondDeliveryChannel(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("package files: %v %v", files, err)
	}
	sawMailstore := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		if strings.Contains(src, "sessioncollab.MailStore") && strings.Contains(src, "s.mail.Deliver(") {
			sawMailstore = true
		}
		for _, banned := range []string{"net/http", "net.Dial", "websocket", "os/exec", "grpc"} {
			if strings.Contains(src, banned) {
				t.Fatalf("%s imports a second delivery path %q — 铁律 8 forbids it", f, banned)
			}
		}
	}
	if !sawMailstore {
		t.Fatal("fan-out must deliver through sessioncollab.MailStore (task 309 channel)")
	}
}

// 验收 4：错峰 + 默认 followup + 小时上限生效（followup 断言在 per-recipient
// 测试内；此处管 pace 与时限）。
func TestStormGovernancePaceAndHourlyCap(t *testing.T) {
	// 生产错峰恒在 2-5s。
	for i := 0; i < 50; i++ {
		d := defaultPace(i)
		if d < 2*time.Second || d > 5*time.Second {
			t.Fatalf("pace %v outside 2-5s", d)
		}
	}

	s, _, advance := newStore(t)
	if _, err := s.CreateChannel("tiny", "", 2, "sc_a", "sc_b", "sc_c"); err != nil {
		t.Fatal(err)
	}
	// 间歇被注入 drain：成员之间至少 N-1 个间隔。
	var gaps []int
	s.SetPace(func(i int) time.Duration {
		gaps = append(gaps, i)
		return 0
	})
	if _, err := s.Publish("tiny", "sc_s", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DrainFanout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 2 {
		t.Fatalf("3 members must pace 2 gaps (between members), got %v", gaps)
	}

	// 小时上限：cap=2 → 第三条拒绝，跨窗后放行。
	advance(1000)
	if _, err := s.Publish("tiny", "sc_s", "m2"); err != nil {
		t.Fatal(err)
	}
	advance(1000)
	_, err := s.Publish("tiny", "sc_s", "m3")
	if !errors.Is(err, ErrHourlyCap) {
		t.Fatalf("third message in the hour must hit the cap, got %v", err)
	}
	advance(hourMs + 1)
	if _, err := s.Publish("tiny", "sc_s", "m4"); err != nil {
		t.Fatalf("window rolled over: %v", err)
	}
}

// 队列持久：重启（新 Store）后 queued 行仍在，下一次 drain 补投——且投过
// 的不重复（presence dedupe）。
func TestQueuedFanoutSurvivesRestartWithoutDuplicates(t *testing.T) {
	s, dir, _ := newStore(t)
	if _, err := s.CreateChannel("dev", "", 0, "sc_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish("dev", "sc_s", "persist me"); err != nil {
		t.Fatal(err)
	}
	// 重启：关旧开新。
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reborn, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reborn.Close()
	reborn.SetPace(func(int) time.Duration { return 0 })
	stats, err := reborn.DrainFanout(context.Background())
	if err != nil || stats.Delivered != 1 {
		t.Fatalf("restart drain: %+v err=%v", stats, err)
	}
	// 再 drain 一次：无 queued 行，不重复投递。
	stats2, err := reborn.DrainFanout(context.Background())
	if err != nil || stats2.Queued != 0 {
		t.Fatalf("second drain: %+v err=%v", stats2, err)
	}
	mail := sessioncollab.NewMailStore(dir)
	inbox, _ := mail.Inbox("sc_a")
	if len(inbox) != 1 {
		t.Fatalf("duplicate fan-out after drain: %d", len(inbox))
	}
}

// 频道实体读写面（工具底座）：list/join/leave/messages 边界。
func TestChannelEntitySurface(t *testing.T) {
	s, _, _ := newStore(t)
	if _, err := s.CreateChannel("dev", "t", 0, "sc_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.Join("dev", "sc_b"); err != nil {
		t.Fatal(err)
	}
	if err := s.Join("dev", "sc_b"); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := s.Leave("dev", "sc_a"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListChannels()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if len(list[0].Members) != 1 || list[0].Members[0] != "sc_b" {
		t.Fatalf("members: %+v", list[0])
	}
	if _, err := s.Messages("nope", 0, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown channel must report ErrNotFound, got %v", err)
	}
	if _, err := s.Publish("nope", "sc_s", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publish to unknown channel: %v", err)
	}
	// limit 上限：构造 >500? messages clamp at 500 — boundary is the clamp itself.
	if _, err := s.Messages("dev", 0, 5000); err != nil {
		t.Fatalf("limit clamp: %v", err)
	}
}
