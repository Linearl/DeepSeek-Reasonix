package main

// 任务894：跨会话回执分级通道——无需行动静默化（返回值+UI 通知层，不注入
// 模型），需行动保留注入。本文件钉三条硬验收：
//
//	① 无需行动类（steer 降级）零模型注入、UI 通知层恰好一条（TestSilentSteerNoticeSurfacesOnSenderTabNotModel）；
//	② 需行动类（refused_*/delivery_failed）保留注入、绝不进静默通道（TestActionOutcomesNeverReachSilentChannel，与既有族测试互证）；
//	③ 反向用例：需行动结局误入静默通道必须拦截——回退注入+大声告警（TestSilentChannelMisrouteFallsBackToInjection）。
//
// 硬约束：talk_to_session 全程非阻塞（本文件全部在泵侧结算点验证，没有任何
// 同步等泵路径）；570 留痕通道不动（回执存储断言见 session_collab_receipt_test.go）。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/control"
	"reasonix/internal/event"
	"reasonix/internal/sessioncollab"
)

// collab894Ctrl 只覆写 sessionCollabLiveTargets 会摸到的 SessionAPI 面
// （SessionPath），其余落在嵌入的 nil 接口上，绝不能被触达。
type collab894Ctrl struct {
	control.SessionAPI
	sessionPath string
}

func (c *collab894Ctrl) SessionPath() string { return c.sessionPath }

// collab894EventRecorder 接住 tab sink 转发的事件（botSink 面）。
type collab894EventRecorder struct {
	events []event.Event
}

func (r *collab894EventRecorder) Emit(e event.Event) { r.events = append(r.events, e) }

// ① UI 通知层断言：无需行动的降级通知进发送方 tab 的 Notice 事件（人可见），
// 且发送方信箱零落盘（模型注入源为空——信箱里的系统消息正是旧注入通道）。
func TestSilentSteerNoticeSurfacesOnSenderTabNotModel(t *testing.T) {
	// 隔离信箱根：本测断言「没有写」，任何误写都必须落临时目录而非真目录。
	setupIsolatedCollabMail(t)
	path := filepath.Join(t.TempDir(), "sessions", "sender.jsonl")
	contact, err := agent.EnsureContactID(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := &collab894EventRecorder{}
	app := &App{tabs: map[string]*WorkspaceTab{
		"tab_sender": {ID: "tab_sender", Ctrl: &collab894Ctrl{sessionPath: path}, sink: &tabEventSink{tabID: "tab_sender", botSink: rec}},
	}}
	p := &sessionCollabPump{app: app}
	msg := sessioncollab.MailMessage{ID: "msg_s1", From: contact, To: "sc_target", Delivery: "steer"}
	p.silenceSenderNotice(msg, "steer_degraded", sessioncollab.ReceiptQueuedFollowup,
		"你发送的 steer 已降级为排队 follow-up。（messageId=msg_s1）")

	var notices []event.Event
	for _, e := range rec.events {
		if e.Kind == event.Notice {
			notices = append(notices, e)
		}
	}
	if len(notices) != 1 {
		t.Fatalf("静默通知必须恰好产生一条 UI Notice 事件，got %d", len(notices))
	}
	if notices[0].Code != event.NoticeCodeCollabDeliverySilent {
		t.Fatalf("UI Notice 必须携带分级通道专用码，got %q", notices[0].Code)
	}
	if !strings.Contains(notices[0].Text, "msg_s1") {
		t.Fatalf("UI Notice 正文必须携带 messageId（排障链），got %q", notices[0].Text)
	}

	// 模型零注入：发送方信箱不得有任何信（旧通道正是往这里 Deliver 系统消息）。
	mail := sessioncollab.NewMailStore(config.SessionCollabMailDir())
	inbox, err := mail.InboxMessages(contact)
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 0 {
		t.Fatalf("静默通道不得向发送方信箱写任何东西（=模型注入源），got %d: %+v", len(inbox), inbox)
	}
}

// ③ 反向用例（最关键）：需行动结局（refused）被误送进静默通道时，生产守卫
// 必须拦截——回退到注入通道把通知送达发送方，宁可多一个轮次也不漏行动。
func TestSilentChannelMisrouteFallsBackToInjection(t *testing.T) {
	mailDir := setupIsolatedCollabMail(t)
	p := &sessionCollabPump{} // app=nil：守卫在无 UI 面时同样生效
	msg := sessioncollab.MailMessage{ID: "msg_m1", From: "sc_from", To: "sc_target"}
	p.silenceSenderNotice(msg, "steer_degraded", sessioncollab.ReceiptRefusedHop,
		"消息被拒并丢弃，需要你处理。（messageId=msg_m1）")

	inbox, err := sessioncollab.NewMailStore(mailDir).InboxMessages("sc_from")
	if err != nil {
		t.Fatal(err)
	}
	if len(inbox) != 1 || !strings.Contains(inbox[0].Body, "messageId=msg_m1") {
		t.Fatalf("误入静默的需行动回执必须回退注入通道送达发送方（漏行动拦截），got %+v", inbox)
	}
}

// ② 需行动类零变化：refused（hop 超限）与 delivery_failed（投递失败将重试）
// 在真实投递链上保留注入通道，静默通道对它们零调用。
func TestActionOutcomesNeverReachSilentChannel(t *testing.T) {
	mail, target := newCollabTestMail(t)
	// 一条正常消息先落盘（顺带建信箱目录），hop 超限的存量记录随后直写。
	// delivery=followup：正常落点不产生任何降级通知，让静默通道保持真空。
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_from", To: target, Body: "ok", Delivery: "followup"}); err != nil {
		t.Fatal(err)
	}
	// hop 超限的存量记录（经 Claim 的 rejected 列表）→ refused_hop。
	if err := appendRawLine(mail.InboxPath(target),
		`{"id":"msg_h1","fromContactId":"sc_from","toContactId":"`+target+`","body":"too deep","hop":99}`); err != nil {
		t.Fatal(err)
	}
	// 普通消息 enqueue 失败 → delivery_failed（未送达，将重试）。
	if _, err := mail.Deliver(context.Background(), sessioncollab.MailMessage{From: "sc_from", To: target, Body: "boom", Delivery: "followup"}); err != nil {
		t.Fatal(err)
	}
	d, _, notices, silentNotices := collabTestDelivery(t, mail, func(msg sessioncollab.MailMessage) error {
		if msg.Body == "boom" {
			return errors.New("target wedged")
		}
		return nil
	})
	delivered, refused, err := runCollabDelivery(mail, target, d)
	if err == nil || delivered != 1 || refused != 1 {
		t.Fatalf("want 1 delivered + 1 refused + 失败上浮: %d/%d err=%v", delivered, refused, err)
	}
	if len(*silentNotices) != 0 {
		t.Fatalf("需行动结局绝不得进静默通道（漏行动），got %v", *silentNotices)
	}
	if len(*notices) != 2 {
		t.Fatalf("refused 与 delivery_failed 都必须保留注入通道，got %d: %v", len(*notices), *notices)
	}
}
