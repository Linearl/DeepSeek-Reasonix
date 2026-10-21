package collabinbox

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/sessioncollab"
)

// 任务 1016：意见箱通知条目携带 refPath（指向权威 md），面板据此渲染「打开
// 来源」。三条契约：①通知条目（`md: <filename>` 行）解析出绝对路径；②非
// 通知正文（无该行）与路径形态的 md 引用（含分隔符）都不产生 refPath——
// 防止任意信件意外暴露文件引用；③未注入目录时（默认）refPath 恒为空。
func TestFeedbackNotificationCarriesRefPath(t *testing.T) {
	root := t.TempDir()
	SetFeedbackRefPathRoot(root)
	t.Cleanup(func() { SetFeedbackRefPathRoot("") })

	s, mail := fixtureStore(t)
	now := s.now()
	notice := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_feedback_inbox", To: "sc_main", Kind: "system", Channel: "意见箱",
		Body: "[意见箱] 新反馈（bug）：快捷键冲突\nmd: feedback-20261020-093000-shortcut.md\n按下 Ctrl+P 时面板无响应",
		At:   now,
	})
	plain := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "点对点派活", At: now - 1000,
	})
	pathLike := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_alice", To: "sc_main", Body: "md: some/nested/path.md（路径形态不算引用）", At: now - 2000,
	})

	snap, err := s.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]Entry{}
	for _, e := range snap.Entries {
		byID[e.ID] = e
	}
	want := filepath.Join(root, "feedback-20261020-093000-shortcut.md")
	if got := byID[notice.ID].RefPath; got != want {
		t.Fatalf("notice refPath: got %q want %q", got, want)
	}
	if byID[plain.ID].RefPath != "" {
		t.Fatalf("point-to-point entry must carry no refPath: %+v", byID[plain.ID])
	}
	if byID[pathLike.ID].RefPath != "" {
		t.Fatalf("path-shaped md line must not become a refPath: %+v", byID[pathLike.ID])
	}

	// 详情读口同样携带（弹窗渲染「打开来源」）。
	detail, err := s.ReadMail(context.Background(), notice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.RefPath != want {
		t.Fatalf("detail refPath: got %q want %q", detail.RefPath, want)
	}

	// 链视图的链首条目同样携带。
	chains, err := s.Chains(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range chains.Chains {
		if c.ThreadID == notice.ThreadID {
			found = true
			if c.RefPath != want {
				t.Fatalf("chain refPath: got %q want %q", c.RefPath, want)
			}
		}
	}
	if !found {
		t.Fatalf("notice chain missing from chain view: %+v", chains.Chains)
	}
}

// 未注入目录时 refPath 恒为空（默认零副作用契约）。
func TestRefPathEmptyWithoutRoot(t *testing.T) {
	SetFeedbackRefPathRoot("")
	s, mail := fixtureStore(t)
	m := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_feedback_inbox", To: "sc_main",
		Body: "[意见箱] md: feedback-x.md", At: s.now(),
	})
	snap, err := s.List(context.Background(), Query{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range snap.Entries {
		if e.ID == m.ID && e.RefPath != "" {
			t.Fatalf("refPath must stay empty without an injected root: %+v", e)
		}
	}
}

// 意见箱通知必须归入 system 桶（显式 Kind 戳优先于一切回退规则）——
// 信件中心的「意见箱来源」标签与桶过滤依赖该分类。
func TestFeedbackNoticeClassifiesSystemBucket(t *testing.T) {
	s, mail := fixtureStore(t)
	m := deliver(t, mail, sessioncollab.MailMessage{
		From: "sc_feedback_inbox", To: "sc_main", Kind: "system", Channel: "意见箱",
		Body: "[意见箱] md: feedback-y.md", At: s.now(),
	})
	snap, err := s.List(context.Background(), Query{Bucket: BucketSystem}, false)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Total != 1 || snap.Entries[0].ID != m.ID {
		t.Fatalf("notice must land in the system bucket: %+v", snap.Entries)
	}
	if !strings.HasPrefix(snap.Entries[0].Preview, "[意见箱]") {
		t.Fatalf("preview must carry the stamp line: %+v", snap.Entries[0])
	}
}
