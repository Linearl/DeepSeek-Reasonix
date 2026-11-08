package main

// bench.go —— 四项实测：①加载+内存 ②单条延迟 ③批量吞吐 ④磁盘+检索粗估。
// 所有读数同时打人读格式与 bench-result.json（原始数据入 commit）。

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"time"

	"golang.org/x/sys/windows/registry"
)

type latencyStat struct {
	Chars int     `json:"chars"`
	Tokens int    `json:"tokens"`
	P50ms float64 `json:"p50_ms"`
	P95ms float64 `json:"p95_ms"`
	MinMs float64 `json:"min_ms"`
	MaxMs float64 `json:"max_ms"`
	MeanMs float64 `json:"mean_ms"`
	Runs  int     `json:"runs"`
}

type benchResult struct {
	Env struct {
		CPUs      int    `json:"logical_cpus"`
		CPUModel  string `json:"cpu_model"`
		GoVersion string `json:"go_version"`
		EP        string `json:"execution_provider"`
	} `json:"env"`
	Load struct {
		DllEnvInitMs     float64 `json:"dll_env_init_ms"`
		SessionCreateMs  float64 `json:"session_create_ms"`
		TotalLoadMs      float64 `json:"total_load_ms"`
		WSBeforeMiB      float64 `json:"workingset_before_mib"`
		WSAfterLoadMiB   float64 `json:"workingset_after_load_mib"`
		WSAfterWarmMiB   float64 `json:"workingset_after_warm_mib"`
		DeltaLoadMiB     float64 `json:"delta_load_mib"`
		DeltaWarmMiB     float64 `json:"delta_after_warm_mib"`
		PagefileAfterMiB float64 `json:"pagefile_after_warm_mib"`
		Dim              int     `json:"embedding_dim"`
		InputNames       []string `json:"input_names"`
		OutputName       string  `json:"output_name"`
		CLSMode          bool    `json:"cls_pooling"`
	} `json:"load"`
	Single []latencyStat `json:"single_latency"`
	Batch  struct {
		N            int     `json:"n"`
		TotalBatchMs float64 `json:"batch_one_shot_ms_median"`
		SerialMs     float64 `json:"serial_total_ms_median"`
		BatchTPS     float64 `json:"batch_throughput_texts_per_s"`
		SerialTPS    float64 `json:"serial_throughput_texts_per_s"`
		Rounds       int     `json:"rounds"`
	} `json:"batch"`
	Bruteforce []struct {
		N            int     `json:"n"`
		Dim          int     `json:"dim"`
		SingleCoreUs float64 `json:"single_core_us_mean"`
		MultiCoreUs  float64 `json:"all_core_us_mean"`
		Runs         int     `json:"runs"`
	} `json:"bruteforce_scan"`
	Disk struct {
		ModelMiB float64 `json:"model_onnx_mib"`
		DllMiB   float64 `json:"onnxruntime_dll_mib"`
		VocabKiB float64 `json:"vocab_kib"`
		IndexMiBPer1k float64 `json:"index_raw_mib_per_1000_vecs"`
	} `json:"disk"`
}

func cpuModel() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return "unknown"
	}
	defer k.Close()
	v, _, err := k.GetStringValue("ProcessorNameString")
	if err != nil {
		return "unknown"
	}
	return v
}

func fileSizeMB(path string) float64 {
	st, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return float64(st.Size()) / (1024 * 1024)
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p * float64(len(sorted)-1))
	return sorted[idx]
}

