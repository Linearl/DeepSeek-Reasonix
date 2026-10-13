package sentinel

// 任务 851 测试矩阵：写判定从「段内出现」升级到「写特征的操作数是敏感路径」
// 后，四条硬禁区规则的只读形态与写形态逐例钉住。
//
//   - write_system_config：只读命令含敏感路径放行（1006 排查 847 时 grep 只读
//     被拦的实锤形态在本矩阵钉死）；重定向/写命令的目标是敏感路径才拦。
//   - force_push_protected：--dry-run/-n 只演算不落盘，放行；真实 force 推照拦。
//   - exfil_credentials：本地读凭据（cat/grep/head …）放行；发送动词才拦。
//   - delete_critical_path：ls/stat/cat .git 等读形态放行；删除动词才拦。

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWriteSystemConfigReadOnlyMatrix 钉住只读放行：已知只读命令含敏感路径
// ——包括文本里出现写特征词形或 '>' 字符（'=>' '>=' '->'、模式词 rm/tee/
// sed -i）——一律放行；读敏感路径 + 重定向到别处同样放行。
func TestWriteSystemConfigReadOnlyMatrix(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetHomeDir(`C:/Users/dev/AppData/Roaming/reasonix`)
		pinUserHome(t, `C:\Users\dev`)
		SetProtectedPaths([]string{
			`C:/Users/dev/AppData/Roaming/reasonix/config.toml`,
			`C:\Users\dev\AppData\Roaming\reasonix\.env`,
		})
		cfg := `C:/Users/dev/AppData/Roaming/reasonix/config.toml`

		readonly := []string{
			// 1006 实锤形态：grep 排查只读被拦
			`grep sentinel ` + cfg,
			`grep -n "hard_forbidden" ` + cfg,
			// 文本含 '>' 字符（旧逻辑按 `>{1,2}` 误判重定向）
			`grep '=>' ` + cfg,
			`grep 'version >= 1.0' ` + cfg,
			`grep -- '->' ` + cfg,
			// 文本含写动词词形（旧逻辑按词表误判）
			`grep 'rm -rf' ` + cfg,
			`grep '\btee\b' ` + cfg,
			`grep 'sed -i' ` + cfg,
			// 其余只读命令逐例
			`cat ` + cfg,
			`cat ` + cfg + ` | head -20`,
			`ls C:/Users/dev/AppData/Roaming/reasonix`,
			`ls -la C:/Users/dev/AppData/Roaming/reasonix/config.toml`,
			`stat ` + cfg,
			`head -5 ` + cfg,
			`tail -20 C:/Users/dev/AppData/Roaming/reasonix/.env`,
			`find C:/Users/dev/AppData/Roaming/reasonix -name '*.toml'`,
			`wc -l ` + cfg,
			`diff ` + cfg + ` D:/work/config.toml`,
			`sed -n '1,10p' ` + cfg,                      // sed 无 -i：纯读
			`cp ` + cfg + ` D:/backup/`,                  // config 作为源复制出去
			`grep sentinel ` + cfg + ` > D:/tmp/out.txt`, // 读敏感路径、写别处
			`grep "$PAT" ` + cfg,                         // 参数含运行时展开
			`grep "$PAT" ` + cfg + ` > D:/tmp/out.txt`,   // 展开参数 + 静态重定向目标非敏感
			`cat ` + cfg + ` << 'EOF'`,                   // heredoc stdin，纯读
			`echo 'diff <(sort a) <(sort b)' > D:/note.txt`,
		}
		for _, cmd := range readonly {
			tool, args := bashCall(t, cmd)
			expectAllowed(t, tool, args)
		}

		// 文件工具只读形态不受限（read_file 不在 mutation 工具表内）。
		expectAllowed(t, "read_file", json.RawMessage(`{"path": "`+cfg+`"}`))
	})
}

