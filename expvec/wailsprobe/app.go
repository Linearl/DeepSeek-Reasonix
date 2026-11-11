package main

// app.go —— 探针逻辑：wails OnStartup 钩子内完成 ORT 加载+推理+内存读数，
// 结果写 exe 同目录 probe-result.json，然后自退出。
// 模型路径从环境变量 WAILSPROBE_MODEL 读（模型不进安装包，运行时给绝对路径，
// 与产品集成口径一致：dll 随 exe 分发、模型放数据目录按绝对路径加载）。
// dll 的加载是 PE 导入表层级的（CGO -lonnxruntime 产生导入项），进程启动时
// Windows loader 就要找到 onnxruntime.dll——这正是本探针要验证的分发硬约束。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type probeResult struct {
	WailsBuilt     bool   `json:"wails_built"`   // true=本进程确实由 wails.Run 启动
	GoVersion      string `json:"go_version"`    // 编译进 exe 的 Go 版本
	DllPathUsed    string `json:"dll_path_used"` // SetSharedLibraryPath 传入的 dll 路径
	ProcessStarted bool   `json:"process_started"` // exe 跑起来=无链接期 dll 硬依赖（yalue 走运行时 LoadLibrary）
	DllLoadOk      bool   `json:"dll_load_ok"`     // InitializeEnvironment 成功=LoadLibrary 找到 dll
	ModelPath      string `json:"model_path"`
	SessionLoadMs  int64  `json:"session_load_ms"` // dll+env+session 创建
	InferOk        bool   `json:"infer_ok"`
	InferMs        int64  `json:"infer_ms"`
	VecDim         int    `json:"vec_dim"`
	VecHead        []float64 `json:"vec_head"` // 前 8 维（证明真实推理而非全零）
	WSAfterMiB     float64   `json:"ws_after_mib"`
	Error          string `json:"error,omitempty"`
	Note           string `json:"note,omitempty"`
}

type App struct {
	ctx context.Context
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go func() {
		res := runProbe()
		writeResult(res)
		runtime.Quit(ctx)
	}()
}

func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func runProbe() probeResult {
	r := probeResult{WailsBuilt: true, GoVersion: goRuntimeVersion()}
	r.DllPathUsed = os.Getenv("WAILSPROBE_DLL")
	if r.DllPathUsed == "" {
		r.DllPathUsed = "onnxruntime.dll" // 默认=按 Windows loader 搜索顺序找 exe 同目录
	}
	r.ModelPath = os.Getenv("WAILSPROBE_MODEL")
	// yalue/onnxruntime_go 在 Windows 用 syscall.LoadLibrary 运行时加载（无 PE 导入表依赖）：
	// 进程能起来=无链接期硬依赖；InitializeEnvironment 成功=运行时找到 dll。
	r.ProcessStarted = true

	t0 := time.Now()
	if err := InitORT(r.DllPathUsed); err != nil {
		r.Error = fmt.Sprintf("ORT env init: %v", err)
		return r
	}
	r.DllLoadOk = true
	emb, err := NewEmbedder(r.ModelPath, os.Getenv("WAILSPROBE_VOCAB"), 512)
	if err != nil {
		r.Error = fmt.Sprintf("session create: %v", err)
		return r
	}
	r.SessionLoadMs = time.Since(t0).Milliseconds()
	tok, err := LoadTokenizer(os.Getenv("WAILSPROBE_VOCAB"), 512)
	if err != nil {
		r.Error = fmt.Sprintf("tokenizer: %v", err)
		return r
	}
	t1 := time.Now()
	vec, err := emb.EncodeBatch([]string{"wails 构建链探针：中文记忆条目编码验证。"}, tok)
	r.InferMs = time.Since(t1).Milliseconds()
	if err != nil {
		r.Error = fmt.Sprintf("infer: %v", err)
		return r
	}
	r.InferOk = true
	r.VecDim = len(vec[0])
	head := make([]float64, 8)
	for i := 0; i < 8 && i < len(vec[0]); i++ {
		head[i] = float64(vec[0][i])
	}
	r.VecHead = head
	if m, err := GetProcMem(); err == nil {
		r.WSAfterMiB = float64(m.WorkingSetBytes) / (1024 * 1024)
	}
	r.Note = "由 wails build -clean -s -skipbindings -nopackage -platform windows/amd64 产出（CGO_ENABLED=1, mingw-w64 gcc 16.2）"
	return r
}

func writeResult(res probeResult) {
	out, _ := json.MarshalIndent(res, "", "  ")
	p := filepath.Join(exeDir(), "probe-result.json")
	if err := os.WriteFile(p, out, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", p, err)
	}
}