func runBench(modelPath, vocabPath, dllPath string) error {
	var r benchResult
	r.Env.CPUs = runtime.NumCPU()
	r.Env.CPUModel = cpuModel()
	r.Env.GoVersion = runtime.Version()
	r.Env.EP = "CPU (default, 1 thread ORT session default settings)"

	memBefore, err := GetProcMem()
	if err != nil {
		return fmt.Errorf("内存读数失败: %w", err)
	}

	// ① 加载：dll+env
	t0 := time.Now()
	if err := InitORT(dllPath); err != nil {
		return fmt.Errorf("初始化 onnxruntime 失败: %w", err)
	}
	tEnv := time.Since(t0)

	// ① session 创建（含模型读盘+反序列化）
	t1 := time.Now()
	tok, err := LoadTokenizer(vocabPath, 512)
	if err != nil {
		return err
	}
	emb, err := NewEmbedder(modelPath, vocabPath, 512)
	if err != nil {
		return err
	}
	tSess := time.Since(t1)

	memAfterLoad, _ := GetProcMem()

	// warmup：首次真实推理（触发 arena/内存映射等一次性开销）
	if _, err := emb.EncodeBatch([]string{"预热：记忆检索向量层成本验证。"}, tok); err != nil {
		return fmt.Errorf("warmup 推理失败: %w", err)
	}
	memAfterWarm, _ := GetProcMem()

	mib := func(b uint64) float64 { return float64(b) / (1024 * 1024) }
	r.Load.DllEnvInitMs = float64(tEnv.Milliseconds())
	r.Load.SessionCreateMs = float64(tSess.Milliseconds())
	r.Load.TotalLoadMs = float64(tEnv+tSess) / 1e6
	r.Load.WSBeforeMiB = mib(memBefore.WorkingSetBytes)
	r.Load.WSAfterLoadMiB = mib(memAfterLoad.WorkingSetBytes)
	r.Load.WSAfterWarmMiB = mib(memAfterWarm.WorkingSetBytes)
	r.Load.DeltaLoadMiB = r.Load.WSAfterLoadMiB - r.Load.WSBeforeMiB
	r.Load.DeltaWarmMiB = r.Load.WSAfterWarmMiB - r.Load.WSBeforeMiB
	r.Load.PagefileAfterMiB = mib(memAfterWarm.PagefileBytes)
	r.Load.Dim = emb.Dim
	r.Load.InputNames = emb.InputNames
	r.Load.OutputName = emb.OutputNames[0]
	r.Load.CLSMode = emb.CLSMode

	fmt.Printf("== ① 加载 ==\n环境初始化(dll+env): %.1fms  session创建(含读盘): %.1fms  合计: %.1fms\n", r.Load.DllEnvInitMs, r.Load.SessionCreateMs, r.Load.TotalLoadMs)
	fmt.Printf("内存 WorkingSet: before=%.1fMiB afterLoad=%.1fMiB(+%.1f) afterWarm=%.1fMiB(+%.1f)  Pagefile(afterWarm)=%.1fMiB\n",
		r.Load.WSBeforeMiB, r.Load.WSAfterLoadMiB, r.Load.DeltaLoadMiB, r.Load.WSAfterWarmMiB, r.Load.DeltaWarmMiB, r.Load.PagefileAfterMiB)
	fmt.Printf("向量维度=%d 输入=%v 输出=%s CLS pooling=%v\n", r.Load.Dim, r.Load.InputNames, r.Load.OutputName, r.Load.CLSMode)

	// ② 单条延迟：4 个长度档
	fmt.Printf("\n== ② 单条延迟 ==\n")
	for _, samp := range benchSamples {
		ids := tok.Encode(samp.text)
		const warm, runs = 5, 60
		for i := 0; i < warm; i++ {
			if _, err := emb.EncodeBatch([]string{samp.text}, tok); err != nil {
				return err
			}
		}
		durs := make([]float64, 0, runs)
		for i := 0; i < runs; i++ {
			s := time.Now()
			if _, err := emb.EncodeBatch([]string{samp.text}, tok); err != nil {
				return err
			}
			durs = append(durs, float64(time.Since(s).Microseconds())/1000.0)
		}
		sort.Float64s(durs)
		var sum float64
		for _, d := range durs {
			sum += d
		}
		st := latencyStat{
			Chars: samp.label, Tokens: len(ids),
			P50ms: percentile(durs, 0.50), P95ms: percentile(durs, 0.95),
			MinMs: durs[0], MaxMs: durs[len(durs)-1],
			MeanMs: sum/float64(runs), Runs: runs,
		}
		r.Single = append(r.Single, st)
		fmt.Printf("  ~%d字档(实际%d字/%dtokens): P50=%.2fms P95=%.2fms min=%.2f max=%.2f mean=%.2f (n=%d)\n",
			st.Chars, st.Chars, st.Tokens, st.P50ms, st.P95ms, st.MinMs, st.MaxMs, st.MeanMs, st.Runs)
	}

	// ③ 批量吞吐：100 条
	fmt.Printf("\n== ③ 批量吞吐(100条) ==\n")
	texts := make([]string, 100)
	for i := 0; i < 100; i++ {
		texts[i] = benchSamples[i%len(benchSamples)].text
	}
	const batchRounds, serialRounds = 5, 3
	// 3a. batch=100 一次推理
	var batchDurs []float64
	for i := 0; i < batchRounds; i++ {
		s := time.Now()
		if _, err := emb.EncodeBatch(texts, tok); err != nil {
			return err
		}
		batchDurs = append(batchDurs, float64(time.Since(s).Microseconds())/1000.0)
	}
	sort.Float64s(batchDurs)
	r.Batch.N = 100
	r.Batch.Rounds = batchRounds
	r.Batch.TotalBatchMs = batchDurs[len(batchDurs)/2]
	// 3b. 串行 100 单条
	var serialDurs []float64
	for i := 0; i < serialRounds; i++ {
		s := time.Now()
		for _, t := range texts {
			if _, err := emb.EncodeBatch([]string{t}, tok); err != nil {
				return err
			}
		}
		serialDurs = append(serialDurs, float64(time.Since(s).Microseconds())/1000.0)
	}
	sort.Float64s(serialDurs)
	r.Batch.SerialMs = serialDurs[len(serialDurs)/2]
	r.Batch.BatchTPS = 1000.0 * 100 / r.Batch.TotalBatchMs
	r.Batch.SerialTPS = 1000.0 * 100 / r.Batch.SerialMs
	fmt.Printf("  batch=100 单次推理(中位,%d轮): %.1fms (%.0f 条/s)\n  逐条串行(中位,%d轮): %.1fms (%.0f 条/s)\n",
		batchRounds, r.Batch.TotalBatchMs, r.Batch.BatchTPS, serialRounds, r.Batch.SerialMs, r.Batch.SerialTPS)

	// ④ 检索粗估：暴力扫（点积）
	fmt.Printf("\n== ④ 检索暴力扫粗估(dim=%d) ==\n", r.Load.Dim)
	// 真实向量底料
	seedTexts := make([]string, 50)
	for i := range seedTexts {
		seedTexts[i] = benchSamples[i%len(benchSamples)].text
	}
	seedVecs, err := emb.EncodeBatch(seedTexts, tok)
	if err != nil {
		return err
	}
	dim := len(seedVecs[0])
	scanN := []int{100, 500, 2000, 5000}
	const scanRuns = 100
	for _, n := range scanN {
		// 用真实向量循环填充到 n 条
		db := make([][]float32, n)
		for i := range db {
			db[i] = seedVecs[i%len(seedVecs)]
		}
		// 单核：单 goroutine
		var sumSink float64
		s := time.Now()
		for run := 0; run < scanRuns; run++ {
			q := seedVecs[run%len(seedVecs)]
			var best float64
			for _, v := range db {
				best += cosine(q, v) // 累加全部防死码消除
			}
			sumSink += best
		}
		el1 := float64(time.Since(s).Microseconds()) / float64(scanRuns)
		// 全核：goroutine 分片
		el2 := bruteforceMulti(db, seedVecs, scanRuns)
		r.Bruteforce = append(r.Bruteforce, struct {
			N            int     `json:"n"`
			Dim          int     `json:"dim"`
			SingleCoreUs float64 `json:"single_core_us_mean"`
			MultiCoreUs  float64 `json:"all_core_us_mean"`
			Runs         int     `json:"runs"`
		}{n, dim, el1, el2, scanRuns})
		fmt.Printf("  N=%d: 单核 %.0fus/次  全核(%d workers) %.0fus/次\n", n, el1, runtime.NumCPU(), el2)
	}

	// 磁盘
	fmt.Printf("\n== 磁盘 ==\n")
	r.Disk.ModelMiB = fileSizeMB(modelPath)
	r.Disk.DllMiB = fileSizeMB(dllPath)
	r.Disk.VocabKiB = fileSizeMB(vocabPath) * 1024
	r.Disk.IndexMiBPer1k = float64(dim*4*1000) / (1024 * 1024)
	fmt.Printf("  model.onnx=%.1fMiB  onnxruntime.dll=%.1fMiB  vocab.txt=%.0fKiB\n", r.Disk.ModelMiB, r.Disk.DllMiB, r.Disk.VocabKiB)
	fmt.Printf("  原始向量索引: %.2fMiB/千条(float32×%d维, 暴力扫无需额外索引结构)\n", r.Disk.IndexMiBPer1k, dim)

	out, _ := json.MarshalIndent(r, "", "  ")
	if err := os.WriteFile("bench-result.json", out, 0644); err != nil {
		fmt.Printf("写 bench-result.json 失败: %v\n", err)
	} else {
		fmt.Println("\n原始读数已写 bench-result.json")
	}
	return nil
}

// bruteforceMulti：全核并发暴力扫，返回单次均值(us)。
func bruteforceMulti(db [][]float32, qs [][]float32, runs int) float64 {
	workers := runtime.NumCPU()
	start := time.Now()
	for run := 0; run < runs; run++ {
		q := qs[run%len(qs)]
		var wg sync.WaitGroup
		chunk := (len(db) + workers - 1) / workers
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(lo, hi int) {
				defer wg.Done()
				var s float64
				if lo > len(db) {
					return
				}
				if hi > len(db) {
					hi = len(db)
				}
				for _, v := range db[lo:hi] {
					s += cosine(q, v)
				}
			}(w*chunk, (w+1)*chunk)
		}
		wg.Wait()
	}
	return float64(time.Since(start).Microseconds()) / float64(runs)
}
