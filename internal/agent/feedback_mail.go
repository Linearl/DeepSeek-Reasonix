package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool/builtin"
)

// 任务 1016 —— 意见箱并入信件中心的采集层落地（P2 批次）。
//
// 双写契约（用户 2026-10-19 拍板）：md 文件是权威源（submit_feedback 已经
// 写盘），本文件负责「加投递的另一半」——向意见箱会话投递一条通知消息，
// 引用 md 文件名 + ≤3 行摘要。分工边界：
//
//   - 通知失败绝不影响提交结果（md 已落盘即提交成立）；返回体如实标注。
//   - 大内容（截图/日志）只落 md，消息不携带——规避单条消息体积上限。
//   - 兜底：意见箱会话的定期扫描（后续批次）按 md 补齐，不依赖消息送达；
//     Deliver 层的重发窗口 + 消息内 md 文件名引用保证重复投递不重复建任务。
//
// 固定名会话（意见箱实体）：会话标题固定「意见箱」，contactId 由宿主工厂
// 首次创建时铸造（随机 sc_ 前缀地址），随后钉进地址注册表
// （<mailDir>/contact-registry.json 的 name → contactId 映射）。转线（归档
// 旧会话 + 新建同名）时只更新注册表——收件方寻址经注册表解析出的地址，
// 不会因标题寻址撞上「已归档」的同名旧会话。
//
// 任务 1058（2026-10-22 实测缺口）：通知的 From 字段曾被硬编码成一个固定
// 字面量 "sc_feedback_inbox"（幽灵地址，从未真正铸造）——收信方拿到的通知
// 模板因此指示「回信到 contact_id=sc_feedback_inbox」，但该 id 从来不在通
// 讯录里（实际铸造的是工厂返回的随机地址），收信方 talk_to_session 报
// not found，无法回执（20261022 意见箱线实测，tl 1058）。根因是「发件方身
// 份」与「寻址可达地址」在代码里被拆成了两个不一致的值：From 写死锚点字面
// 量，寻址经注册表拿真实铸造地址。修复：From 必须是真实铸造地址
// （ensureInbox 返回的 contactID），与注册表/寻址完全同源；「sc_feedback_
// inbox」这个字面量从代码里彻底删除，不再冒充一个可寻址地址。
const (
	// FeedbackInboxTitle is the fixed session title (转线时新会话沿用同名，
	// 旧会话改名「意见箱（已归档）」保持标题唯一).
	FeedbackInboxTitle = "意见箱"
	// FeedbackInboxIdentityType registers the inbox as a system-layer entity
	// (任务 348 五值枚举): the mail-center resolver then classifies its mail
	// into the system bucket without a body sniff.
	FeedbackInboxIdentityType = sessioncollab.IdentitySystem
	// FeedbackInboxPurpose is the one-line duty stamped on the session.
	FeedbackInboxPurpose = "意见箱：收信→分析/查重→建或更新任务→归档 md→按 894 分级回执（任务 1016）"
	// FeedbackInboxKind stamps the notification mail so the mail-center index
	// classifies it into the system bucket explicitly (an explicit Kind stamp
	// outranks every fallback rule in collabinbox.classify).
	FeedbackInboxKind = "system"
	// FeedbackInboxRegistryName is the registry key for the inbox entry.
	FeedbackInboxRegistryName = "feedback-inbox"
	// FeedbackInboxGroup is the sidebar group the fixed-name session is filed
	// under (created on demand by the host's create path).
	FeedbackInboxGroup = "意见箱"
	// FeedbackInboxChannel is the provenance stamp the mail-center panel
	// renders as the 来源频道 tag (信件中心来源标签, 1016 增强项 ①) — the same
	// Channel field the task-349 group fan-out uses, presented the same way.
	FeedbackInboxChannel = "意见箱"
)

// FeedbackInboxSystemStamp is embedded in the first line of every notification
// body so the mail preview itself is recognizable even in a raw jsonl read.
const FeedbackInboxSystemStamp = "[意见箱]"

