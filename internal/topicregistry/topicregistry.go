// Package topicregistry 是 topic 注册的进程内唯一索引（tl1208）。
//
// 1074 家族（1074/1095/1197）反复复发的根因是注册层没有唯一归属：同一
// topicId 可以同时挂在 desktop-projects.json 的 globalTopics 与某项目的
// topics 两个列表里，每个读侧（agent 地址簿、会话目录列表、桌面树）各自
// 用自带的启发式决定「哪行是真的」。1197 用三处 ad-hoc 收敛止了血；本包
// 把归属规则收成一张 map：topicId → Entry，结构上一个 id 只有一个归属。
//
// 分工（与既有真相源的关系）：
//   - desktop-projects.json 仍是唯一的持久真相源（1145 SSOT 方向不变）；
//   - 本包是它的派生索引（读加速 + 唯一归属裁决），持久化缓存文件只是
//     兜底读面（headless 进程免加载全量列表），永远可以丢弃重建；
//   - 写入只有 desktop 侧 registerTopic/unregisterTopic 两个收点（写口唯二），
//     它们改完权威文件后调用本包 Register/Unregister 同步索引。
//
// listindex 式兜底四要素（tl 1197 方案获批件）：
//  1. format 版本门：缓存文件 format 不等于 FormatVersion 时整份丢弃重建
//     （迁移回退环风险的对 half：老二进制读到新 format 同样丢弃——权威在
//     desktop-projects.json，缓存永远可重建，不构成环）；
//  2. stamp 过期：缓存携带权威源（desktop-projects.json）的 size+mtime
//     指纹，源变更后第一次直查前必定重建（重建先于直查）；
//  3. 原子写：缓存落盘走 tmp+rename（fileutil.ReplaceFile），torn 态不可见；
//  4. 规模阈值：条目数超过 MaxPersistEntries 时跳过缓存落盘（防每次写
//     注册的 IO 放大），内存索引照常服务。
package topicregistry

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"reasonix/internal/fileutil"
)

// FormatVersion 是缓存文件的格式版本。结构变更时递增：旧格式文件在版本门
// 处被整份丢弃并重建。
const FormatVersion = 1

// Scope 取值（与 desktop 侧 normalizeOrganizationTarget 一致）。
const (
	ScopeGlobal  = "global"
	ScopeProject = "project"
)

// MaxPersistEntries 是规模阈值：重建出的条目数超过它时跳过缓存落盘。
const MaxPersistEntries = 4096

// Entry 是一个 topicId 的唯一归属行。
type Entry struct {
	TopicID string `json:"topicId"`
	Scope   string `json:"scope"`
	Root    string `json:"root,omitempty"`
	Pinned  bool   `json:"pinned,omitempty"`
}

// Stamp 是权威源文件的 size+mtime 指纹（desktop configFileStamp 同约定；
// 源文件缺失 = 零值，首建后即视为已装载）。
type Stamp struct {
	Exists bool  `json:"exists"`
	Size   int64 `json:"size"`
	Mtime  int64 `json:"mtimeUnixNano"`
}

// ProjectTopics 是重建源里一个项目的 topic 列表。
type ProjectTopics struct {
	Root   string
	Topics []string
	Pinned []string
}

// SourceView 是权威源的一次快照（desktop 注入 loadProjectsFile 的投影）。
type SourceView struct {
	Global       []string
	GlobalPinned []string
	Projects     []ProjectTopics
	Deleted      map[string]bool
}

// cacheFile 是持久化缓存文件的结构（派生索引，可随时丢弃）。
type cacheFile struct {
	Format  int     `json:"format"`
	Stamp   Stamp   `json:"stamp"`
	Entries []Entry `json:"entries"`
}

