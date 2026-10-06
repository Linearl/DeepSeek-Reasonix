package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"reasonix/internal/baseproc"
)

func TestClassifyBaseServeArgs(t *testing.T) {
	serveArgv := []string{"base", "serve", "--stdio"}
	tests := []struct {
		name    string
		args    []string
		serve   bool
		handled bool
	}{
		{name: "spawn 契约的精确形态", args: serveArgv, serve: true, handled: true},
		{name: "spawn 形态（参数序不变，多余标志照走 flag 解析）", args: []string{"base", "serve", "--stdio", "--stdio"}, serve: true, handled: true},
		{name: "缺 --stdio 走用法错误", args: []string{"base", "serve"}, serve: false, handled: true},
		{name: "未知标志走用法错误", args: []string{"base", "serve", "--bogus"}, serve: false, handled: true},
		{name: "base 无子命令走用法错误", args: []string{"base"}, serve: false, handled: true},
		{name: "base 非 serve 子命令走用法错误", args: []string{"base", "status"}, serve: false, handled: true},
		{name: "空 argv 与本分发无关", args: nil, serve: false, handled: false},
		{name: "GUI 正常启动与本分发无关", args: []string{"launch", "--detach"}, serve: false, handled: false},
		{name: "safe-mode 与本分发无关", args: []string{"--safe-mode"}, serve: false, handled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serve, handled := classifyBaseServeArgs(tt.args, io.Discard)
			if serve != tt.serve || handled != tt.handled {
				t.Fatalf("classifyBaseServeArgs(%q) = (%v, %v), want (%v, %v)",
					tt.args, serve, handled, tt.serve, tt.handled)
			}
		})
	}
}

// GUI 启动面（含远程窗口票据前缀）绝不能被 base 分发截走——截走即远程窗口
// 子进程静默退出，用户只会看到「窗口没弹出来」。
func TestMaybeRunBaseServeLeavesGuiLaunchArgsAlone(t *testing.T) {
	guiArgv := [][]string{
		nil,
		{"--safe-mode"},
		{"launch"},
		{"--detach"},
		{remoteWindowTicketArgPrefix + "ticket-1"},
		{remoteWindowHostArgPrefix + "abcd1234"},
		{remoteWindowOwnerArgPrefix + "0123456789abcdef0123456789abcdef"},
		{remoteWindowParentArgPrefix + "4242"},
		{"launch", "--detach", "--safe-mode"},
	}
	for _, args := range guiArgv {
		if _, handled := maybeRunBaseServe(args); handled {
			t.Fatalf("maybeRunBaseServe(%q) claimed GUI launch argv", args)
		}
	}
}

func TestMaybeRunBaseServeUsageErrorsExit2(t *testing.T) {
	for _, args := range [][]string{
		{"base"},
		{"base", "status"},
		{"base", "serve"},
		{"base", "serve", "--bogus"},
	} {
		code, handled := maybeRunBaseServe(args)
		if !handled || code != 2 {
			t.Fatalf("maybeRunBaseServe(%q) = (%d, %v), want (2, true)", args, code, handled)
		}
	}
}

// 拦截时机是正确性的一部分：base 分发必须在 installDesktopLogging 之前——
// fd 2 此刻仍是父进程传入的 base.log 句柄（F2），desktop.log 不能截走子进程
// 诊断；也必须在单实例锁等任何 GUI 机制之前。源码守护与
// TestLifecycleDiagnosticsUsePreWailsOwnershipGate 同款。
func TestBaseServeDispatchRunsBeforeDesktopLogging(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	beforeLogging, _, ok := strings.Cut(string(source), "installDesktopLogging()")
	if !ok {
		t.Fatal("main.go no longer installs desktop logging")
	}
	if !strings.Contains(beforeLogging, "maybeRunBaseServe(os.Args[1:])") {
		t.Fatal("base serve dispatch must run before installDesktopLogging (fd 2 must stay the parent-passed base.log handle)")
	}
}

// 进程内驱动同一段分发体（runBaseServe → baseproc.RunStdioServer）：hello
// 握手、ping、stdin EOF 干净退出（D4 孤儿路径）。这是桌面 exe 对 spawn 契约
// 应答什么的真行为断言，不依赖真 spawn 二进制。
func TestRunBaseServeSpeaksV1AndExitsCleanOnEOF(t *testing.T) {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errBuf := &bytes.Buffer{}
	done := make(chan int, 1)
	go func() { done <- runBaseServe(context.Background(), inR, outW, errBuf) }()

	sendRequest := func(t *testing.T, id int, method string, params any) {
		t.Helper()
		raw, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(baseproc.Frame{
			JSONRPC: baseproc.JSONRPCVersion,
			ID:      json.RawMessage(strconv.Itoa(id)),
			Method:  method,
			Params:  raw,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := baseproc.WriteFrame(inW, payload); err != nil {
			t.Fatalf("write %s request: %v", method, err)
		}
	}
	readResponse := func(t *testing.T) baseproc.Frame {
		t.Helper()
		payload, err := baseproc.ReadFrame(outR)
		if err != nil {
			t.Fatalf("read frame: %v", err)
		}
		var frame baseproc.Frame
		if err := json.Unmarshal(payload, &frame); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if frame.Error != nil {
			t.Fatalf("%s answered with rpc error %d: %s", frame.Method, frame.Error.Code, frame.Error.Message)
		}
		return frame
	}

	// hello：真服务进程带 session face，capability 里应有 sessions。
	sendRequest(t, 1, baseproc.MethodHello, baseproc.HelloParams{
		ProtocolVersion: baseproc.ProtocolVersion,
		ClientPID:       os.Getpid(),
	})
	helloFrame := readResponse(t)
	var hello baseproc.HelloResult
	if err := json.Unmarshal(helloFrame.Result, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.ProtocolVersion != baseproc.ProtocolVersion {
		t.Fatalf("hello protocol_version = %d, want %d", hello.ProtocolVersion, baseproc.ProtocolVersion)
	}
	if hello.ServerVersion == "" {
		t.Fatal("hello must carry the server build identity")
	}
	if !containsString(hello.Capabilities, baseproc.CapSessions) {
		t.Fatalf("real serve subprocess must advertise %q, got %v", baseproc.CapSessions, hello.Capabilities)
	}

	// ping：管理器健康循环赖以判活的应答。
	sendRequest(t, 2, baseproc.MethodPing, nil)
	pingFrame := readResponse(t)
	var ping baseproc.PingResult
	if err := json.Unmarshal(pingFrame.Result, &ping); err != nil {
		t.Fatal(err)
	}
	if !ping.OK {
		t.Fatal("ping must answer ok")
	}

	// stdin EOF（父进程死亡）→ 干净退出码 0（D4 孤儿路径）。
	if err := inW.Close(); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 {
		t.Fatalf("runBaseServe exit code = %d after stdin EOF, want 0", code)
	}
	_ = outW.Close()
}

func containsString(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
