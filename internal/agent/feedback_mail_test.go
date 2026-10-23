package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/collabinbox"
	"reasonix/internal/config"
	"reasonix/internal/tool/builtin"
)

// feedbackMailFixture wires a FeedbackMailConfig against temp dirs with a fake
// session factory that mimics create_collab_session's contract: an empty
// transcript + a branch meta with a fresh contact id (minted once per call).
type feedbackMailFixture struct {
	cfg     FeedbackMailConfig
	mailDir string
	sessDir string
	inbox   string
	// creates counts factory invocations (the mint-once contract's assertion).
	creates int
	// minted records every address the factory minted (assertion aid).
	minted []string
	// registryPath is the pinned address file's location.
	registryPath string
}

func newFeedbackMailFixture(t *testing.T) *feedbackMailFixture {
	t.Helper()
	root := t.TempDir()
	f := &feedbackMailFixture{
		mailDir:      filepath.Join(root, "session-chat"),
		sessDir:      filepath.Join(root, "sessions"),
		inbox:        filepath.Join(root, "feedback-inbox"),
		registryPath: filepath.Join(root, "session-chat", "contact-registry.json"),
	}
	if err := os.MkdirAll(f.mailDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.sessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	builtin.SetFeedbackHome(f.inbox)
	SetFeedbackInboxDir(f.inbox)
	t.Cleanup(func() {
		builtin.SetFeedbackHome("")
		SetFeedbackInboxDir("")
		SetContactRegistryPath(func(mailDir string) string {
			return filepath.Join(mailDir, "contact-registry.json")
		})
	})
	f.cfg = FeedbackMailConfig{
		SessionDir:    f.sessDir,
		WorkspaceRoot: "",
		MailDir:       f.mailDir,
		CreateSession: func(req CreateCollabSessionRequest) (CreateCollabSessionResult, error) {
			f.creates++
			path := filepath.Join(f.sessDir, fmt.Sprintf("collab-%d.jsonl", f.creates))
			if err := os.WriteFile(path, nil, 0o644); err != nil {
				return CreateCollabSessionResult{}, err
			}
			contact, err := SetSessionDuty(path, req.Purpose, &SessionIdentity{
				Type: sessioncollabIdentitySystem(), Domain: "feedback",
			}, nil)
			if err != nil {
				return CreateCollabSessionResult{}, err
			}
			// 记下工厂铸的地址，测试据此断言收件人。
			f.minted = append(f.minted, contact)
			return CreateCollabSessionResult{
				ContactID:   contact,
				SessionPath: path,
			}, nil
		},
	}
	return f
}

// sessioncollabIdentitySystem keeps the import surface honest without a named
// import collision in this test file.
func sessioncollabIdentitySystem() string { return "system" }

// readRegistry loads the address registry (absent = empty map).
func (f *feedbackMailFixture) readRegistry(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(f.registryPath)
	if err != nil {
		return map[string]string{}
	}
	var reg struct {
		Contacts map[string]string `json:"contacts"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatalf("registry JSON: %v", err)
	}
	return reg.Contacts
}

// inboxMessages reads the fixed inbox contact's delivered mail.
func (f *feedbackMailFixture) inboxMessages(t *testing.T) []sessioncollabMail {
	t.Helper()
	mail := sessioncollabMailStore(f.mailDir)
	return mail
}

// 验收①③：一次提交 → 通知落到固定名收件人（mint-once）；第二次提交复用同一
// 地址，不再触发工厂（注册表钉住）。
func TestNotifyFeedbackNoteMintsOnceAndReuses(t *testing.T) {
	f := newFeedbackMailFixture(t)
	ctx := context.Background()

	if err := f.cfg.NotifyFeedbackNote(ctx, builtin.FeedbackNote{
		FileName: "feedback-20261020-100000-a.md", Kind: "bug", Title: "第一条", Summary: "摘要一",
	}); err != nil {
		t.Fatalf("first notify: %v", err)
	}
	if f.creates != 1 || len(f.minted) != 1 {
		t.Fatalf("factory calls after first notify = %d (minted=%v), want 1", f.creates, f.minted)
	}
	first := f.minted[0]
	contacts := f.readRegistry(t)
	if contacts[FeedbackInboxRegistryName] != first {
		t.Fatalf("registry must pin the minted address: %+v vs %q", contacts, first)
	}

	if err := f.cfg.NotifyFeedbackNote(ctx, builtin.FeedbackNote{
		FileName: "feedback-20261020-100100-b.md", Kind: "idea", Title: "第二条", Summary: "摘要二",
	}); err != nil {
		t.Fatalf("second notify: %v", err)
	}
	if f.creates != 1 {
		t.Fatalf("second notify must reuse the pinned address (factory calls = %d)", f.creates)
	}

	// 两封信都投到同一收件人；正文携带 md 文件名引用（幂等键）与来源戳。
	msgs := readInbox(t, f.mailDir, first)
	if len(msgs) != 2 {
		t.Fatalf("inbox messages = %d, want 2", len(msgs))
	}
	for i, m := range msgs {
		if m.To != first {
			t.Fatalf("msg %d not addressed to the fixed contact: %+v", i, m)
		}
		if m.Kind != "system" || m.Channel != "意见箱" {
			t.Fatalf("msg %d missing kind/channel stamps: %+v", i, m)
		}
	}
	if !strings.Contains(msgs[0].Body, "md: feedback-20261020-100000-a.md") ||
		!strings.Contains(msgs[1].Body, "md: feedback-20261020-100100-b.md") {
		t.Fatalf("bodies must cite their md file names: %+v", msgs)
	}
}

// 任务 1058：通知的 From 必须是真实铸造地址（与注册表/寻址同源），不得是硬编
// 码幽灵地址——收信方按此 From 回信必须真正可达。回归钉：把 From 换回硬编码
// 常量（历史 bug 形态），下面两条断言必须同时失败；常量已删除，改用等价字面量
// 复现同一 bug 形态。
func TestNotifyFeedbackNoteFromIsMintedAddressAndOneWay(t *testing.T) {
	f := newFeedbackMailFixture(t)
	if err := f.cfg.NotifyFeedbackNote(context.Background(), builtin.FeedbackNote{
		FileName: "feedback-20261022-105800-a.md", Kind: "bug", Title: "寻址回归", Summary: "摘要",
	}); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if len(f.minted) != 1 {
		t.Fatalf("factory must mint exactly one address: %v", f.minted)
	}
	inboxContact := f.minted[0]

	// 反例对照钉：修复前的 bug 形态（幽灵字面量冒充 From）必须被下面两条断言
	// 捕获——From 改回不可寻址的常量时，第一条（From==minted）立刻失败。
	const ghost = "sc_feedback_inbox" // 历史 bug 的常量值，仅用于对照，非当前实现
	if ghost == inboxContact {
		t.Fatalf("test premise broken: the minted address must never equal the old ghost literal")
	}

	msgs := readInbox(t, f.mailDir, inboxContact)
	if len(msgs) != 1 {
		t.Fatalf("inbox messages = %d, want 1", len(msgs))
	}
	m := msgs[0]
	if m.From != inboxContact {
		t.Fatalf("From must be the minted address (same source as the registry/resolve path), got %q want %q", m.From, inboxContact)
	}
	if !strings.Contains(m.Body, FeedbackInboxOneWayNotice) {
		t.Fatalf("body must carry the explicit one-way notice (tl 1058): %s", m.Body)
	}

	// 可寻址性：收信方按 From 回信必须真的能投递成功——目录扫描（talk_to_
	// session 的寻址同一条路径）必须能按该 From 解析出会话，不是仅字符串断言。
	ids := scanAddressable(f.sessDir, "")
	found := false
	for _, id := range ids {
		if id.ContactID == m.From {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("From %q must be resolvable in the contact directory scan: %+v", m.From, ids)
	}
}

// 无工厂宿主（CLI/serve）：未钉住地址时通知报错但不影响 md（Execute 层契约在
// builtin 测试里；这里断言错误信息可辨识且不落盘半成品注册表）。
func TestNotifyWithoutFactoryRefusesCleanly(t *testing.T) {
	f := newFeedbackMailFixture(t)
	f.cfg.CreateSession = nil
	err := f.cfg.NotifyFeedbackNote(context.Background(), builtin.FeedbackNote{FileName: "feedback-x.md"})
	if err == nil || !strings.Contains(err.Error(), "cannot create sessions") {
		t.Fatalf("want a descriptive factory-missing error, got %v", err)
	}
	if _, statErr := os.Stat(f.registryPath); !os.IsNotExist(statErr) {
		t.Fatalf("no registry file must be written on refusal: %v", statErr)
	}
}

// 兜底扫描：缺 notified 标记的 md 补投通知并打标；重跑不重复投（幂等）；已有
// 标记的 md 跳过。这是「消息丢失也能最终一致」的可测面。
func TestBackfillFeedbackNotificationsIdempotent(t *testing.T) {
	f := newFeedbackMailFixture(t)
	builtin.SetFeedbackHome(f.inbox)
	// 预置两条 md（不经过 Execute，直接走权威源写入接口）。
	if _, err := builtin.AppendFeedbackEntry(builtin.FeedbackEntry{Kind: "bug", Text: "甲问题"}); err != nil {
		t.Fatal(err)
	}
	if _, err := builtin.AppendFeedbackEntry(builtin.FeedbackEntry{Kind: "idea", Text: "乙建议"}); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	res, err := f.cfg.BackfillFeedbackNotifications(ctx)
	if err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	if res.Delivered != 2 || res.Errors != 0 {
		t.Fatalf("first backfill = %+v, want delivered=2 errors=0", res)
	}
	if f.creates != 1 || len(f.minted) != 1 {
		t.Fatalf("backfill must mint exactly one inbox session: creates=%d minted=%v", f.creates, f.minted)
	}
	if got := len(readInbox(t, f.mailDir, f.minted[0])); got != 2 {
		t.Fatalf("inbox after first backfill = %d, want 2", got)
	}
	// 打标后 md 带 notified 行（权威源上的收据）。
	stamped := 0
	pairs, perr := FeedbackEntriesWithFiles()
	if perr != nil {
		t.Fatal(perr)
	}
	for _, p := range pairs {
		raw, _ := os.ReadFile(p.Path)
		if strings.Contains(string(raw), FeedbackNotifiedMarker+":") {
			stamped++
		}
	}
	if stamped != len(pairs) || len(pairs) != 2 {
		t.Fatalf("stamped=%d pairs=%d, want both stamped", stamped, len(pairs))
	}

	// 重跑：无新增投递（幂等）。
	res2, err := f.cfg.BackfillFeedbackNotifications(ctx)
	if err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if res2.Delivered != 0 || res2.Errors != 0 {
		t.Fatalf("second backfill = %+v, want a no-op", res2)
	}
	if got := len(readInbox(t, f.mailDir, f.minted[0])); got != 2 {
		t.Fatalf("inbox after idempotent rerun = %d, want 2", got)
	}
}

// 注册表读写：转线语义的最小可测面——重写 name 后下一次通知走新地址。
func TestRegistryRepointOnTransfer(t *testing.T) {
	f := newFeedbackMailFixture(t)
	ctx := context.Background()
	if err := f.cfg.NotifyFeedbackNote(ctx, builtin.FeedbackNote{FileName: "a.md", Title: "T", Kind: "bug"}); err != nil {
		t.Fatal(err)
	}
	// 模拟转线：新会话文件落到可被目录扫描发现的 .jsonl（扫不到 ⇒ 会按注册表
	// 的地址重铸，那是另一个契约），拿到新地址后把注册表改指它。
	newPath := filepath.Join(f.sessDir, "collab-moved.jsonl")
	if err := os.WriteFile(newPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	newContact, err := SetSessionDuty(newPath, FeedbackInboxPurpose, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := registerContactByName(f.mailDir, FeedbackInboxRegistryName, newContact); err != nil {
		t.Fatal(err)
	}
	createsBefore := f.creates
	if err := f.cfg.NotifyFeedbackNote(ctx, builtin.FeedbackNote{FileName: "b.md", Title: "T2", Kind: "idea"}); err != nil {
		t.Fatalf("notify after repoint: %v", err)
	}
	if f.creates != createsBefore {
		t.Fatalf("repointed notify must not mint a new session (creates=%d)", f.creates)
	}
	msgs := readInbox(t, f.mailDir, newContact)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Body, "md: b.md") {
		t.Fatalf("repointed mail must land on the new address: %+v", msgs)
	}
}

// ---- helpers ----

// sessioncollabMail is a local alias so the test reads messages without a
// second import line clashing with the fixture's own use.
type sessioncollabMail = struct {
	ID          string `json:"id"`
	From        string `json:"fromContactId,omitempty"`
	To          string `json:"toContactId"`
	Body        string `json:"body"`
	Delivery    string `json:"delivery,omitempty"`
	Kind        string `json:"kind,omitempty"`
	Channel     string `json:"channel,omitempty"`
	ToTitle     string `json:"toTitle,omitempty"`
	At          int64  `json:"at"`
	ThreadID    string `json:"threadId,omitempty"`
}

func sessioncollabMailStore(mailDir string) []sessioncollabMail { return nil }

// readInbox decodes a contact's inbox.jsonl directly (the transport contract:
// one JSON message per line, oldest first).
func readInbox(t *testing.T, mailDir, contactID string) []sessioncollabMail {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(mailDir, contactID+".inbox.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []sessioncollabMail
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m sessioncollabMail
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("inbox line: %v (%s)", err, line)
		}
		out = append(out, m)
	}
	return out
}

// silence unused-import guards for config (the production code imports it; the
// test only needs the package to link).
var _ = config.ReasonixHomeDir

// collabinbox is referenced for its bucket constants in assertions.
var _ = collabinbox.BucketSystem
