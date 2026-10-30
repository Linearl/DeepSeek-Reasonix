package main

import (
	"errors"
	"os"
	"strings"

	"reasonix/internal/agent"
)

// tl 1266 — 装机重启后会话 runtime 未重站且 UI 无感知：tab 挂 running 残影
// （绿点+已工作计数）但 contact directory 查无实体，「继续」把消息塞进已死
// 执行体（20261030 用户实锤）。
//
// 会话实体的本机事实源 = 会话 .jsonl 文件本身——agent 侧 contact directory
// （collabRoster → sessioncollab.ScanDir）枚举的就是这些文件，桌面侧一切
// 「这个会话还存在吗」的判断必须与它同一语义，而不是另立真相源。
// 会话文件查无（被删除/清理中/来自已销毁的旧运行实例且从未落盘）时：
//   - 方向②：tab 不得再对 running/待确认/后台作业 投绿点残影（tabMeta 投影）；
//   - 方向①：重启恢复的骨架 tab 直接以「已断开」可见态示人，而不是装作
//     一切正常的 starting（tabMeta 投影；点击后的重建路径走诚实启动失败）；
//   - 方向③：「继续/发送」前置校验实体存在，查无时返回明确报错而不是把
//     消息降级进已死执行体的队列。
//
// 全部为读取型判定：进程内不改任何 tab/runtime 注册表状态，实体重新出现
// （网络盘恢复、用户撤销删除）时投影自愈。

// errSessionDisconnected 是查无会话实体的哨兵错误：提交链路把它原样透传，
// 前端按普通发送失败渲染（warn notice），不落入 ErrTurnRunning 的静默降级。
var errSessionDisconnected = errors.New(sessionDisconnectedUserMessage)

// sessionDisconnectedUserMessage 是面向用户的断开文案（任务书口径：
// 「会话已断开请重新打开」），tab 徽章/启动失败横幅/提交报错共用同一段。
const sessionDisconnectedUserMessage = "会话已断开，请重新打开：该会话的对话实体已不存在（可能已被删除，或来自已结束的运行实例）"

// sessionMissingRuntimeIssueCode 是 tab 条 runtime issue 的 grep 锚：前端
// TabBar 徽章按它渲染「已断开」，与通用启动失败（startup_failed）区分。
const sessionMissingRuntimeIssueCode = "session_missing"

// sessionEntityExists 报告 tab 声称延续的会话实体是否仍在盘上。
// 空路径 = 尚未落盘的新会话（首轮发送前），视为存在——没有实体可断。
// 清理待执行（IsCleanupPending）= 实体正在被删除，按查无处理。
func sessionEntityExists(sessionPath string) bool {
	path := strings.TrimSpace(sessionPath)
	if path == "" {
		return true
	}
	if agent.IsCleanupPending(path) {
		return false
	}
	if info, err := os.Stat(path); err == nil {
		return !info.IsDir()
	}
	return false
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

// tabSessionEntityMissing 判定一个 tab 是否处于「实体查无」态。只对声明过
// 会话路径的 tab 生效（新会话骨架没有实体可查）。
func tabSessionEntityMissing(sessionPath string) bool {
	return strings.TrimSpace(sessionPath) != "" && !sessionEntityExists(sessionPath)
}

// sessionEntityAdmissionErr 是提交链路的前置实体校验（方向③）：tab 的当前
// 会话路径查无实体时拒绝受理并返回断开文案。控制器不存在/只读 tab 不在此
// 拦——它们各自已有更精确的既有报错（workspaceNotReadyErr/readOnly），
// 本守卫只负责「控制器活着但实体没了」这一种查无。
func (a *App) sessionEntityAdmissionErr(tabID string) error {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return nil
	}
	sessionPath := tab.currentSessionPath()
	a.mu.RUnlock()
	if tabSessionEntityMissing(sessionPath) {
		return errSessionDisconnected
	}
	return nil
}
