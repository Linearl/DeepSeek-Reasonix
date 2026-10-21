package acp

import (
	"testing"

	"reasonix/internal/testenv"
)

// 任务 1009 门禁保养：与 serve/config/control 同一模式——session/new 一类的
// 处理链会按用户状态根解析 transcript 落点（fakeFactory 无 SessionDir 时退回
// 空，但凡走真实目录的服务端路径都会落到开发机的真实状态目录）。整包统一挂
// testenv 隔离，测试不再读写本机用户状态。
func TestMain(m *testing.M) {
	testenv.RunWithIsolatedUserState(m)
}
