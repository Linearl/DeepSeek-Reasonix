package agent

// 任务 285（会话信息面工具补强）的 agent 侧三工具：
//   - get_session_info：结构化 meta 查询（副本标记/workspaceRoot/scope/最后活动
//     等），不再靠 bash 读 .meta 文件猜；
//   - list_session_versions：列一个会话所在对话的 recovery 版本（与 UI「查看
//     版本」同数据源），agent 通道作为 241/34 版本入口断的兜底；
//   - adopt_session_version：带确认参数切换 active 版本（复用宿主既有
//     recovery 选择路径，不新造机制）。
//
// 契约照搬 collab tool extension pattern（task 274 模型探针同款）：数据经
// SessionCollabConfig 注入的宿主探针在调用时求值（不用 boot 快照），探针为
// nil（CLI/测试）时给可操作的拒绝而不是猜。三个工具都是元数据级——唯一有
// 副作用的 adopt 必须显式 confirm=true。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"reasonix/internal/sessioncollab"
	"reasonix/internal/tool"
)

// SessionVersionInfo is one lineage member as the host reports it: the physical
// copy's path, its role in the lineage, and which one is currently active.
type SessionVersionInfo struct {
	VersionID   string `json:"versionId,omitempty"`
	Path        string `json:"path"`
	Role        string `json:"role,omitempty"`
	VersionKind string `json:"versionKind,omitempty"`
	Selected    bool   `json:"selected,omitempty"`
	Canonical   bool   `json:"canonical,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	LastActive  int64  `json:"lastActiveAt,omitempty"`
}

// sessionInfoKnownFields is the field contract get_session_info answers with —
// also the error text when a filter matches nothing (the next step must exist).
const sessionInfoKnownFields = "sessionPath contactId topicId title purpose scope workspaceRoot model archived updatedAt createdAt sizeBytes modelRef provider group versionCount recoveryRole recoveryVersionKind recoverySelected"

// ── shared target resolution ─────────────────────────────────────────────────

// resolveInfoTarget resolves a directory reference (contact_id / topic_id /
// exact title) or, without one, the calling session itself. The caller may not
// be in the directory yet (no contact_id minted): its identity is then built
// from the meta it definitely has.
func resolveInfoTarget(cfg SessionCollabConfig, target string) (sessioncollab.Identity, error) {
	if strings.TrimSpace(target) != "" {
		ids := scanAddressable(cfg.SessionDir, cfg.WorkspaceRoot)
		return ResolveTarget(ids, target)
	}
	session := strings.TrimSpace(cfg.currentSessionPath())
	if session == "" {
		return sessioncollab.Identity{}, fmt.Errorf("no session path (pass target, or run inside a session)")
	}
	id := sessioncollab.Identity{SessionPath: session, Workspace: cfg.WorkspaceRoot}
	if meta, _, err := LoadBranchMeta(session); err == nil {
		id.ContactID = meta.ContactID
		id.TopicID = meta.TopicID
		id.Title = firstNonEmpty(meta.TopicTitle, meta.CustomTitle, SessionDirectoryTitle(session))
		id.Purpose = meta.Purpose
		id.Scope = meta.Scope
		id.Workspace = firstNonEmpty(meta.WorkspaceRoot, cfg.WorkspaceRoot)
	} else {
		id.Title = SessionDirectoryTitle(session)
	}
	return id, nil
}

// ── get_session_info ─────────────────────────────────────────────────────────

func NewGetSessionInfoTool(cfg SessionCollabConfig) tool.Tool {
	return getSessionInfoTool{cfg: cfg}
}

type getSessionInfoTool struct{ cfg SessionCollabConfig }

func (getSessionInfoTool) Name() string { return "get_session_info" }

func (getSessionInfoTool) Description() string {
	return "Structured metadata for one directory session (task 285): title, purpose, contact_id, topic_id, scope, workspaceRoot, model, group, sizes/timestamps, and its role in the recovery lineage (recovery copy or not). Read-only, zero side effects — the fields a cross-session coordinator otherwise guesses by reading .meta files with bash. Pass `fields` to select a subset; unknown field names are reported, never silently dropped. Use list_addressable_sessions first when you do not know the target yet. Experimental."
}

func (getSessionInfoTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"contact_id, topic_id, or exact title from list_addressable_sessions; omit for the calling session itself."},"fields":{"type":"array","items":{"type":"string"},"description":"Optional subset of fields to return (e.g. [\"group\",\"workspaceRoot\"]). Omit for everything."}},"required":[]}`)
}

