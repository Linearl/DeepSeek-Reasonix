package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/klauspost/compress/zstd"

	"reasonix/internal/secrets"
	"reasonix/internal/store"
)

// tl 1174 —— 主转录冷会话整文件 zstd 压缩（experimental_jsonl_zstd，可选档默认关）。
//
// 与 tl 1172（events 超限驱动）不同，本件按「冷度」驱动：主转录 .jsonl 是发现
// 锚与兼容检查点（事件日志在位时为权威转录），但对一个长期不打开的会话，它的
// 明文字节没有任何读者——全库实测（2026-10-26）392 会话 1316MB，5~10× 压缩
// 可省约 1.0~1.2GB。热/冷分层红线（任务书，不可简化）：
//  1. 热会话绝不压缩——冷阈值内有活动的会话保持明文（zstd 整文件对活跃写入
//     是 IO 灾难：每次追加都要解压→改→重压）；
//  2. 冷会话（不活跃 ≥ sessionTranscriptColdAfter）整文件压缩为 .jsonl.zst；
//  3. 冷读透明——打开会话时 ensureSessionTranscriptPlain 先物化回明文，上层
//     读取链无感（照 tl 1172 的 limitsForSessionLog 收口先例）；
//  4. 恢复活跃即物化——写路径 publish 明文后清掉过期归档（writeSessionMessages
//     是全量重写，落盘即物化）；
//  5. 压缩失败回退明文 + WARN，绝不留半压缩态（tmp 先行 + 发布前校验）；
//  6. 开关关闭态 = 逐字节一致——不压、不建归档、无额外文件；读路径只多一次
//     存在性 stat（tl 1172 同款零代价口径）。
//
// 存储形态约定（与 store.SessionTranscriptZstSuffix 注释一致）：
//   - 明文 <id>.jsonl 与归档 <id>.jsonl.zst 稳态下只存在其一；
//   - 两者并存（压缩中途崩溃）时明文是事实源，归档视为待覆盖的残件；
//   - 归档自描述：首行 JSON 头（kind/plain_bytes/时间/原因），物化时先读掉
//     再搬运其余字节；plain_bytes 与物化产物尺寸强校验，不符视为损坏。
//
// 竞品对照（任务书要求注明）：codex rollout 的 compression.rs 同款机制——
// MIN_ROLLOUT_AGE=7 天冷判定、zstd level 3、编码后 verify、persist_noclobber
// 发布、发布后再核对文件状态、最后删明文；物化用 tmp+fsync+原子改名+删归档。
// 本实现沿用该分层模式；差异点：冷阈值取 14 天（见下）、压缩在 session 文件
// 锁内（codex 无对应锁语义，我们已有 withSessionSaveLocks 纪律）、串行执行
// （codex MAX_CONCURRENT_COMPRESSION_JOBS=2，我们经桌面空闲 sweep 单 goroutine
// 且每 pass 限流，等价更保守）。

// SessionTranscriptColdAfter 是冷判定阈值：转录 mtime 距今超过该时长视为冷。
//
// 取 14 天的依据（用户要求常量+论证）：
//   - 太小（<7 天）：周会话在「压缩⇄物化」间来回横跳——每次打开都付一次解压
//     （1172 实测 100MiB 级 ~0.2s，量级可接受但无收益：刚压完又被打开），且
//     打开即物化回明文，14 天内不再压缩，压缩收益集中在真正陈旧的会话上；
//   - 太大（>30 天）：收益下滑——全库扫描里 30 天内会话仍占活跃盘存相当比例，
//     1316MB 中冷存量随阈值上升递减；
//   - codex rollout 先例取 7 天（MIN_ROLLOUT_AGE），但其 rollout 是一次性落盘
//     文件（无「打开即物化」语义）；我们的会话打开频率更高，取 2× 先例值
//     降低横跳面，同时 392 会话的主存量大头（数月前的会话）不受影响。
const SessionTranscriptColdAfter = 14 * 24 * time.Hour

