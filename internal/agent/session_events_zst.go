package agent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/klauspost/compress/zstd"

	"reasonix/internal/secrets"
	"reasonix/internal/store"
)

// 任务 1172 L2 —— events 整文件 zstd 归档（照 codex rollout 的 .jsonl.zst 机制）。
//
// 解决的缺陷：over-cap 且 fold 不回本（EventsFoldProfitable == false）的会话，
// 折叠省不到 10% 被正确拒绝（频繁折叠防线，任务 339/1073），但拒绝后的动作是
// 「left in place」——最大最慢的会话永远原地不动（107MB 实测，hydrate:read
// 1418ms）。物理压缩是收益确定的手段：JSONL 文本 5~10×，压完远离 cap，天然
// 不会频繁触发；且压缩无损可逆（红线：不删历史，解出即逐字节还原）。
//
// 存储形态约定（与 store.SessionEventLogZst 的注释一致）：
//   - 明文 <id>.events.jsonl 与归档 <id>.events.jsonl.zst 稳态下只存在其一；
//   - 两者并存（压缩中途崩溃）时明文是事实源，归档视为待覆盖的残件；
//   - 任何读取与追加先走 ensureSessionEventLogPlain 物化回明文，读到的内容
//     与压缩前逐字节一致（zstd 无损 + 头部原字节数校验），因此内存基线
//     （logTail / CAS digest）不需要任何迁移。
//
// 并发契约：
//   - 压缩（CompressSessionEventLogZst）在 session 文件锁内执行——与所有保存
//     路径（withSessionSaveLocks 持同一把锁）互斥，「读明文→发归档→删明文」
//     窗口内不可能有追加（追加只发生在保存路径的锁内）；
//   - 物化不加锁：产出走同目录 tmp + fsync + 原子改名，内容与归档一致，并发
//     物化互不冲突；压缩持锁期间读者物化出的明文可能被压缩的收尾删除按路径
//     删掉（内容相同，无损失），探针侧以重读校验 + 一次重试兜底
//     （ensureSessionEventLogPlain）。写路径的物化全部发生在保存锁内，与压缩
//     天然互斥。
//
// 归档自描述：首行是一条 JSON 头（来源标记、原字节数、压缩时间、原因），
// 物化时先读掉再搬运其余字节——归档形态自带「这是什么、何时为何产生」
// （红线：可解释）。头部 plain_bytes 与物化产物尺寸强校验，不符即视为损坏。
//
// 开关：experimental_events_slim_oversize（默认关，档位可选）。关态下压缩
// 永不触发，也就没有任何归档需要物化——读路径只有一个额外 stat 的代价为零。

// sessionEventZstCompressFloor is the smallest plaintext worth archiving.
// The gate only fires far above this (cap 默认 50MiB)；这里是直接调用方的
// 防御性地板，避免误把小日志压出净负收益。
const sessionEventZstCompressFloor = 4 << 20

// sessionEventZstIOConcurrency keeps encode/decode single-threaded: the sweep
// runs at idle（100MiB 级 SpeedFastest ≈ 0.2s），多线程只会抢前台 IO。
const sessionEventZstIOConcurrency = 1

const (
	sessionEventZstSchemaVersion = 1
	sessionEventZstKind          = "reasonix-session-events-zst"
	sessionEventZstHeaderMax     = 4 << 10
)

// sessionEventZstHeader is the self-describing first line of an archive.
type sessionEventZstHeader struct {
	SchemaVersion int       `json:"schema_version"`
	Kind          string    `json:"kind"`
	PlainBytes    int64     `json:"plain_bytes"`
	CompressedAt  time.Time `json:"compressed_at"`
	Reason        string    `json:"reason,omitempty"`
}

// zstDecoderPool 复用解码器构造成本：每次物化/分类借出一个，release 时
// Reset(nil) 放回。tl1318 并发物化测试实证：旧的进程级单例在并发物化下
// 互踩——一方 Reset(nil) 放流会把另一方正在读的流置 nil（nil reader deref
// panic），一方 Reset(src) 换绑也会从他人脚下抽走数据流；物化按设计不加锁
// （「并发物化互不冲突」契约），必须靠每流独占解码器兑现。Pool 的借出/
// 归还天然一一配对，即单流独占。
var zstDecoderPool = sync.Pool{
	New: func() any {
		dec, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(sessionEventZstIOConcurrency),
			zstd.WithDecoderMaxMemory(uint64(sessionReplayMaxBytesHard)),
		)
		if err != nil {
			return nil // 非法 option 才会到这；调用侧退化路径每次现建。
		}
		return dec
	},
}

