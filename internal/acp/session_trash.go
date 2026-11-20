package acp

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/store"
)

// 本文件是 ACP 远程删除的「回收」路径（tl1722，2026-11-20 用户拍板：远程与
// 本地同权——默认回收进 trash 可恢复，显式 permanent 才硬删）。
//
// 纪律：不重写落桶机制。桌面是独立 module（无法 import），落桶的底层全部在
// 共享包——清单单一来源 store.SessionTrashSidecars、搬移计数 agent.
// MoveTrashArtifactTo、stem 前缀兜底清扫 agent.SweepTrashStragglers、运行时
// 句柄排除集 agent.TrashRuntimeHandleNames、日志 agent.LogTrashMoveCounts。
// 本文件只拼桌面 main 链（desktop session_trash_move.go / sessions.go）同款
// 薄封装：同一桶结构（<dir>/.trash/<唯一桶>/<原名工件> + .trash-meta.json），
// 桌面的回收站列表/还原/清除按 meta 全量列举，天然读写 ACP 落的桶。

// acpSessionTrashDir / acpSessionTrashMetaFile 与桌面（desktop sessions.go
// sessionTrashDir / sessionTrashMetaFile）同拼写：同一目录约定，两套运行时
// 各自持有常量（跨 module 不共享 package main 符号）。
const (
	acpSessionTrashDir      = ".trash"
	acpSessionTrashMetaFile = ".trash-meta.json"
)

// acpTrashedSessionMeta 与桌面 trashedSessionMeta 同 JSON 形状（key/deletedAt），
// 桌面还原链靠它识别桶内的转录名与删除时间。
type acpTrashedSessionMeta struct {
	Key       string `json:"key"`
	DeletedAt int64  `json:"deletedAt"`
}

// acpTrashArtifact pairs the live source path of one session-owned artifact
// with the name it keeps inside the trash bucket (same shape as the desktop's
// sessionTrashArtifact).
type acpTrashArtifact struct {
	src  string
	name string
}

// acpTrashArtifacts lists every file and directory a session owns, with the
// name each keeps inside the trash entry. Same derivation as the desktop
// sessionTrashArtifacts: the transcript first (bucket keeps its original
// name), then the full store.SessionTrashSidecars inventory (the single
// source every deletion chain shares). ACP deletes never rename the target,
// so src and key always share the stem and the desktop's rename mapping has
// nothing to do here.
func acpTrashArtifacts(sessionPath, key string) []acpTrashArtifact {
	artifacts := []acpTrashArtifact{{src: sessionPath, name: key}}
	for _, src := range store.SessionTrashSidecars(sessionPath) {
		artifacts = append(artifacts, acpTrashArtifact{src: src, name: filepath.Base(src)})
	}
	// The ACP metadata sidecar is ACP-owned (not in the store inventory) and
	// must follow the family into the bucket like the hard-delete path removes it.
	if meta := acpMetaPath(sessionPath); meta != "" {
		artifacts = append(artifacts, acpTrashArtifact{src: meta, name: filepath.Base(meta)})
	}
	return artifacts
}

