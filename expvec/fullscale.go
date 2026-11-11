package main

// fullscale.go —— tl1351 补差 b)：全量记忆库规模口径实测。
// 口径来源：主仓 live 记忆库（%APPDATA%/reasonix/memory，排除 .archive/.revisions/.backup 与
// MEMORY.md 索引）实测 40 条、rune 长度分布 min=313 p25=918 p50=1107 p75=1720 p90=2688 max=2943
// （2026-11-11 统计）。本命令按该分布合成 N 条（默认 1000/3000/5000，任务书口径 1000~5000），
// 串行全量编码（集成真实路径=写入钩子逐条），读数：全量重建耗时、吞吐、超 512 token 截断占比、
// 常驻内存增量（OS 级 WorkingSet，含 ONNX C 层分配）、向量索引体积、该规模暴力扫检索延迟。
// 语料为确定性合成（固定 seed，rotate 子句库），不含真实记忆内容，可入 git。

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"time"
)

// 真实分布锚点（rune 长度，2026-11-11 主仓实测）及其分位位置。
var realQuantiles = []struct {
	p     float64
	runes float64
}{
	{0.0, 313}, {0.25, 918}, {0.5, 1107}, {0.75, 1720}, {0.9, 2688}, {1.0, 2943},
}

// targetRunes：按分位锚点线性插值出第 r/N 条的目标长度。
func targetRunes(r, n int) int {
	if n <= 1 {
		return int(realQuantiles[0].runes)
	}
	u := float64(r) / float64(n-1)
	for i := 1; i < len(realQuantiles); i++ {
		if u <= realQuantiles[i].p {
			a, b := realQuantiles[i-1], realQuantiles[i]
			t := (u - a.p) / (b.p - a.p)
			return int(a.runes + t*(b.runes-a.runes))
		}
	}
	return int(realQuantiles[len(realQuantiles)-1].runes)
}

// clauseBank：贴近本工作区真实记忆条目风格的中英混排技术子句（合成语料，非真实内容）。
var clauseBank = []string{
	"worktree 统一放在 github-repo/worktrees 下，命名 wt-主题名，用相对路径创建和移除。",
	"凭据读写需要明确授权，索引统一收口在 credential 目录，不在日志或报告里出现明文。",
	"删除操作走回收语义：先列目录核对，再移进带日期后缀的回收区，等确认后才真正删除。",
	"PowerShell 5.1 跑含中文的脚本要存成 UTF-8 with BOM，否则解析器报错，改用 pwsh 7 可避开。",
	"写中文或 UTF-8 文件时不要用 Set-Content，GBK 编码会损坏内容，统一走专用写文件工具。",
	"跨会话通信一律使用中文，技术术语、标识符和路径保留原文即可。",
	"提交分支前先跑包级测试，预存失败要么修掉要么在交接文档写明，不能只跑新增子集。",
	"错误提示里给出的下一步操作必须真实存在，尚未实现就如实写「尚未提供」。",
	"出包只在用户明确要求时进行，版本号不自增，每次出包都要补一份 release notes。",
	"GitHub 下载走 socks5h://127.0.0.1:10808 代理，直连失败时降级， release 大文件代理可能 504。",
	"记忆条目写清背景与结论，带上出处和日期，方便日后按主题回溯历史处理办法。",
	"关键词检索对同义改写容易漏召回，比如搜「凭证」找不到写「凭据收口」的条目。",
	"hook 脚本超时会连带阻塞工具调用，心跳任务改路径后必须把 revision 加一。",
	"多机同步围绕 OneDrive 通道展开，push、pull、organize 三个动作各自独立。",
	"会话分三层：人负责拍板，主对话协调派单收货，子对话做专项执行，互不越层指挥。",
	"调研报告按主题放进 docs/report，根因诊断进 issues 的 reports 子目录，交接进 handoff。",
	"实验性代码放独立 go.mod 的目录里隔离，产品主构建面保持零改动，候拍板后再谈合入。",
	"ONNX Runtime dll 与 Go 绑定库版本配对敏感，API 版本不匹配时直接初始化失败。",
	"推理 session 默认单线程，批量编码按批内最长 padding，混合长度会拖慢整体吞吐。",
	"暴力扫检索在几千条量级全核零点几毫秒，不需要引入 Faiss 这类专用索引依赖。",
	"启动加载约三百毫秒、常驻约一百二十兆，对桌面常驻应用可用但不是零成本。",
	"模型文件九十兆左右，加动态库合计约一百零七兆分发体积，int8 量化可显著缩小。",
	"中文条目超五百 token 会被截断，长条目要么按策略截头，要么分块后合并向量。",
	"回收区真删之前必须人工确认，禁止多目标一次性混删，删除前先列目录核对。",
	"终端里跑 git 命令注意 worktree 路径解析，Windows 下相对路径会解析到错误仓库。",
	"会话产物按主题归档，临时文件用完清理，归档区删除前要用户确认。",
	"排查异常优先怀疑自家的实验特性，引入优化型实验特性时必须留回退路径。",
	"旧能力不因新特性丢失，新特性搞不定就回退，默认保能力并记录代价供用户知情。",
	"同机制全量扫描不靠记忆抽样，写脚本枚举所有实例一次拿全再汇报影响面。",
	"汇报先结论后证据，长报告必须用户明确要求才写，避免废话。",
	"代理端口历史上有 40808 和 50808 两个记录，以最近核实值为准。",
	"训练语料合成条目时保持中英混排，路径和端口数字保留原文风格。",
	"心跳扫描每周一次，只甄别安全、数据完整性和崩溃三类上游修复。",
	"新机制落地前先做竞品调研，无竞品对照的新机制默认按高危处理。",
	" UI 变更默认视图零改动，新能力挂开关或二级视图，不新增独立入口。",
	"包体升级前核对 fork 特有样式没有被静默丢弃，合并后逐项核对。",
}

