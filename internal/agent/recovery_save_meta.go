package agent

import "strings"

func (s *Session) prepareRecoveryBranchMetaLocked(path string, opts RecoveryBranchOptions, preview string, turns int, digest string, depth int, contentUnchanged bool) (BranchMeta, error) {
	existing, ok, err := LoadBranchMeta(path)
	if err != nil {
		return BranchMeta{}, err
	}
	meta := opts.BranchMeta
	meta.ID = BranchID(path)
	if strings.TrimSpace(meta.Name) == "" {
		meta.Name = firstNonEmpty(strings.TrimSpace(opts.Name), RecoveryBranchDefaultName)
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
	meta.ParentConversationID = firstNonEmpty(meta.ParentConversationID, meta.TopicID)
	meta.ParentVersionID = firstNonEmpty(meta.ParentVersionID, meta.ParentID)
	meta.BaseRevision = opts.BaseRevision
	meta.DiskRevision = opts.DiskRevision
	meta.RecoveryReason = firstNonEmpty(strings.TrimSpace(opts.Reason), "session snapshot conflict")
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