var (
	mu sync.RWMutex
	// byID 是唯一归属 map：结构上保证一个 topicId 只有一行。
	byID map[string]Entry
	// stamp 是上次构建索引时权威源的指纹。
	stamp Stamp
	// loaded 区分「空注册表」与「尚未构建」。
	loaded bool
	// persistSkipWarned 保证超阈值告警每进程只打一次。
	persistSkipWarned bool

	// cfg 是包级配置（Configure 一次性写入；读路径无锁——写入发生在
	// desktop 启动早期、任何直查之前）。
	cfg config
)

type config struct {
	cachePath  string
	sourcePath string
	rebuild    func() SourceView
}

// Configure 设置缓存文件落点、权威源路径与重建源投影。desktop 启动早期
// 调用一次；headless/测试不调用时缓存禁用、OwnerOf 恒 miss（读侧回落
// 既有启发式）。
func Configure(cachePath, sourcePath string, rebuildSource func() SourceView) {
	mu.Lock()
	defer mu.Unlock()
	cfg = config{cachePath: cachePath, sourcePath: sourcePath, rebuild: rebuildSource}
	loaded = false // 强制下次直查前重建（重建先于直查）
}

// ResetForTest 恢复包级默认态（仅测试用）。
func ResetForTest() {
	mu.Lock()
	defer mu.Unlock()
	cfg = config{}
	byID = nil
	stamp = Stamp{}
	loaded = false
	persistSkipWarned = false
}

// OwnerOf 返回 topicId 的唯一归属行。直查前先做 stamp 过期检查（重建先于
// 直查）：源变更/缓存缺失/格式不符都会就地重建后再答。
func OwnerOf(topicID string) (Entry, bool) {
	topicID = NormalizeTopicID(topicID)
	if topicID == "" {
		return Entry{}, false
	}
	ensureFresh()
	mu.RLock()
	defer mu.RUnlock()
	if byID == nil {
		return Entry{}, false
	}
	e, ok := byID[topicID]
	return e, ok
}

// Snapshot 返回全部归属行（按 topicId 排序，稳定输出）。
func Snapshot() []Entry {
	ensureFresh()
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Entry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TopicID < out[j].TopicID })
	return out
}

// Register 在索引里登记/更新一个归属行并落缓存。desktop 写口在改完权威
// 文件后调用；内部先重建（源里已含本次写入），保证索引与文件一致。
// scope=global 时 root 规整为空；scope=project 时 root 必填。
func Register(scope, root, topicID string, pinned bool) error {
	scope, root, err := normalizeScopeRoot(scope, root)
	if err != nil {
		return err
	}
	topicID = NormalizeTopicID(topicID)
	if topicID == "" {
		return fmt.Errorf("topicregistry: topicID is required")
	}
	if cfg.rebuild == nil {
		return fmt.Errorf("topicregistry: register without a rebuild source is refused (index must stay anchored to the authoritative file)")
	}
	if err := rebuild(false); err != nil {
		return err
	}
	mu.Lock()
	if byID == nil {
		byID = map[string]Entry{}
	}
	byID[topicID] = Entry{TopicID: topicID, Scope: scope, Root: root, Pinned: pinned}
	persistLocked()
	mu.Unlock()
	return nil
}

// Unregister 从索引注销一个 topicId 并落缓存。幂等：不存在的 id 安静返回。
func Unregister(topicID string) error {
	topicID = NormalizeTopicID(topicID)
	if topicID == "" {
		return nil
	}
	if cfg.rebuild == nil {
		return fmt.Errorf("topicregistry: unregister without a rebuild source is refused")
	}
	if err := rebuild(false); err != nil {
		return err
	}
	mu.Lock()
	if _, existed := byID[topicID]; existed {
		delete(byID, topicID)
		persistLocked()
	}
	mu.Unlock()
	return nil
}

// Refresh 做 stamp 过期检查，源有变则重建。返回是否发生了重建。
// desktop 侧可主动调用（eager）；查询路径 ensureFresh 兜底（lazy）。
func Refresh() bool {
	changed, err := refreshIfStale()
	if err != nil {
		slog.Warn("topicregistry: refresh failed; serving last good index", "err", err)
		return false
	}
	return changed
}