// sessionEventZstStreamDecoder binds a stream to a pooled decoder (or a fresh
// one when pool construction failed). release 必须**恰好调用一次**：借出的
// 池化解码器只允许 Reset(nil) 放回——对它调 Close 会毒化复用（后续 Reset
// 报 "decoder used after Close"，实测 2026-10-26）；现建解码器才 Close。
func sessionEventZstStreamDecoder(src io.Reader) (dec *zstd.Decoder, release func(), err error) {
	if pooled, _ := zstDecoderPool.Get().(*zstd.Decoder); pooled != nil {
		pooled.Reset(src)
		return pooled, func() {
			pooled.Reset(nil)
			zstDecoderPool.Put(pooled)
		}, nil
	}
	fresh, freshErr := zstd.NewReader(src, zstd.WithDecoderConcurrency(sessionEventZstIOConcurrency))
	if freshErr != nil {
		return nil, nil, freshErr
	}
	return fresh, fresh.Close, nil
}

// sessionEventLogPlainPath mirrors store.ResolveSessionEventLog: either input
// form (transcript path or resolved event-log path) normalizes here.
func sessionEventLogPlainPath(sessionPathOrLog string) string {
	return store.ResolveSessionEventLog(sessionPathOrLog)
}

// sessionEventLogZstPathFor returns the archive path for either input form.
func sessionEventLogZstPathFor(sessionPathOrLog string) string {
	plain := sessionEventLogPlainPath(sessionPathOrLog)
	if plain == "" {
		return ""
	}
	return strings.TrimSuffix(plain, store.SessionEventLogSuffix) + store.SessionEventLogZstSuffix
}

// SessionEventLogZstSize reports the at-rest footprint of a session's event
// log: the plaintext size when present, else the archive size, else 0. 门禁、
// 面板与 sweep 的「这个会话的 events 现在多大」共用这一个口径——归档态会话
// 以归档字节数参与超限判定（压完远离 cap ⇒ 判「未超限」⇒ 不再被碰）。
func SessionEventLogZstSize(sessionPath string) int64 {
	plain := sessionEventLogPlainPath(sessionPath)
	if plain != "" {
		if info, err := os.Stat(plain); err == nil && !info.IsDir() {
			return info.Size()
		}
	}
	zst := sessionEventLogZstPathFor(sessionPath)
	if zst == "" {
		return 0
	}
	if info, err := os.Stat(zst); err == nil && !info.IsDir() {
		return info.Size()
	}
	return 0
}

