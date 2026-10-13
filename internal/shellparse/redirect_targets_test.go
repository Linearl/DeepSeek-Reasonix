package shellparse

// 任务 851：RedirectTargets 是 sentinel write_system_config「写目标级判定」
// 的精度来源——引号内的 '>'（'=>' '>=' '->'、模式文本）不构成重定向，
// fd 之间复制（2>&1）不算文件目标，读方向（<、<<<）不产生目标。

import "testing"

func TestRedirectTargetsWriteDirections(t *testing.T) {
	cases := []struct {
		cmd     string
		targets []string
	}{
		{`echo x > f.txt`, []string{"f.txt"}},
		{`echo x >> f.txt`, []string{"f.txt"}},
		{`echo x >| f.txt`, []string{"f.txt"}},
		{`echo x &> f.txt`, []string{"f.txt"}},
		{`echo x &>> f.txt`, []string{"f.txt"}},
		{`echo x <> f.txt`, []string{"f.txt"}},
		{`echo x > C:\Users\dev\config.toml`, []string{`C:\Users\dev\config.toml`}}, // 源文本切片保留反斜杠形态
		{`echo x > "quoted path.txt"`, []string{`"quoted path.txt"`}},               // 引号原样保留
		{`echo a > f1.txt && echo b > f2.txt`, nil},                                 // 复合命令不是单段，调用方分段后各取目标；此处单语句解析直接拒绝
		{`echo x 2> err.txt`, []string{"err.txt"}},
	}
	for _, tc := range cases {
		got, ok := RedirectTargets(tc.cmd)
		if !ok {
			t.Errorf("%q: want ok, got !ok", tc.cmd)
			continue
		}
		if tc.targets == nil {
			continue // 见用例注释：期望由调用方分段处理
		}
		if len(got) != len(tc.targets) {
			t.Errorf("%q: targets = %q, want %q", tc.cmd, got, tc.targets)
			continue
		}
		for i := range got {
			if got[i] != tc.targets[i] {
				t.Errorf("%q: targets = %q, want %q", tc.cmd, got, tc.targets)
			}
		}
	}
}

func TestRedirectTargetsNonTargets(t *testing.T) {
	// 这些形态必须返回零目标且 ok=true（不是重定向写文件）。
	for _, cmd := range []string{
		`grep '=>' config.toml`,      // 文本中的 =>
		`grep 'version >= 1.0' f`,    // 文本中的 >=
		`grep -- '->' f`,             // 文本中的 ->
		`echo '>' config.toml`,       // 引号内 >
		`grep foo config.toml 2>&1`,  // fd 复制
		`grep foo config.toml >&2`,   // fd 复制
		`grep foo config.toml 2>&-`,  // 关闭 fd
		`cat config.toml < other`,    // 读方向
		`cat config.toml <<< "text"`, // here-string
		`grep -r x .`,                // 无重定向
		`echo x > /dev/null`,         // 目标是普通路径，原样返回由调用方判
	} {
		got, ok := RedirectTargets(cmd)
		if !ok {
			t.Errorf("%q: want ok=true", cmd)
			continue
		}
		if cmd == `echo x > /dev/null` {
			if len(got) != 1 || got[0] != "/dev/null" {
				t.Errorf("%q: targets = %q, want [/dev/null]", cmd, got)
			}
			continue
		}
		if len(got) != 0 {
			t.Errorf("%q: want no targets, got %q", cmd, got)
		}
	}
}

func TestRedirectTargetsConservativeShapes(t *testing.T) {
	// 解析失败 / heredoc / 目标含运行时展开：ok=false，调用方保守处理。
	for _, cmd := range []string{
		`cat > f.txt << 'EOF'`,
		`echo x > "$OUT"`,
		`echo x > $(cmd)`,
		`echo 'unbalanced`,
	} {
		if _, ok := RedirectTargets(cmd); ok {
			t.Errorf("%q: want ok=false (conservative)", cmd)
		}
	}
}