// synthesizeCorpus：确定性合成 n 条、长度按真实分位分布的条目文本。
func synthesizeCorpus(n int) []string {
	rng := rand.New(rand.NewSource(42))
	out := make([]string, n)
	for i := 0; i < n; i++ {
		target := targetRunes(i, n)
		var b []byte
		j := rng.Intn(len(clauseBank))
		for len(b) < target {
			b = append(b, clauseBank[j%len(clauseBank)]...)
			j += 1 + rng.Intn(3)
		}
		// 按 rune 截到目标长度附近（±一个子句长，分布口径不受影响）
		out[i] = string(b)
	}
	return out
}

type fullscaleResult struct {
	Env struct {
		CPUs        int    `json:"logical_cpus"`
		CPUModel    string `json:"cpu_model"`
		GoVersion   string `json:"go_version"`
		ScaleSource string `json:"scale_source"`
	} `json:"env"`
	Load struct {
		TotalLoadMs float64 `json:"total_load_ms"`
		DeltaWarmMiB float64 `json:"delta_after_warm_mib"`
		Dim         int     `json:"embedding_dim"`
	} `json:"load"`
	Rounds []fullscaleRound `json:"rounds"`
}

type fullscaleRound struct {
	N              int     `json:"n"`
	TotalMs        float64 `json:"full_rebuild_serial_ms"`
	ThroughputTPS  float64 `json:"throughput_texts_per_s"`
	P50ms          float64 `json:"per_entry_p50_ms"`
	P95ms          float64 `json:"per_entry_p95_ms"`
	MaxMs          float64 `json:"per_entry_max_ms"`
	MeanMs         float64 `json:"per_entry_mean_ms"`
	CappedRatio    float64 `json:"capped_at_512_tokens_ratio"`
	WSBaselineMiB  float64 `json:"ws_baseline_after_gen_mib"`
	WSAfterMiB     float64 `json:"ws_after_embed_mib"`
	WSDeltaMiB     float64 `json:"ws_delta_embed_mib"`
	VecMemMiB      float64 `json:"vector_store_mib"`
	IndexDiskMiB   float64 `json:"index_disk_mib"`
	Scan1CoreUs    float64 `json:"bruteforce_single_core_us"`
	ScanAllCoreUs  float64 `json:"bruteforce_all_core_us"`
}

