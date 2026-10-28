package sessioncollab

import "sync"

// 投递侧广播缝（tl1226 遗留①「新信到达角标即涨」）：MailStore.Deliver 每把
// 一封新信成功落盘（appendJSONL 成功），就通知一次注册过的观察者。desktop 层
// 借此把投递事件转成 Wails 运行时事件（CollabMailChanged），前端未读角标
// hook 订阅后即时重查 CountUnreadCollabMail——通知通路，不引轮询；计数真相
// 源仍是 inbox.jsonl + seen 游标，这里不造第二份计数。
//
// 语义边界：
//   - 只在「真正落了新行」时通知。内容幂等（任务461 P8 ①）的重发窗口内返原
//     信不落新行，未读数没有变化，因此不通知。
//   - 投递失败（校验拒绝/写盘失败）不通知。
//   - 观察者在各自 goroutine 里执行且 panic 被吞掉（记日志层面交给观察者自
//     己）：投递路径持有 mail 锁，绝不能被慢观察者或观察者异常拖住。
//   - 观察者拿到的 mailDir 是本次投递写入的信箱根目录；desktop 层用它过滤
//     「是不是本应用信箱」的投递。

var (
	mailDeliveryMu        sync.Mutex
	mailDeliveryNextID    int
	mailDeliveryObservers = map[int]func(msg MailMessage, mailDir string){}
)

// OnMailDelivered registers a delivery observer and returns its cancel func.
// Nil observers are accepted and dropped (callers can wire unconditionally).
func OnMailDelivered(fn func(msg MailMessage, mailDir string)) (cancel func()) {
	if fn == nil {
		return func() {}
	}
	mailDeliveryMu.Lock()
	defer mailDeliveryMu.Unlock()
	id := mailDeliveryNextID
	mailDeliveryNextID++
	mailDeliveryObservers[id] = fn
	return func() {
		mailDeliveryMu.Lock()
		defer mailDeliveryMu.Unlock()
		delete(mailDeliveryObservers, id)
	}
}

// notifyMailDelivered fans one delivery out to every live observer, each on
// its own goroutine so a slow or panicking observer can never stall the mail
// lock the caller is still holding.
func notifyMailDelivered(mailDir string, msg MailMessage) {
	mailDeliveryMu.Lock()
	observers := make([]func(msg MailMessage, mailDir string), 0, len(mailDeliveryObservers))
	for _, fn := range mailDeliveryObservers {
		observers = append(observers, fn)
	}
	mailDeliveryMu.Unlock()
	for _, fn := range observers {
		go func(fn func(msg MailMessage, mailDir string), msg MailMessage, mailDir string) {
			// An observer crash must never take the delivery (or the process)
			// down: the notify is a best-effort side channel by contract.
			defer func() { _ = recover() }()
			fn(msg, mailDir)
		}(fn, msg, mailDir)
	}
}