// FeedbackInboxOneWayNotice (任务 1058) is appended to every notification
// body. It tells the recipient, in plain text, that no reply is expected and
// the message cannot be replied to — closing the loop the moment the generic
// delivery template (sessionCollabDeliveryText / drainInboxRenderText) would
// otherwise offer a "reply to the sender" fallback. Without this stamp, a
// system-identity sender that never registers a ReplyTo reads as an ordinary
// "replyable" peer: the generic template falls back to the sender's contact_id
// as the reply address, and the recipient burns a turn discovering that
// address is unreachable (tl 1058 root cause: the recipient tried to reply to
// "sc_feedback_inbox" and got not-found).
const FeedbackInboxOneWayNotice = "本条为通知，无需回信（发送方是意见箱系统入口，不是可对话会话）。"

// feedbackInboxBody renders the notification message body: the stamp line, the
// md file name (the idempotent key), the ≤3-line summary, and (任务1058) an
// explicit one-way notice so a reader never wastes a turn hunting for a reply
// address.
func feedbackInboxBody(note builtin.FeedbackNote) string {
	var b strings.Builder
	b.WriteString(FeedbackInboxSystemStamp + " ")
	b.WriteString("新反馈（" + note.Kind + "）：" + note.Title + "\n")
	fmt.Fprintf(&b, "md: %s\n", note.FileName)
	if s := strings.TrimSpace(note.Summary); s != "" {
		b.WriteString(s)
		b.WriteString("\n")
	}
	b.WriteString(FeedbackInboxOneWayNotice)
	return b.String()
}

// ---- 地址注册表（name → contactId，1016 处理层） ----

// contactRegistry is the on-disk name → contactId map living beside the mail
// directory. JSON so a hand-inspected file stays readable; absent file = only
// the fixed anchor entry exists.
type contactRegistry struct {
	Contacts map[string]string `json:"contacts"`
}

// contactRegistryPath resolves the registry file (overridable for tests).
var contactRegistryPath = func(mailDir string) string {
	return filepath.Join(mailDir, "contact-registry.json")
}

// SetContactRegistryPath overrides the registry file location (tests only).
func SetContactRegistryPath(fn func(mailDir string) string) {
	contactRegistryPath = fn
}

// resolveContactByName reads the registry and answers the contactId registered
// for name ("" when absent). A corrupt registry is treated as absent — the
// fixed anchor still works, and the next write repairs the file.
func resolveContactByName(mailDir, name string) string {
	raw, err := os.ReadFile(contactRegistryPath(mailDir))
	if err != nil {
		return ""
	}
	var reg contactRegistry
	if json.Unmarshal(raw, &reg) != nil {
		return ""
	}
	return strings.TrimSpace(reg.Contacts[name])
}

