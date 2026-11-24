package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/store"
)

// tl1793（20261124 真机，包113）：对话归档后按 topic 打开的静默空壳。
//
// 现场链：对话的会话文件已随归档进回收站（DeleteConversationWithCopies /
// trashTopic），topic 注册行未随之收敛（同族仍有活成员或元数据残留）→
// 用户点击侧栏该对话（或 heartbeat 按 topic 激活）→
// findTopicSessionForTarget 只扫活目录返回空、tl1197 跨 scope 查找同样只
// 找活文件 → openTopicTabWithActivation 落进 sessionPath=="" 的新建分支，
// createEmptySessionFile 铸出一条 0 字节投影 + 继承 topic 标题的 meta——
// 用户看到的「原始对话变空壳」（空壳桶实证：.jsonl 0 字节、无事件日志、
// meta.topic_title=原对话名、created_at=新建时刻）。
//
// 本探针只回答一个问题：这个 topic 的会话是不是只躺在回收站里。打开链在
// 即将新建空会话之前问它，命中即拒绝新建并明说去向；真新话题（回收站无
// 同 topic 证据）不受影响，新建语义零变化。

// errTopicOpenArchived names the refusal when an open by topic id finds no
// live session anywhere but its conversation buckets sit in the trash.
var errTopicOpenArchived = errors.New("this conversation is in the trash; restore it from the trash before opening")

// topicArchivedInTrashDirs reports whether any known session directory's
// trash holds a bucket whose session meta carries this topic id. Reading the
// bucket's primary meta is the same evidence the trash listing itself renders
// (titles come from that sidecar), so the probe and the restore flow agree on
// what a bucket belongs to. Buckets whose meta is missing or unreadable
// simply carry no evidence — the probe stays a new-shell guard, not a
// truth source.
func topicArchivedInTrashDirs(dirs []string, topicID string) bool {
	topicID = strings.TrimSpace(topicID)
	if topicID == "" {
		return false
	}
	for _, dir := range dirs {
		if topicArchivedInTrashDir(dir, topicID) {
			return true
		}
	}
	return false
}

func topicArchivedInTrashDir(dir, topicID string) bool {
	buckets, err := os.ReadDir(filepath.Join(dir, sessionTrashDir))
	if err != nil {
		return false
	}
	for _, entry := range buckets {
		if !entry.IsDir() {
			continue
		}
		key := entry.Name()
		primary := filepath.Join(dir, sessionTrashDir, key, key)
		b, err := os.ReadFile(store.SessionMeta(primary))
		if err != nil {
			continue
		}
		var meta struct {
			TopicID string `json:"topic_id"`
		}
		if json.Unmarshal(b, &meta) != nil {
			continue
		}
		if strings.TrimSpace(meta.TopicID) == topicID {
			return true
		}
	}
	return false
}
