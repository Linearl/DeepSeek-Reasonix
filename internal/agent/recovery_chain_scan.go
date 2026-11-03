package agent

import (
	"os"
	"time"

	"reasonix/internal/provider"
)

// tl1355：DAG 索引删除后（tl1280）的链信息内容扫描合成。
//
// 链合并/比较信息（条数/轮次/预览/最后活动/覆盖关系）原本由 schema-2 事件
// 日志的 DAG 重放给出；tl1227 读侧单路径化 + tl1280 慢车道退役后，
// ListSessionHeads 成为恒空垫片，面板候选链全部退化成 0·0+无预览。本文件
// 把链关系改为**会话文件内容扫描合成**：每个候选转录文件本身就是一条完整
// 的链（schema-1 单链形态），读数直接从内容来；链与链之间的关系用既有的
// digest/前缀覆盖判定（SessionContentSnapshot.Covers，与合并引擎同一套存
// 储等价判定）标出「谁包含谁」。扫描成本（整文件重放）比读索引高——任务
// 书明示可接受，且 LoadSessionContentSnapshot 的 path|digest 缓存让面板反
// 复展开同一会话零重付。

// ChainScanMaxBytes 是链扫描愿意为单个候选文件付出的读取上限。超过它的文
// 件跳过整文件解析，纯行计数填条数后按 Degraded 降级——与面板 heads 路径
// （panelHeadsReplayMaxBytes，256 MiB）同档语义：只拦病态大文件，不拦正
// 常大日志。var 而非 const：测试收窄它来验证降级路径。
var ChainScanMaxBytes int64 = 256 << 20

// ChainScanTimeBudget 是单组候选扫描的累计时间预算（tl1355 协调线输入②，
// 用户 20261103 定 10s）：超时即停整文件解析，余行以纯行计数降级——面板
// 展开一个多副本会话最坏等 ~10s + 单文件一次解析的量级，而不是无限期。
// var 而非 const：测试收窄它来验证降级路径。
var ChainScanTimeBudget = 10 * time.Second

// ChainScanView 是一个候选转录文件的内容扫描读数。Path 之外的字段分两档：
// Loadable（严格快照可载）时全部读数可信且可参与覆盖判定；仅 Readable
// （tolerant 兜底读出）时计数真实但未经验证，行面以 Degraded 标注；两者
// 皆否时只剩文件身份（计数全零）——面板保留该行而不是隐藏。
type ChainScanView struct {
	Path         string
	MessageCount int
	Turns        int
	Preview      string
	LastActivity time.Time
	// Digest 是严格快照的内容指纹（仅 Loadable 时非空）。
	Digest string
	// Loadable：严格快照可载（未脏化、事件日志未损）——读数可作为覆盖判定
	// 与合并决策的依据。
	Loadable bool
	// Readable：至少 tolerant 兜底读出了内容——计数真实（可能来自残缺日志
	// 的可读前缀），面板照实展示并标 Degraded。
	Readable bool
	// CoveredBy 指向内容严格覆盖本文件的另一候选（前缀覆盖判定）。空 =
	// 没有候选包含本文件的全部内容。仅 Loadable 候选参与判定（fail-closed：
	// 残缺日志不得产生覆盖声明）。
	CoveredBy string
}

// PreviewTurns returns the session preview (newest recognizable user turn,
// tail-first) and the user-turn count — the same readings a DAG-era head
// record carried, now derived from the transcript content itself.
func (s SessionContentSnapshot) PreviewTurns() (string, int) {
	return SessionPreviewFromMessages(s.messages)
}

// Digest returns the snapshot's content fingerprint (hex), as recorded by
// LoadSessionContentSnapshot. Ties catalog lineage decisions and chain scans
// to the same content identity.
func (s SessionContentSnapshot) Digest() string { return s.digest }

