package control

import (
	"testing"
	"time"
)

// tl1450: 本包固定 5s 接收窗在机器负载下随机红（事件实际送达，只是晚于 5s；
// 隔离单跑全绿）。统一改用负载容忍接收窗：channel-receive 语义不变（超时仍
// fail），只把窗口从固定 5s 放宽到 30s，与本包既有 30s 窗族一致。
const controlTestReceiveWindow = 30 * time.Second

// waitForSignal 阻塞等待 signal 触发；超过负载容忍窗后 fail，失败文案保留
// 调用方语境（what）并附带实际窗口时长。
func waitForSignal(t *testing.T, what string, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(controlTestReceiveWindow):
		t.Fatalf("%s (timed out after %s)", what, controlTestReceiveWindow)
	}
}

// waitForValue 是 waitForSignal 的带值版本：等到值则返回；超窗则 fail。
func waitForValue[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(controlTestReceiveWindow):
		t.Fatalf("%s (timed out after %s)", what, controlTestReceiveWindow)
		var zero T
		return zero
	}
}
