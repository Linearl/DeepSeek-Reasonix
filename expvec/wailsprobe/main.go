package main

// main.go —— wailsprobe 入口：tl1351 补差 a) 的 CGO 产物过 wails build 打包链验证件。
// 目的：证明「wails build 完整构建链（CGO_ENABLED=1 + mingw-w64 gcc）产出的桌面 exe，
// 能在真实分发形态（exe 同目录 onnxruntime.dll）下加载 dll 并跑通推理」。
// 构建命令与产品 release 工作流同款 flags：
//   wails build -clean -s -skipbindings -nopackage -platform windows/amd64
// 探针逻辑：OnStartup 里做 ORT 初始化 + 中文文本编码 + 内存读数，结果写 exe 同目录
// probe-result.json 后自退出（runtime.Quit），不产生交互。

import (
	"embed"
	goruntime "runtime"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// goRuntimeVersion：编译进 exe 的 Go 版本（供探针结果自证）。
func goRuntimeVersion() string { return goruntime.Version() }

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:             "wailsprobe (tl1351)",
		Width:             320,
		Height:            200,
		DisableResize:     true,
		AssetServer:       &assetserver.Options{Assets: assets},
		OnStartup:         app.startup,
		Bind:              []interface{}{app},
	})
	_ = err // 探针结果以 probe-result.json 为准；窗口启动失败也落盘
}
