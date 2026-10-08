package sentinel

import (
	"testing"

	"reasonix/internal/tool"
)

// 任务 426 单一事实源守护：forbidden.go 的 isFileMutationTool 镜像列表必须与
// internal/tool 安全元数据表（SafetySpec.FileMutation）逐名一致。生产代码仍然
// 不导入 internal/tool（zero_model_test.go 钉住 sentinel 的最小依赖面），本文件
// 是测试侧的同步断言——两侧任一改动未同步，这里红。
func TestFileMutationMirrorMatchesSafetyMetadata(t *testing.T) {
	for _, bt := range tool.Builtins() {
		name := bt.Name()
		want := tool.SafetyOf(bt).FileMutation
		if got := isFileMutationTool(name); got != want {
			t.Errorf("mirror drift for %q: sentinel isFileMutationTool=%v, tool.SafetySpec.FileMutation=%v; update forbidden.go and the safety table in the same change", name, got, want)
		}
	}
	// 运行期注册（不在编译期名册）的工具也核对镜像。
	for _, name := range []string{"ui_interact", "restart_update"} {
		want := tool.SafetyOfName(name).FileMutation
		if got := isFileMutationTool(name); got != want {
			t.Errorf("mirror drift for runtime-registered %q: sentinel=%v, metadata=%v", name, got, want)
		}
	}
}
