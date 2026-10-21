package agent

import (
	"context"
	"os"
	"testing"
	"time"

	"reasonix/internal/testenv"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	// 任务 1009 门禁保养（667 隔离缺口的根治面）：collab 目录扫描除调用方传入的
	// SessionDir 外还会走机器级全局会话目录（config.ArchiveDir /
	// AllProjectSessionDirs，都落在用户状态根下）。此前只有零星测试逐个
	// t.Setenv("REASONIX_STATE_HOME", ...)——漏设的（667 黄金测试）在开发机上
	// 会把本机真实会话扫进断言，产生 124 条"幽灵会话"假红。整个包统一挂
	// testenv 隔离（与 serve/config/control 同一模式），任何测试不再需要
	// 自己 setenv，也不可能再读到本机会话。
	cleanupUserState, err := testenv.IsolateUserState()
	if err != nil {
		panic(err)
	}
	// Stream body retries use multi-second backoff in production; collapse it
	// in package tests so recovery suites stay deterministic and fast.
	streamRetrySleep = func(ctx context.Context, _ int) bool {
		return ctx.Err() == nil
	}
	recoverySleep = func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil }
	goleak.VerifyTestMain(m, goleak.Cleanup(func(exitCode int) {
		cleanupUserState()
		os.Exit(exitCode)
	}))
}