// SyncFromSource 无条件从权威源全量重建索引并落缓存。desktop 写口在改盘
// 成功后调用一次：索引随即与文件一致（写后即同步，读侧直查不悬空）。
func SyncFromSource() error {
	if cfg.rebuild == nil {
		return fmt.Errorf("topicregistry: sync without a rebuild source is refused")
	}
	return rebuild(true)
}

// NormalizeTopicID 规整 topicId（trim）。导出给写口与读侧共用同一规整。
func NormalizeTopicID(s string) string { return strings.TrimSpace(s) }

// ensureFresh 保证「重建先于直查」：缓存缺失/源 stamp 变化 → 先重建。
func ensureFresh() {
	if _, err := refreshIfStale(); err != nil {
		slog.Warn("topicregistry: stale check failed; serving last good index", "err", err)
	}
}

func refreshIfStale() (bool, error) {
	if cfg.rebuild == nil {
		// 无重建源（headless）：一次性装载持久缓存兜底，此后不再判过期
		// （缓存新鲜度由 desktop 侧写口维护）。
		mu.RLock()
		isLoaded := loaded
		mu.RUnlock()
		if !isLoaded {
			mu.Lock()
			if !loaded {
				loadCacheLocked()
			}
			mu.Unlock()
		}
		return false, nil
	}
	current := sourceStamp()
	mu.RLock()
	cached, isLoaded := stamp, loaded
	mu.RUnlock()
	if isLoaded && cached == current {
		// size+mtime 指纹与 desktop configFileStamp 同约定：写口经原子替换
		// 落盘，替换后 mtime 必变，因此「未变 ⇒ 内容未变」在写口纪律下成立。
		return false, nil
	}
	if err := rebuild(true); err != nil {
		return false, err
	}
	return true, nil
}

// rebuild 从权威源重建唯一归属 map。persist=false 供写口路径复用
// （Register/Unregister 随后会再落一次缓存，避免双写）。
func rebuild(persist bool) error {
	if cfg.rebuild == nil {
		// 无源注入：尝试装载持久缓存（headless 只读兜底），没有就空表。
		mu.Lock()
		defer mu.Unlock()
		loadCacheLocked()
		return nil
	}
	entries := Dedupe(cfg.rebuild())
	next := make(map[string]Entry, len(entries))
	for _, e := range entries {
		next[e.TopicID] = e
	}
	current := sourceStamp()
	mu.Lock()
	defer mu.Unlock()
	byID = next
	stamp = current
	loaded = true
	if persist {
		persistLocked()
	}
	return nil
}

// Dedupe 把权威源的多列表投影收敛成唯一归属行：
//   - DeletedTopics 里的 id 不出门（墓碑优先级最高）；
//   - 同一 id 跨 scope 重复（1074 家族遗留泄露形态）时判给 project——
//     global 侧的重复行是「项目会话泄露进 Global」的影子（1074/1197 定性）；
//     写口漏斗生效后新写不会再产生跨 scope 重复；
//   - 空白 id 丢弃，pinned 取归属侧的置顶态。
//
// 导出以便 desktop 侧守卫测试直接验证裁决规则。
func Dedupe(view SourceView) []Entry {
	deleted := view.Deleted
	out := map[string]Entry{}
	// global 先放，project 后写覆盖（project 优先语义见上）。
	for _, id := range view.Global {
		id = NormalizeTopicID(id)
		if id == "" || deleted[id] {
			continue
		}
		out[id] = Entry{TopicID: id, Scope: ScopeGlobal, Pinned: containsID(view.GlobalPinned, id)}
	}
	for _, p := range view.Projects {
		root := NormalizeRoot(p.Root)
		if root == "" {
			continue
		}
		for _, id := range p.Topics {
			id = NormalizeTopicID(id)
			if id == "" || deleted[id] {
				continue
			}
			out[id] = Entry{TopicID: id, Scope: ScopeProject, Root: root, Pinned: containsID(p.Pinned, id)}
		}
	}
	entries := make([]Entry, 0, len(out))
	for _, e := range out {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].TopicID < entries[j].TopicID })
	return entries
}

