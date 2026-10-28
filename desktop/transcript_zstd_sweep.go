package main

import (
	"log/slog"
	"os"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/config"
	"reasonix/internal/secrets"
)

// tl 1174：冷会话主转录整文件 zstd 压缩的空闲 sweep（experimental_jsonl_zstd，
// 可选档默认关）。任务 921 events 空闲 sweep 同款骨架：不设条件的 ticker +
// 每 tick 现读开关（关=一次 config.Load 的代价、免重启），冷度由 agent 层
// 双保险复核（CompressSessionTranscriptZst 持文件锁后再核 mtime）。
//
// 与 921 的差异：本 sweep 没有「重入 park」——压缩是幂等形态迁移（稳态下
// 明文与 .zst 只存其一，压完的会话自然退出候选集），不存在「清完又清」。
// codex 先例的 MAX_CONCURRENT_COMPRESSION_JOBS=2 在此等价实现为串行 +
// 每 pass 限流（单 goroutine + maxPerPass），更保守。

const (
	// 首扫延迟：给启动期让路（配置加载、tab 恢复），也给「刚重启的会话」
	// 一个被 lease 保护的窗口（921 同值同理由）。
	transcriptZstdSweepFirstDelay = 5 * time.Minute
	// 常规 tick：冷压缩是低频事件（14 天阈值），30 分钟粒度绰绰有余
	// （921 同值）。
	transcriptZstdSweepTickInterval = 30 * time.Minute
	// 每 tick 处理上限：单文件压缩是全量读+编码+重命名+删除，串行限量防止
	// 升级后首扫面对大批量冷存量时一次性堆叠 IO（921 同值；存量 392 会话
	// 量级下收敛天数为个位数）。
	transcriptZstdSweepMaxPerPass = 3
)

// transcriptZstdSweepConfig is the per-tick knob set. 零值=关。
type transcriptZstdSweepConfig struct {
	enabled bool
}

func loadTranscriptZstdSweepConfig() transcriptZstdSweepConfig {
	cfg, err := config.Load()
	if err != nil {
		return transcriptZstdSweepConfig{}
	}
	return transcriptZstdSweepFromConfig(cfg)
}

// transcriptZstdSweepFromConfig is the knob judgment in one pure function
// (921 同款可测形态)：nil/err 读作全关。
func transcriptZstdSweepFromConfig(cfg *config.Config) transcriptZstdSweepConfig {
	if cfg == nil || !cfg.Desktop.ExperimentalJsonlZstd {
		return transcriptZstdSweepConfig{}
	}
	return transcriptZstdSweepConfig{enabled: true}
}

type transcriptZstdSweepLoop struct {
	a *App
}

// startTranscriptZstdSweepLoop boots the unconditioned ticker: the lab switch
// is read live on every tick, so OFF costs one config read and never touches a
// session, while switching on applies without a restart.
func (a *App) startTranscriptZstdSweepLoop() {
	l := &transcriptZstdSweepLoop{a: a}
	a.goSafe("transcriptZstdSweepLoop", l.run)
}

func (l *transcriptZstdSweepLoop) run() {
	timer := time.NewTimer(transcriptZstdSweepFirstDelay)
	defer timer.Stop()
	<-timer.C
	l.tick(time.Now())
	ticker := time.NewTicker(transcriptZstdSweepTickInterval)
	defer ticker.Stop()
	for now := range ticker.C {
		l.tick(now)
	}
}

// tick walks every known session directory and compresses cold candidates.
// A session is a candidate when: 明文在、mtime 冷（≥ agent.SessionTranscriptColdAfter）、
// 体积过地板（≥ agent.SessionTranscriptZstCompressFloor）、且不 busy（本进程
// tab 在开或外部 lease 在身都不碰——热会话绝不压缩，红线 1 的运行时面）。
func (l *transcriptZstdSweepLoop) tick(now time.Time) {
	knobs := loadTranscriptZstdSweepConfig()
	if !knobs.enabled {
		return
	}
	compressed, skipped := 0, 0
	for _, dir := range l.a.knownSessionDirs() {
		if compressed >= transcriptZstdSweepMaxPerPass {
			break
		}
		infos, err := agent.ListSessionOrder(dir)
		if err != nil {
			continue
		}
		for _, info := range infos {
			if compressed >= transcriptZstdSweepMaxPerPass {
				break
			}
			path := info.Path
			fi, statErr := os.Stat(path)
			if statErr != nil {
				continue // 归档态（明文缺失）：已压缩，无活可干
			}
			if age := now.Sub(fi.ModTime()); age < agent.SessionTranscriptColdAfter {
				continue
			}
			if fi.Size() < agent.SessionTranscriptZstCompressFloor {
				continue
			}
			// busy 门：本进程 tab 打开中，或外部运行时 lease 在身。
			// （tab 打开 ⇒ 刚被读过 ⇒ 也不会过冷门；这里是双保险。）
			if _, ctrl := l.a.controllerForSessionPath(path); ctrl != nil {
				skipped++
				continue
			}
			if leaseInfo, leaseErr := agent.LoadSessionLeaseInfo(path); leaseErr == nil && leaseInfo != nil {
				skipped++
				continue
			}
			before, after, compressErr := agent.CompressSessionTranscriptZst(path, now)
			if compressErr != nil {
				// 压缩失败回退明文（函数保证无半压缩态）；WARN 后继续下一候选。
				slog.Warn("desktop: transcript zstd sweep compress failed (plaintext kept)",
					"path", path, "err", secrets.RedactError(compressErr))
				skipped++
				continue
			}
			compressed++
			slog.Info("desktop: transcript zstd sweep archived cold session",
				"path", path, "bytesBefore", before, "bytesAfter", after,
				"freedBytes", before-after)
		}
	}
	if compressed > 0 || skipped > 0 {
		slog.Info("desktop: transcript zstd sweep pass done",
			"at", now.Format(time.RFC3339), "compressed", compressed, "skipped", skipped)
	}
}