// TestWriteSystemConfigWriteMatrix 钉住写拦截：重定向目标、原地写动词、
// tee/touch/rm 族参数、cp/mv 目标、find 写动作位命中敏感路径才拦。
func TestWriteSystemConfigWriteMatrix(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetHomeDir(`C:/Users/dev/AppData/Roaming/reasonix`)
		pinUserHome(t, `C:\Users\dev`)
		SetProtectedPaths([]string{
			`C:/Users/dev/AppData/Roaming/reasonix/config.toml`,
			`C:\Users\dev\AppData\Roaming\reasonix\.env`,
		})
		cfg := `C:/Users/dev/AppData/Roaming/reasonix/config.toml`
		env := `C:\Users\dev\AppData\Roaming\reasonix\.env`

		hits := []string{
			// 重定向（目标 = 敏感路径；含 tilde、clobber、fd 级形态）
			`echo "x=1" > ` + cfg,
			`echo "x=1" >> ` + env,
			`echo "x=1" > ~/AppData/Roaming/reasonix/config.toml`,
			`echo x >| ` + cfg,
			`echo x &> ` + cfg,
			`grep foo ` + cfg + ` 2> ` + cfg, // stderr 重定向写敏感路径
			`cat other.txt > ` + cfg,
			// 原地写动词
			`sed -i 's/a/b/' ` + cfg,
			`sed --in-place 's/a/b/' ` + cfg, // 旧正则漏掉的长形式
			`sed --in-place=.bak 's/a/b/' ` + cfg,
			`sed -in 's/a/b/' ` + cfg, // 短旗标组合
			// tee/touch/shred/truncate/PowerShell：位置参数即写目标
			`tee ` + cfg,
			`tee -a ` + env + ` < extra`,
			`echo x | tee ` + cfg, // 管道分段后 tee 段目标命中
			`touch ` + cfg,
			`shred ` + cfg,
			`truncate -s 0 ` + cfg,
			`set-content ` + cfg + ` -Value x`,
			// 删除与覆盖
			`cp evil.toml ` + cfg,
			`mv evil.toml ~/AppData/Roaming/reasonix/config.toml`,
			// find 的写动作位
			`find C:/Users/dev/AppData/Roaming/reasonix -name '*.bak' -delete`,
			`find ` + cfg + ` -exec rm {} \;`,
			// 写目标不可静态判定（运行时展开）：单段含敏感路径，保守拦
			`cat ` + cfg + ` > "$OUT"`,
		}
		for _, cmd := range hits {
			tool, args := bashCall(t, cmd)
			expectBlocked(t, tool, args, RuleWriteSystemConfig)
		}

		// rm 命中在 home 之下时 delete_critical_path 先拦（first-match-wins，
		// 与既有 TestWriteSystemConfigRules 同款断言语义：拦截即正确）。
		tool, args := bashCall(t, `rm `+cfg)
		if v := CheckToolCall(tool, args, ""); !v.Blocked {
			t.Fatal("rm of a protected config must be blocked")
		}

		// 单独验证 write rule 自身的删除分支：敏感路径注册在 home 之外。
		withSentinelState(t, func(t *testing.T) {
			SetHomeDir(`C:/Users/dev/AppData/Roaming/reasonix`)
			pinUserHome(t, `C:\Users\dev`)
			SetProtectedPaths([]string{`D:/services/irontide/config.toml`})
			tool, args := bashCall(t, `rm D:/services/irontide/config.toml`)
			expectBlocked(t, tool, args, RuleWriteSystemConfig)
		})
	})
}

// TestWriteSystemConfigReasonAlignsWithBehavior 钉文案对齐：拦截理由写明
// 触发形态，并明示只读访问不受限（851 用户拍板：读不需要拦截）。
func TestWriteSystemConfigReasonAlignsWithBehavior(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetHomeDir(`C:/Users/dev/AppData/Roaming/reasonix`)
		pinUserHome(t, `C:\Users\dev`)
		cfg := `C:/Users/dev/AppData/Roaming/reasonix/config.toml`
		SetProtectedPaths([]string{cfg})

		tool, args := bashCall(t, `echo "x=1" > `+cfg)
		v := CheckToolCall(tool, args, "")
		if !v.Blocked {
			t.Fatal("want blocked")
		}
		for _, want := range []string{"重定向写入", "只读访问不受本规则限制"} {
			if !strings.Contains(v.Reason, want) {
				t.Errorf("reason must mention %q, got: %s", want, v.Reason)
			}
		}
	})
}

// TestForcePushReadOnlyMatrix 钉 --dry-run 只读放行与真实推拦截。
func TestForcePushReadOnlyMatrix(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		readonly := []string{
			"git push --dry-run --force origin main",
			"git push --force --dry-run origin main",
			"git push -n --force origin main",
			"git push --force -n origin main",
			"git push --force origin main --dry-run",
			"git push --force origin main -n",
			// 非 push 的只读 git 形态
			"git log origin/main..main",
			"git ls-remote origin main",
			"git fetch origin main",
			"git diff main origin/main",
		}
		for _, cmd := range readonly {
			tool, args := bashCall(t, cmd)
			expectAllowed(t, tool, args)
		}

		hits := []string{
			"git push --force origin main",
			"git push -f origin main-v2-stable",
			"git push --force", // bare：目标不可知，仍拦
		}
		for _, cmd := range hits {
			tool, args := bashCall(t, cmd)
			expectBlocked(t, tool, args, RuleForcePushProtected)
		}
	})
}

// TestExfilReadOnlyMatrix 钉本地读凭据形态放行（发送动词才拦）。
func TestExfilReadOnlyMatrix(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		readonly := []string{
			"cat ~/.ssh/id_rsa",
			"head ~/.ssh/id_rsa",
			"stat ~/.ssh/id_rsa",
			"ls -la ~/.ssh",
			"wc -c ~/.aws/credentials",
			"grep KEY C:/Users/dev/AppData/Roaming/reasonix/.env",
			"cat .env | head -5",     // 管道下游也是读
			"cat .env > D:/tmp/copy", // 读凭据 + 写本地别处（非发送）
			"tail -3 ~/.git-credentials",
		}
		for _, cmd := range readonly {
			tool, args := bashCall(t, cmd)
			expectAllowed(t, tool, args)
		}
	})
}

// TestDeleteCriticalReadOnlyMatrix 钉关键路径读形态放行（删除动词才拦）。
func TestDeleteCriticalReadOnlyMatrix(t *testing.T) {
	withSentinelState(t, func(t *testing.T) {
		SetHomeDir(`C:/Users/dev/AppData/Roaming/reasonix`)
		pinUserHome(t, `C:\Users\dev`)
		readonly := []string{
			"ls .git",
			"ls -la .git/",
			"cat .git/HEAD",
			"stat .git",
			"find . -maxdepth 2 -name '.git'",
			"du -sh ~/.reasonix",
			"ls C:/Users/dev/AppData/Roaming/reasonix/sessions",
			"cat ~/.gitignore", // 组件边界：.gitignore 不是 .git
		}
		for _, cmd := range readonly {
			tool, args := bashCall(t, cmd)
			expectAllowed(t, tool, args)
		}
	})
}