// NormalizeRoot 规整项目根（路径分隔符统一 + trim；与 desktop
// normalizeProjectRoot 的路径语义对齐，缺 filepath.Abs 依赖——包不引
// desktop，绝对化由 desktop 侧在注入前完成）。
func NormalizeRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Clean(root)
}

// persistLocked 把索引原子落盘（tmp+rename）。调用方持写锁。
// 规模阈值：条目超限跳过落盘（每进程只告警一次），内存索引照常。
func persistLocked() {
	if cfg.cachePath == "" {
		return
	}
	if len(byID) > MaxPersistEntries {
		if !persistSkipWarned {
			persistSkipWarned = true
			slog.Warn("topicregistry: index exceeds persist threshold; cache file writes disabled for this process",
				"entries", len(byID), "threshold", MaxPersistEntries)
		}
		return
	}
	cf := cacheFile{Format: FormatVersion, Stamp: stamp, Entries: make([]Entry, 0, len(byID))}
	for _, e := range byID {
		cf.Entries = append(cf.Entries, e)
	}
	sort.Slice(cf.Entries, func(i, j int) bool { return cf.Entries[i].TopicID < cf.Entries[j].TopicID })
	b, err := json.Marshal(cf)
	if err != nil {
		slog.Warn("topicregistry: marshal cache", "err", err)
		return
	}
	dir := filepath.Dir(cfg.cachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Warn("topicregistry: cache dir", "err", err)
		return
	}
	tmp := cfg.cachePath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		slog.Warn("topicregistry: write cache tmp", "err", err)
		return
	}
	if err := fileutil.ReplaceFile(tmp, cfg.cachePath); err != nil {
		slog.Warn("topicregistry: replace cache", "err", err)
	}
}

// loadCacheLocked 装载持久缓存。format 版本门：格式不符/损坏/无 stamp
// 一律视为缺失（byID 保持 nil），读侧得到恒 miss 的空索引。
func loadCacheLocked() {
	if cfg.cachePath == "" {
		return
	}
	b, err := os.ReadFile(cfg.cachePath)
	if err != nil {
		return
	}
	var cf cacheFile
	if err := json.Unmarshal(b, &cf); err != nil || cf.Format != FormatVersion {
		// 版本门：旧/新格式或损坏文件整份丢弃——缓存永远可重建。
		return
	}
	next := make(map[string]Entry, len(cf.Entries))
	for _, e := range cf.Entries {
		e.TopicID = NormalizeTopicID(e.TopicID)
		if e.TopicID == "" {
			continue
		}
		e.Scope = strings.TrimSpace(e.Scope)
		if e.Scope == ScopeGlobal {
			e.Root = ""
		} else {
			e.Root = NormalizeRoot(e.Root)
		}
		next[e.TopicID] = e
	}
	byID = next
	stamp = cf.Stamp
	loaded = true
}

// sourceStamp 采权威源当前指纹。
func sourceStamp() Stamp {
	if cfg.sourcePath == "" {
		return Stamp{}
	}
	info, err := os.Stat(cfg.sourcePath)
	if err != nil {
		return Stamp{}
	}
	return Stamp{Exists: true, Size: info.Size(), Mtime: info.ModTime().UnixNano()}
}

func normalizeScopeRoot(scope, root string) (string, string, error) {
	scope = strings.TrimSpace(scope)
	switch scope {
	case ScopeGlobal:
		return ScopeGlobal, "", nil
	case ScopeProject:
		root = NormalizeRoot(root)
		if root == "" {
			return "", "", fmt.Errorf("topicregistry: workspaceRoot is required for project scope")
		}
		return ScopeProject, root, nil
	default:
		return "", "", fmt.Errorf("topicregistry: unsupported scope %q", scope)
	}
}

func containsID(list []string, id string) bool {
	for _, v := range list {
		if strings.TrimSpace(v) == id {
			return true
		}
	}
	return false
}