// SessionTranscriptZstCompressFloor is the smallest transcript worth archiving:
// the sweep only offers cold candidates above this size. 小于该值的转录压缩
// 收益接近零（归档头+帧开销吃掉大半），却要付一轮重命名+删除的文件系统噪声。
const SessionTranscriptZstCompressFloor = 1 << 20 // 1 MiB

// sessionTranscriptZstKind is the self-describing archive kind marker.
const sessionTranscriptZstKind = "reasonix-session-transcript-zst"

// sessionTranscriptZstPaths resolves the (plain, zst) pair for a session path.
func sessionTranscriptZstPaths(sessionPath string) (plain, zst string) {
	if sessionPath == "" {
		return "", ""
	}
	return sessionPath, store.SessionTranscriptZst(sessionPath)
}

// ensureSessionTranscriptPlain guarantees the plaintext transcript exists,
// materializing it from the archive when needed. 这是全部读路径的透明解压收口：
// loadSessionTranscript / loadSessionTranscriptTail 各调一次（1172 在
// limitsForSessionLog 收口的同款位置选择——所有读会话的入口都从这两处过）。
// 返回是否执行了物化；既无明文也无归档（新会话）返回 (false, nil)。
// 不加锁：产出走同目录 tmp + fsync + 原子改名，内容与归档一致（读锁语义下
// 与压缩的临界区互斥由调用方持有的 save 锁保证；无锁调用点的并发物化互不
// 冲突，见 tl 1172 同名函数的并发契约注释）。
func ensureSessionTranscriptPlain(sessionPath string) (bool, error) {
	plain, zst := sessionTranscriptZstPaths(sessionPath)
	if plain == "" {
		return false, nil
	}
	if _, err := os.Stat(plain); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if _, err := os.Stat(zst); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	// 至多两轮：压缩的收尾删除可能恰好按路径删掉了本轮刚物化的明文（内容
	// 与归档相同，无数据损失）——重读发现缺失就再物化一次。两轮之后仍不稳
	// 才报错（tl 1172 同款）。
	for attempt := 0; attempt < 2; attempt++ {
		if err := materializeSessionTranscriptZst(plain, zst); err != nil {
			return false, err
		}
		if _, err := os.Stat(plain); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, fmt.Errorf("session transcript archive did not materialize: %s", plain)
}

// materializeSessionTranscriptZst decompresses the archive into the plain
// transcript via a staged tmp + fsync + atomic rename, then removes the
// archive. 崩溃安全与坏档纪律照 tl 1172：tmp 先落盘、改名后归档才删；解码
// 失败（坏帧/截断/头不符）时归档挪进 .damaged 保留现场，明文不落盘。
func materializeSessionTranscriptZst(plain, zst string) error {
	src, err := os.Open(zst)
	if err != nil {
		return err
	}
	dec, releaseDec, setupErr := sessionEventZstStreamDecoder(src)
	if setupErr != nil {
		src.Close()
		preserveBrokenZst(zst, setupErr, "transcript")
		return fmt.Errorf("session transcript archive: create decoder: %w", setupErr)
	}
	header, err := readZstArchiveHeader(dec.IOReadCloser(), sessionTranscriptZstKind)
	if err != nil {
		// Windows 句柄纪律（同 compressSessionEventLogZstLocked）：先放开
		// 解码器、关掉自己的 src，再挪走坏归档。
		releaseDec()
		src.Close()
		preserveBrokenZst(zst, err, "transcript")
		return fmt.Errorf("session transcript archive header: %w", err)
	}
	tmp := plain + ".materializing"
	dst, dstErr := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if dstErr != nil {
		releaseDec()
		src.Close()
		return dstErr
	}
	_, copyErr := io.Copy(dst, dec.IOReadCloser())
	releaseDec()
	if copyErr == nil {
		copyErr = dst.Sync()
	}
	if closeErr := dst.Close(); copyErr == nil {
		copyErr = closeErr
	}
	src.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		preserveBrokenZst(zst, copyErr, "transcript")
		return fmt.Errorf("session transcript archive decode: %w", copyErr)
	}
	if header.PlainBytes > 0 {
		if info, statErr := os.Stat(tmp); statErr != nil || info.Size() != header.PlainBytes {
			_ = os.Remove(tmp)
			mismatch := fmt.Errorf("plain_bytes mismatch: header %d", header.PlainBytes)
			if statErr == nil {
				mismatch = fmt.Errorf("plain_bytes mismatch: header %d != materialized %d", header.PlainBytes, info.Size())
			}
			preserveBrokenZst(zst, mismatch, "transcript")
			return mismatch
		}
	}
	if err := os.Rename(tmp, plain); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Remove(zst); err != nil && !os.IsNotExist(err) {
		// 明文已就位，归档清理失败不阻塞读取——并存态明文优先，下次物化清。
		slog.Warn("session: transcript archive remove after materialize failed (plaintext wins)",
			"zst", zst, "err", secrets.RedactError(err))
	}
	return nil
}