func (getSessionInfoTool) ReadOnly() bool { return true }

func (t getSessionInfoTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target string   `json:"target"`
		Fields []string `json:"fields"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	id, err := resolveInfoTarget(t.cfg, p.Target)
	if err != nil {
		return "", fmt.Errorf("get_session_info: %w", err)
	}
	info := t.sessionInfo(id)
	payload := map[string]any{"session": info}
	if len(p.Fields) > 0 {
		unknown := applyInfoFieldFilter(info, p.Fields)
		if len(info) == 0 {
			return "", fmt.Errorf("get_session_info: no requested field exists; known fields: %s", sessionInfoKnownFields)
		}
		if len(unknown) > 0 {
			payload["unknownFields"] = unknown
		}
	}
	out, _ := json.Marshal(payload)
	return string(out), nil
}

// sessionInfo gathers the structured record. Host probes (task 274 model
// visibility; task 285 group + lineage) evaluate at call time: a nil probe
// leaves its field absent, never guessed.
func (t getSessionInfoTool) sessionInfo(id sessioncollab.Identity) map[string]any {
	meta, _, metaErr := LoadBranchMeta(id.SessionPath)
	updatedAt := id.UpdatedAt
	if id.TopicID == "" {
		id.TopicID = meta.TopicID
	}
	if id.Scope == "" {
		id.Scope = meta.Scope
	}
	if id.Workspace == "" {
		id.Workspace = meta.WorkspaceRoot
	}
	contact := firstNonEmpty(id.ContactID, meta.ContactID)
	info := map[string]any{
		"sessionPath":   id.SessionPath,
		"contactId":     contact,
		"topicId":       id.TopicID,
		"title":         firstNonEmpty(id.Title, meta.TopicTitle, meta.CustomTitle, SessionDirectoryTitle(id.SessionPath)),
		"purpose":       firstNonEmpty(id.Purpose, meta.Purpose),
		"scope":         id.Scope,
		"workspaceRoot": id.Workspace,
		"model":         meta.Model,
		"archived":      id.Archived,
		"updatedAt":     updatedAt,
		"createdAt":     meta.CreatedAt.UnixMilli(),
	}
	if meta.UpdatedAt.After(time.UnixMilli(0)) && updatedAt == 0 {
		info["updatedAt"] = meta.UpdatedAt.UnixMilli()
	}
	if size, err := os.Stat(id.SessionPath); err == nil {
		info["sizeBytes"] = size.Size()
	}
	if metaErr != nil {
		info["metaRegistered"] = false
	}
	if t.cfg.SessionInfo != nil && contact != "" {
		if ref, prov, known := t.cfg.SessionInfo(contact); known {
			info["modelRef"] = ref
			info["provider"] = prov
		}
	}
	if t.cfg.SessionGroup != nil && id.TopicID != "" {
		if group, known := t.cfg.SessionGroup(id.TopicID); known {
			info["group"] = group
		}
	}
	if t.cfg.SessionVersions != nil {
		if members, ok := t.cfg.SessionVersions(id.Scope, id.Workspace, id.TopicID, id.SessionPath); ok {
			info["versionCount"] = len(members)
			for _, m := range members {
				if sameInfoPath(m.Path, id.SessionPath) {
					info["recoveryRole"] = m.Role
					info["recoveryVersionKind"] = m.VersionKind
					info["recoverySelected"] = m.Selected
					break
				}
			}
		}
	}
	return info
}

// applyInfoFieldFilter keeps only the requested keys in place and returns the
// requested names that matched nothing (reported, never silently dropped).
func applyInfoFieldFilter(info map[string]any, fields []string) []string {
	kept := make(map[string]any, len(fields))
	unknown := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if v, ok := info[f]; ok {
			kept[f] = v
		} else {
			unknown = append(unknown, f)
		}
	}
	if len(kept) > 0 {
		for k := range info {
			delete(info, k)
		}
		for k, v := range kept {
			info[k] = v
		}
	}
	return unknown
}

func sameInfoPath(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

// ── list_session_versions ────────────────────────────────────────────────────

func NewListSessionVersionsTool(cfg SessionCollabConfig) tool.Tool {
	return listSessionVersionsTool{cfg: cfg}
}

type listSessionVersionsTool struct{ cfg SessionCollabConfig }

func (listSessionVersionsTool) Name() string { return "list_session_versions" }

func (listSessionVersionsTool) Description() string {
	return "List the recovery versions (physical copies) of one conversation (task 285) — the same lineage the UI's version viewer shows: version id, path, role, size, which one is active. Agent-side fallback for when the UI entry is out of reach (task 241/34). Metadata only; switching versions is adopt_session_version. Experimental."
}

func (listSessionVersionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"contact_id, topic_id, or exact title from list_addressable_sessions; omit for the calling session's own conversation."}},"required":[]}`)
}