// reserveUniqueACPTrashItemDir allocates the trash bucket directory, same
// unique-name rule as the desktop reserveUniqueSessionTrashItemDir. The ACP
// chain always allocates a fresh unique bucket: no collision handling with
// same-key leftovers is needed, and the desktop's listing walks every bucket
// and reads its meta, so unique names list and restore identically.
func reserveUniqueACPTrashItemDir(dir, key string) (string, error) {
	root := filepath.Join(dir, acpSessionTrashDir)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	stem := strings.TrimSuffix(key, store.SessionTranscriptSuffix)
	for i := range 100 {
		name := fmt.Sprintf("%s.jsonl-deleted-%d-%02d", stem, time.Now().UnixNano(), i)
		itemDir := filepath.Join(root, name)
		if err := os.Mkdir(itemDir, 0o755); err == nil {
			return itemDir, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate unique trash target for session: %s", key)
}

// trashACPSubagentArtifacts moves the parent session's sub-agent artifacts
// into the bucket's "subagents" directory — the same bucket layout the
// desktop trashSubagentArtifacts writes and its restore path reads back
// (checkRestoreSubagentConflicts walks itemDir/subagents).
func trashACPSubagentArtifacts(dir, sessionPath, itemDir string, report *agent.TrashMoveReport) error {
	artifacts, err := agent.ListSubagentsByParent(dir, agent.BranchID(sessionPath))
	if err != nil {
		return err
	}
	trashSubagentDir := filepath.Join(itemDir, "subagents")
	for _, artifact := range artifacts {
		stem := strings.TrimSuffix(filepath.Base(artifact.SessionPath), store.SessionTranscriptSuffix)
		paths := []string{artifact.SessionPath, artifact.MetaPath}
		paths = append(paths, store.SessionSidecarFiles(artifact.SessionPath)...)
		for _, src := range paths {
			if strings.TrimSpace(src) == "" {
				continue
			}
			if _, err := agent.MoveTrashArtifactTo(src, filepath.Join(trashSubagentDir, filepath.Base(src)), stem, report); err != nil {
				return err
			}
		}
	}
	return nil
}

// trashSessionFiles moves the session's whole artifact family (transcript,
// store sidecar inventory, ACP metadata sidecar, subagents, and the
// stem-prefixed catch-all sweep) into a fresh ".trash" bucket under the
// session directory, then writes the desktop-compatible trash meta and clears
// the cleanup-pending marker. Directory artifacts (checkpoints, jobs, inbox,
// reminders, pre-repair backups) ride the store inventory, so the jobs
// artifacts are recoverable instead of hard-removed.
func trashSessionFiles(sessionPath string) error {
	sessionPath = strings.TrimSpace(sessionPath)
	if sessionPath == "" {
		return nil
	}
	dir := filepath.Dir(sessionPath)
	key := filepath.Base(sessionPath)
	keyStem := strings.TrimSuffix(key, store.SessionTranscriptSuffix)
	itemDir, err := reserveUniqueACPTrashItemDir(dir, key)
	if err != nil {
		return err
	}
	report := &agent.TrashMoveReport{}
	seen := map[string]bool{}
	for _, artifact := range acpTrashArtifacts(sessionPath, key) {
		moved, err := agent.MoveTrashArtifactTo(artifact.src, filepath.Join(itemDir, artifact.name), keyStem, report)
		if err != nil {
			return err
		}
		if moved {
			seen[filepath.Base(artifact.src)] = true
		}
	}
	if err := trashACPSubagentArtifacts(dir, sessionPath, itemDir, report); err != nil {
		return err
	}
	// tl1442-ext 的兜底语义照搬：全清单搬移之外，stem 前缀清扫保证
	// 「删除=零残留」；清扫有捕获即 Warn 点名清单过期。
	stem := strings.TrimSuffix(filepath.Base(sessionPath), store.SessionTranscriptSuffix)
	swept, err := agent.SweepTrashStragglers(dir, stem, agent.TrashRuntimeHandleNames(sessionPath), itemDir, seen, report)
	if err != nil {
		return err
	}
	if swept > 0 {
		slog.Warn("acp: session trash swept stem-prefixed stragglers not in the sidecar inventory — extend store.SessionTrashSidecars",
			"key", key, "dir", dir, "names", strings.Join(report.Stragglers, ","))
	}
	meta := acpTrashedSessionMeta{Key: key, DeletedAt: time.Now().UnixMilli()}
	b, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(itemDir, acpSessionTrashMetaFile), b, 0o644); err != nil {
		return err
	}
	if err := agent.ClearCleanupPending(sessionPath); err != nil {
		return err
	}
	agent.LogTrashMoveCounts("acp: session artifacts trashed", dir, key, report)
	return nil
}
