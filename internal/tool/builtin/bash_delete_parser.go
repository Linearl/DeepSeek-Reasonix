package builtin

// Host PowerShell delete parser (task 903, upstream 11782/PR11786 语义重实施).
// PowerShell 语法分析在宿主 powershell/pwsh 进程里做纯 AST 解析（见
// shellparse.PowerShellDeleteServer）。为摊薄进程启动成本，每个 shell 路径
// 复用一个常驻 worker（每请求一行 JSON）；超时/出错即杀进程、下次重建。
// 解析进程永不执行被分析的源码：stdin 是数据、-NoProfile -NonInteractive。

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"sync"
	"time"

	"reasonix/internal/proc"
	"reasonix/internal/sandbox"
	"reasonix/internal/secrets"
	"reasonix/internal/shellparse"
)

var errDeleteParser = errors.New("host shell parser unavailable")

type deleteParserProcess struct {
	cmd    *exec.Cmd
	input  io.WriteCloser
	output *bufio.Reader
}

type deleteParserWorker struct {
	token   chan struct{}
	process *deleteParserProcess
}

var deleteParsers = struct {
	sync.Mutex
	workers map[string]*deleteParserWorker
}{workers: make(map[string]*deleteParserWorker)}

// analyzePowerShellDelete parses one PowerShell command through the shared
// worker for sh.Path. Errors mean the analysis could not be trusted; callers
// must refuse execution (fail-closed).
func analyzePowerShellDelete(ctx context.Context, sh sandbox.Shell, command string) (shellparse.DeleteAnalysis, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deleteParsers.Lock()
	worker := deleteParsers.workers[sh.Path]
	if worker == nil {
		worker = &deleteParserWorker{token: make(chan struct{}, 1)}
		worker.token <- struct{}{}
		deleteParsers.workers[sh.Path] = worker
	}
	deleteParsers.Unlock()
	select {
	case <-ctx.Done():
		return shellparse.DeleteAnalysis{}, ctx.Err()
	case <-worker.token:
	}
	defer func() { worker.token <- struct{}{} }()
	if ctx.Err() != nil {
		return shellparse.DeleteAnalysis{}, ctx.Err()
	}
	if worker.process == nil {
		p, err := startDeleteParser(sh)
		if err != nil {
			return shellparse.DeleteAnalysis{}, errDeleteParser
		}
		worker.process = p
	}
	p := worker.process
	type response struct {
		data []byte
		err  error
	}
	done := make(chan response, 1)
	go func() {
		data, err := json.Marshal(command)
		if err == nil {
			_, err = p.input.Write(append(data, '\n'))
		}
		if err == nil {
			// The persistent protocol needs exactly one stdout line per request
			// so diagnostics cannot be mistaken for the next analysis response.
			data, err = p.output.ReadBytes('\n')
		}
		done <- response{data, err}
	}()
	var answer response
	select {
	case <-ctx.Done():
		stopDeleteParser(p)
		worker.process = nil
		<-done
		return shellparse.DeleteAnalysis{}, ctx.Err()
	case answer = <-done:
	}
	var analysis shellparse.DeleteAnalysis
	if answer.err != nil || json.Unmarshal(answer.data, &analysis) != nil || analysis.HostError {
		stopDeleteParser(p)
		worker.process = nil
		return shellparse.DeleteAnalysis{}, errDeleteParser
	}
	return analysis, nil
}

func startDeleteParser(sh sandbox.Shell) (*deleteParserProcess, error) {
	cmd := proc.Command(sh.Path, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", sandbox.PowerShellUTF8Script(shellparse.PowerShellDeleteServer))
	cmd.Env = secrets.ProcessEnv()
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		_ = input.Close()
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		_ = input.Close()
		_ = output.Close()
		return nil, err
	}
	return &deleteParserProcess{cmd: cmd, input: input, output: bufio.NewReader(output)}, nil
}

func stopDeleteParser(p *deleteParserProcess) {
	_ = p.input.Close()
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
}
