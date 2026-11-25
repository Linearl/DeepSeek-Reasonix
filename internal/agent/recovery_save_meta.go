package agent

import (
	"strings"

	"reasonix/internal/textutil"
)

func (s *Session) prepareRecoveryBranchMetaLocked(path string, opts RecoveryBranchOptions, preview string, turns int, digest string, depth int, contentUnchanged bool) (BranchMeta, error) {
	existing, ok, err := LoadBranchMeta(path)
	if err != nil {
		return BranchMeta{}, err
	}
	meta := opts.BranchMeta
	meta.ID = BranchID(path)
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = textutil.First(strings.TrimSpace(opts.Name), RecoveryBranchDefaultName)
	}
	if strings.TrimSpace(meta.ParentID) == "" {
		meta.ParentID = recoveryRootID(opts.OriginalPath)
	}
	meta.ForkTurn = -1
	meta.ForkMessageIndex = len(s.Snapshot())
	meta.Preview = preview
	meta.Turns = turns
	meta.SchemaVersion = BranchMetaCountsVersion
	meta.Recovered = true
	meta.VersionKind = VersionRecovery
	meta.VersionState = VersionActive
	meta.ParentConversationID = textutil.First(meta.ParentConversationID, meta.TopicID)
	meta.ParentVersionID = textutil.First(meta.ParentVersionID, meta.ParentID)
	meta.BaseRevision = opts.BaseRevision
	meta.DiskRevision = opts.DiskRevision
	meta.RecoveryReason = textutil.First(strings.TrimSpace(opts.Reason), "session snapshot conflict")
	meta.RecoveryDigest = digest
	meta.RecoveryDepth = depth
	meta.Revision = 1
	ledgerCurrent := false
	if ok {
		if meta.BaseRevision == 0 {
			meta.BaseRevision = existing.BaseRevision
		}
		if meta.DiskRevision == 0 {
			meta.DiskRevision = existing.DiskRevision
		}
		if meta.ParentConversationID == "" {
			meta.ParentConversationID = existing.ParentConversationID
		}
		if meta.ParentVersionID == "" {
			meta.ParentVersionID = existing.ParentVersionID
		}
		ledgerCurrent = contentUnchanged && existing.Revision > 0 &&
			strings.TrimSpace(existing.ContentDigest) == digest &&
			strings.TrimSpace(existing.RecoveryDigest) == digest
		if ledgerCurrent {
			meta.Revision = existing.Revision
		} else {
			meta.Revision = max(int64(1), existing.Revision+1)
		}
		meta.InFlightTurn = existing.InFlightTurn
		meta.DismissedTodoBatches = MergeDismissedTodoBatches(existing.DismissedTodoBatches, meta.DismissedTodoBatches)
		if ledgerCurrent && strings.TrimSpace(existing.WriterID) != "" {
			meta.WriterID = existing.WriterID
		}
	}
	meta.ContentDigest = digest
	// tl1403 追加面：分叉继承主会话身份——fork/recovery 是同一会话的延续，
	// contact_id（多会话通讯地址）必须随行；此前副本 meta 只带 opts/已有侧车
	// 字段，主 meta 的 contact_id 不在复制面里，分叉态发信身份解析失据
	// （2026-11-05 现场：b26cee 副本 33 字段无 contact_id）。只继承不铸造：
	// 主会话自身没有地址时给副本铸一个新 id 会把同一会话的身份永久劈成两半。
	if strings.TrimSpace(meta.ContactID) == "" && strings.TrimSpace(opts.OriginalPath) != "" {
		meta.ContactID = SessionContactID(opts.OriginalPath)
	}
	// tl1793r2 写面：副本出生即继承原对话标题。桌面回调（tabSessionRecoveryMeta）
	// 通常已带 tab 标题，但它为空（旧包产物、tab 无题）时副本 meta 两级标题全空，
	// 版本管理/恢复链只剩 stem 兜底——20261125 现场：回收站恢复的副本在版本管理
	// 显示文件字符串而回收站族渲染出中文名。这里按显示链同序（custom_title 先、
	// topic_title 后）从原对话 meta 补继承；只填空位不覆盖（副本自己已有的标题、
	// 回调已带的标题都原样保留），原 meta 读不到（缺失/损坏/并发写裂）就放弃，
	// 副本标题留给读面兜底（版本管理 registry/父链两级），不阻塞救援写本身。
	if custom, topic, changed := recoveryInheritedTitles(opts.OriginalPath, meta.CustomTitle, meta.TopicTitle); changed {
		meta.CustomTitle, meta.TopicTitle = custom, topic
	}
	stampSessionListingProjection(&meta)
	if !ledgerCurrent || strings.TrimSpace(meta.WriterID) == "" {
		meta.WriterID = SessionWriterID()
	}
	return meta, nil
}

func (s *Session) saveRecoveryBranchMetaLocked(path string, opts RecoveryBranchOptions, preview string, turns int, digest string, depth int, contentUnchanged bool) (BranchMeta, error) {
	meta, err := s.prepareRecoveryBranchMetaLocked(path, opts, preview, turns, digest, depth, contentUnchanged)
	if err != nil {
		return BranchMeta{}, err
	}
	if err := saveBranchMeta(path, meta, true); err != nil {
		return BranchMeta{}, err
	}
	return meta, nil
}

// recoveryInheritedTitles reads the original conversation's two title fields
// (display-chain order: custom_title first, then topic_title) and fills only
// the ones the recovery copy is missing. ok is false when there is nothing to
// inherit: no original path, both fields already set, or an original meta
// sidecar that is missing, corrupt, or mid-write (plain unlocked read on
// purpose — the recovery write must not depend on, or wait for, the parent's
// meta lock).
func recoveryInheritedTitles(originalPath, customTitle, topicTitle string) (custom, topic string, ok bool) {
	if strings.TrimSpace(originalPath) == "" {
		return customTitle, topicTitle, false
	}
	if strings.TrimSpace(customTitle) != "" && strings.TrimSpace(topicTitle) != "" {
		return customTitle, topicTitle, false
	}
	parent, _, err := LoadBranchMeta(originalPath)
	if err != nil ||
		(strings.TrimSpace(parent.CustomTitle) == "" && strings.TrimSpace(parent.TopicTitle) == "") {
		return customTitle, topicTitle, false
	}
	if strings.TrimSpace(customTitle) == "" {
		custom = parent.CustomTitle
	} else {
		custom = customTitle
	}
	if strings.TrimSpace(topicTitle) == "" {
		topic = parent.TopicTitle
	} else {
		topic = topicTitle
	}
	return custom, topic, custom != customTitle || topic != topicTitle
}