// ensureSessionEventLogPlain guarantees the plaintext log exists, materializing
// it from the archive when needed. Accepts either input form. 返回本次是否执行
// 了物化；既无明文也无归档（新会话）返回 (false, nil)。
//
// 这是全部读/写路径的透明解压收口：探针、重放、追加入口各调一次。压缩态会话
// 第一次被读/写时付一次解压（100MiB 级 ~0.2s，远低于同体量明文的读取），此后
// 与普通会话无异。
func ensureSessionEventLogPlain(sessionPathOrLog string) (bool, error) {
	plain := sessionEventLogPlainPath(sessionPathOrLog)
	if plain == "" {
		return false, nil
	}
	if _, err := os.Stat(plain); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	zst := sessionEventLogZstPathFor(sessionPathOrLog)
	if zst == "" {
		return false, nil
	}
	if _, err := os.Stat(zst); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	// 至多两轮：压缩的收尾删除可能恰好按路径删掉了本轮刚物化的明文（内容与
	// 归档相同，无数据损失）——重读发现缺失就再物化一次，此时压缩临界区必然
	// 已结束。两轮之后仍不稳才报错。
	for attempt := 0; attempt < 2; attempt++ {
		if err := materializeSessionEventLogZst(plain, zst); err != nil {
			return false, err
		}
		if _, err := os.Stat(plain); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	return false, fmt.Errorf("session events archive did not materialize: %s", plain)
}

// materializeSessionEventLogZst decompresses zst into plain via a staged
// tmp + fsync + atomic rename, then removes the archive. 崩溃安全：tmp 先落
// 盘、改名后归档才删；中途崩溃留下的「两者并存」由明文优先规则自愈。
// 解码失败（坏帧/截断/头不符）时把归档挪进 .damaged 保留现场，明文不落盘
// ——历史只剩这份残件时绝不静默丢弃，也绝不让保存路径误判「无日志」后另起
// 明文覆盖。
func materializeSessionEventLogZst(plain, zst string) error {
	src, err := os.Open(zst)
	if err != nil {
		return err
	}
	dec, releaseDec, setupErr := sessionEventZstStreamDecoder(src)
	if setupErr != nil {
		src.Close()
		preserveBrokenZst(zst, setupErr, "events")
		return fmt.Errorf("session events archive: create decoder: %w", setupErr)
	}
	// tl1318：头部解析与正文搬运共用同一个 bufio.Reader（readZstArchiveHeader
	// 只消费调用方传入的缓冲，不再自建再丢弃）。头部行会让 bufio 预读（tl1220
	// 同族 bug：生产形态归档实测预读吞掉 65,385B），这些字节只存在于 bufio.buf
	// 里、解码位置已越过它们——若回头走 dec.IOReadCloser() 续读正文，预读窗口
	// 内的字节无处可取，产物必然短于 header.PlainBytes，健康归档会被误改名
	// .damaged。正确性不得依赖编码器「头行独立成块」的块形巧合。
	buffered := bufio.NewReaderSize(dec.IOReadCloser(), 64<<10)
	header, err := readSessionEventZstHeader(buffered)
	if err != nil {
		// Windows 句柄纪律（同 compressSessionEventLogZstLocked）：先放开
		// 解码器、关掉自己的 src，再挪走坏归档——否则 rename 以「正由另一
		// 进程使用」失败。
		releaseDec()
		src.Close()
		preserveBrokenZst(zst, err, "events")
		return fmt.Errorf("session events archive header: %w", err)
	}
	// tl1318：物化临时文件用随机唯一名（os.CreateTemp，O_EXCL 无截断语义）
	// ——固定路径 + O_TRUNC 在并发物化下互相截断（实测：一方 rename 时另一
	// 方还顶着同名 tmp，「cannot find/being used by another process」或产物
	// 交错损坏）。同目录保证 rename 原子性不跨卷；CreateTemp 默认 0600。
	dst, dstErr := os.CreateTemp(filepath.Dir(plain), filepath.Base(plain)+".materializing-*")
	if dstErr != nil {
		releaseDec()
		src.Close()
		return dstErr
	}
	tmp := dst.Name()
	// 正文源是上面同一个 buffered：bufio.buf 里的预读字节必须先于解码器新解
	// 出的内容被消费（tl1318 修复陷阱之二——绕过 bufio 直取解码器必丢预读）。
	_, copyErr := io.Copy(dst, buffered)
	releaseDec()
	if copyErr == nil {
		copyErr = dst.Sync()
	}
	if closeErr := dst.Close(); copyErr == nil {
		copyErr = closeErr
	}
	// 解码收尾：src 到此读完了，显式关闭——后面 size 校验失败要挪走坏归档，
	// rename/remove 同样不能顶着未关句柄做（Windows）。
	src.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		preserveBrokenZst(zst, copyErr, "events")
		return fmt.Errorf("session events archive decode: %w", copyErr)
	}
	if header.PlainBytes > 0 {
		if info, statErr := os.Stat(tmp); statErr != nil || info.Size() != header.PlainBytes {
			_ = os.Remove(tmp)
			mismatch := fmt.Errorf("plain_bytes mismatch: header %d", header.PlainBytes)
			if statErr == nil {
				mismatch = fmt.Errorf("plain_bytes mismatch: header %d != materialized %d", header.PlainBytes, info.Size())
			}
			preserveBrokenZst(zst, mismatch, "events")
			return mismatch
		}
	}
	// Windows：并发改名同一目标时 MoveFileEx 的「替换已存在目标」不是原子
	// 互斥的，可能瞬时 Access denied（Unix rename 原子，重试永不触发）。有界
	// 重试吃掉竞态窗口——与 ensureSessionEventLogPlain 的两轮重试同款纪律。
	var renameErr error
	for attempt := 0; attempt < 3; attempt++ {
		if renameErr = os.Rename(tmp, plain); renameErr == nil {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 5 * time.Millisecond)
	}
	if renameErr != nil {
		_ = os.Remove(tmp)
		return renameErr
	}
	if err := os.Remove(zst); err != nil && !os.IsNotExist(err) {
		// 明文已就位，归档清理失败不阻塞读取——并存态明文优先，下次物化清。
		slog.Warn("session: events archive remove after materialize failed (plaintext wins)",
			"zst", zst, "err", secrets.RedactError(err))
	}
	return nil
}

