package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"reasonix/internal/shellparse"
	"reasonix/internal/shellsafe"
)

// 任务 575：bash「重命令独占」声明（方案 C）。
//
// 为什么需要这张表：optimistic_write（#9213）的设计本意是「用 expected 基线
// 替代串行锁」，但 bash 跑的命令既无 expected、也不取写租约
// （internal/config/config.go Sandbox.OptimisticWrite 注释自己声明了这块
// 覆盖缺口）。实证后果：多会话并发 `pnpm install` / 前端构建时共享
// node_modules 被互相破坏（2026-10-07 出包三连败）。本表给这类「共享资源
// 破坏型」命令补上替代品：命中即取 workspace 写守卫（HoldWrite），普通命令
// 照常乐观并行零开销。
//
// 为什么是这些命令（白名单的收录判据，改表前先读）：
//   1. 装依赖/构建类（npm/pnpm/yarn/bun/go/cargo/pip/uv/poetry/dotnet/
//      composer/bundle 的 install/build 族子命令）——事故本体：并发安装/构建
//      互相破坏共享依赖树与构建产物；
//   2. 构建编排器整工具命中（make/cmake/ninja/meson/gradle/mvn/bazel 族）——
//      任意子命令都会触发配置阶段并写项目内缓存（.gradle/、target/ 等），
//      `gradle tasks` 也不安全，故不按子命令细分；
//   3. git 变更类子命令（checkout/merge/rebase/reset/restore/clean/stash/
//      cherry-pick/pull 族）——与安装/构建同级的「整树突变」向量；`git status`
//      等只读子命令不在表内（边界：不得误伤只读命令）。
//
// 刻意不收录（已知缺口，宁缺勿滥）：
//   - `npx`/`bunx`/`uvx`/`pnpm exec`：参数是任意本地二进制，多数为只读检查
//     （tsc/eslint），按头命令整收会大面积误伤；脚手架类（npx create-*）
//     目前覆盖不到，属可接受残留缺口；
//   - `npm test` / `go test` 族：主要写 GOCACHE 等工作区外缓存，非本表针对
//     的共享工作区破坏类；
//   - 直接调 bundler/解释器（`vite`、`node build.js`、`python x.py`）：写面
//     完全不可静态判定，与任意命令同级，不声明。
// 误伤方向是安全的：本表只用于「是否取写守卫」，宁可多取不可漏取；漏取
// 只回到现状（乐观并行无防护），不会引入新风险。

// bashHeavyWholeToolHeads：这些头命令的任意调用都按重命令处理（构建编排器，
// 见上方判据 2）。
var bashHeavyWholeToolHeads = map[string]bool{
	"make": true, "gmake": true, "just": true,
	"cmake": true, "ninja": true, "meson": true,
	"gradle": true, "gradlew": true,
	"mvn": true, "mvnw": true,
	"bazel": true, "buck": true, "buck2": true, "pants": true,
}

// bashHeavySubcommandHeads：头命令 → 变更型子命令集。键支持空格连接的双词
// 形式（如 go 的 "mod download"）；匹配时对 argv 做单词与相邻双词两轮比对。
// 收录判据见文件头（判据 1 与 3）。
var bashHeavySubcommandHeads = map[string]map[string]bool{
	// JS/Node 包管理器。`run`/`run-script` 收录：`npm run build` 是继 install
	// 之后的第二大共享产物破坏向量（dist/ 输出）；dev server 类脚本（npm run
	// dev）因此也会持锁——方向正确：dev server 在跑时另一会话装依赖恰是事故类。
	"npm": setOf("install", "add", "remove", "rm", "uninstall", "update", "upgrade",
		"link", "unlink", "ci", "dedupe", "prune", "rebuild", "import", "patch",
		"run", "run-script", "init", "create"),
	"pnpm": setOf("install", "add", "remove", "rm", "uninstall", "update", "upgrade",
		"link", "unlink", "ci", "dedupe", "prune", "rebuild", "import", "patch",
		"patch-commit", "run", "run-script", "init", "create"),
	"yarn": setOf("install", "add", "remove", "rm", "uninstall", "upgrade", "set",
		"link", "unlink", "dedupe", "import", "rebuild", "run", "run-script",
		"init", "create"),
	"yarnpkg": setOf("install", "add", "remove", "rm", "uninstall", "upgrade", "set",
		"link", "unlink", "dedupe", "import", "rebuild", "run", "run-script",
		"init", "create"),
	"bun": setOf("install", "add", "remove", "rm", "uninstall", "update", "link",
		"unlink", "run", "init", "create"),
	// go：单词子命令 + "mod"/"work" 双词族。go test 不收录（只写 GOCACHE）。
	"go": setOf("build", "install", "get", "generate", "clean",
		"mod download", "mod tidy", "mod vendor", "work sync", "work use", "work vendor"),
	// cargo：build/check/test/run 都会写 target/；fix 会改写源码。
	"cargo": setOf("build", "check", "test", "bench", "run", "install", "uninstall",
		"add", "remove", "update", "clean", "vendor", "package", "publish", "doc", "fix"),
	// Python。pip download/wheel 会向工作区落文件。
	"pip":  setOf("install", "uninstall", "download", "wheel"),
	"pip3": setOf("install", "uninstall", "download", "wheel"),
	"uv": setOf("sync", "lock", "add", "remove", "install", "uninstall", "venv",
		"build", "run", "pip"),
	"poetry": setOf("install", "add", "remove", "update", "lock", "build", "run"),
	// .NET / JVM 之外的 PHP。
	"dotnet":   setOf("restore", "build", "publish", "pack", "add", "remove", "clean", "format"),
	"composer": setOf("install", "update", "require", "remove", "create-project"),
	"bundle":   setOf("install", "update", "add", "remove", "lock"),
	// git 变更类子命令（判据 3）。`git status`/`log`/`diff`/`show`/`branch` 等
	// 只读子命令不在集内——命中判据是「argv 中出现集内子命令」，只读调用不会
	// 携带这些词，不会取锁。
	"git": setOf("checkout", "switch", "merge", "rebase", "reset", "restore",
		"clean", "stash", "apply", "am", "cherry-pick", "revert", "pull"),
}