// LastActivityTime walks the snapshot from the end for the newest message
// carrying a local timestamp (CreatedAt, unix milliseconds). Messages without
// timestamps contribute nothing; a transcript with none reports the zero time
// and the caller falls back to the file mtime — a real observation either
// way, never an invented one.
func (s SessionContentSnapshot) LastActivityTime() time.Time {
	return lastMessageActivity(s.messages)
}

// lastMessageActivity scans backwards for the first non-zero CreatedAt.
func lastMessageActivity(msgs []provider.Message) time.Time {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].CreatedAt != 0 {
			return time.UnixMilli(msgs[i].CreatedAt)
		}
	}
	return time.Time{}
}

// countTranscriptLines counts the checkpoint's message lines without parsing
// any JSON — the cheap reading that keeps degraded rows' counts real when the
// full parse is out of budget (协调线输入①/⑤：纯行计数 ~0.1s/86MB，全量解
// 析的 1/10)。The checkpoint is one message per line (writeSessionMessages
// encodes per message), so lines ≈ the strict snapshot's Len including the
// leading system line. A file that cannot be read counts zero — an identity
// row, not an invented number.
func countTranscriptLines(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	count := 0
	partial := false
	buf := make([]byte, 256<<10)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			for _, b := range buf[:n] {
				if b == '\n' {
					count++
					partial = false
				} else {
					partial = true
				}
			}
		}
		if err != nil {
			break
		}
	}
	if partial {
		count++
	}
	return count
}

// TolerantChainReadings counts and previews a transcript with the tolerant
// loader — the path damaged or unnormalized copies take when the strict
// snapshot refuses them. ok is false when the tolerant loader cannot read the
// file either; the caller then shows a file-identity-only row instead of
// inventing numbers.
func TolerantChainReadings(path string) (messageCount, turns int, preview string, ok bool) {
	msgs := tolerantReplay(path)
	if msgs == nil {
		return 0, 0, "", false
	}
	preview, turns = SessionPreviewFromMessages(msgs)
	return len(msgs), turns, preview, true
}

