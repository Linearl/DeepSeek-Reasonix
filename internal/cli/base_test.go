package cli

import "testing"

func TestBaseCommandRequiresServeSubcommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{name: "no args", args: nil},
		{name: "wrong subcommand", args: []string{"start"}},
		{name: "help is not serve", args: []string{"--help"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if code := baseCommand(tt.args, "test"); code != 2 {
				t.Fatalf("baseCommand(%v) = %d, want 2", tt.args, code)
			}
		})
	}
}

func TestBaseCommandRequiresStdioFlag(t *testing.T) {
	// --stdio 是 v1 唯一传输；缺失/显式关都拒绝，绝不落回读 stdin 的路径。
	cases := []struct {
		name string
		args []string
	}{
		{name: "missing flag", args: []string{"serve"}},
		{name: "explicit false", args: []string{"serve", "--stdio=false"}},
		{name: "unknown flag", args: []string{"serve", "--tcp=9000"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if code := baseCommand(tt.args, "test"); code != 2 {
				t.Fatalf("baseCommand(%v) = %d, want 2", tt.args, code)
			}
		})
	}
}
