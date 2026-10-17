package main

import (
	"fmt"
	"os"
	"strings"

	"reasonix/internal/agent"
)

// 任务 938：会话职责（purpose）的桌面桥方法——侧栏 hover 读 + 「编辑职责」
// 弹窗写。后端链路早已存在（agent 的 set_session_purpose 工具直写 sidecar），
// 缺的只是 UI 面的 host 读写口。

// GetSessionPurpose answers the sidebar hover and the edit dialog's initial
// value: the sidecar-registered duty for ONE session path. It is a per-path
// read on purpose — the project-tree payload never carries purpose (that would
// cost a branch-meta sidecar read per row on every tree refresh), so hover and
// edit fetch lazily and the frontend caches. A missing sidecar answers empty,
// not an error: a session without a registered duty is a normal state.
func (a *App) GetSessionPurpose(sessionPath string) (string, error) {
	path := strings.TrimSpace(sessionPath)
	if path == "" {
		return "", fmt.Errorf("session path is required")
	}
	meta, ok, err := agent.LoadBranchMeta(path)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return strings.TrimSpace(meta.Purpose), nil
}

// SetSessionPurpose is the desktop half of the sidebar「编辑职责」dialog. It
// reuses the agent's own write chain (SetSessionDuty) with nil identity/duties
// — the presence-based task-348 semantics keep the structured fields exactly as
// they are, so a UI edit of the free-text duty can never erase what it cannot
// see. Empty purpose clears the duty (the dialog's "delete the text" intent);
// the same chain mints or keeps the contact id. The transcript must exist: an
// orphan sidecar is never created for a path that is not a session.
//
// No project-tree event is emitted: the catalog does not store purpose, the
// frontend updates its own hover cache from the save callback, and every other
// directory consumer (list_addressable_sessions, the collab views) reads the
// sidecar live or polls on its own cadence.
func (a *App) SetSessionPurpose(sessionPath, purpose string) error {
	path := strings.TrimSpace(sessionPath)
	if path == "" {
		return fmt.Errorf("session path is required")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("session %q does not exist", path)
		}
		return fmt.Errorf("session %q: %w", path, err)
	}
	if _, err := agent.SetSessionDuty(path, purpose, nil, nil); err != nil {
		return err
	}
	return nil
}