// bashHeavyCommandTools：启用重命令独占声明的工具名（当前仅 bash；shell 是
// bash 的历史别名面，经 evidence 归一，实际注册名恒为 "bash"）。
var bashHeavyCommandTools = map[string]bool{"bash": true}

// bashHeavyWriteCommand reports whether a bash tool invocation runs at least
// one "heavy" command from the task-575 declarative whitelist. Heavy commands
// take the workspace write guard (HoldWrite) even under optimistic_write;
// everything else keeps running lock-free. false positives are safe (an extra
// hold), false negatives only fall back to today's unprotected behavior.
func bashHeavyWriteCommand(toolName string, args json.RawMessage) bool {
	if !bashHeavyCommandTools[toolName] {
		return false
	}
	command := bashCommandFromArgs(args)
	if command == "" {
		return false
	}
	return bashCommandIsHeavy(command)
}

// bashCommandIsHeavy matches one shell command string against the whitelist.
// Primary path: shellparse.SplitTopLevel + shellsafe.CommandArgv（静态可证的
// argv，env 前缀已剥、路径头已归一）；任一段命中即为重命令（`cd app &&
// pnpm install` 命中）。解析不了（展开、动态形状）时退化为分词邻接扫描，
// 保守方向（可能多判，不漏判）。
func bashCommandIsHeavy(command string) bool {
	if segments, _, ok := shellparse.SplitTopLevel(command); ok && len(segments) > 0 {
		for _, segment := range segments {
			if argv, _, ok := shellsafe.CommandArgv(segment); ok {
				if argvIsHeavy(argv) {
					return true
				}
				continue
			}
			if tokenScanIsHeavy(segment) {
				return true
			}
		}
		return false
	}
	return tokenScanIsHeavy(command)
}

// argvIsHeavy checks one statically proven argv against the whitelist.
func argvIsHeavy(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	head := normalizeBashHead(argv[0])
	if bashHeavyWholeToolHeads[head] {
		return true
	}
	heavy, ok := bashHeavySubcommandHeads[head]
	if !ok {
		return false
	}
	rest := argv[1:]
	for i, token := range rest {
		if heavy[strings.ToLower(token)] {
			return true
		}
		if i+1 < len(rest) && heavy[strings.ToLower(token)+" "+strings.ToLower(rest[i+1])] {
			return true
		}
	}
	return false
}

// tokenScanIsHeavy is the fail-open fallback for shapes the static parser
// rejects (expansions, exotic operators). It walks whitespace tokens and
// matches whole-tool heads plus head+subcommand adjacency (`pnpm install`).
// It can over-match inside quoted text (echo "pnpm install"); over-matching
// takes an extra hold, which is the safe direction.
func tokenScanIsHeavy(command string) bool {
	for _, op := range []string{"&&", "||", ";", "|", "\n"} {
		command = strings.ReplaceAll(command, op, " ")
	}
	tokens := strings.Fields(command)
	for i, token := range tokens {
		head := normalizeBashHead(token)
		if bashHeavyWholeToolHeads[head] {
			return true
		}
		if heavy, ok := bashHeavySubcommandHeads[head]; ok {
			if i+1 < len(tokens) && heavy[strings.ToLower(tokens[i+1])] {
				return true
			}
			if i+2 < len(tokens) && heavy[strings.ToLower(tokens[i+1])+" "+strings.ToLower(tokens[i+2])] {
				return true
			}
		}
	}
	return false
}

// normalizeBashHead folds a command head to its comparable form: last path
// component (./gradlew, node_modules/.bin/vite, C:\...\pnpm.cmd), Windows
// shim suffixes (.exe/.cmd/.bat/.ps1) stripped, lowercased.
func normalizeBashHead(head string) string {
	head = strings.ToLower(strings.TrimSpace(head))
	head = filepath.Base(head)
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		head = strings.TrimSuffix(head, suffix)
	}
	return head
}

func setOf(items ...string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}
