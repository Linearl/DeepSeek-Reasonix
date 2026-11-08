package main

import "reasonix/internal/agent"

// SessionMeta summarises one saved session for the history panel.
type SessionMeta struct {
	Path    string `json:"path"`
	Preview string `json:"preview"`         // newest recognizable user message (tl 1177 tail-first)
	Title   string `json:"title,omitempty"` // user-chosen name, when set (overrides preview)
	Turns   int    `json:"turns"`
	// RecentExcerpts are the newest noise-filtered user messages, newest
	// first (tl 1177) — the session wall card's hover overlay scans them
	// without opening the transcript. Empty until the session's listing
	// projection has been rewritten at least once.
	RecentExcerpts []string `json:"recentExcerpts,omitempty"`
	TurnsState     string   `json:"turnsState"`
	CreatedAt      int64    `json:"createdAt"`      // unix milliseconds
	LastActivityAt int64    `json:"lastActivityAt"` // unix milliseconds
	ModTime        int64    `json:"modTime"`        // compatibility alias for lastActivityAt
	DeletedAt      int64    `json:"deletedAt,omitempty"`
	Current        bool     `json:"current"`
	Open           bool     `json:"open"`
	Scope          string   `json:"scope,omitempty"`
	WorkspaceRoot  string   `json:"workspaceRoot,omitempty"`
	TopicID        string   `json:"topicId,omitempty"`
	TopicTitle     string   `json:"topicTitle,omitempty"`
	Kind           string   `json:"kind,omitempty"` // "channel" for external IM transcripts
	Channel        string   `json:"channel,omitempty"`
	ChannelLabel   string   `json:"channelLabel,omitempty"`
	RemoteID       string   `json:"remoteId,omitempty"`
	ChatType       string   `json:"chatType,omitempty"`
	UserID         string   `json:"userId,omitempty"`
	ThreadID       string   `json:"threadId,omitempty"`
	SessionSource  string   `json:"sessionSource,omitempty"`
	Recovered      bool     `json:"recovered,omitempty"`    // created by conflict recovery, including an adopted/continued branch
	RecoveryCopy   bool     `json:"recoveryCopy,omitempty"` // actual branch content is unchanged and covered by its parent
	// Optional v1.24.2 lineage fields; older clients ignore them safely.
	RecoveryGroupID   string `json:"recoveryGroupId,omitempty"`
	RecoveryRole      string `json:"recoveryRole,omitempty"` // normal|covered_copy|adopted|diverged
	RecoveryCanonical bool   `json:"recoveryCanonical,omitempty"`
	// tl1429d2: 回收站同族归组的只读线索,仅回收站装配路径填充(其余 listing
	// 留空)。TrashFamilyKey=同族归组键(主线桶=transcript stem;recovery 副本
	// 桶=剥掉 -recovery-<16hex> 后的根 stem,与主线桶同键);TrashFamilyCopy=
	// 该桶是否副本形状(纯命名判据,不碰 RecoveryCopy 的 coverage 契约)。
	// 渲染层据此把同族多桶合并为一行(主线为主体,副本进展开明细);桶本身
	// 仍是还原/清除的单桶语义,这两个字段不参与任何还原/清除判定。
	TrashFamilyKey  string `json:"trashFamilyKey,omitempty"`
	TrashFamilyCopy bool   `json:"trashFamilyCopy,omitempty"`
}

func sessionMetaFromInfo(s agent.SessionInfo, title string, current, open bool, deletedAt int64, parentDir string) SessionMeta {
	turnsState := "unknown"
	if s.CountsKnown {
		turnsState = "valid"
	}
	return SessionMeta{
		Path:           s.Path,
		Preview:        s.Preview,
		Title:          title,
		RecentExcerpts: s.RecentExcerpts,
		Turns:          s.Turns,
		TurnsState:     turnsState,
		CreatedAt:      s.CreatedAt.UnixMilli(),
		LastActivityAt: s.LastActivityAt.UnixMilli(),
		ModTime:        s.LastActivityAt.UnixMilli(),
		DeletedAt:      deletedAt,
		Current:        current,
		Open:           open,
		Scope:          s.Scope,
		WorkspaceRoot:  s.WorkspaceRoot,
		TopicID:        s.TopicID,
		TopicTitle:     s.TopicTitle,
		Recovered:      sessionInfoIsAutomaticRecovery(s),
		RecoveryCopy:   sessionInfoIsUnmodifiedRecoveryCopy(s, parentDir),
	}
}
