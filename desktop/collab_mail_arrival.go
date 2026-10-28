package main

import (
	"sync"

	"reasonix/internal/config"
	"reasonix/internal/sessioncollab"
)

// tl1226 遗留①（新信到达角标即涨）：把投递侧的 OnMailDelivered 观察者接到
// Wails 运行时事件 CollabMailChanged 上。前端未读角标 hook 订阅该事件即时重
// 查 CountUnreadCollabMail——「新信到达角标即涨」不靠轮询（hook 无轮询系原
// 设计），计数真相源仍是后端（inbox.jsonl + seen 游标），这里只补「后端投递
// → 前端刷新」的通知通路。
//
// 边界（如实声明）：观察者按 mailDir 过滤，只广播本应用信箱的投递；其他进程
// （CLI/serve/busworker）写同一信箱目录的投递不在本进程事件面内，这类新信仍
// 按既有刷新时机（挂载/面板开合/面板写广播）到达角标。
const collabMailChangedEvent = "CollabMailChanged"

var collabMailArrivalWatch sync.Once

// watchCollabMailArrivals registers the process-wide delivery observer (once).
// startup calls it after a.ctx is captured; the observer itself filters to this
// app's own mail directory before emitting.
func (a *App) watchCollabMailArrivals() {
	collabMailArrivalWatch.Do(func() {
		sessioncollab.OnMailDelivered(a.collabMailArrivalObserver())
	})
}

// collabMailArrivalObserver builds the emitting observer (extracted for tests:
// the event name/payload/dir-filter contract is pinned without the sync.Once).
func (a *App) collabMailArrivalObserver() func(msg sessioncollab.MailMessage, mailDir string) {
	return func(msg sessioncollab.MailMessage, mailDir string) {
		if a == nil || a.ctx == nil {
			return
		}
		if mailDir != config.SessionCollabMailDir() {
			return // 别处目录的投递与本窗口的信件角标无关
		}
		runtimeEventsEmitFallback(a.ctx, collabMailChangedEvent, map[string]string{
			"id":   msg.ID,
			"from": msg.From,
			"to":   msg.To,
		})
	}
}