// ScanSessionChainViews reads every candidate transcript once and returns its
// content-scan readings, then marks the coverage relations between them: a
// view is CoveredBy the candidate whose content strictly contains its own
// (the same leading-system-strip and storage equality the merge engine's
// Covers uses, so a scan can never call a branch "covered" that the merge
// would not, or the reverse).
//
// The caller passes the main transcript first: among equal-length mutual
// covers (two files with byte-identical content), the earlier candidate wins
// the coverer role, so the main file is preferred as the container — matching
// how the merge prefers the main as the default winner.
//
// Only strictly loadable candidates participate in coverage (fail-closed, the
// same rule SessionContentCovers applies): a damaged log must not produce a
// coverage claim, but it still shows its tolerant readings with the row
// marked degraded. Files over ChainScanMaxBytes degrade to identity rows
// without any content read — the panel keeps the row, never hangs the page.
//
// tl1355 协调线输入②（用户 20261103 定）：扫描有 10 秒时间预算——单组累计
// 超时即停，余下候选不再做整文件解析，改用纯行计数（不解析 JSON，实测
// 86MB 全量解析 1.09s vs 纯计数 ~0.1s 量级）填真实条数后按 Degraded 降级
// 行展示。整文件重放（严格快照、tolerant 兜底）仍是条数/轮次/预览的权威
// 来源，但只在预算内进行。
func ScanSessionChainViews(paths []string) []ChainScanView {
	views := make([]ChainScanView, 0, len(paths))
	start := time.Now()
	for _, path := range paths {
		v := ChainScanView{Path: path}
		var modTime time.Time
		if info, err := os.Stat(path); err == nil {
			modTime = info.ModTime()
		}
		// 时间预算：超时后停止整文件解析，余行以纯计数降级（计数仍真实，
		// 轮次/预览/覆盖不可得）。预算检查逐候选进行——单个大文件的一次解
		// 析（实测 86MB ≈ 1.1s）是最大超冲量，面板不会被任何单文件挂死。
		if time.Since(start) > ChainScanTimeBudget {
			v.MessageCount = countTranscriptLines(path)
			v.LastActivity = modTime
			views = append(views, v)
			continue
		}
		if info, err := os.Stat(path); err == nil && info.Size() > ChainScanMaxBytes {
			// 病态大文件：不做整文件解析（与 heads 面板预算同语义），纯计数
			// 填条数后降级——计数仍真实，不再是无读数的身份行。
			v.MessageCount = countTranscriptLines(path)
			v.LastActivity = modTime
			views = append(views, v)
			continue
		}
		snap, ok := LoadSessionContentSnapshot(path)
		if ok {
			preview, turns := snap.PreviewTurns()
			v.MessageCount = snap.Len()
			v.Turns = turns
			v.Preview = preview
			v.LastActivity = snap.LastActivityTime()
			if v.LastActivity.IsZero() {
				// 转录里没有可用的消息时间戳（本地元数据可被旧版本剥离）：
				// 文件修改时间是仍然真实的最后活动观察，不编造、不留零值。
				v.LastActivity = modTime
			}
			v.Digest = snap.Digest()
			v.Loadable = true
			v.Readable = true
			views = append(views, v)
			continue
		}
		// 严格快照拒绝（脏化/事件日志损）：预算内先试 tolerant 兜底。读数真
		// 实（可能是残缺日志的可读前缀），Loadable=false 让覆盖判定跳过它。
		if time.Since(start) <= ChainScanTimeBudget {
			if count, turns, preview, tok := TolerantChainReadings(path); tok {
				v.MessageCount = count
				v.Turns = turns
				v.Preview = preview
				// tolerantReplay 有缓存（TolerantChainReadings 刚付过重放），这里
				// 的第二次取用零额外成本；文件在扫描中途消失时拿到 nil，零时间
				// 落到 mtime 兜底。
				v.LastActivity = lastMessageActivity(tolerantReplay(path))
				if v.LastActivity.IsZero() {
					v.LastActivity = modTime
				}
				v.Readable = true
			}
		}
		if !v.Readable {
			// tolerant 也读不出（或预算已尽）：纯行计数填条数——降级行的计数
			// 仍真实（不解析 JSON），轮次/预览不可得，面板照实标 Degraded。
			v.MessageCount = countTranscriptLines(path)
			if v.LastActivity.IsZero() {
				v.LastActivity = modTime
			}
		}
		views = append(views, v)
	}
	markChainCoverage(views)
	return views
}

// markChainCoverage marks, for each strictly loadable view, the strongest
// coverer among the other candidates: most messages wins, candidate order
// breaks ties (main first — see ScanSessionChainViews). A zero-message file
// covers nothing, so two empty files never mark each other.
func markChainCoverage(views []ChainScanView) {
	snapshots := make(map[string]SessionContentSnapshot, len(views))
	for i := range views {
		if !views[i].Loadable {
			continue
		}
		if snap, ok := LoadSessionContentSnapshot(views[i].Path); ok {
			snapshots[views[i].Path] = snap
		}
	}
	for i := range views {
		if !views[i].Loadable {
			continue
		}
		covered, ok := snapshots[views[i].Path]
		if !ok {
			continue
		}
		best := -1
		bestLen := 0
		for j := range views {
			if j == i || !views[j].Loadable {
				continue
			}
			coverer, ok := snapshots[views[j].Path]
			if !ok || coverer.Len() == 0 || coverer.Len() < covered.Len() {
				continue
			}
			if !coverer.Covers(covered) {
				continue
			}
			// 等长互覆盖（两份字节同内容的文件）：只标后出现的那个为被覆盖，
			// 先出现的（调用方传主转录在先）保住容器身份。
			if coverer.Len() == covered.Len() && j > i {
				continue
			}
			if best == -1 || coverer.Len() > bestLen {
				best, bestLen = j, coverer.Len()
			}
		}
		if best >= 0 {
			views[i].CoveredBy = views[best].Path
		}
	}
}
