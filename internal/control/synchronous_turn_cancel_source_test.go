package control

// 任务1017 批B（2026-10-20）：context canceled 必须带取消源（验收判据②）。
// 现场：空闲开轮桥的 turn 13 秒后死于 context canceled，日志只有结果——
// 存储在 c.cancel 上的取消句柄被谁按下、无从得知。三态钉死：
//   1. 经存储 cancel 的外部取消 → 错误附 [cancel-source: external cancel()
//      caller] + 调用方栈（栈里能看到 Cancel 的入口）；
//   2. 未经存储句柄的取消（内部子 ctx / 上游 ctx 消亡）→ 错误明说来源不在
//      存储句柄，不再让裸的 "context canceled" 悬案；
//   3. 正常完成 → 错误为零值，绝不附加标注（finish 走裸 cancel，不打点）。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
)

// TestRunSynchronousTurnCancelSourceNamesExternalCaller（判据② 主用例）：
// turn 运行中经 c.Cancel()（用户停止语义）取消，返回错误必须携带
// cancel-source 标注且栈里指认 Cancel 调用方（本测试函数名可检索）。
func TestRunSynchronousTurnCancelSourceNamesExternalCaller(t *testing.T) {
	c := New(Options{Sink: event.FuncSink(func(event.Event) {})})
	t.Cleanup(c.Close)

	started := make(chan struct{})
	release := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- c.runSynchronousTurn(context.Background(), nil, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			close(release)
			return ctx.Err()
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("turn never started")
	}
	c.Cancel()
	err := <-errCh
	<-release
	if err == nil {
		t.Fatal("a canceled turn must return an error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error must still satisfy errors.Is(context.Canceled), got %v", err)
	}
	if !strings.Contains(err.Error(), "[cancel-source: external cancel() caller]") {
		t.Fatalf("external cancel must be named in the error, got %v", err)
	}
	if !strings.Contains(err.Error(), "TestRunSynchronousTurnCancelSourceNamesExternalCaller") {
		t.Fatalf("the captured stack must name the cancel caller, got %v", err)
	}
}

// TestRunSynchronousTurnCancelSourceNamesUnattributedShape：run 返回
// context.Canceled 但存储 cancel 从未触发（模拟内部子 ctx 消亡冒泡）→
// 错误明说「取消未经存储句柄」，不留悬案。
func TestRunSynchronousTurnCancelSourceNamesUnattributedShape(t *testing.T) {
	c := New(Options{Sink: event.FuncSink(func(event.Event) {})})
	t.Cleanup(c.Close)

	err := c.runSynchronousTurn(context.Background(), nil, func(ctx context.Context) error {
		// 内部某层用自有 ctx 包住失败再冒泡：错误是 context.Canceled，
		// 但本 turn 的存储 cancel 从未被按下。
		return fmt.Errorf("inner stage: %w", context.Canceled)
	})
	if err == nil {
		t.Fatal("expected the propagated cancellation error")
	}
	if !strings.Contains(err.Error(), "[cancel-source: stored cancel never fired") {
		t.Fatalf("an unattributed cancellation must be said out loud, got %v", err)
	}
}

// TestRunSynchronousTurnSuccessCarriesNoCancelAnnotation：正常完成不附加
// cancel-source 标注（finish 的裸 cancel 不是取消源）。
func TestRunSynchronousTurnSuccessCarriesNoCancelAnnotation(t *testing.T) {
	c := New(Options{Sink: event.FuncSink(func(event.Event) {})})
	t.Cleanup(c.Close)

	err := c.runSynchronousTurn(context.Background(), nil, func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("a clean turn must return nil, got %v", err)
	}
}
