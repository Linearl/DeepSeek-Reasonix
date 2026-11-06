package main

import (
	"path/filepath"
	"strings"

	"reasonix/internal/store"
)

// sessionTrashArtifact pairs the live source path of one session-owned
// artifact with the name it keeps inside the trash bucket.
type sessionTrashArtifact struct {
	src  string
	name string
}

// sessionTrashArtifacts lists every file and directory a session owns, with
// the name each keeps inside the trash entry, so moving a session aside and
// restoring it round-trip the whole set.
//
// tl1442-ext（2026-11-07 用户裁决 A）：清单本体上移 store.SessionTrashSidecars
// 成为唯一事实源（宪法 III）——本函数只做派生：src 对 live 会话路径套清单，
// 桶内名对 key 套同一清单再剥 stem。桶内名与恢复目标路径因此保持既有的解耦
// 语义（key 是原名，sessionPath 可以是改名后的目标），store 布局变更时
// desktop/recovery/merge-promote 三面的行为在同一 commit 内同步变。
func sessionTrashArtifacts(sessionPath, key string) []sessionTrashArtifact {
	// The transcript itself stays the first entry: the trash bucket keeps it
	// under its original name, and restore's discardable-transcript special
	// case keys off src == target.
	artifacts := []sessionTrashArtifact{{src: sessionPath, name: key}}
	sessionStem := strings.TrimSuffix(filepath.Base(sessionPath), store.SessionTranscriptSuffix)
	keyStem := strings.TrimSuffix(key, store.SessionTranscriptSuffix)
	for _, src := range store.SessionTrashSidecars(sessionPath) {
		base := filepath.Base(src)
		name := base
		if sessionStem != keyStem && strings.HasPrefix(base, sessionStem) {
			// 改名后的目标：桶内名跟随 key 的 stem（src 与 key 的 stem 在
			// 现网所有调用点一致，这里只做防御性映射）。
			name = keyStem + strings.TrimPrefix(base, sessionStem)
		}
		artifacts = append(artifacts, sessionTrashArtifact{src: src, name: name})
	}
	return artifacts
}