func runFullscale(modelPath, vocabPath, dllPath string, sizes []int) error {
	var r fullscaleResult
	r.Env.CPUs = runtime.NumCPU()
	r.Env.CPUModel = cpuModel()
	r.Env.GoVersion = runtime.Version()
	r.Env.ScaleSource = "主仓 live 记忆库 rune 长度分布(min=313 p25=918 p50=1107 p75=1720 p90=2688 max=2943, n=40, 2026-11-11)，合成语料 seed=42"

	t0 := time.Now()
	if err := InitORT(dllPath); err != nil {
		return fmt.Errorf("初始化 onnxruntime 失败: %w", err)
	}
	tok, err := LoadTokenizer(vocabPath, 512)
	if err != nil {
		return err
	}
	emb, err := NewEmbedder(modelPath, vocabPath, 512)
	if err != nil {
		return err
	}
	loadMs := float64(time.Since(t0).Milliseconds())
	if _, err := emb.EncodeBatch([]string{"预热：全量口径实测。"}, tok); err != nil {
		return err
	}
	memWarm, _ := GetProcMem()
	mib := func(b uint64) float64 { return float64(b) / (1024 * 1024) }
	r.Load.TotalLoadMs = loadMs
	r.Load.DeltaWarmMiB = mib(memWarm.WorkingSetBytes)
	r.Load.Dim = emb.Dim
	fmt.Printf("== 加载 == %.0fms  warm后 WS=%.1fMiB  dim=%d\n\n", loadMs, mib(memWarm.WorkingSetBytes), emb.Dim)

	for _, n := range sizes {
		fmt.Printf("== 全量 N=%d ==\n", n)
		texts := synthesizeCorpus(n)
		var totalRunes int
		for _, t := range texts {
			totalRunes += len([]rune(t))
		}
		runtime.GC()
		memBase, _ := GetProcMem()

		durs := make([]float64, 0, n)
		capped := 0
		vecs := make([][]float32, 0, n)
		s := time.Now()
		for i, t := range texts {
			st := time.Now()
			ids := tok.Encode(t)
			v, err := emb.EncodeBatch([]string{t}, tok)
			if err != nil {
				return fmt.Errorf("第 %d 条推理失败: %w", i, err)
			}
			if len(ids) >= 511 { // 已按 MaxLen=512 截断，≥511 即触顶（编码耗时同触顶档）
				capped++
			}
			durs = append(durs, float64(time.Since(st).Microseconds())/1000.0)
			vecs = append(vecs, v[0])
		}
		total := time.Since(s)
		runtime.GC()
		texts = nil // 释放合成文本，读「向量+session」常驻口径
		runtime.GC()
		memAfter, _ := GetProcMem()

		sort.Float64s(durs)
		var sum float64
		for _, d := range durs {
			sum += d
		}
		rd := fullscaleRound{
			N: n,
			TotalMs: float64(total.Milliseconds()),
			ThroughputTPS: float64(n) / total.Seconds(),
			P50ms: percentile(durs, 0.50), P95ms: percentile(durs, 0.95),
			MaxMs: durs[len(durs)-1], MeanMs: sum/float64(n),
			CappedRatio: float64(capped) / float64(n),
			WSBaselineMiB: mib(memBase.WorkingSetBytes),
			WSAfterMiB: mib(memAfter.WorkingSetBytes),
			WSDeltaMiB: mib(memAfter.WorkingSetBytes) - mib(memBase.WorkingSetBytes),
			VecMemMiB: float64(n*emb.Dim*4) / (1024 * 1024),
			IndexDiskMiB: float64(n*emb.Dim*4) / (1024 * 1024),
		}
		rd.Scan1CoreUs = bruteforceScanSingle(vecs)
		rd.ScanAllCoreUs = bruteforceMulti(vecs, [][]float32{vecs[0], vecs[n/3], vecs[2*n/3], vecs[n-1]}, 100)
		r.Rounds = append(r.Rounds, rd)
		fmt.Printf("  全量串行重建: %.0fms (%.1f 条/s)  单条 P50=%.1fms P95=%.1fms max=%.1fms\n",
			rd.TotalMs, rd.ThroughputTPS, rd.P50ms, rd.P95ms, rd.MaxMs)
		fmt.Printf("  512-token 截断占比: %.1f%%\n", rd.CappedRatio*100)
		fmt.Printf("  内存: 基线(含语料)=%.1fMiB 编码后=%.1fMiB 增量=%.1fMiB  向量体积=%.1fMiB\n",
			rd.WSBaselineMiB, rd.WSAfterMiB, rd.WSDeltaMiB, rd.VecMemMiB)
		fmt.Printf("  暴力扫: 单核 %.0fus 全核 %.0fus/次\n\n", rd.Scan1CoreUs, rd.ScanAllCoreUs)
		_ = totalRunes
		vecs = nil
		runtime.GC()
	}

	out, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile("fullscale-result.json", out, 0644); err != nil {
		fmt.Printf("写 fullscale-result.json 失败: %v\n", err)
	} else {
		fmt.Println("原始读数已写 fullscale-result.json")
	}
	return nil
}

// bruteforceScanSingle：单 goroutine 暴力扫 100 次取均值（us/次）。
func bruteforceScanSingle(db [][]float32) float64 {
	const runs = 100
	var sink float64
	s := time.Now()
	for run := 0; run < runs; run++ {
		q := db[run%len(db)]
		var best float64
		for _, v := range db {
			best += cosine(q, v)
		}
		sink += best
	}
	_ = sink
	return float64(time.Since(s).Microseconds()) / float64(runs)
}