// readSessionEventZstHeader consumes the self-describing first line and
// validates the kind marker. buffered 由调用方持有：读头后同一缓冲继续作为
// 正文源（tl1318）。
func readSessionEventZstHeader(buffered *bufio.Reader) (sessionEventZstHeader, error) {
	return readZstArchiveHeader(buffered, sessionEventZstKind)
}

// readZstArchiveHeader is the kind-parameterized header reader shared by the
// tl-1172 events archive and the tl-1174 transcript archive (same self
// describing first-line shape).
//
// tl1318 纪律（tl1220 同族判例：分类器自持 bufio）：本函数只消费调用方传入
// 的 *bufio.Reader，绝不内部自建——自建缓冲的预读（至多 64KiB）会在函数
// 返回时随缓冲一起丢弃，而 klauspost v1.20.1 的解码位置已经越过这些字节
// （Decoder.Read sync 模式按整块短读、async 模式循环填满 p，两种形态下预读
// 都不会回到解码流里）。调用方必须用同一个缓冲继续读正文，物化才能逐字节
// 完整。头部行 ≤ sessionEventZstHeaderMax，64KiB 窗口不会把整段正文滞留在
// 缓冲里付不起内存。
func readZstArchiveHeader(buffered *bufio.Reader, wantKind string) (sessionEventZstHeader, error) {
	var header sessionEventZstHeader
	line, err := buffered.ReadBytes('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			return header, errors.New("archive header line not terminated")
		}
		return header, err
	}
	if len(line) > sessionEventZstHeaderMax {
		return header, errors.New("archive header line too long")
	}
	if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &header); err != nil {
		return header, err
	}
	if header.Kind != wantKind {
		return header, fmt.Errorf("unknown archive kind %q", header.Kind)
	}
	return header, nil
}

// preserveBrokenZst moves an undecodable archive aside so the bytes stay
// available for manual recovery, mirroring the .damaged convention. label
// names the archive family in the log line ("events"/"transcript").
func preserveBrokenZst(zst string, cause error, label string) {
	damaged := zst + ".damaged"
	if err := os.Rename(zst, damaged); err != nil {
		slog.Warn("session: broken archive could not be moved aside",
			"family", label, "zst", zst, "err", secrets.RedactError(err))
		return
	}
	slog.Warn("session: broken archive moved aside (manual recovery possible)",
		"family", label, "zst", damaged, "cause", secrets.RedactError(cause))
}

// CompressSessionEventLogZst archives the plaintext event log to
// <id>.events.jsonl.zst and removes the plaintext, reporting byte sizes.
// tl 1172 L2 主入口：sweep 的三级降级在 fold 不回本时调用它。
//
// 串行化：整个压缩在 session 文件锁内（与保存路径互斥）。不取 lease——
// lease 的持有者是「绑定该会话的运行时」（打开的 tab 全程持有），空闲判定
// 是调用方的职责；文件锁保证的是「压缩期间没有追加落在读明文→删明文的
// 窗口里」，这是数据安全边界；lease 语义边界由调用方守。
func CompressSessionEventLogZst(sessionPath string) (before, after int64, err error) {
	plain := sessionEventLogPlainPath(sessionPath)
	if plain == "" {
		return 0, 0, fmt.Errorf("empty session event log path")
	}
	unlock, lockErr := lockSessionFile(sessionPath)
	if lockErr != nil {
		return 0, 0, fmt.Errorf("lock session file: %w", lockErr)
	}
	defer unlock()
	return compressSessionEventLogZstLocked(plain, sessionEventLogZstPathFor(sessionPath), "tl1172-slim-oversize")
}