// CompressSessionTranscriptZst archives a COLD plaintext transcript to
// <id>.jsonl.zst and removes the plaintext, reporting byte sizes. tl 1174
// 主入口：桌面空闲 sweep（experimental_jsonl_zstd 开关守门）调用。
//
// 串行化：整个压缩在 session 文件锁内（与保存路径互斥），保证「读明文→发
// 归档→删明文」窗口内没有追加落在明文上。冷度由调用方判定（本函数再核验
// 一次 mtime，双保险）；热门会话被误传入时按 not-cold 拒绝并返回原尺寸。
func CompressSessionTranscriptZst(sessionPath string, now time.Time) (before, after int64, err error) {
	plain, zst := sessionTranscriptZstPaths(sessionPath)
	if plain == "" {
		return 0, 0, fmt.Errorf("empty session transcript path")
	}
	unlock, lockErr := lockSessionFile(sessionPath)
	if lockErr != nil {
		return 0, 0, fmt.Errorf("lock session file: %w", lockErr)
	}
	defer unlock()
	return compressSessionTranscriptZstLocked(plain, zst, now, "tl1174-cold-sweep")
}

// compressSessionTranscriptZstLocked is the body with the session file lock
// already held; tests drive it directly.
func compressSessionTranscriptZstLocked(plain, zst string, now time.Time, reason string) (before, after int64, err error) {
	info, err := os.Stat(plain)
	if err != nil {
		if os.IsNotExist(err) {
			// 已是归档态（或转录不存在）：幂等返回当前归档尺寸。
			if zi, zerr := os.Stat(zst); zerr == nil {
				return 0, zi.Size(), nil
			}
			return 0, 0, os.ErrNotExist
		}
		return 0, 0, err
	}
	if info.IsDir() {
		return 0, 0, fmt.Errorf("session transcript path is a directory: %s", plain)
	}
	// 冷度双保险：调用方判冷之后 mtime 又前移（会话恰好被碰了）⇒ 拒绝。
	// 热会话绝不压缩（红线 1）。
	if age := now.Sub(info.ModTime()); age < SessionTranscriptColdAfter {
		return info.Size(), info.Size(), fmt.Errorf("session transcript not cold (age %s < %s)", age.Round(time.Second), SessionTranscriptColdAfter)
	}
	before = info.Size()
	if before < SessionTranscriptZstCompressFloor {
		return before, before, fmt.Errorf("session transcript under compress floor (%d < %d bytes)", before, int64(SessionTranscriptZstCompressFloor))
	}
	src, err := os.Open(plain)
	if err != nil {
		return before, 0, err
	}
	// Windows 句柄纪律：src 必须在删除明文之前显式关闭（os.Open 不带
	// FILE_SHARE_DELETE，实测未关句柄时 os.Remove 报「正由另一进程使用」）。
	tmp := zst + ".tmp"
	dst, dstErr := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if dstErr != nil {
		src.Close()
		return before, 0, dstErr
	}
	enc, encErr := zstd.NewWriter(dst,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithWindowSize(4<<20),
		zstd.WithEncoderCRC(true),
		zstd.WithEncoderConcurrency(sessionEventZstIOConcurrency),
	)
	if encErr != nil {
		dst.Close()
		_ = os.Remove(tmp)
		src.Close()
		return before, 0, fmt.Errorf("session transcript archive: create encoder: %w", encErr)
	}
	header, err := json.Marshal(sessionEventZstHeader{
		SchemaVersion: sessionEventZstSchemaVersion,
		Kind:          sessionTranscriptZstKind,
		PlainBytes:    before,
		CompressedAt:  time.Now().UTC(),
		Reason:        reason,
	})
	if err == nil {
		header = append(header, '\n')
		_, err = enc.Write(header)
	}
	if err == nil {
		_, err = io.Copy(enc, src)
	}
	if closeErr := src.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = enc.Close()
	} else {
		enc.Close()
	}
	if err == nil {
		err = dst.Sync()
	}
	if closeErr := dst.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return before, 0, err
	}
	// 发布前校验：头可解、原字节数对得上；失败丢弃残件保留明文。
	if err := validateZstArchiveKind(tmp, before, sessionTranscriptZstKind); err != nil {
		_ = os.Remove(tmp)
		return before, before, fmt.Errorf("session transcript archive validation failed (plaintext kept): %w", err)
	}
	// 归档保留原 mtime：mtime 是冷度事实源，压缩不得让会话「看起来刚活跃」，
	// 否则下次 sweep 的冷判定被归档自身的诞生时间污染。
	if err := os.Chtimes(tmp, info.ModTime(), info.ModTime()); err != nil {
		// 非 Windows 关键路径；失败仅降级（mtime=压缩时刻），记 WARN 不回退——
		// 回退（删归档重压）的代价比一个偏新的 mtime 高。
		slog.Warn("session: transcript archive could not preserve mtime (cold checks see compress time)",
			"zst", zst, "err", secrets.RedactError(err))
	}
	if err := os.Rename(tmp, zst); err != nil {
		_ = os.Remove(tmp)
		return before, before, err
	}
	// 归档已就位；文件锁保证此刻到删除明文之间没有追加。删除失败：两者并存，
	// 明文优先规则让归档退化为待覆盖残件——不阻塞、不重试。
	if rmErr := os.Remove(plain); rmErr != nil {
		slog.Warn("session: transcript archive published but plaintext remove failed (both kept, plaintext wins)",
			"plain", plain, "zst", zst, "err", secrets.RedactError(rmErr))
		return before, before, nil
	}
	zi, statErr := os.Stat(zst)
	if statErr != nil {
		return before, 0, statErr
	}
	slog.Info("session: cold transcript archived (zstd, lossless; materializes on next open)",
		"plain", plain, "zst", zst, "bytesBefore", before, "bytesAfter", zi.Size(),
		"reason", reason)
	return before, zi.Size(), nil
}

// cleanupStaleSessionTranscriptZst removes a transcript archive that no longer
// matches the plaintext-wins invariant: whenever a fresh plaintext exists, the
// archive is at best a stale residue of a pre-rewrite world (writeSessionMessages
// published newer bytes). Best-effort: a remove failure leaves both in place and
// plaintext keeps winning on every read.
func cleanupStaleSessionTranscriptZst(sessionPath string) {
	plain, zst := sessionTranscriptZstPaths(sessionPath)
	if plain == "" || zst == "" {
		return
	}
	// 常态（无归档）一次 stat 出口：保存是热路径，别先 stat 明文白付两次。
	if _, err := os.Stat(zst); err != nil {
		return // 无归档，无事可做
	}
	if _, err := os.Stat(plain); err != nil {
		return // 无明文 ⇒ 归档是唯一事实源，绝不能删
	}
	if err := os.Remove(zst); err != nil && !os.IsNotExist(err) {
		slog.Warn("session: stale transcript archive remove failed (plaintext wins)",
			"plain", plain, "zst", zst, "err", secrets.RedactError(err))
	}
}
