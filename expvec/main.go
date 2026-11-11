package main

// main.go —— expvec 入口：bench（四项实测）/ retrieve（召回对照）/ selfcheck（tokenizer 自洽）
// / fullscale（全量口径实测，tl1351 补差 b）。

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type sample struct {
	label int // 目标字数档
	text  string
}

// bench 样本：真实感中文段落，实际 rune 数在 bench 输出中打印（报告引实测值）。
var benchSamples = []sample{
	{100, "用户的开发环境是 Windows 11 加 Git Bash，主仓库在 global-workspace 下的 github-repo 目录，worktree 统一放在 worktrees 子目录里。记忆条目多为中文，夹杂英文术语和路径，检索时要能区分大小写的英文标识符与中文描述。"},
	{200, "reasonix 的记忆条目来源有三类：会话里用户拍板的决策、排查后沉淀的根因结论、以及跨会话复用的流程约定。写入时要求写清楚背景与结论，最好带上出处和日期，方便日后回溯。检索时常见的需求是按主题找历史处理办法，比如 hook 超时怎么处理、代理端口是多少、某个报告放在哪个目录。关键词检索的问题是同义改写经常搜不到，比如用户问凭证放哪，而条目里写的是凭据收口。"},
	{300, "fork 开发纪律是多条硬规则组成的清单。先写验收矩阵再改代码，N 态必须逐态接线并各写测试；禁止静默跳过测试，构造失败必须 Fatal；错误文案里给的下一步必须真实存在，未实现就写尚未提供；交分支前跑包级测试，预存红要么修掉要么在交接文档里写明；落地默认保留分支并交分支名，不自行合并进稳定分支，合并由用户指定会话执行；出包只在用户明确要求时进行，版本号不自增，出了包就要写 release notes。这些规则分布在多个文档里，检索需求通常是拿一句口语化的问题找对应规则原文。"},
	{500, "多机同步体系围绕 OneDrive 通道展开，push、pull、organize 三个动作各自独立，跑之前先读 README 确认方向。工作区是跨会话共享的全局空间，里面有跨会话成果、主题任务、凭据、脚本、技能包、仓库副本和系统缓存的混合体，按主题分目录存放：调研报告进 report 目录，根因诊断进 issues 的 reports 子目录，会话交接进 handoff 目录，可复用的流程进 scripts 或技能包。删除一律走回收语义，先列目录核对，再移进带日期后缀的回收区，等用户确认后才真正删除，禁止多目标一次性混删。凭据是极慎重项，读写需要明确授权，不得明文出现在日志或报告里。会话组织采用三层架构：人负责决策拍板，主对话负责协调派单收货催办总结，子对话负责专项执行；AI 创建的会话默认放进指定分组，主对话按主题分线，互不跨线指挥。日常沟通要求结论先行、证据随附，避免废话；报告必须用户明确要求才写长文；术语首次出现要给一句中文解释。这些约定本身就是典型的记忆条目：条目之间有主题关联、有同义表述、有具体路径和端口数字，检索时既有精确关键词需求，也有口语化改写需求。"},
}

func usage() {
	fmt.Println(`用法:
  expvec bench    -model models/model.onnx -vocab models/vocab.txt -dll models/onnxruntime.dll
  expvec retrieve -model ... -vocab ... -dll ... -corpus eval/corpus.tsv -queries eval/queries.txt
  expvec selfcheck -vocab models/vocab.txt`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	model := fs.String("model", "models/model.onnx", "onnx 模型路径")
	vocab := fs.String("vocab", "models/vocab.txt", "词表路径")
	dll := fs.String("dll", "models/onnxruntime.dll", "onnxruntime 共享库路径")
	corpus := fs.String("corpus", "eval/corpus.tsv", "语料 TSV (id\\ttext)")
	queries := fs.String("queries", "eval/queries.txt", "query 列表（每行一条）")
	sizes := fs.String("sizes", "1000,3000,5000", "fullscale 各轮规模（逗号分隔）")
	fs.Parse(os.Args[2:])

	var err error
	switch cmd {
	case "bench":
		err = runBench(*model, *vocab, *dll)
	case "retrieve":
		err = runRetrieve(*model, *vocab, *dll, *corpus, *queries)
	case "selfcheck":
		err = runSelfcheck(*vocab)
	case "fullscale":
		var ns []int
		for _, f := range strings.Split(*sizes, ",") {
			v, e := strconv.Atoi(strings.TrimSpace(f))
			if e != nil || v <= 0 {
				err = fmt.Errorf("非法 -sizes: %q", *sizes)
				break
			}
			ns = append(ns, v)
		}
		if err == nil {
			err = runFullscale(*model, *vocab, *dll, ns)
		}
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}

// runSelfcheck：tokenizer 自洽性——编码再解码，统计 UNK 与还原差异。
func runSelfcheck(vocabPath string) error {
	tok, err := LoadTokenizer(vocabPath, 512)
	if err != nil {
		return err
	}
	fmt.Printf("词表条目: %d\n", tok.VocabSize())
	totalUNK := 0
	for _, s := range benchSamples {
		ids := tok.Encode(s.text)
		decoded := tok.DecodeTokens(ids)
		unk := 0
		for _, id := range ids {
			if int(id) == tok.unkID {
				unk++
			}
		}
		totalUNK += unk
		src := []rune(s.text)
		fmt.Printf("  %d字档: %d字 -> %dtokens, UNK=%d, 解码长度=%drune (原%d)\n",
			s.label, len(src), len(ids), unk, len([]rune(decoded)), len(src))
	}
	if totalUNK > 0 {
		fmt.Printf("总 UNK=%d（生僻字/词表外符号会落 UNK，解码后信息丢失）\n", totalUNK)
	} else {
		fmt.Println("全部样本 UNK=0，中文样本词表覆盖完好")
	}
	return nil
}