// compressSessionEventLogZstLocked is the body with the session file lock
// already held; tests drive it directly for crash-window coverage.
func compressSessionEventLogZstLocked(plain, zst, reason string) (before, after int64, err error) {
	info, err := os.Stat(plain)
	if err != nil {
		if os.IsNotExist(err) {
			// 已是归档态（或日志不存在）：幂等返回当前归档尺寸。
			if zi, zerr := os.Stat(zst); zerr == nil {
				return 0, zi.Size(), nil
			}
			return 0, 0, os.ErrNotExist
		}
		return 0, 0, err
	}
	if info.IsDir() {
		return 0, 0, fmt.Errorf("session event log path is a directory: %s", plain)
	}
	before = info.Size()
	if before < sessionEventZstCompressFloor {
		return before, before, fmt.Errorf("session event log under compress floor (%d < %d bytes)", before, int64(sessionEventZstCompressFloor))
	}
	src, err := os.Open(plain)
	if err != nil {
		return before, 0, err
	}
	// 注意：src 必须在删除明文之前显式关闭——Windows 的 os.Open 不带
	// FILE_SHARE_DELETE，句柄未关时 os.Remove 会以「正由另一进程使用」失败
	// （实测 2026-10-26），归档退化为「两者并存」残件态。不用 defer。
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
		return before, 0, fmt.Errorf("session events archive: create encoder: %w", encErr)
	}
	header, err := json.Marshal(sessionEventZstHeader{
		SchemaVersion: sessionEventZstSchemaVersion,
		Kind:          sessionEventZstKind,
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
	// 发布前校验：从 tmp 解出头部，确认帧可读、原字节数对得上。校验失败
	// 丢弃残件保留明文——压缩失败绝不能演变成数据丢失。
	if err := validateZstArchiveKind(tmp, before, sessionEventZstKind); err != nil {
		_ = os.Remove(tmp)
		return before, before, fmt.Errorf("session events archive validation failed (plaintext kept): %w", err)
	}
	if err := os.Rename(tmp, zst); err != nil {
		_ = os.Remove(tmp)
		return before, before, err
	}
	// 归档已就位；此刻到删除明文之间没有追加（文件锁保证），删除后任何
	// 读/写都会先物化。删除失败：两者并存，明文优先规则让归档退化为待覆盖
	// 残件，下次压缩覆盖、下次物化清理——不阻塞、不重试。
	if rmErr := os.Remove(plain); rmErr != nil {
		slog.Warn("session: events archive published but plaintext remove failed (both kept, plaintext wins)",
			"plain", plain, "zst", zst, "err", secrets.RedactError(rmErr))
		return before, before, nil
	}
	zi, statErr := os.Stat(zst)
	if statErr != nil {
		return before, 0, statErr
	}
	slog.Info("session: oversized event log archived (zstd, lossless; materializes on next read/append)",
		"plain", plain, "zst", zst, "bytesBefore", before, "bytesAfter", zi.Size(),
		"reason", reason)
	return before, zi.Size(), nil
}

// validateZstArchive reopens a just-written archive and checks the header and
// the declared plaintext size round-trip. 尾部不整读（校验必须比压缩本身便宜）：
// 数据帧完整性由编码器 CRC（WithEncoderCRC）在写路径保证，这里确认头可解、
// 流可推进即可。
func validateZstArchive(path string, wantPlain int64) error {
	return validateZstArchiveKind(path, wantPlain, sessionEventZstKind)
}

// validateZstArchiveKind is the kind-parameterized form shared by the tl-1172
// and tl-1174 archives.
func validateZstArchiveKind(path string, wantPlain int64, wantKind string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	dec, decErr := zstd.NewReader(f, zstd.WithDecoderConcurrency(sessionEventZstIOConcurrency))
	if decErr != nil {
		return decErr
	}
	defer dec.Close()
	// tl1318：头部与探测同样共用一个 bufio（同 materialize 的纪律——探测
	// 续读若绕过缓冲，读到的是预读窗口之后的位置，探针失去代表性）。
	buffered := bufio.NewReaderSize(dec.IOReadCloser(), 64<<10)
	header, err := readZstArchiveHeader(buffered, wantKind)
	if err != nil {
		return err
	}
	if header.PlainBytes != wantPlain {
		return fmt.Errorf("header plain_bytes %d != %d", header.PlainBytes, wantPlain)
	}
	probe := make([]byte, 64)
	if _, err := io.ReadFull(buffered, probe); err != nil &&
		!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	return nil
}