func (listSessionVersionsTool) ReadOnly() bool { return true }

func (t listSessionVersionsTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target string `json:"target"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &p)
	}
	if t.cfg.SessionVersions == nil {
		return "", fmt.Errorf("list_session_versions: this host does not expose the recovery lineage (no version probe registered)")
	}
	id, err := resolveInfoTarget(t.cfg, p.Target)
	if err != nil {
		return "", fmt.Errorf("list_session_versions: %w", err)
	}
	members, ok := t.cfg.SessionVersions(id.Scope, id.Workspace, id.TopicID, id.SessionPath)
	if !ok {
		return "", fmt.Errorf("list_session_versions: no version lineage is known for topic %q (single-version conversations have none)", id.TopicID)
	}
	out, _ := json.Marshal(map[string]any{
		"topicId":  id.TopicID,
		"count":    len(members),
		"versions": members,
		"content":  "none — use read_session_tail(target) for transcript bytes",
		"note":     "switch with adopt_session_version(target, version_id, confirm=true)",
	})
	return string(out), nil
}

// ── adopt_session_version ────────────────────────────────────────────────────

func NewAdoptSessionVersionTool(cfg SessionCollabConfig) tool.Tool {
	return adoptSessionVersionTool{cfg: cfg}
}

type adoptSessionVersionTool struct{ cfg SessionCollabConfig }

func (adoptSessionVersionTool) Name() string { return "adopt_session_version" }

func (adoptSessionVersionTool) Description() string {
	return "Switch a conversation's active version to the named recovery copy (task 285) — the host's existing recovery selection path, no new mechanism. Destructive-leaning: the conversation continues on the adopted copy. Requires confirm=true (the default refusal names the flag); list_session_versions first to pick the version id. Experimental."
}

func (adoptSessionVersionTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","description":"contact_id, topic_id, or exact title from list_addressable_sessions; omit for the calling session's own conversation."},"version_id":{"type":"string","description":"The versionId (or exact path) from list_session_versions."},"confirm":{"type":"boolean","description":"Must be true — the switch moves the conversation onto the named copy."}},"required":["version_id","confirm"]}`)
}

func (adoptSessionVersionTool) ReadOnly() bool { return false }

func (t adoptSessionVersionTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Target    string `json:"target"`
		VersionID string `json:"version_id"`
		Confirm   bool   `json:"confirm"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &p); err != nil {
			return "", fmt.Errorf("invalid args: %w", err)
		}
	}
	if strings.TrimSpace(p.VersionID) == "" {
		return "", fmt.Errorf("version_id is required (list_session_versions first)")
	}
	if !p.Confirm {
		return "", fmt.Errorf("refused: adopt_session_version moves the conversation onto the named copy — pass confirm=true (and pick version_id from list_session_versions)")
	}
	if t.cfg.AdoptSessionVersion == nil {
		return "", fmt.Errorf("adopt_session_version: this host does not expose version adoption (no adopt probe registered)")
	}
	id, err := resolveInfoTarget(t.cfg, p.Target)
	if err != nil {
		return "", fmt.Errorf("adopt_session_version: %w", err)
	}
	if err := t.cfg.AdoptSessionVersion(id.Scope, id.Workspace, id.TopicID, id.SessionPath, strings.TrimSpace(p.VersionID)); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"adopted":   true,
		"versionId": strings.TrimSpace(p.VersionID),
		"topicId":   id.TopicID,
	})
	return string(out), nil
}