// registerContactByName atomically writes name → contactId in the registry.
func registerContactByName(mailDir, name, contactID string) error {
	path := contactRegistryPath(mailDir)
	reg := contactRegistry{Contacts: map[string]string{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &reg)
	}
	if reg.Contacts == nil {
		reg.Contacts = map[string]string{}
	}
	reg.Contacts[name] = contactID
	raw, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---- 意见箱会话（固定名实体，1016 处理层） ----

// FeedbackInboxDirOverride lets the host pin the feedback-inbox directory for
// the ensure/scan paths (tests). Empty = builtin's own resolution (which
// honours SetFeedbackHome / REASONIX_FEEDBACK_INBOX / ReasonixHome).
var feedbackInboxDirMu sync.RWMutex

var feedbackInboxDirOverride string

// SetFeedbackInboxDir overrides the feedback-inbox dir used by the ensure and
// backfill paths (tests only).
func SetFeedbackInboxDir(dir string) {
	feedbackInboxDirMu.Lock()
	defer feedbackInboxDirMu.Unlock()
	feedbackInboxDirOverride = dir
}

func feedbackInboxDir() string {
	feedbackInboxDirMu.RLock()
	override := feedbackInboxDirOverride
	feedbackInboxDirMu.RUnlock()
	if d := strings.TrimSpace(override); d != "" {
		return d
	}
	if d, err := builtin.FeedbackInboxDir(); err == nil {
		return d
	}
	return filepath.Join(config.ReasonixHomeDir(), "feedback-inbox")
}

// feedbackInboxArchivedDir is where a consumed note's md moves to (task 1016
// 处理层: 归档 md). The name matches the documented layout.
func feedbackInboxArchivedDir() string { return filepath.Join(feedbackInboxDir(), "archived") }

// FeedbackEntriesWithFiles pairs every inbox note with its md base name — the
// dual-write consistency surface (验收②): the message cites exactly this name,
// so the pairing is derived from the same directory the submission wrote to.
func FeedbackEntriesWithFiles() ([]FeedbackEntryFile, error) {
	dir := feedbackInboxDir()
	entries, err := builtin.ListFeedbackEntries(0)
	if err != nil {
		return nil, err
	}
	out := make([]FeedbackEntryFile, 0, len(entries))
	for _, e := range entries {
		name, path, ferr := feedbackFileFor(dir, e)
		if ferr != nil {
			continue // an unlocatable file cannot be paired; the note itself stays listed
		}
		out = append(out, FeedbackEntryFile{Entry: e, FileName: name, Path: path})
	}
	return out, nil
}

// FeedbackEntryFile is one inbox note plus the md file it lives in.
type FeedbackEntryFile struct {
	Entry    builtin.FeedbackEntry
	FileName string
	Path     string
}

// ---- 投递 ----

// FeedbackMailConfig is the minimal surface the notifier needs (kept separate
// from SessionCollabConfig so the desktop can construct it without a full
// collab tool config).
type FeedbackMailConfig struct {
	// SessionDir / WorkspaceRoot locate the addressable directory scan used by
	// the registry re-resolve path.
	SessionDir    string
	WorkspaceRoot string
	// MailDir overrides the shared collab mailbox root; empty uses config's.
	MailDir string
	// ResolveSession answers the addressable roster (contact → session path);
	// nil falls back to the directory scan over SessionDir (production shape:
	// the host injects its cached scan so each submission costs no rescan).
	ResolveSession func(contactID string) (sessionPath string, ok bool)
	// CreateSession creates the fixed-name inbox session on first use (the
	// create_collab_session implementation); nil makes the inbox un-creatable
	// (CLI/serve hosts without a session factory).
	CreateSession func(req CreateCollabSessionRequest) (CreateCollabSessionResult, error)
	// Now is the injectable clock (tests); nil = time.Now.
	Now func() time.Time
}

func (c FeedbackMailConfig) mailDirOrDefault() string {
	if d := strings.TrimSpace(c.MailDir); d != "" {
		return d
	}
	return config.SessionCollabMailDir()
}

// sessionDirOrDefault resolves the directory the addressable scan walks:
// an injected SessionDir first (the desktop's global sessions dir), else the
// caller's workspace sessions dir (CLI/serve per-workspace shape).
func (c FeedbackMailConfig) sessionDirOrDefault() string {
	if d := strings.TrimSpace(c.SessionDir); d != "" {
		return d
	}
	if strings.TrimSpace(c.WorkspaceRoot) != "" {
		return filepath.Join(c.WorkspaceRoot, "sessions")
	}
	return config.SessionDir()
}

// NotifyFeedbackNote is the builtin dual-write hook: deliver ONE notification
// message to the feedback inbox session (creating it on first use). The
// returned error never fails the submission — the md is already the
// authoritative record.
func (c FeedbackMailConfig) NotifyFeedbackNote(ctx context.Context, note builtin.FeedbackNote) (err error) {
	// defer 里不能对一个还没赋值的具名返回值调用 Error()（零值 nil 会 panic）；
	// 先落一个哨兵错误，函数体正常返回时覆盖它。
	err = errors.New("feedback inbox: notify did not complete")
	defer func() {
		if err != nil {
			slog.Warn("feedback: notification not delivered", "md", note.FileName, "err", err)
		}
	}()
	contactID, _, title, ferr := c.ensureInbox()
	if ferr != nil {
		err = ferr
		return err
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	mail := sessioncollab.NewMailStore(c.mailDirOrDefault())
	_, derr := mail.Deliver(ctx, sessioncollab.MailMessage{
		// 任务 1058：From 必须是真实铸造地址（与注册表/寻址同源），不再是硬
		// 编码的幽灵字面量——收信方按此 From 回信才真正可达。
		From:    contactID,
		To:      contactID,
		ToTitle: title,
		Body:    feedbackInboxBody(note),
		// 每条通知自带 md 文件名（body 里），Deliver 层的内容去重窗口因此
		// 永远不会把两条不同意见折叠成一条——幂等由「文件名引用」承担。
		Kind:    FeedbackInboxKind,
		Channel: FeedbackInboxChannel,
		At:      now().UnixMilli(),
	})
	if derr != nil {
		err = fmt.Errorf("feedback inbox: deliver notification: %w", derr)
		return err
	}
	slog.Info("feedback: notification delivered to inbox", "md", note.FileName, "contact", contactID)
	err = nil
	return nil
}

// sessionForContact resolves a contact's session path: injected resolver
// first, directory scan fallback.
func (c FeedbackMailConfig) sessionForContact(contactID string) (string, bool) {
	if c.ResolveSession != nil {
		return c.ResolveSession(contactID)
	}
	for _, id := range scanAddressable(c.sessionDirOrDefault(), c.WorkspaceRoot) {
		if id.ContactID == contactID {
			return id.SessionPath, true
		}
	}
	return "", false
}

// ensureInbox implements the mint-once contract behind NotifyFeedbackNote:
// registry pin first (the address of record), else create the fixed-name
// session, register its system identity, and pin it.
func (c FeedbackMailConfig) ensureInbox() (contactID, sessionPath, title string, err error) {
	mailDir := c.mailDirOrDefault()
	if mailDir == "" {
		return "", "", "", errors.New("feedback inbox: collaboration mailbox directory is unavailable")
	}
	if pinned := resolveContactByName(mailDir, FeedbackInboxRegistryName); pinned != "" {
		if path, ok := c.sessionForContact(pinned); ok {
			return pinned, path, FeedbackInboxTitle, nil
		}
	}
	if c.CreateSession == nil {
		return "", "", "", errors.New("feedback inbox: this host cannot create sessions (no session factory attached)")
	}
	result, cerr := c.CreateSession(CreateCollabSessionRequest{
		WorkspaceRoot: c.WorkspaceRoot,
		Title:         FeedbackInboxTitle,
		Purpose:       FeedbackInboxPurpose,
		Group:         FeedbackInboxGroup,
	})
	if cerr != nil {
		return "", "", "", fmt.Errorf("feedback inbox: create session: %w", cerr)
	}
	if result.ContactID == "" || result.SessionPath == "" {
		return "", "", "", errors.New("feedback inbox: host created no addressable session")
	}
	// 防御：工厂返回了地址但目录扫描看不见它 ⇒ 下一次通知会再铸一个地址，
	// 固定名会话的「mint-once」就被打破了。宁可明确报错（宿主接线缺口），也不
	// 静默铸第二个意见箱。
	if _, ok := c.sessionForContact(result.ContactID); !ok {
		return "", "", "", fmt.Errorf("feedback inbox: host created %s but the addressable scan cannot see it — the session must live in the scanned sessions directory (%s)", result.ContactID, c.sessionDirOrDefault())
	}
	if _, derr := SetSessionDuty(result.SessionPath, FeedbackInboxPurpose, &SessionIdentity{
		Type:   FeedbackInboxIdentityType,
		Domain: "feedback",
	}, nil); derr != nil {
		slog.Warn("feedback inbox: identity registration failed", "err", derr)
	}
	if rerr := registerContactByName(mailDir, FeedbackInboxRegistryName, result.ContactID); rerr != nil {
		return "", "", "", fmt.Errorf("feedback inbox: pin address in registry: %w", rerr)
	}
	return result.ContactID, result.SessionPath, FeedbackInboxTitle, nil
}

// ---- 兜底扫描（1016 可靠性兜底 ②） ----

// FeedbackBackfillResult reports one backfill pass.
type FeedbackBackfillResult struct {
	// Delivered counts notes that had no notification yet and got one.
	Delivered int
	// Errors counts notes whose delivery failed this pass (they stay
	// undelivered and are retried on the next pass — md is untouched).
	Errors int
}

// FeedbackNotifiedMarker is the front-matter key stamped onto a note's md once
// its notification has been delivered. Absent = never delivered (or delivery
// unconfirmed); the backfill pass treats it as pending. This is the md-side
// receipt that makes the scan idempotent and independent of mail delivery.
const FeedbackNotifiedMarker = "notified"

// BackfillFeedbackNotifications scans the feedback inbox for notes whose md
// lacks the notified marker and delivers their notification, then stamps the
// marker. Idempotent by construction: a re-run finds nothing to do; a mail
// delivery that later fails is retried on the next pass; a note already
// stamped is skipped even if its message was pruned. This is the "定期扫描
// 未处理项兜底补齐" half of 1016's reliability ruling — it never depends on
// message delivery having succeeded the first time.
func (c FeedbackMailConfig) BackfillFeedbackNotifications(ctx context.Context) (FeedbackBackfillResult, error) {
	pairs, err := FeedbackEntriesWithFiles()
	if err != nil {
		return FeedbackBackfillResult{}, err
	}
	var res FeedbackBackfillResult
	for _, p := range pairs {
		// The notified marker lives in the md front matter — a stamped note is
		// a settled note (skip). ListFeedbackEntries parses the front matter
		// but drops unknown keys, so read the raw file head here.
		if feedbackNotified(p.Path) {
			continue
		}
		if err := c.notifyEntry(ctx, p); err != nil {
			res.Errors++
			slog.Warn("feedback: backfill delivery failed", "title", p.Entry.Title, "err", err)
			continue
		}
		res.Delivered++
	}
	if res.Delivered > 0 {
		slog.Info("feedback: backfill delivered notifications", "count", res.Delivered, "errors", res.Errors)
	}
	return res, nil
}

// notifyEntry delivers one parsed entry's notification and stamps its md.
func (c FeedbackMailConfig) notifyEntry(ctx context.Context, p FeedbackEntryFile) error {
	if err := c.NotifyFeedbackNote(ctx, builtin.FeedbackNote{
		FileName: p.FileName,
		Kind:     p.Entry.Kind,
		Title:    p.Entry.Title,
		Summary:  feedbackSummaryOf(p.Entry.Text),
	}); err != nil {
		return err
	}
	return stampFeedbackNotified(p.Path)
}

// feedbackSummaryOf mirrors builtin's ≤3-line fold for a parsed entry's text.
func feedbackSummaryOf(text string) string {
	return builtin.FeedbackSummaryBounded(text, 3)
}

// feedbackFileFor locates the md file matching a parsed entry by scanning the
// inbox dir for a feedback-*.md whose first line of the body matches the
// entry title. Returns the base name and the absolute path.
func feedbackFileFor(dir string, e builtin.FeedbackEntry) (string, string, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return "", "", err
	}
	for _, fi := range files {
		name := fi.Name()
		if fi.IsDir() || !strings.HasPrefix(name, "feedback-") || !strings.HasSuffix(name, ".md") {
			continue
		}
		raw, rerr := os.ReadFile(filepath.Join(dir, name))
		if rerr != nil {
			continue
		}
		parsed := parseFeedbackHead(string(raw))
		if parsed == e.Title {
			return name, filepath.Join(dir, name), nil
		}
	}
	return "", "", fmt.Errorf("feedback backfill: no md matches title %q", e.Title)
}

// parseFeedbackHead extracts the "# title" heading from a note's md body —
// the first non-empty line after stripping the optional leading "#".
func parseFeedbackHead(raw string) string {
	body := raw
	if strings.HasPrefix(raw, "---\n") {
		if i := strings.Index(raw[4:], "\n---"); i >= 0 {
			body = raw[4+i+4:]
		}
	}
	body = strings.TrimSpace(body)
	body = strings.TrimPrefix(body, "#")
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// feedbackNotified reports whether the note's md already carries the notified
// marker (front-matter `notified: <RFC3339>`).
func feedbackNotified(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return true // unreadable ⇒ do not spam a re-delivery attempt loop
	}
	return strings.Contains(feedbackFrontMatter(string(raw)), FeedbackNotifiedMarker+":")
}

// feedbackFrontMatter returns the leading "--- ... ---" block ("" when absent).
func feedbackFrontMatter(raw string) string {
	if !strings.HasPrefix(raw, "---\n") {
		return ""
	}
	i := strings.Index(raw[4:], "\n---")
	if i < 0 {
		return ""
	}
	return raw[:4+i+4]
}

// stampFeedbackNotified inserts the notified marker into a note's front
// matter. It rewrites only the front-matter block (temp file + rename), never
// touching the body.
func stampFeedbackNotified(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if strings.Contains(feedbackFrontMatter(string(raw)), FeedbackNotifiedMarker+":") {
		return nil
	}
	front := feedbackFrontMatter(string(raw))
	if front == "" {
		return fmt.Errorf("feedback backfill: %s has no front matter to stamp", filepath.Base(path))
	}
	// front 形如 "---\n...\n---"（末尾无换行），闭合线后的第一个字符就是换行；
	// 在 "---" 前插一行，其余字节原样保留。
	stamped := strings.TrimSuffix(front, "---") +
		FeedbackNotifiedMarker + ": " + time.Now().UTC().Format(time.RFC3339) + "\n---" + string(raw[len(front):])
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(stamped), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
