package main

import (
	"errors"
	"os"
	"strings"

	"reasonix/internal/agent"
)

// tl 1266 — 装机重启后会话 runtime 未重站且 UI 无感知：tab 挂 running 残影
// （绿点+已工作计数）但 contact directory 查无实体，「继续」把消息塞进已死
// 执行体（20261030 用户实锤：1263 会话建于旧 runtime，装包重启销毁全部
// runtime 后未重站，status unmatched → contact directory not found 两级查无，
// tab 残留重启前 running 态，点继续两次无反应）。
//
// 会话实体的本机事实源 = 会话 .jsonl 文件本身。agent 侧 contact directory
// （collabRoster → sessioncollab.ScanDir）枚举的就是这些文件（global、各
// project sessions 目录、archive），桌面侧一切「这个会话还存在吗」的判断与它
// 同一语义：路径指向的文件在盘上 = 实体在；查无 = 断开。不另立真相源。
//
// 查无（文件被删/清理待执行/来自已销毁旧实例且从未落盘）时：
//   - 方向②：tab 不得再对 running/待确认/后台作业 投绿点残影（tabMeta 投影）；
//   - 方向①：重启恢复的骨架 tab 直接以「已断开」可见态示人，而不是装作
//     一切正常的 starting（tabMeta 投影；点击后的重建路径保持任务 619 惰性
//     契约不变——本轮只修 UI 真相，不改重建语义）；
//   - 方向③：「继续/发送」受理链前置校验实体存在，查无时返回明确报错，
//     而不是让 ErrTurnRunning 降级把消息排进已死执行体的队列。
//
// 全部为读取型判定：不改任何 tab/runtime 注册表状态（持久化、唤醒、收件箱
// 语义不动），实体重新出现（网络盘恢复、用户撤销删除）时投影在下次
// ListTabs 刷新自愈。

// errSessionDisconnected 是查无会话实体的哨兵错误：受理链原样透传给前端。
// 前端降级器只认「turn already running」，本错误走普通发送失败面
// （Send failed + 原文案），不会把消息降级进已死执行体的队列。
var errSessionDisconnected = errors.New(sessionDisconnectedUserMessage)

// sessionDisconnectedUserMessage 是面向用户的断开文案（任务书口径：
// 「会话已断开请重新打开」），tab 徽章/提交报错共用同一段。
const sessionDisconnectedUserMessage = "会话已断开，请重新打开：该会话的对话实体已不存在（可能已被删除，或来自已结束的运行实例）"

// sessionMissingRuntimeIssueCode 是 tab 条 runtime issue 的 grep 锚：前端
// TabBar 徽章按它渲染「已断开」，与通用启动失败（startup_failed）和租户
// 占用（session_lease_held）区分。
const sessionMissingRuntimeIssueCode = "session_missing"

// sessionEntityExists 报告会话实体（.jsonl）是否仍在盘上——与 contact
// directory 的枚举对象同一文件集。语义边界：
//   - 空路径 = 尚未落盘的新会话（首轮发送前），视为存在——没有实体可断，
//     不得对新建会话误报断开；
//   - 清理待执行（IsCleanupPending，延迟删除标记在案）= 实体正在被删除，
//     恢复/名册面（IsVisibleSession）已把它当不可见，这里同口径按查无。
func sessionEntityExists(sessionPath string) bool {
	path := strings.TrimSpace(sessionPath)
	if path == "" {
		return true
	}
	if agent.IsCleanupPending(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// tabSessionEntityMissing 判定一个 tab 声称延续的会话实体是否查无。只对
// 声明过会话路径的 tab 生效（新会话骨架没有实体可查）。
func tabSessionEntityMissing(sessionPath string) bool {
	return strings.TrimSpace(sessionPath) != "" && !sessionEntityExists(sessionPath)
}

// sessionMissingRuntimeIssue 构造「已断开」issue 视图。retryable=false：
// 实体查无不是重试能解决的，出路是用户从历史重新打开会话。
func sessionMissingRuntimeIssue() *SessionRuntimeIssue {
	return &SessionRuntimeIssue{
		Code:      sessionMissingRuntimeIssueCode,
		Message:   sessionDisconnectedUserMessage,
		Retryable: false,
	}
}

// sessionEntityAdmissionErrForTab 是受理链的前置实体校验（方向③）：tab 的
// 当前会话路径查无实体时返回断开哨兵错误。调用方已把 tab 解析出来（与
// tabIsReadOnly / workspaceRuntimeAdmissionErr 同一批前置检查）；路径在锁内
// 取快照，Stat 在锁外做（元数据操作，不持 App.mu 做 I/O）。控制器不存在或
// 只读 tab 不在此拦——它们各自已有更精确的既有报错（workspaceNotReady /
// readOnly），本守卫只负责「控制器活着但实体没了」与「骨架声称延续的实体
// 没了」这两种查无。
func (a *App) sessionEntityAdmissionErrForTab(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	sessionPath := tab.currentSessionPath()
	a.mu.RUnlock()
	if tabSessionEntityMissing(sessionPath) {
		return errSessionDisconnected
	}
	return nil
}
