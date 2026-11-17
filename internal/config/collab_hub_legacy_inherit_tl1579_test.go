package config

import (
	"os"
	"path/filepath"
	"testing"
)

// tl1579 —— 协作中心入口偶发消失根因修复的回归钉。
//
// 根因：inheritLegacyCollabHubKey（856 ③ 一次性改名迁移）挂在每次
// decodeTOMLBytes 上。loadForRoot 把用户 config.toml 与项目级 reasonix.toml
// 先后解码进同一个累积 cfg：项目文件不含 desktop.experimental_collab_hub
// 新键时，钩子对已合并进来的用户值二次触发继承，用早已退役的 legacy 键
// （experimental_collab_group_view）的值覆盖用户磁盘上的值 → 启动快照
// （LoadForRootReadOnly）读到被覆盖后的 false → 前端入口卸载。
//
// 修复（报告 1.7 最小档）：项目合并前快照用户解码值；项目文件未定义新键时
// 还原快照——项目显式定义仍按 TOML 后写胜语义生效，两文件都未定义时继承
// legacy 的钩子原语义保留。
//
// 验收矩阵（报告 1.7 四态 + 两个防回归变体）：
//
//	S1 复现钉：用户 true + 项目无键（[desktop] 表含其他键，现场形态）→ true
//	   （修复前此处被覆盖为 false，即报障现场）。
//	S2 钩子原语义：两文件都无新键无 legacy → false（继承 legacy 默认值）。
//	S3 项目显式优先：用户 true + 项目显式 false → false。
//	S4 legacy 继承：用户无新键 + legacy=true + 项目无键 → true。
//	S5 else-if 链防回归：用户 true + 项目定义 agent.system_prompt_file
//	   （无新键）→ true——还原判断不得被同文件其他键的分枝短路。
//	S6 项目显式 true：用户 false + 项目显式 true → true。
//
// 全部走 LoadForRootReadOnly——桌面启动快照的生产读取路径。
func TestLoadForRootCollabHubInheritanceAcrossProjectMerge(t *testing.T) {
	tests := []struct {
		name         string
		userTOML     string
		projectTOML  string
		writeProject bool
		want         bool
	}{
		{
			name:         "S1 用户true+项目[desktop]表无新键不被legacy覆盖（tl1579复现钉）",
			userTOML:     "[desktop]\nexperimental_collab_hub = true\n",
			projectTOML:  "[desktop]\nlanguage = \"zh\"\n",
			writeProject: true,
			want:         true,
		},
		{
			name:         "S2 两文件均无新键继承legacy默认false",
			userTOML:     "[desktop]\nlanguage = \"en\"\n",
			projectTOML:  "[desktop]\nlanguage = \"zh\"\n",
			writeProject: true,
			want:         false,
		},
		{
			name:         "S3 项目显式false覆盖用户true",
			userTOML:     "[desktop]\nexperimental_collab_hub = true\n",
			projectTOML:  "[desktop]\nexperimental_collab_hub = false\n",
			writeProject: true,
			want:         false,
		},
		{
			name:         "S4 用户仅legacy=true且项目无键继承true",
			userTOML:     "[desktop]\nexperimental_collab_group_view = true\n",
			projectTOML:  "[desktop]\nlanguage = \"zh\"\n",
			writeProject: true,
			want:         true,
		},
		{
			name:         "S5 项目带system_prompt_file无新键仍还原用户值",
			userTOML:     "[desktop]\nexperimental_collab_hub = true\n",
			projectTOML:  "[agent]\nsystem_prompt_file = \"AGENTS.md\"\n\n[desktop]\nlanguage = \"zh\"\n",
			writeProject: true,
			want:         true,
		},
		{
			name:         "S6 项目显式true覆盖用户false",
			userTOML:     "[desktop]\nexperimental_collab_hub = false\n",
			projectTOML:  "[desktop]\nexperimental_collab_hub = true\n",
			writeProject: true,
			want:         true,
		},
		{
			name:         "S7 项目文件不存在时用户值原样保留",
			userTOML:     "[desktop]\nexperimental_collab_hub = true\n",
			writeProject: false,
			want:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			project := t.TempDir()
			t.Setenv("REASONIX_HOME", home)
			t.Setenv("REASONIX_CREDENTIALS_STORE", "file")
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(tt.userTOML), 0o644); err != nil {
				t.Fatal(err)
			}
			if tt.writeProject {
				if err := os.WriteFile(filepath.Join(project, "reasonix.toml"), []byte(tt.projectTOML), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			cfg, err := LoadForRootReadOnly(project)
			if err != nil {
				t.Fatalf("LoadForRootReadOnly: %v", err)
			}
			if got := cfg.Desktop.ExperimentalCollabHub; got != tt.want {
				t.Fatalf("Desktop.ExperimentalCollabHub = %v, want %v（tl1579：项目合并不得覆盖用户显隐值）", got, tt.want)
			}
		})
	}
}
