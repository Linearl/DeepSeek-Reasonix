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

// tabSessionEntityMissing 判定一个路径锚是否处于「实体查无」态。只对声明过
// 会话路径的 tab 生效（新会话骨架没有实体可查）。
func tabSessionEntityMissing(sessionPath string) bool {
	return strings.TrimSpace(sessionPath) != "" && !sessionEntityExists(sessionPath)
}

// tabSessionEntityGone 是 tab 级的实体查无判定（tl 1266 三向防线的统一锚）：
// tab 的持久化锚路径与控制器的活路径都查无时才算断开。只看其中一边会在
// 两类既有场景误伤：
//   - workspace 迁移后的 stale binding（tab 锚在新目录健在、控制器还指着旧
//     路径）——下一次提交正是自愈重建的触发器，拦下它就砍断了既有的
//     「提交即修复」契约（app_commands_2 stale-binding 家族钉的场景）；
//   - 会话轮换/恢复分支切换的瞬窗（旧路径已不在、新文件尚未落盘）。
func tabSessionEntityGone(tab *WorkspaceTab) bool {
	if tab == nil {
		return false
	}
	tabPath := strings.TrimSpace(tab.SessionPath)
	if tabPath != "" && sessionEntityExists(tabPath) {
		return false
	}
	ctrlPath := ""
	if tab.Ctrl != nil {
		ctrlPath = strings.TrimSpace(tab.Ctrl.SessionPath())
		if ctrlPath != "" && sessionEntityExists(ctrlPath) {
			return false
		}
	}
	return tabSessionEntityMissing(tabPath) || (tabPath == "" && tabSessionEntityMissing(ctrlPath))
}

// missingPinnedSessionResumeErr 是重建路径（方向①）的判定缝：tab 钉住的会话
// 路径加载结果「ok 但无可恢复会话」时，区分两种 nil——文件还在的空占位
// （合法的新会话起点，返回 nil 照旧）与文件已消失的实体查无（返回断开
// 哨兵，诚实失败而非静默空会话重置）。
func missingPinnedSessionResumeErr(path string, resumeSession *agent.Session) error {
	if resumeSession != nil {
		return nil
	}
	if !tabSessionEntityMissing(path) {
		return nil
	}
	return errSessionDisconnected
}

// sessionEntityAdmissionErr 是提交链路的前置实体校验（方向③）：控制器谎报
// busy（running/待确认/后台作业）而会话实体（持久化锚与控制器活路径都）查无
// 时，拒绝受理并返回断开文案——这正是残影 tab 上「继续」点击撞到的状态：
// 不拦，ErrTurnRunning 会被前端静默降级进已死执行体的队列。
//
// 只对 busy 控制器生效是硬边界：新会话的首条消息发生在实体落盘之前（首轮
// 自动保存才创建 .jsonl），空闲放行是既有契约——按任意实体查无拦提交会把
// 每个新会话的第一条消息误判成「会话已断开」（app_memory_context 家族实锤：
// 空闲拦截令首消息静默被拒、测试端等轮启动的通道永远等不到）。重启后实体
// 查无的 tab 到不了「空闲+活控制器」态：重建路径已按方向①诚实失败。
// stale-binding 自愈窗口由 tabSessionEntityGone 的双边锚放行。
func (a *App) sessionEntityAdmissionErr(tabID string) error {
	a.mu.RLock()
	tab := a.tabByIDLocked(tabID)
	if tab == nil {
		a.mu.RUnlock()
		return nil
	}
	gone := tab.hasActiveRuntimeWork() && tabSessionEntityGone(tab)
	a.mu.RUnlock()
	if gone {
		return errSessionDisconnected
	}
	return nil
}
