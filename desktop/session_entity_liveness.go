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
// 语义边界（为什么不能对「文件查无」一刀切）：本进程新建的会话在首轮发送
// 落盘前文件也不存在——对它误报断开会拦掉每一段新对话的第一条消息。因此
// 「查无」判定必须带来源：
//   - 重启恢复的 tab：restorePersistedTabs 在还原布局时对声明路径做一次
//     存在性判定，查无则把那条路径记入 tab.restoredMissingPath（那是唯一
//     知道「这条路径来自上一进程、文件该在而不在」的时刻），此后联合实时
//     Stat 生效且只对同一路径生效；
//   - 清理待执行（IsCleanupPending，延迟删除标记在案）：任何 tab 即时生效，
//     与恢复/名册面（IsVisibleSession）同口径；
//   - 其余（含新建 tab）：不判查无。
// 实体重新出现（撤销删除、网络盘恢复）时实时 Stat 翻假，投影自愈。
//
// 查无（命中上述判定）时：
//   - 方向②：tab 不得再对 running/待确认/后台作业 投绿点残影（tabMeta 投影）；
//   - 方向①：重启恢复的骨架 tab 直接以「已断开」可见态示人，而不是装作
//     一切正常的 starting（tabMeta/Meta 投影；点击后的重建路径保持任务 619
//     惰性契约不变——本轮只修 UI 真相，不改重建语义）；
//   - 方向③：「继续/发送」受理链前置校验，查无返回明确报错，而不是让
//     ErrTurnRunning 降级把消息排进已死执行体的队列。
//
// 全部为读取型判定：不改任何 tab/runtime 注册表状态（持久化、唤醒、收件箱
// 语义不动）。

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
// directory 的枚举对象同一文件集。空路径 = 尚未绑定会话路径，视为存在
// （没有实体可断）。
func sessionEntityExists(sessionPath string) bool {
	path := strings.TrimSpace(sessionPath)
	if path == "" {
		return true
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// tabSessionDisconnected 判定一个 tab 是否处于「会话实体查无」断开态。调用方
// 需持有 App.mu（读 tab.restoredMissingPath 与 currentSessionPath）或已确认
// tab 不再变更。判定口径见文件头注释：
//   - 清理待执行即时生效（删除进行中的会话没有「继续」可言）；
//   - 恢复时标记只对同一条路径生效（sessionRuntimeKey 归一比对）——tab 轮换
//     到新会话后自动失活，不给轮换后尚未落盘的新路径误报；
//   - 标记命中的路径再联合实时 Stat——文件回来了就自愈。
func tabSessionDisconnected(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	path := strings.TrimSpace(tab.currentSessionPath())
	if path == "" {
		return false
	}
	if agent.IsCleanupPending(path) {
		return true
	}
	if tab.restoredMissingPath == "" ||
		sessionRuntimeKey(tab.restoredMissingPath) != sessionRuntimeKey(path) {
		return false
	}
	return !sessionEntityExists(path)
}

// tabSessionDisconnectedForPath 是无 tab 上下文的纯路径变体，供恢复时点
// （restorePersistedTabs）在 SessionPath 刚写入、tab 尚未发布前做同口径判定。
// restored 表达「这条路径来自上一进程的持久化布局」——只有它才允许判查无。
func tabSessionDisconnectedForPath(sessionPath string, restored bool) bool {
	path := strings.TrimSpace(sessionPath)
	if path == "" {
		return false
	}
	if agent.IsCleanupPending(path) {
		return true
	}
	if !restored {
		return false
	}
	return !sessionEntityExists(path)
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

// sessionEntityAdmissionErrForTab 是受理链的前置实体校验（方向③）：tab 处于
// 断开态时返回断开哨兵错误。调用方已把 tab 解析出来（与 tabIsReadOnly /
// workspaceRuntimeAdmissionErr 同一批前置检查）；本检查排在其前，保证查无
// 提交先拿到断开文案而不是工作区未就绪/已在运行中的静默降级。控制器不存在
// 或只读 tab 不在此拦——它们各自已有更精确的既有报错（workspaceNotReady /
// readOnly），本守卫只负责「实体查无」这一种断开。
func (a *App) sessionEntityAdmissionErrForTab(tab *WorkspaceTab) error {
	if tab == nil {
		return nil
	}
	a.mu.RLock()
	disconnected := tabSessionDisconnected(tab)
	a.mu.RUnlock()
	if disconnected {
		return errSessionDisconnected
	}
	return nil
}
